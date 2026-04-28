// Package inspection gerencia o registro de inspeções PSC.
package inspection

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
)

// Handler serve as rotas de inspeção.
type Handler struct {
	q *sqlc.Queries
}

// NewHandler cria o handler.
func NewHandler(q *sqlc.Queries) *Handler {
	return &Handler{q: q}
}

type formPage struct {
	User        *auth.UserSession
	VesselID    int64
	VesselName  string
	PortCallID  int64
	Terminal    string
	ETADate     string
	ETDDate     string
	DefaultDate string // pré-preenche a data de hoje
	Error       string
}

// ShowForm — GET /vessels/:id/inspect
// Busca o port call ativo e exibe o formulário de inspeção.
func (h *Handler) ShowForm(c *gin.Context) {
	vesselID, err := parseID(c)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	vessel, err := h.q.GetVessel(c.Request.Context(), vesselID)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	pc, err := h.q.GetActivePortCallByVessel(c.Request.Context(), vesselID)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(vesselID, 10)+"?flash=Nenhum+port+call+ativo+para+este+navio")
		return
	}

	c.HTML(http.StatusOK, "inspection_form.html", formPage{
		User:        auth.GetUser(c),
		VesselID:    vesselID,
		VesselName:  vessel.Name,
		PortCallID:  pc.ID,
		Terminal:    derefStr(pc.Terminal),
		ETADate:     fmtDate(pc.EtaDate),
		ETDDate:     fmtDate(pc.EtdDate),
		DefaultDate: time.Now().Format("2006-01-02"),
	})
}

// Register — POST /vessels/:id/inspect
// Cria o registro de inspeção e atualiza last_inspection_date do vessel.
func (h *Handler) Register(c *gin.Context) {
	vesselID, err := parseID(c)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels")
		return
	}

	portCallID, err := strconv.ParseInt(c.PostForm("port_call_id"), 10, 64)
	if err != nil {
		c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(vesselID, 10))
		return
	}

	dateStr := strings.TrimSpace(c.PostForm("inspection_date"))
	result := strings.TrimSpace(c.PostForm("result"))
	notes := strings.TrimSpace(c.PostForm("notes"))

	if dateStr == "" || result == "" {
		showFormWithError(c, h, vesselID, portCallID, "Data e resultado são obrigatórios.")
		return
	}

	inspDate, err := parseDate(dateStr)
	if err != nil {
		showFormWithError(c, h, vesselID, portCallID, "Data inválida.")
		return
	}

	var notesPtr *string
	if notes != "" {
		notesPtr = &notes
	}

	ctx := c.Request.Context()

	_, err = h.q.CreateInspection(ctx, sqlc.CreateInspectionParams{
		PortCallID:     portCallID,
		InspectionDate: inspDate,
		Result:         result,
		Notes:          notesPtr,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			showFormWithError(c, h, vesselID, portCallID, "Este port call já possui uma inspeção registrada.")
			return
		}
		showFormWithError(c, h, vesselID, portCallID, "Erro ao salvar inspeção: "+err.Error())
		return
	}

	// Atualiza last_inspection_date do vessel.
	_ = h.q.UpdateVesselLastInspectionDate(ctx, sqlc.UpdateVesselLastInspectionDateParams{
		ID:                 vesselID,
		LastInspectionDate: inspDate,
	})

	c.Redirect(http.StatusFound, "/vessels/"+strconv.FormatInt(vesselID, 10)+"?flash=Inspeção+registrada+com+sucesso")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func showFormWithError(c *gin.Context, h *Handler, vesselID, portCallID int64, msg string) {
	vessel, _ := h.q.GetVessel(c.Request.Context(), vesselID)
	pc, _ := h.q.GetActivePortCallByVessel(c.Request.Context(), vesselID)
	page := formPage{
		User:        auth.GetUser(c),
		VesselID:    vesselID,
		VesselName:  vessel.Name,
		PortCallID:  portCallID,
		DefaultDate: time.Now().Format("2006-01-02"),
		Error:       msg,
	}
	if pc.ID != 0 {
		page.Terminal = derefStr(pc.Terminal)
		page.ETADate = fmtDate(pc.EtaDate)
		page.ETDDate = fmtDate(pc.EtdDate)
	}
	c.HTML(http.StatusBadRequest, "inspection_form.html", page)
}

func parseID(c *gin.Context) (int64, error) {
	return strconv.ParseInt(c.Param("id"), 10, 64)
}

func parseDate(s string) (pgtype.Date, error) {
	var d pgtype.Date
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return d, err
	}
	_ = d.Scan(t)
	return d, nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func fmtDate(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("02/01/2006")
}

// ResultLabel converte o valor do banco em texto legível.
func ResultLabel(r string) string {
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

// Ensure pgx.ErrNoRows is imported (used by callers).
var _ = pgx.ErrNoRows
