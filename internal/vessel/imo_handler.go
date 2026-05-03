package vessel

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/scraper"
)

// IMOFinder agrega a lógica de busca de IMO via VesselFinder.
type IMOFinder struct {
	q               *sqlc.Queries
	vesselFinderURL string
}

// NewIMOFinder cria o finder.
func NewIMOFinder(q *sqlc.Queries, vesselFinderURL string) *IMOFinder {
	return &IMOFinder{q: q, vesselFinderURL: vesselFinderURL}
}

// FindForVessel — POST /vessels/:id/find-imo
// Busca o IMO de um navio específico no VesselFinder e atualiza o banco.
func (f *IMOFinder) FindForVessel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	v, err := f.q.GetVessel(c.Request.Context(), id)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	if v.Imo != nil {
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(id, 10)+"?flash=Este+navio+já+tem+IMO+registrado")
		return
	}

	imo, err := scraper.FindIMO(c.Request.Context(), f.vesselFinderURL, v.Name,
		numericToFloat(v.LengthM), numericToFloat(v.BeamM))
	if err != nil {
		slog.Error("falha ao buscar IMO", "component", "imo-handler", "vessel", v.Name, "error", err)
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(id, 10)+
			"?error="+url.QueryEscape("IMO não encontrado: "+err.Error()))
		return
	}

	if err := f.q.UpdateVesselIMO(c.Request.Context(), sqlc.UpdateVesselIMOParams{
		ID:  id,
		Imo: &imo,
	}); err != nil {
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(id, 10)+
			"?error="+url.QueryEscape("Erro ao salvar IMO: "+err.Error()))
		return
	}

	c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(id, 10)+
		"?flash="+url.QueryEscape(fmt.Sprintf("IMO %s encontrado e registrado", imo)))
}

// BatchProgressPage — GET /vessels/find-imo
// Exibe a página de progresso da busca de IMO em lote.
func (f *IMOFinder) BatchProgressPage(c *gin.Context) {
	vessels, err := f.q.ListVesselsWithoutIMO(c.Request.Context())
	total := 0
	if err == nil {
		total = len(vessels)
	}
	c.HTML(http.StatusOK, "imo_progress.html", gin.H{
		"User":  auth.GetUser(c),
		"Total": total,
	})
}

// BatchStream — GET /vessels/find-imo/stream
// SSE stream que processa navios sem IMO um a um e envia eventos de progresso.
func (f *IMOFinder) BatchStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // desativa buffer do nginx se houver

	send := func(event, data string) {
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
		c.Writer.Flush()
	}

	vessels, err := f.q.ListVesselsWithoutIMO(c.Request.Context())
	if err != nil {
		send("error", "Erro ao carregar navios: "+err.Error())
		return
	}

	if len(vessels) == 0 {
		send("done", "Nenhum navio sem IMO com port call ativo.")
		return
	}

	send("start", strconv.Itoa(len(vessels)))

	found, failed := 0, 0
	for _, v := range vessels {
		// Verifica se o cliente ainda está conectado.
		if c.Request.Context().Err() != nil {
			return
		}

		send("searching", v.Name)

		// Delay respeitoso entre requisições ao VesselFinder.
		select {
		case <-c.Request.Context().Done():
			return
		case <-time.After(500 * time.Millisecond):
		}

		imo, err := scraper.FindIMO(c.Request.Context(), f.vesselFinderURL, v.Name,
			numericToFloat(v.LengthM), numericToFloat(v.BeamM))
		if err != nil {
			slog.Error("falha ao buscar IMO em lote", "component", "imo-handler", "vessel", v.Name, "error", err)
			send("fail", v.Name+"|"+err.Error())
			failed++
			continue
		}

		if err := f.q.UpdateVesselIMO(c.Request.Context(), sqlc.UpdateVesselIMOParams{
			ID:  v.ID,
			Imo: &imo,
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				msg := "IMO " + imo + " já existe em outro cadastro"
				if existing, lookupErr := f.q.GetVesselByIMO(c.Request.Context(), &imo); lookupErr == nil {
					msg += ": " + existing.Name + " (ID " + strconv.FormatInt(existing.ID, 10) + ") — use Mesclar"
				}
				send("fail", v.Name+"|"+msg)
			} else {
				send("fail", v.Name+"|erro ao salvar: "+err.Error())
			}
			failed++
			continue
		}

		send("found", v.Name+"|"+imo)
		found++
	}

	send("done", fmt.Sprintf("%d encontrado(s) · %d não encontrado(s)", found, failed))
}

// numericToFloat converte pgtype.Numeric em float64, retornando 0 se inválido.
func numericToFloat(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}
