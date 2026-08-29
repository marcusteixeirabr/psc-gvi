package vessel

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/scraper"
)

// CIALAHandler gerencia as consultas ao portal CIALA.
// O cliente HTTP é criado na primeira requisição e reutilizado — login único por sessão do servidor.
type CIALAHandler struct {
	q               *sqlc.Queries
	cialaURL        string
	username        string
	password        string
	vesselFinderURL string
	mu              sync.Mutex
	client          *scraper.CIALAClient
}

// NewCIALAHandler cria o handler.
func NewCIALAHandler(q *sqlc.Queries, cialaURL, username, password, vesselFinderURL string) *CIALAHandler {
	return &CIALAHandler{
		q:               q,
		cialaURL:        cialaURL,
		username:        username,
		password:        password,
		vesselFinderURL: vesselFinderURL,
	}
}

// getClient retorna o cliente CIALA, criando-o na primeira chamada.
func (h *CIALAHandler) getClient() (*scraper.CIALAClient, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client == nil {
		c, err := scraper.NewCIALAClient(h.cialaURL, h.username, h.password)
		if err != nil {
			return nil, err
		}
		h.client = c
	}
	return h.client, nil
}

// resetClient descarta o cliente atual (força novo login na próxima chamada).
func (h *CIALAHandler) resetClient() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.client = nil
}

// LookupVessel — POST /vessels/:id/ciala
func (h *CIALAHandler) LookupVessel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	v, err := h.q.GetVessel(c.Request.Context(), id)
	if err != nil || v.Imo == nil {
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(id, 10)+
			"?error="+url.QueryEscape("Vessel sem IMO — consulte o VesselFinder primeiro"))
		return
	}

	client, err := h.getClient()
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(id, 10)+
			"?error="+url.QueryEscape("Erro ao criar cliente CIALA: "+err.Error()))
		return
	}

	result, err := client.LookupIMO(c.Request.Context(), *v.Imo)
	if err != nil {
		// Tenta reset + nova tentativa uma vez (sessão pode ter expirado).
		slog.Warn("falha no CIALA, tentando reset de sessão", "component", "ciala-handler", "error", err)
		h.resetClient()
		client, err2 := h.getClient()
		if err2 == nil {
			result, err = client.LookupIMO(c.Request.Context(), *v.Imo)
		}
	}
	if err != nil {
		slog.Error("falha no CIALA após reset", "component", "ciala-handler", "imo", *v.Imo, "error", err)
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(id, 10)+
			"?error="+url.QueryEscape("CIALA: "+err.Error()))
		return
	}

	if err := h.save(c, id, result); err != nil {
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(id, 10)+
			"?error="+url.QueryEscape("Erro ao salvar dados CIALA: "+err.Error()))
		return
	}

	msg := "CIALA: " + cialaResultSummary(result)
	c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(id, 10)+"?flash="+url.QueryEscape(msg))
}

// BatchProgressPage — GET /ciala-search
// Aceita ?mode=all para atualizar todos (inclusive os que já têm dados).
func (h *CIALAHandler) BatchProgressPage(c *gin.Context) {
	mode := c.DefaultQuery("mode", "missing")
	var total int
	if mode == "all" {
		vessels, _ := h.q.ListAllVesselsWithIMO(c.Request.Context())
		total = len(vessels)
	} else {
		vessels, _ := h.q.ListVesselsForCIALA(c.Request.Context())
		total = len(vessels)
	}
	c.HTML(http.StatusOK, "ciala_progress.html", gin.H{
		"User":  auth.GetUser(c),
		"Total": total,
		"Mode":  mode,
	})
}

// BatchStream — GET /ciala-search/stream
// SSE que processa navios via CIALA. ?mode=all atualiza inclusive os que já têm dados.
func (h *CIALAHandler) BatchStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	send := func(event, data string) {
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
		c.Writer.Flush()
	}

	mode := c.DefaultQuery("mode", "missing")
	var vessels []sqlc.Vessel
	var err error
	if mode == "all" {
		vessels, err = h.q.ListAllVesselsWithIMO(c.Request.Context())
	} else {
		vessels, err = h.q.ListVesselsForCIALA(c.Request.Context())
	}
	if err != nil {
		send("error", "Erro ao listar navios: "+err.Error())
		return
	}
	if len(vessels) == 0 {
		send("done", "Todos os navios já têm dados do CIALA.")
		return
	}

	send("start", strconv.Itoa(len(vessels)))

	client, err := h.getClient()
	if err != nil {
		send("error", "Erro ao criar cliente CIALA: "+err.Error())
		return
	}

	found, failed := 0, 0
	for _, v := range vessels {
		if c.Request.Context().Err() != nil {
			return
		}
		imo := *v.Imo
		send("searching", v.Name+" ("+imo+")")

		result, err := client.LookupIMO(c.Request.Context(), imo)
		if err != nil {
			slog.Error("falha na consulta CIALA em lote", "component", "ciala-handler", "vessel", v.Name, "error", err)
			send("fail", v.Name+"|"+err.Error())
			failed++
			continue
		}

		if err := h.save(c, v.ID, result); err != nil {
			send("fail", v.Name+"|erro ao salvar: "+err.Error())
			failed++
			continue
		}

		send("found", v.Name+"|"+cialaResultSummary(result))
		found++
	}

	send("done", fmt.Sprintf("%d atualizado(s) · %d falha(s)", found, failed))
}

// AtualizarDados — POST /vessels/:id/atualizar
// Pipeline completo: IMO (VesselFinder) → dimensões (VesselFinder) → CIALA.
func (h *CIALAHandler) AtualizarDados(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	v, err := h.q.GetVessel(c.Request.Context(), id)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	redirectErr := func(msg string) {
		c.Redirect(http.StatusFound,
			fmt.Sprintf("/vessels/%d/edit?error=%s", id, url.QueryEscape(msg)))
	}

	// ── Passo 1: navio sem IMO → busca no VesselFinder pelo nome ──────────
	imo := ""
	if v.Imo != nil {
		imo = *v.Imo
	}
	if imo == "" {
		loa := numericToFloat(v.LengthM)
		beam := numericToFloat(v.BeamM)
		found, findErr := scraper.FindIMO(c.Request.Context(), h.vesselFinderURL, v.Name, loa, beam)
		if findErr != nil {
			if strings.Contains(findErr.Error(), "múltiplos navios") || strings.Contains(findErr.Error(), "ambíguo") {
				redirectErr("Não foi possível resolver ambiguidade, entre com os dados manualmente.")
			} else {
				redirectErr("IMO não encontrado: " + findErr.Error())
			}
			return
		}
		if err := h.q.UpdateVesselIMO(c.Request.Context(), sqlc.UpdateVesselIMOParams{
			ID: id, Imo: &found,
		}); err != nil {
			redirectErr("Erro ao salvar IMO: " + err.Error())
			return
		}
		imo = found
		v, _ = h.q.GetVessel(c.Request.Context(), id)
	}

	// ── Passo 2: sem LOA ou Beam → busca dimensões no VesselFinder pelo IMO ──
	loaVal := numericToFloat(v.LengthM)
	beamVal := numericToFloat(v.BeamM)
	if loaVal == 0 || beamVal == 0 {
		results, searchErr := scraper.SearchVesselFinder(c.Request.Context(), h.vesselFinderURL, imo, 0)
		if searchErr == nil && len(results) > 0 {
			if len(results) > 1 && loaVal == 0 && beamVal == 0 {
				redirectErr("Não foi possível resolver ambiguidade, entre com os dados manualmente.")
				return
			}
			r := results[0]
			if r.LOA > 0 || r.Beam > 0 {
				_ = h.q.UpdateVesselDimensions(c.Request.Context(), sqlc.UpdateVesselDimensionsParams{
					ID:      id,
					LengthM: floatToNumeric(r.LOA),
					BeamM:   floatToNumeric(r.Beam),
				})
				loaVal = r.LOA
				beamVal = r.Beam
			}
		}
	}

	// ── Passo 3: consulta o CIALA ─────────────────────────────────────────
	client, err := h.getClient()
	if err != nil {
		redirectErr("Erro ao criar cliente CIALA: " + err.Error())
		return
	}
	result, err := client.LookupIMO(c.Request.Context(), imo)
	if err != nil {
		slog.Warn("CIALA falhou, tentando reset", "component", "ciala-handler", "imo", imo, "error", err)
		h.resetClient()
		if c2, err2 := h.getClient(); err2 == nil {
			result, err = c2.LookupIMO(c.Request.Context(), imo)
		}
	}
	if err != nil {
		redirectErr("CIALA: " + err.Error())
		return
	}

	if err := h.save(c, id, result); err != nil {
		redirectErr("Erro ao salvar dados CIALA: " + err.Error())
		return
	}

	msg := "Dados atualizados: " + cialaResultSummary(result)
	if loaVal > 0 || beamVal > 0 {
		msg += fmt.Sprintf(" · LOA %.0f m · Boca %.0f m", loaVal, beamVal)
	}
	c.Redirect(http.StatusFound,
		fmt.Sprintf("/vessels/%d?flash=%s", id, url.QueryEscape(msg)))
}

// floatToNumeric converte float64 em pgtype.Numeric para persistência.
func floatToNumeric(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(strconv.FormatFloat(f, 'f', 2, 64))
	return n
}

// save persiste os dados CIALA no banco.
func (h *CIALAHandler) save(c *gin.Context, vesselID int64, r *scraper.CIALAResult) error {
	var riskLevel *string
	if r.RiskLevel != "" {
		riskLevel = &r.RiskLevel
	}

	var inspDate pgtype.Date
	if r.LastInspectionDate != nil {
		_ = inspDate.Scan(*r.LastInspectionDate)
	}

	var flag *string
	if r.Flag != "" {
		flag = &r.Flag
	}

	var vesselType *string
	if r.VesselType != "" {
		vesselType = &r.VesselType
	}

	var yearBuilt *int32
	if r.YearBuilt > 0 {
		y := int32(r.YearBuilt)
		yearBuilt = &y
	}

	var deficiencies *string
	if r.LastInspectionDeficiencies != "" {
		deficiencies = &r.LastInspectionDeficiencies
	}

	return h.q.UpdateVesselCIALA(c.Request.Context(), sqlc.UpdateVesselCIALAParams{
		ID:                          vesselID,
		RiskLevel:                   riskLevel,
		LastInspectionDate:          inspDate,
		LastInspectionDeficiencies:  deficiencies,
		Flag:                        flag,
		VesselType:                  vesselType,
		YearBuilt:                   yearBuilt,
	})
}

func cialaResultSummary(r *scraper.CIALAResult) string {
	if r.RiskLevel == "not_found" {
		return "Sem registro no CIALA"
	}
	parts := []string{}
	if r.Flag != "" {
		parts = append(parts, "Bandeira: "+r.Flag)
	}
	if r.RiskLevel != "" {
		parts = append(parts, "Risco: "+riskLabel(r.RiskLevel))
	}
	if r.LastInspectionDate != nil {
		parts = append(parts, "Inspeção: "+r.LastInspectionDate.Format("02/01/2006"))
	} else if r.RiskLevel != "" {
		parts = append(parts, "Sem inspeção prévia")
	}
	if r.YearBuilt > 0 {
		parts = append(parts, fmt.Sprintf("Ano: %d", r.YearBuilt))
	}
	if len(parts) == 0 {
		return "sem dados retornados"
	}
	return strings.Join(parts, " · ")
}

func riskLabel(r string) string {
	switch r {
	case "high":
		return "Alto"
	case "standard":
		return "Padrão"
	case "low":
		return "Baixo"
	}
	return r
}
