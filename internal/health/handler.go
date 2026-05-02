// Package health fornece o painel de saúde do sistema para admins.
package health

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
)

// EmailTester é uma interface mínima para envio de e-mail de teste.
type EmailTester interface {
	SendTest() error
}

// Handler serve o painel /admin/health.
type Handler struct {
	q       *sqlc.Queries
	mailer  EmailTester // nil = SMTP não configurado
}

// NewHandler cria o handler de saúde.
func NewHandler(q *sqlc.Queries) *Handler {
	return &Handler{q: q}
}

// WithMailer anexa o testador de e-mail ao handler.
func (h *Handler) WithMailer(m EmailTester) *Handler {
	h.mailer = m
	return h
}

// ScraperCard é a view-model de um scraper no painel.
type ScraperCard struct {
	Name       string
	Label      string
	Status     string // "success" | "partial" | "error" | "never"
	LastRun    string
	Duration   string
	Processed  int32
	Failed     int32
	ErrorMsg   string
}

// knownScrapers define a ordem e labels dos scrapers conhecidos.
var knownScrapers = []struct{ id, label string }{
	{"zp21", "ZP-21 (Manobras)"},
	{"auto_imo", "Auto-IMO (VesselFinder)"},
	{"auto_ciala", "Auto-CIALA (Risco PSC)"},
}

// Show — GET /admin/health
func (h *Handler) Show(c *gin.Context) {
	ctx := c.Request.Context()

	latestRuns, _ := h.q.GetLatestRunPerScraper(ctx)
	recentRuns, _ := h.q.ListRecentRuns(ctx)

	// Indexa última execução por nome de scraper.
	latestByName := make(map[string]sqlc.ScraperRun)
	for _, r := range latestRuns {
		latestByName[r.Scraper] = r
	}

	cards := make([]ScraperCard, 0, len(knownScrapers))
	for _, s := range knownScrapers {
		card := ScraperCard{Name: s.id, Label: s.label, Status: "never"}
		if run, ok := latestByName[s.id]; ok {
			card.Status = run.Status
			card.LastRun = FormatTS(run.StartedAt)
			card.Duration = FormatDuration(run.DurationMs)
			card.Processed = run.ItemsProcessed
			card.Failed = run.ItemsFailed
			if run.ErrorMessage != nil {
				card.ErrorMsg = *run.ErrorMessage
			}
		}
		cards = append(cards, card)
	}

	flash := c.Query("flash")
	errMsg := c.Query("error")

	c.HTML(http.StatusOK, "health.html", gin.H{
		"User":          auth.GetUser(c),
		"Cards":         cards,
		"RecentRuns":    recentRuns,
		"SMTPReady":     h.mailer != nil,
		"Flash":         flash,
		"Error":         errMsg,
	})
}

// TestEmail — POST /admin/health/test-email
// Envia um e-mail de teste para verificar a configuração SMTP.
func (h *Handler) TestEmail(c *gin.Context) {
	if h.mailer == nil {
		c.Redirect(http.StatusFound, "/admin/health?error=SMTP+não+configurado+no+.env")
		return
	}
	if err := h.mailer.SendTest(); err != nil {
		c.Redirect(http.StatusFound, "/admin/health?error=Falha+ao+enviar:+"+url.QueryEscape(err.Error()))
		return
	}
	c.Redirect(http.StatusFound, "/admin/health?flash=E-mail+de+teste+enviado+com+sucesso!")
}

// FormatTS formata um timestamp para exibição em templates.
func FormatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return "—"
	}
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	return ts.Time.In(loc).Format("02/01 15:04:05")
}

// FormatDuration converte milissegundos em string legível para templates.
func FormatDuration(ms int32) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	m := ms / 60000
	s := (ms % 60000) / 1000
	return fmt.Sprintf("%dm %ds", m, s)
}
