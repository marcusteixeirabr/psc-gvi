package portcall

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/pagination"
	"github.com/marcusteixeirabr/psc-gvi/internal/vessel"
)

// EscalaView é o modelo de apresentação de uma escala para o template.
type EscalaView struct {
	ID               int64
	VesselID         int64
	VesselName       string
	VesselIMO        string
	VesselFlag       string
	Terminal         string
	ETADate          string // previsão chegada
	ETDDate          string // previsão saída
	BerthingDate     string // atracação real (actual_arrival) — DD/MM/YYYY
	BerthingDateISO  string // atracação real — YYYY-MM-DD para input[type=date]
	DepartureDate    string // suspensão real (actual_departure) — DD/MM/YYYY
	DepartureDateISO string // suspensão real — YYYY-MM-DD para input[type=date]
	Status           string // port_call_status
	StatusLabel      string
	RiskLevel        string // grau de risco atual (ou snapshot se concluída)
	Priority         string // prioridade calculada (ou snapshot se concluída)
	Inspected        bool
	InspectionResult string
}

// EscalaHandler gerencia a página de escalas.
type EscalaHandler struct {
	q *sqlc.Queries
}

// NewEscalaHandler cria o handler.
func NewEscalaHandler(q *sqlc.Queries) *EscalaHandler {
	return &EscalaHandler{q: q}
}

// NewForm — GET /escalas/new
// Exibe o formulário de criação manual de escala.
func (h *EscalaHandler) NewForm(c *gin.Context) {
	vessels, err := h.q.ListAllVessels(c.Request.Context())
	if err != nil {
		c.HTML(http.StatusInternalServerError, "escala_new.html", gin.H{
			"User":  auth.GetUser(c),
			"Error": "Erro ao carregar navios: " + err.Error(),
		})
		return
	}
	c.HTML(http.StatusOK, "escala_new.html", gin.H{
		"User":    auth.GetUser(c),
		"Vessels": vessel.ToViews(vessels),
		"Error":   c.Query("error"),
	})
}

// NewSave — POST /escalas/new
// Cria uma escala manualmente. Bloqueia se o navio já tiver escala ativa/planejada.
func (h *EscalaHandler) NewSave(c *gin.Context) {
	vesselIDStr := c.PostForm("vessel_id")
	vesselID, err := strconv.ParseInt(vesselIDStr, 10, 64)
	if err != nil || vesselID == 0 {
		c.Redirect(http.StatusFound, "/escalas/new?error="+url.QueryEscape("Selecione um navio"))
		return
	}

	// Guard: impede criação se já existir escala ativa ou planejada para o navio.
	existing, err := h.q.GetActivePortCallByVessel(c.Request.Context(), vesselID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		c.Redirect(http.StatusFound, "/escalas/new?error="+url.QueryEscape("Erro ao verificar escalas existentes"))
		return
	}
	if err == nil {
		msg := fmt.Sprintf("Este navio já possui escala %s (ID #%d). Edite a escala existente.",
			escalaStatusLabel(existing.PortCallStatus), existing.ID)
		c.Redirect(http.StatusFound, "/escalas/new?error="+url.QueryEscape(msg))
		return
	}

	terminalVal := strings.TrimSpace(c.PostForm("terminal"))
	var terminal *string
	if terminalVal != "" {
		terminal = &terminalVal
	}

	etaDate := parsePgDateOptional(c.PostForm("eta_date"))
	etdDate := parsePgDateOptional(c.PostForm("etd_date"))
	arrivalTS := parseTsOptional(c.PostForm("actual_arrival"))

	pcStatus := "planned"
	vesselStatus := "navigating"
	if arrivalTS.Valid {
		pcStatus = "active"
		vesselStatus = "berthed"
	}

	pc, err := h.q.CreatePortCall(c.Request.Context(), sqlc.CreatePortCallParams{
		VesselID:       vesselID,
		Terminal:       terminal,
		VesselStatus:   vesselStatus,
		PortCallStatus: pcStatus,
		EtaDate:        etaDate,
		EtdDate:        etdDate,
		Zp21Sourced:    false,
	})
	if err != nil {
		c.Redirect(http.StatusFound, "/escalas/new?error="+url.QueryEscape("Erro ao criar escala: "+err.Error()))
		return
	}

	// Se atracação foi informada, registra via UpdateBerthingDate para manter a hierarquia.
	if arrivalTS.Valid {
		_ = h.q.RegisterBerthing(c.Request.Context(), sqlc.RegisterBerthingParams{
			ID:            pc.ID,
			ActualArrival: arrivalTS,
		})
	}

	c.Redirect(http.StatusFound, fmt.Sprintf("/escalas?flash=%s",
		url.QueryEscape(fmt.Sprintf("Escala #%d criada com sucesso", pc.ID))))
}

// List — GET /escalas
func (h *EscalaHandler) List(c *gin.Context) {
	now := time.Now()

	statusFilter := c.DefaultQuery("status", "")
	monthStr := c.DefaultQuery("mes", fmt.Sprintf("%d-%02d", now.Year(), now.Month()))
	year, month := parseYearMonth(monthStr)

	vesselIDStr := c.DefaultQuery("vessel_id", "0")
	vesselID, _ := strconv.ParseInt(vesselIDStr, 10, 64)

	params := sqlc.CountEscalasParams{
		Status:   statusFilter,
		VesselID: vesselID,
		Year:     int32(year),
		Month:    int32(month),
	}

	total, err := h.q.CountEscalas(c.Request.Context(), params)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "escalas.html", gin.H{"Error": err.Error()})
		return
	}

	pg := pagination.FromQuery(c, int(total), pagination.DefaultPageSize)

	rows, err := h.q.ListEscalasPaged(c.Request.Context(), sqlc.ListEscalasPagedParams{
		Status:   statusFilter,
		VesselID: vesselID,
		Year:     int32(year),
		Month:    int32(month),
		Offset:   int32(pg.Offset),
	})
	if err != nil {
		c.HTML(http.StatusInternalServerError, "escalas.html", gin.H{"Error": err.Error()})
		return
	}

	entries := make([]EscalaView, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, toEscalaView(r))
	}

	months := buildMonthOptions(now)

	c.HTML(http.StatusOK, "escalas.html", gin.H{
		"User":      auth.GetUser(c),
		"Entries":   entries,
		"MesSel":    monthStr,
		"Months":    months,
		"StatusSel": statusFilter,
		"VesselID":  vesselID,
		"Flash":     c.Query("flash"),
		"Error":     c.Query("error"),
		"Page":      pg,
	})
}

// RegisterBerthing — POST /escalas/:id/berthing
// Registra a data de atracação efetiva.
func (h *EscalaHandler) RegisterBerthing(c *gin.Context) {
	id := parseID(c)
	back := referer(c, "/escalas")

	dateStr := strings.TrimSpace(c.PostForm("berthing_date"))
	if dateStr == "" {
		c.Redirect(http.StatusFound, back+"&error="+url.QueryEscape("Data de atracação obrigatória"))
		return
	}
	ts, err := parseDateToTimestamptz(dateStr)
	if err != nil {
		c.Redirect(http.StatusFound, back+"&error="+url.QueryEscape("Data inválida: "+dateStr))
		return
	}

	if err := h.q.RegisterBerthing(c.Request.Context(), sqlc.RegisterBerthingParams{
		ID:            id,
		ActualArrival: ts,
	}); err != nil {
		c.Redirect(http.StatusFound, back+"&error="+url.QueryEscape("Erro ao registrar atracação: "+err.Error()))
		return
	}

	c.Redirect(http.StatusFound, back+"&flash="+url.QueryEscape("Atracação registrada em "+dateStr))
}

// RegisterSuspension — POST /escalas/:id/suspension
// Registra a suspensão (partida/desamarre) efetiva e tira o snapshot final.
func (h *EscalaHandler) RegisterSuspension(c *gin.Context) {
	id := parseID(c)
	back := referer(c, "/escalas")

	dateStr := strings.TrimSpace(c.PostForm("suspension_date"))
	if dateStr == "" {
		c.Redirect(http.StatusFound, back+"&error="+url.QueryEscape("Data de suspensão obrigatória"))
		return
	}
	ts, err := parseDateToTimestamptz(dateStr)
	if err != nil {
		c.Redirect(http.StatusFound, back+"&error="+url.QueryEscape("Data inválida: "+dateStr))
		return
	}

	// Busca a escala com dados do navio para calcular o snapshot.
	escala, err := h.q.GetEscala(c.Request.Context(), id)
	if err != nil {
		c.Redirect(http.StatusFound, back+"&error="+url.QueryEscape("Escala não encontrada"))
		return
	}

	// Snapshot: se a inspeção já o capturou (estado pré-inspeção), preserva.
	// Caso contrário calcula agora com os dados atuais do vessel.
	riskSnap := escala.RiskLevelSnapshot
	var prioritySnap string
	if escala.PrioritySnapshot != nil {
		prioritySnap = *escala.PrioritySnapshot
	} else {
		riskSnap = escala.VesselRiskLevel
		prioritySnap = string(vessel.CalcPriority(riskSnap, escala.VesselLastInspectionDate))
	}

	if err := h.q.RegisterDeparture(c.Request.Context(), sqlc.RegisterDepartureParams{
		ID:                  id,
		ActualDeparture:     ts,
		RiskLevelSnapshot:   riskSnap,
		PrioritySnapshot:    &prioritySnap,
	}); err != nil {
		c.Redirect(http.StatusFound, back+"&error="+url.QueryEscape("Erro ao registrar partida: "+err.Error()))
		return
	}

	c.Redirect(http.StatusFound, back+"&flash="+url.QueryEscape("Suspensão registrada em "+dateStr+" — snapshot salvo"))
}

// EditForm — GET /escalas/:id/edit
func (h *EscalaHandler) EditForm(c *gin.Context) {
	id := parseID(c)
	pc, err := h.q.GetPortCallByID(c.Request.Context(), id)
	if err != nil {
		c.Redirect(http.StatusFound, "/escalas")
		return
	}
	c.HTML(http.StatusOK, "escala_form.html", gin.H{
		"User":   auth.GetUser(c),
		"PC":     pc,
		"ID":     id,
		"Error":  c.Query("error"),
	})
}

// EditSave — POST /escalas/:id/edit
func (h *EscalaHandler) EditSave(c *gin.Context) {
	id := parseID(c)

	terminalVal := strings.TrimSpace(c.PostForm("terminal"))
	var terminal *string
	if terminalVal != "" {
		terminal = &terminalVal
	}

	etaDate   := parsePgDateOptional(c.PostForm("eta_date"))
	etdDate   := parsePgDateOptional(c.PostForm("etd_date"))
	arrivalTS := parseTsOptional(c.PostForm("actual_arrival"))
	departTS  := parseTsOptional(c.PostForm("actual_departure"))
	status    := c.PostForm("port_call_status")
	if status == "" {
		status = "planned"
	}

	if err := h.q.UpdatePortCallFull(c.Request.Context(), sqlc.UpdatePortCallFullParams{
		ID:              id,
		Terminal:        terminal,
		EtaDate:         etaDate,
		EtdDate:         etdDate,
		ActualArrival:   arrivalTS,
		ActualDeparture: departTS,
		PortCallStatus:  status,
	}); err != nil {
		c.Redirect(http.StatusFound, fmt.Sprintf("/escalas/%d/edit?error=%s",
			id, url.QueryEscape("Erro ao salvar: "+err.Error())))
		return
	}

	// Volta para a página do navio se o referer for o detalhe do navio.
	ref := c.Request.Referer()
	if strings.Contains(ref, "/vessels/") {
		c.Redirect(http.StatusFound, ref)
		return
	}
	c.Redirect(http.StatusFound, "/escalas?flash="+url.QueryEscape("Escala atualizada"))
}

// DeleteEscala — POST /escalas/:id/delete
func (h *EscalaHandler) DeleteEscala(c *gin.Context) {
	id := parseID(c)
	back := c.Request.Referer()
	if back == "" {
		back = "/escalas"
	}

	if err := h.q.DeletePortCall(c.Request.Context(), id); err != nil {
		// FK violation = tem inspeção vinculada.
		msg := "Erro ao excluir escala"
		if strings.Contains(err.Error(), "23503") || strings.Contains(err.Error(), "foreign key") {
			msg = "Esta escala possui inspeção registrada e não pode ser excluída. Remova a inspeção primeiro."
		}
		c.Redirect(http.StatusFound, back+"?error="+url.QueryEscape(msg))
		return
	}
	c.Redirect(http.StatusFound, back+"?flash="+url.QueryEscape("Escala excluída"))
}

// CancelBerthing — POST /escalas/:id/cancel-berthing
// Cancela a atracação (e a suspensão, se houver). O front-end avisa o usuário antes.
func (h *EscalaHandler) CancelBerthing(c *gin.Context) {
	id := parseID(c)
	back := c.Request.Referer()
	if back == "" {
		back = "/escalas"
	}
	_ = h.q.CancelBerthing(c.Request.Context(), id)
	c.Redirect(http.StatusFound, back)
}

// CancelSuspension — POST /escalas/:id/cancel-suspension
// Cancela apenas a suspensão, mantendo a atracação.
func (h *EscalaHandler) CancelSuspension(c *gin.Context) {
	id := parseID(c)
	back := c.Request.Referer()
	if back == "" {
		back = "/escalas"
	}
	_ = h.q.CancelSuspension(c.Request.Context(), id)
	c.Redirect(http.StatusFound, back)
}

// ListByVessel retorna as escalas de um vessel para a página de detalhe.
func (h *EscalaHandler) ListByVessel(ctx context.Context, vesselID int64) ([]EscalaView, error) {
	rows, err := h.q.ListEscalasByVessel(ctx, vesselID)
	if err != nil {
		return nil, err
	}
	views := make([]EscalaView, 0, len(rows))
	for _, r := range rows {
		views = append(views, toEscalaViewFromByVessel(r))
	}
	return views, nil
}

// EditBerthing — POST /escalas/:id/edit-berthing
// Edição manual da data de atracação (editores).
func (h *EscalaHandler) EditBerthing(c *gin.Context) {
	id := parseID(c)
	back := c.Request.Referer()
	if back == "" {
		back = "/escalas"
	}

	dateStr := strings.TrimSpace(c.PostForm("berthing_date"))
	if dateStr == "" {
		c.Redirect(http.StatusFound, back)
		return
	}
	ts, err := parseDateToTimestamptz(dateStr)
	if err != nil {
		c.Redirect(http.StatusFound, back)
		return
	}
	_ = h.q.UpdateBerthingDate(c.Request.Context(), sqlc.UpdateBerthingDateParams{
		ID: id, ActualArrival: ts,
	})
	c.Redirect(http.StatusFound, back)
}

// EditSuspension — POST /escalas/:id/edit-suspension
// Edição manual da data de suspensão (editores). Só permite em escalas já concluídas.
func (h *EscalaHandler) EditSuspension(c *gin.Context) {
	id := parseID(c)
	back := c.Request.Referer()
	if back == "" {
		back = "/escalas"
	}

	dateStr := strings.TrimSpace(c.PostForm("suspension_date"))
	if dateStr == "" {
		c.Redirect(http.StatusFound, back)
		return
	}
	ts, err := parseDateToTimestamptz(dateStr)
	if err != nil {
		c.Redirect(http.StatusFound, back)
		return
	}
	_ = h.q.UpdateDepartureDate(c.Request.Context(), sqlc.UpdateDepartureDateParams{
		ID: id, ActualDeparture: ts,
	})
	c.Redirect(http.StatusFound, back)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func toEscalaView(r sqlc.ListEscalasRow) EscalaView {
	// Prioridade: usa snapshot (escalas concluídas) ou calcula do vessel (escalas ativas/planejadas).
	risk := derefStr(r.RiskLevelSnapshot)
	prio := derefStr(r.PrioritySnapshot)
	if prio == "" {
		p := vessel.CalcPriority(r.VesselRiskLevel, r.VesselLastInspectionDate)
		prio = string(p)
		risk = derefStr(r.VesselRiskLevel)
	}

	v := EscalaView{
		ID:           r.ID,
		VesselID:     r.VesselID,
		VesselName:   r.VesselName,
		VesselFlag:   derefStr(r.VesselFlag),
		Terminal:     derefStr(r.Terminal),
		Status:       r.PortCallStatus,
		StatusLabel:  escalaStatusLabel(r.PortCallStatus),
		RiskLevel:    risk,
		Priority:     prio,
		Inspected:    r.InspectionID != nil,
	}
	if r.VesselImo != nil {
		v.VesselIMO = *r.VesselImo
	}
	v.InspectionResult = inspectionResultLabel(r.InspectionResult)
	if r.EtaDate.Valid {
		v.ETADate = r.EtaDate.Time.Format("02/01/2006")
	}
	if r.EtdDate.Valid {
		v.ETDDate = r.EtdDate.Time.Format("02/01/2006")
	}
	if r.ActualArrival.Valid {
		v.BerthingDate    = r.ActualArrival.Time.Format("02/01/2006")
		v.BerthingDateISO = r.ActualArrival.Time.Format("2006-01-02")
	}
	if r.ActualDeparture.Valid {
		v.DepartureDate    = r.ActualDeparture.Time.Format("02/01/2006")
		v.DepartureDateISO = r.ActualDeparture.Time.Format("2006-01-02")
	}
	return v
}

func toEscalaViewFromByVessel(r sqlc.ListEscalasByVesselRow) EscalaView {
	risk := derefStr(r.RiskLevelSnapshot)
	prio := derefStr(r.PrioritySnapshot)
	if prio == "" {
		p := vessel.CalcPriority(r.VesselRiskLevel, r.VesselLastInspectionDate)
		prio = string(p)
		risk = derefStr(r.VesselRiskLevel)
	}

	v := EscalaView{
		ID:          r.ID,
		VesselID:    r.VesselID,
		VesselName:  r.VesselName,
		VesselFlag:  derefStr(r.VesselFlag),
		Terminal:    derefStr(r.Terminal),
		Status:      r.PortCallStatus,
		StatusLabel: escalaStatusLabel(r.PortCallStatus),
		RiskLevel:   risk,
		Priority:    prio,
		Inspected:   r.InspectionID != nil,
	}
	if r.VesselImo != nil {
		v.VesselIMO = *r.VesselImo
	}
	v.InspectionResult = inspectionResultLabel(r.InspectionResult)
	if r.EtaDate.Valid {
		v.ETADate = r.EtaDate.Time.Format("02/01/2006")
	}
	if r.EtdDate.Valid {
		v.ETDDate = r.EtdDate.Time.Format("02/01/2006")
	}
	if r.ActualArrival.Valid {
		v.BerthingDate    = r.ActualArrival.Time.Format("02/01/2006")
		v.BerthingDateISO = r.ActualArrival.Time.Format("2006-01-02")
	}
	if r.ActualDeparture.Valid {
		v.DepartureDate    = r.ActualDeparture.Time.Format("02/01/2006")
		v.DepartureDateISO = r.ActualDeparture.Time.Format("2006-01-02")
	}
	return v
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func inspectionResultLabel(r *string) string {
	if r == nil {
		return ""
	}
	switch *r {
	case "no_deficiencies":
		return "Sem deficiências"
	case "deficiencies":
		return "Com deficiências"
	case "detained":
		return "Detido"
	}
	return *r
}

func escalaStatusLabel(s string) string {
	switch s {
	case "planned":
		return "Planejada"
	case "active":
		return "Atracado"
	case "completed":
		return "Concluída"
	case "closed":
		return "Encerrada"
	case "aborted":
		return "Cancelada"
	}
	return s
}

func parsePgDateOptional(s string) pgtype.Date {
	var d pgtype.Date
	s = strings.TrimSpace(s)
	if s == "" {
		return d
	}
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return d
	}
	_ = d.Scan(t)
	return d
}

func parseTsOptional(s string) pgtype.Timestamptz {
	var ts pgtype.Timestamptz
	s = strings.TrimSpace(s)
	if s == "" {
		return ts
	}
	t, err := parseDateToTimestamptz(s)
	if err != nil {
		return ts
	}
	return t
}

func parseDateToTimestamptz(s string) (pgtype.Timestamptz, error) {
	var ts pgtype.Timestamptz
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return ts, err
	}
	_ = ts.Scan(t)
	return ts, nil
}

func parseID(c *gin.Context) int64 {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	return id
}

func referer(c *gin.Context, fallback string) string {
	ref := c.Request.Referer()
	if ref == "" {
		return fallback + "?"
	}
	if !strings.Contains(ref, "?") {
		return ref + "?"
	}
	return ref + "&"
}

func parseYearMonth(s string) (int, int) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		now := time.Now()
		return now.Year(), int(now.Month())
	}
	y, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	return y, m
}

type MonthOption struct {
	Value string // "YYYY-MM"
	Label string // "Abril 2026"
}

func buildMonthOptions(now time.Time) []MonthOption {
	months := make([]MonthOption, 13)
	months[0] = MonthOption{Value: "", Label: "Todos os meses"}
	for i := 0; i < 12; i++ {
		t := now.AddDate(0, -i, 0)
		months[i+1] = MonthOption{
			Value: fmt.Sprintf("%d-%02d", t.Year(), t.Month()),
			Label: fmt.Sprintf("%s %d", ptMonth(t.Month()), t.Year()),
		}
	}
	return months
}

func ptMonth(m time.Month) string {
	names := [...]string{"", "Janeiro", "Fevereiro", "Março", "Abril", "Maio", "Junho",
		"Julho", "Agosto", "Setembro", "Outubro", "Novembro", "Dezembro"}
	if m >= 1 && m <= 12 {
		return names[m]
	}
	return ""
}
