// Package dashboard serve a página principal do sistema PSC-GVI.
package dashboard

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/vessel"
)

// Handler serve o dashboard principal.
type Handler struct {
	q *sqlc.Queries
}

// NewHandler cria o handler.
func NewHandler(q *sqlc.Queries) *Handler {
	return &Handler{q: q}
}

// Entry é a visão de uma linha do dashboard (vessel + port call combinados).
type Entry struct {
	VesselID         int64
	Name             string
	IMO              string
	LengthM          string
	BeamM            string
	Age              string
	AgeOld           bool
	RiskLevel        string
	Terminal         string
	ETADate          string
	ETATime          string
	ETDDate          string
	ETDTime          string
	StatusLabel      string
	IsBerthed        bool
	Priority         vessel.Priority
	PriorityLabel    string
	Incomplete       bool
	Inspected        bool
	InspectionResult string
	// Deficiencies: texto de deficiências da última inspeção PSC (do CIALA).
	// Regra futura: navio com deficiências sempre aparece no dashboard mesmo que P3.
	Deficiencies string
}

// InWindow reporta se a prioridade está na janela de inspeção (P1 ou P2).
func (e Entry) InWindow() bool {
	return e.Priority == vessel.P1 || e.Priority == vessel.P2
}

type page struct {
	User          *auth.UserSession
	Entries       []Entry
	Total         int
	InWindow      int
	Flash         string
	MonthLabel    string
	MonthKPI      float64
	MonthColor    string // "green" | "orange" | "red"
	MonthTotal    int64
	MonthInspected int64
}

// Index — GET /
func (h *Handler) Index(c *gin.Context) {
	ctx := c.Request.Context()

	rows, err := h.q.ListDashboardEntries(ctx)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "dashboard.html", page{
			User:  auth.GetUser(c),
			Flash: "Erro ao carregar dashboard: " + err.Error(),
		})
		return
	}

	// Obtém o timestamp do último ciclo de scraping ZP-21 para filtrar o dashboard.
	// Validade: 72 horas — se a última consulta foi há mais de 72h, dashboard limpo para ZP-21.
	lastScrape, _ := h.q.GetLastZP21ScrapeTime(ctx)
	zp21Valid := lastScrape.Valid && lastScrape.Time.After(time.Now().Add(-72*time.Hour))
	var minSeen time.Time
	if zp21Valid {
		minSeen = lastScrape.Time.Add(-time.Minute) // margem de 1 min para o mesmo lote
	}

	// SQL pode retornar múltiplos port calls por vessel (ex: após merge).
	// Dedup por VesselID, mantendo a primeira linha (mais próxima por data, ordem do SQL).
	seen := make(map[int64]bool)
	entries := make([]Entry, 0, len(rows))
	for _, r := range rows {
		if seen[r.ID] {
			continue
		}

		// Filtro de visibilidade no dashboard:
		// Condição principal: navio está na lista ZP-21 da última consulta válida (72h).
		// Exceção: navio com escala 'atracado' aparece mesmo fora do ZP-21 atual.
		inCurrentZP21 := zp21Valid && r.LastZp21SeenAt.Valid && !r.LastZp21SeenAt.Time.Before(minSeen)
		isBerthed := r.PortCallStatus == "active"
		if !inCurrentZP21 && !isBerthed {
			continue
		}

		p := vessel.CalcPriority(r.RiskLevel, r.LastInspectionDate)
		deficiencies := derefStr(r.LastInspectionDeficiencies)

		// Filtro de prioridade:
		// - P1 e P2: sempre aparecem
		// - P3: só com deficiências registradas no CIALA
		// - N/D (sem CIALA): aparece se estrangeiro + não afretado + controlado
		//   (a query SQL já garante essas condições — não é necessário filtrar aqui)
		if p == vessel.P3 && deficiencies == "" {
			continue
		}

		seen[r.ID] = true

		e := Entry{
			VesselID:         r.ID,
			Name:             r.Name,
			IMO:              derefStr(r.Imo),
			LengthM:          fmtNumeric(r.LengthM),
			BeamM:            fmtNumeric(r.BeamM),
			Age:              calcAge(r.YearBuilt),
			AgeOld:           isOld(r.YearBuilt),
			RiskLevel:        derefStr(r.RiskLevel),
			Terminal:         derefStr(r.Terminal),
			ETADate:          fmtDate(r.EtaDate),
			ETATime:          fmtTime(r.EtaTime),
			ETDDate:          fmtDate(r.EtdDate),
			ETDTime:          fmtTime(r.EtdTime),
			IsBerthed:        r.VesselStatus == "berthed",
			StatusLabel:      simplifyStatus(r.VesselStatus),
			Priority:         p,
			PriorityLabel:    vessel.PriorityLabelFor(p),
			Incomplete:       r.Flag == nil || r.RiskLevel == nil,
			Inspected:        r.InspectionID != nil,
			InspectionResult: inspectionResultLabel(r.InspectionResult),
			Deficiencies:     deficiencies,
		}
		entries = append(entries, e)
	}

	// Desempate por prioridade dentro do mesmo dia (SQL já ordenou por data).
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].ETADate != entries[j].ETADate {
			return false
		}
		return priorityOrder(entries[i].Priority) < priorityOrder(entries[j].Priority)
	})

	inWindow := 0
	for _, e := range entries {
		if e.InWindow() {
			inWindow++
		}
	}

	now := time.Now()
	kpiRow, _ := h.q.CountReportKPI(ctx, sqlc.CountReportKPIParams{
		Year:  int32(now.Year()),
		Month: int32(now.Month()),
	})
	monthKPI := 0.0
	if kpiRow.Total > 0 {
		monthKPI = float64(kpiRow.Inspected) / float64(kpiRow.Total) * 100
	}
	monthColor := "red"
	if monthKPI >= 20 {
		monthColor = "green"
	} else if monthKPI >= 10 {
		monthColor = "orange"
	}

	c.HTML(http.StatusOK, "dashboard.html", page{
		User:           auth.GetUser(c),
		Entries:        entries,
		Total:          len(entries),
		InWindow:       inWindow,
		Flash:          c.Query("flash"),
		MonthLabel:     fmt.Sprintf("%s %d", ptMonthName(now.Month()), now.Year()),
		MonthKPI:       monthKPI,
		MonthColor:     monthColor,
		MonthTotal:     kpiRow.Total,
		MonthInspected: kpiRow.Inspected,
	})
}

func priorityOrder(p vessel.Priority) int {
	switch p {
	case vessel.P1:
		return 0
	case vessel.P2:
		return 1
	case vessel.P3:
		return 2
	}
	return 3
}

// simplifyStatus converte o status técnico em rótulo simples para inspetores.
func inspectionResultLabel(r *string) string {
	if r == nil {
		return ""
	}
	switch *r {
	case "no_deficiencies":
		return "Sem Deficiências"
	case "deficiencies":
		return "Com Deficiências"
	case "detained":
		return "Detido"
	}
	return *r
}

func simplifyStatus(s string) string {
	if s == "berthed" {
		return "Atracado"
	}
	return "A caminho"
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func fmtNumeric(n pgtype.Numeric) string {
	if !n.Valid {
		return ""
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return ""
	}
	return fmt.Sprintf("%.1f", f.Float64)
}

func fmtDate(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("02/01")
}

func fmtTime(t pgtype.Time) string {
	if !t.Valid {
		return "TBC"
	}
	total := t.Microseconds / 1_000_000
	h := total / 3600
	m := (total % 3600) / 60
	return fmt.Sprintf("%02d:%02d", h, m)
}

func calcAge(yearBuilt *int32) string {
	if yearBuilt == nil || *yearBuilt == 0 {
		return ""
	}
	age := time.Now().Year() - int(*yearBuilt)
	return fmt.Sprintf("%d a", age)
}

func isOld(yearBuilt *int32) bool {
	if yearBuilt == nil || *yearBuilt == 0 {
		return false
	}
	return time.Now().Year()-int(*yearBuilt) >= 30
}

func ptMonthName(m time.Month) string {
	names := [...]string{"", "Janeiro", "Fevereiro", "Março", "Abril", "Maio", "Junho",
		"Julho", "Agosto", "Setembro", "Outubro", "Novembro", "Dezembro"}
	if m >= 1 && m <= 12 {
		return names[m]
	}
	return ""
}
