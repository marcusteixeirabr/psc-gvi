package vessel

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
)

// Handler gerencia as requisições HTTP de navios.
// Recebe *sqlc.Queries (gerado pelo sqlc) — o objeto que tem todos os métodos de banco.
type Handler struct {
	q *sqlc.Queries
}

// NewHandler cria o handler injetando as queries do banco.
func NewHandler(q *sqlc.Queries) *Handler {
	return &Handler{q: q}
}

// ── Modelos de página ─────────────────────────────────────────────────────────
// Cada template recebe uma struct específica — evita map[string]any genérico.

type listPage struct {
	User    *auth.UserSession
	Vessels []VesselView
	Flash   string // mensagem de sucesso/erro após redirect
}

type formPage struct {
	User    *auth.UserSession
	Vessel  *VesselView // nil = formulário de criação; preenchido = edição
	Error   string
}

type detailPage struct {
	User   *auth.UserSession
	Vessel VesselView
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// List — GET /vessels
func (h *Handler) List(c *gin.Context) {
	vessels, err := h.q.ListVessels(c.Request.Context())
	if err != nil {
		c.HTML(http.StatusInternalServerError, "vessel_list.html", listPage{
			User:  auth.GetUser(c),
			Flash: "Erro ao carregar navios: " + err.Error(),
		})
		return
	}
	c.HTML(http.StatusOK, "vessel_list.html", listPage{
		User:    auth.GetUser(c),
		Vessels: ToViews(vessels),
		Flash:   c.Query("flash"),
	})
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
		Imo:        c.PostForm("imo"),
		Name:       c.PostForm("name"),
		Flag:       parseOptionalString(c.PostForm("flag")),
		YearBuilt:  parseOptionalInt32(c.PostForm("year_built")),
		VesselType: parseOptionalString(c.PostForm("vessel_type")),
		LengthM:    parseNumeric(c.PostForm("length_m")),
		BeamM:      parseNumeric(c.PostForm("beam_m")),
	}

	if params.Imo == "" || params.Name == "" {
		c.HTML(http.StatusBadRequest, "vessel_form.html", formPage{
			User:  auth.GetUser(c),
			Error: "IMO e Nome são obrigatórios.",
		})
		return
	}

	_, err := h.q.CreateVessel(c.Request.Context(), params)
	if err != nil {
		// pgconn.PgError código 23505 = violação de constraint UNIQUE (IMO duplicado).
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.HTML(http.StatusConflict, "vessel_form.html", formPage{
				User:  auth.GetUser(c),
				Error: "IMO " + params.Imo + " já está cadastrado.",
			})
			return
		}
		c.HTML(http.StatusInternalServerError, "vessel_form.html", formPage{
			User:  auth.GetUser(c),
			Error: "Erro ao salvar: " + err.Error(),
		})
		return
	}

	// Redirect-after-POST: evita reenvio do formulário ao pressionar F5.
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

	c.HTML(http.StatusOK, "vessel_detail.html", detailPage{
		User:   auth.GetUser(c),
		Vessel: ToView(v),
	})
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

	_, err = h.q.UpdateVessel(c.Request.Context(), params)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "vessel_form.html", formPage{
			User:  auth.GetUser(c),
			Error: "Erro ao atualizar: " + err.Error(),
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

// parseID extrai e converte o parâmetro :id da URL.
func parseID(c *gin.Context) (int64, error) {
	return strconv.ParseInt(c.Param("id"), 10, 64)
}
