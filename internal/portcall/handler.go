package portcall

import (
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/scraper"
)

// Handler serve as rotas HTTP de port calls.
type Handler struct {
	q       *sqlc.Queries
	zp21URL string
}

// NewHandler cria o handler injetando as queries e a URL do ZP-21.
func NewHandler(q *sqlc.Queries, zp21URL string) *Handler {
	return &Handler{q: q, zp21URL: zp21URL}
}

// ScrapeZP21 — POST /scrape/zp21
// Busca as Manobras Previstas do ZP-21 e atualiza vessels e port calls no banco.
// Redireciona de volta para o Referer (dashboard) ou para / se não houver Referer.
func (h *Handler) ScrapeZP21(c *gin.Context) {
	// Determina para onde voltar — usa Referer para manter a página atual.
	back := c.Request.Referer()
	if back == "" || back == c.Request.URL.String() {
		back = "/"
	}
	// Garante que o flash vai junto, sem duplicar query params existentes.
	addFlash := func(msg string) string {
		sep := "?"
		if len(back) > 0 && back[len(back)-1] != '/' {
			sep = "&"
		}
		if containsQuery(back) {
			sep = "&"
		}
		return back + sep + "flash=" + url.QueryEscape(msg)
	}

	rows, err := scraper.FetchManobras(c.Request.Context(), h.zp21URL)
	if err != nil {
		c.Redirect(http.StatusFound, addFlash("Erro ao buscar ZP-21: "+err.Error()))
		return
	}

	result := ProcessManobras(c.Request.Context(), h.q, rows, nil)
	c.Redirect(http.StatusFound, addFlash("ZP-21: "+result.Summary()))
}

func containsQuery(u string) bool {
	for _, ch := range u {
		if ch == '?' {
			return true
		}
	}
	return false
}
