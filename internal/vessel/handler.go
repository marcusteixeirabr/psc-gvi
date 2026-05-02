package vessel

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/pagination"
)

// Handler gerencia as requisições HTTP de navios.
type Handler struct {
	q *sqlc.Queries
}

// NewHandler cria o handler injetando as queries do banco.
func NewHandler(q *sqlc.Queries) *Handler {
	return &Handler{q: q}
}

// ── Modelos de página ─────────────────────────────────────────────────────────

type listPage struct {
	User       *auth.UserSession
	Vessels    []VesselView
	Flash      string
	ShowAll    bool // admin: exibindo navios desativados
	Query      string
	TotalAll   int
	Page       pagination.Page
	Filter     VesselFilter
}

type formPage struct {
	User   *auth.UserSession
	Vessel *VesselView // nil = criação; preenchido = edição
	Error  string
}

// PortCallInfo agrega as informações do port call ativo para exibição no detalhe.
type PortCallInfo struct {
	ID               int64
	Terminal         string
	ETADate          string
	ETATime          string
	ETDDate          string
	ETDTime          string
	StatusLabel      string
	Inspected        bool
	InspectionDate   string
	InspectionResult string // label legível
}

type detailPage struct {
	User            *auth.UserSession
	Vessel          VesselView
	Flash           string
	FlashError      string
	SameNameVessels []VesselView
	ActivePortCall  *PortCallInfo // nil se não houver port call ativo
	Escalas         []EscalaEntry // histórico de escalas do navio
}

// EscalaEntry é o modelo mínimo de escala para a página de detalhe do navio.
// Definido aqui para evitar dependência circular (vessel ← portcall ← vessel).
type EscalaEntry struct {
	ID               int64
	Terminal         string
	ETADate          string
	ETDDate          string
	BerthingDate     string
	DepartureDate    string
	Status           string
	StatusLabel      string
	PrioritySnapshot string
	Inspected        bool
	InspectionResult string
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// List — GET /vessels
// Suporta busca (?q=), filtros (?prioridade=, ?risco=, ?bandeira=) e paginação (?page=).
// Admin com ?all=1 vê todos os navios, incluindo desativados.
func (h *Handler) List(c *gin.Context) {
	user := auth.GetUser(c)
	showAll := user.Role == "admin" && c.Query("all") == "1"
	q := strings.TrimSpace(c.Query("q"))

	var vessels []sqlc.Vessel
	var err error

	// ── Busca global por nome ou IMO ────────────────────────────────────────
	if q != "" {
		imoArg := (*string)(nil)
		if isIMO(q) {
			imoArg = &q
		}
		vessels, err = h.q.SearchVessels(c.Request.Context(), sqlc.SearchVesselsParams{
			NamePattern: "%" + q + "%",
			Imo:         imoArg,
		})
		if err == nil && len(vessels) == 1 {
			c.Redirect(http.StatusFound, fmt.Sprintf("/vessels/%d", vessels[0].ID))
			return
		}
	} else if showAll {
		vessels, err = h.q.ListAllVessels(c.Request.Context())
	} else {
		vessels, err = h.q.ListVessels(c.Request.Context())
	}

	if err != nil {
		c.HTML(http.StatusInternalServerError, "vessel_list.html", listPage{
			User:  user,
			Flash: "Erro ao carregar navios: " + err.Error(),
		})
		return
	}

	views := ToViews(vessels)
	totalAll := len(views)

	// ── Filtros avançados (aplicados antes da paginação) ────────────────────
	filter := VesselFilter{
		Prioridade: c.Query("prioridade"),
		Risco:      c.Query("risco"),
		Bandeira:   c.Query("bandeira"),
	}
	if q == "" {
		views = filter.Apply(views)
	}

	// ── Paginação in-memory ─────────────────────────────────────────────────
	pg := pagination.FromQuery(c, len(views), pagination.DefaultPageSize)
	start, end := pagination.Slice(len(views), pg.Offset, pg.PageSize)
	views = views[start:end]

	c.HTML(http.StatusOK, "vessel_list.html", listPage{
		User:     user,
		Vessels:  views,
		Flash:    c.Query("flash"),
		ShowAll:  showAll,
		Query:    q,
		TotalAll: totalAll,
		Page:     pg,
		Filter:   filter,
	})
}

// isIMO retorna true se o termo parece um número IMO (7 dígitos numéricos).
func isIMO(s string) bool {
	if len(s) != 7 {
		return false
	}
	return strings.TrimLeft(s, "0123456789") == ""
}

// NewForm — GET /vessels/new
func (h *Handler) NewForm(c *gin.Context) {
	c.HTML(http.StatusOK, "vessel_form.html", formPage{
		User: auth.GetUser(c),
	})
}

// Create — POST /vessels
func (h *Handler) Create(c *gin.Context) {
	params := sqlc.CreateVesselParams{
		Imo:        parseOptionalString(c.PostForm("imo")),
		Name:       c.PostForm("name"),
		Flag:       parseOptionalString(c.PostForm("flag")),
		YearBuilt:  parseOptionalInt32(c.PostForm("year_built")),
		VesselType: parseOptionalString(c.PostForm("vessel_type")),
		LengthM:    parseNumeric(c.PostForm("length_m")),
		BeamM:      parseNumeric(c.PostForm("beam_m")),
		Afretado:   c.PostForm("afretado") == "true",
	}

	if params.Name == "" || params.Flag == nil || *params.Flag == "" {
		c.HTML(http.StatusBadRequest, "vessel_form.html", formPage{
			User:  auth.GetUser(c),
			Error: "Nome e Bandeira são obrigatórios no cadastro manual.",
		})
		return
	}
	if params.Imo != nil && *params.Imo != "" {
		if err := ValidateIMO(*params.Imo); err != nil {
			c.HTML(http.StatusBadRequest, "vessel_form.html", formPage{
				User:  auth.GetUser(c),
				Error: "IMO inválido: " + err.Error(),
			})
			return
		}
	}

	_, err := h.q.CreateVessel(c.Request.Context(), params)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.HTML(http.StatusConflict, "vessel_form.html", formPage{
				User:  auth.GetUser(c),
				Error: "Este IMO já está cadastrado.",
			})
			return
		}
		c.HTML(http.StatusInternalServerError, "vessel_form.html", formPage{
			User:  auth.GetUser(c),
			Error: "Erro ao salvar: " + err.Error(),
		})
		return
	}

	c.Redirect(http.StatusFound, "/vessels?flash=Navio+cadastrado+com+sucesso")
}

// Detail — GET /vessels/:id
func (h *Handler) Detail(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	v, err := h.q.GetVessel(c.Request.Context(), id)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	page := detailPage{
		User:       auth.GetUser(c),
		Vessel:     ToView(v),
		Flash:      c.Query("flash"),
		FlashError: c.Query("error"),
	}

	// Carrega port call ativo (se existir) com status de inspeção.
	if pc, err := h.q.GetActivePortCallByVessel(c.Request.Context(), id); err == nil {
		pci := &PortCallInfo{
			ID:          pc.ID,
			Terminal:    derefStr(pc.Terminal),
			ETADate:     fmtPgDate(pc.EtaDate),
			ETATime:     fmtPgTime(pc.EtaTime),
			ETDDate:     fmtPgDate(pc.EtdDate),
			ETDTime:     fmtPgTime(pc.EtdTime),
			StatusLabel: simplifyStatus(pc.VesselStatus),
		}
		if ins, err := h.q.GetInspectionByPortCall(c.Request.Context(), pc.ID); err == nil {
			pci.Inspected = true
			pci.InspectionDate = fmtPgDate(ins.InspectionDate)
			pci.InspectionResult = resultLabel(ins.Result)
		}
		page.ActivePortCall = pci
	}

	// Carrega histórico de escalas do navio.
	if escalas, err := h.q.ListEscalasByVessel(c.Request.Context(), id); err == nil {
		for _, e := range escalas {
			entry := EscalaEntry{
				ID:           e.ID,
				Terminal:     derefStr(e.Terminal),
				Status:       e.PortCallStatus,
				StatusLabel:  escalaStatusLabel(e.PortCallStatus),
				Inspected:    e.InspectionID != nil,
			}
			if e.PrioritySnapshot != nil {
				entry.PrioritySnapshot = *e.PrioritySnapshot
			}
			if e.InspectionResult != nil {
				entry.InspectionResult = *e.InspectionResult
			}
			if e.EtaDate.Valid {
				entry.ETADate = e.EtaDate.Time.Format("02/01/2006")
			}
			if e.EtdDate.Valid {
				entry.ETDDate = e.EtdDate.Time.Format("02/01/2006")
			}
			if e.ActualArrival.Valid {
				entry.BerthingDate = e.ActualArrival.Time.Format("02/01/2006")
			}
			if e.ActualDeparture.Valid {
				entry.DepartureDate = e.ActualDeparture.Time.Format("02/01/2006")
			}
			page.Escalas = append(page.Escalas, entry)
		}
	}

	// Para admins, carrega vessels com o mesmo nome para o painel de mesclar.
	if auth.GetUser(c).Role == "admin" {
		candidates, _ := h.q.GetVesselsByNameExcludingSelf(c.Request.Context(),
			sqlc.GetVesselsByNameExcludingSelfParams{Name: v.Name, SelfID: id})
		page.SameNameVessels = ToViews(candidates)
	}

	c.HTML(http.StatusOK, "vessel_detail.html", page)
}

// EditForm — GET /vessels/:id/edit
func (h *Handler) EditForm(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	v, err := h.q.GetVessel(c.Request.Context(), id)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	view := ToView(v)
	c.HTML(http.StatusOK, "vessel_form.html", formPage{
		User:   auth.GetUser(c),
		Vessel: &view,
	})
}

// Update — POST /vessels/:id
func (h *Handler) Update(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	params := sqlc.UpdateVesselParams{
		ID:         id,
		Name:       c.PostForm("name"),
		Flag:       parseOptionalString(c.PostForm("flag")),
		YearBuilt:  parseOptionalInt32(c.PostForm("year_built")),
		VesselType: parseOptionalString(c.PostForm("vessel_type")),
		LengthM:    parseNumeric(c.PostForm("length_m")),
		BeamM:      parseNumeric(c.PostForm("beam_m")),
		Afretado:    c.PostForm("afretado") == "true",
		Acompanhado: c.PostForm("nao_acompanhado") != "true", // checkbox marcado → não acompanhado
		Imo:        parseOptionalString(c.PostForm("imo")),
	}

	if params.Name == "" {
		v, _ := h.q.GetVessel(c.Request.Context(), id)
		view := ToView(v)
		c.HTML(http.StatusBadRequest, "vessel_form.html", formPage{
			User:   auth.GetUser(c),
			Vessel: &view,
			Error:  "Nome é obrigatório.",
		})
		return
	}
	if params.Imo != nil && *params.Imo != "" {
		if imoErr := ValidateIMO(*params.Imo); imoErr != nil {
			v, _ := h.q.GetVessel(c.Request.Context(), id)
			view := ToView(v)
			c.HTML(http.StatusBadRequest, "vessel_form.html", formPage{
				User:   auth.GetUser(c),
				Vessel: &view,
				Error:  "IMO inválido: " + imoErr.Error(),
			})
			return
		}
	}

	_, err = h.q.UpdateVessel(c.Request.Context(), params)
	if err != nil {
		v2, _ := h.q.GetVessel(c.Request.Context(), id)
		view2 := ToView(v2)
		errMsg := "Erro ao atualizar: " + err.Error()
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			errMsg = "Este IMO já está cadastrado em outro navio."
		}
		c.HTML(http.StatusBadRequest, "vessel_form.html", formPage{
			User:   auth.GetUser(c),
			Vessel: &view2,
			Error:  errMsg,
		})
		return
	}

	c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(id, 10)+"?flash=Navio+atualizado+com+sucesso")
}

// Deactivate — POST /vessels/:id/deactivate
func (h *Handler) Deactivate(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	if err := h.q.DeactivateVessel(c.Request.Context(), id); err != nil {
		c.Redirect(http.StatusFound, "/vessels?flash=Erro+ao+desativar+navio")
		return
	}

	c.Redirect(http.StatusFound, "/vessels?flash=Navio+desativado")
}

// MergeVessel — POST /vessels/:id/merge
// Transfere todos os port calls deste vessel para o vessel alvo, depois deleta este.
// Resolve duplicatas sem perder histórico.
func (h *Handler) MergeVessel(c *gin.Context) {
	sourceID, err := parseID(c)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	targetID, err := strconv.ParseInt(c.PostForm("target_id"), 10, 64)
	if err != nil || targetID == sourceID {
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(sourceID, 10))
		return
	}

	ctx := c.Request.Context()

	// 1. Transfere todos os port calls do source para o target.
	if err := h.q.ReassignPortCalls(ctx, sqlc.ReassignPortCallsParams{
		SourceID: sourceID,
		TargetID: targetID,
	}); err != nil {
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(sourceID, 10)+"?error=Erro+ao+transferir+port+calls")
		return
	}

	// 2. Deleta o vessel fonte (agora sem port calls, a FK deixa passar).
	if err := h.q.DeleteVessel(ctx, sourceID); err != nil {
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(targetID, 10)+
			"?flash=Port+calls+transferidos.+Delete+manualmente+o+navio+fonte+(ID+"+strconv.FormatInt(sourceID, 10)+")")
		return
	}

	c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(targetID, 10)+"?flash=Navios+mesclados+com+sucesso")
}

// Delete — POST /vessels/:id/delete
// Hard delete: remove o vessel do banco. Só permitido se:
//   - O vessel NÃO for desativado (use Desativar para isso)
//   - O vessel NÃO tiver port calls (FK RESTRICT impede; retornamos erro legível)
func (h *Handler) Delete(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	v, err := h.q.GetVessel(c.Request.Context(), id)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	// Vessel não-acompanhado não pode ser deletado se tiver histórico.
	if !v.Acompanhado {
		view := ToView(v)
		c.HTML(http.StatusBadRequest, "vessel_detail.html", detailPage{
			User:       auth.GetUser(c),
			Vessel:     view,
			FlashError: "Navios não-acompanhados não podem ser deletados. Reative-o primeiro se necessário.",
		})
		return
	}

	if err := h.q.DeleteVessel(c.Request.Context(), id); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			view := ToView(v)
			c.HTML(http.StatusConflict, "vessel_detail.html", detailPage{
				User:       auth.GetUser(c),
				Vessel:     view,
				FlashError: "Este navio possui port calls registrados e não pode ser deletado. Use Desativar ou Mesclar.",
			})
			return
		}
		c.Redirect(http.StatusFound, "/vessels?flash=Erro+ao+deletar+navio")
		return
	}

	c.Redirect(http.StatusFound, "/vessels?flash=Navio+deletado+permanentemente")
}

// parseID extrai e converte o parâmetro :id da URL.
func parseID(c *gin.Context) (int64, error) {
	return strconv.ParseInt(c.Param("id"), 10, 64)
}

func escalaStatusLabel(s string) string {
	switch s {
	case "planned":
		return "Planejada"
	case "active":
		return "Atracado"
	case "completed":
		return "Concluída"
	case "aborted":
		return "Cancelada"
	}
	return s
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func fmtPgDate(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("02/01/2006")
}

func fmtPgTime(t pgtype.Time) string {
	if !t.Valid {
		return "TBC"
	}
	total := t.Microseconds / 1_000_000
	h := total / 3600
	m := (total % 3600) / 60
	return fmt.Sprintf("%02d:%02d", h, m)
}

func simplifyStatus(s string) string {
	if s == "berthed" {
		return "Atracado"
	}
	return "A caminho"
}

func resultLabel(r string) string {
	switch r {
	case "no_deficiencies":
		return "Sem Deficiências"
	case "deficiencies":
		return "Com Deficiências (liberado)"
	case "detained":
		return "Detido"
	}
	return r
}

// Referência para evitar erro de import não usado.
var _ = pgx.ErrNoRows
