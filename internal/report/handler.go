// Pacote report: página de relatório mensal de escalas + exportação CSV.
//
// O relatório lista todas as escalas que tocaram o porto no mês selecionado
// (navios acompanhados). No topo exibe o KPI de inspeções sobre estrangeiros
// (meta: 20%). O botão "Exportar CSV" gera o mesmo conjunto de dados em formato
// separado por ponto-e-vírgula, compatível com Excel em pt-BR.
package report

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	dbsqlc "github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
)

// Handler agrupa as dependências do relatório.
type Handler struct {
	q *dbsqlc.Queries
}

// NewHandler cria um Handler com as queries do banco.
func NewHandler(q *dbsqlc.Queries) *Handler {
	return &Handler{q: q}
}

// ReportEntry é a view struct usada no template — campos prontos para exibição.
type ReportEntry struct {
	ID               int64 // port call ID
	VesselID         int64 // para link /vessels/:id
	VesselName       string
	VesselIMO        string
	VesselFlag       string
	VesselType       string
	YearBuilt        string
	Terminal         string
	ETADate          string // previsto chegada
	ETDDate          string // previsto saída
	BerthingDate     string // atracação efetiva (actual_arrival)
	DepartureDate    string // suspensão efetiva (actual_departure)
	Status           string
	StatusLabel      string
	PrioritySnapshot string
	Inspected        bool
	InspectionResult string
	InspectionDate   string
	DaysInPort       string // dias em porto (arrival→departure); "—" se incompleto
	IsBrazilian      bool   // para estilo visual
	IsAfretado       bool
}

// KPI resume os números do mês para o card de topo.
type KPI struct {
	Total     int64
	Inspected int64
	Completed int64
	Percent   float64 // Inspected / Total * 100 (se Total > 0)
}

// monthOption é um item do seletor de meses.
type monthOption struct {
	Value    string // "YYYY-MM"
	Label    string // "Abril 2026"
	Selected bool
}

// Show exibe a página de relatório mensal (GET /relatorio).
func (h *Handler) Show(c *gin.Context) {
	year, month, mesSel := parseMes(c.Query("mes"))

	rows, err := h.q.ListReportEntries(c.Request.Context(), dbsqlc.ListReportEntriesParams{
		Year:  int32(year),
		Month: int32(month),
	})
	if err != nil {
		c.HTML(http.StatusInternalServerError, "relatorio.html", gin.H{
			"User":     auth.GetUser(c),
			"Months":   buildMonthOptions(mesSel),
			"MesSel":   mesSel,
			"MesLabel": mesLabel(year, month),
			"KPI":      KPI{},
			"Entries":  []ReportEntry{},
			"Error":    "Erro ao carregar relatório: " + err.Error(),
		})
		return
	}

	kpiRow, err := h.q.CountReportKPI(c.Request.Context(), dbsqlc.CountReportKPIParams{
		Year:  int32(year),
		Month: int32(month),
	})
	if err != nil {
		kpiRow = dbsqlc.CountReportKPIRow{}
	}

	kpi := KPI{
		Total:     kpiRow.Total,
		Inspected: kpiRow.Inspected,
		Completed: kpiRow.Completed,
	}
	if kpi.Total > 0 {
		kpi.Percent = float64(kpi.Inspected) / float64(kpi.Total) * 100
	}

	entries := make([]ReportEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, buildEntry(r))
	}

	c.HTML(http.StatusOK, "relatorio.html", gin.H{
		"User":    auth.GetUser(c),
		"Entries": entries,
		"KPI":     kpi,
		"Months":  buildMonthOptions(mesSel),
		"MesSel":  mesSel,
		"MesLabel": mesLabel(year, month),
	})
}

// ExportCSV gera o arquivo CSV para download (GET /relatorio/export).
func (h *Handler) ExportCSV(c *gin.Context) {
	year, month, mesSel := parseMes(c.Query("mes"))

	rows, err := h.q.ListReportEntries(c.Request.Context(), dbsqlc.ListReportEntriesParams{
		Year:  int32(year),
		Month: int32(month),
	})
	if err != nil {
		c.String(http.StatusInternalServerError, "Erro: "+err.Error())
		return
	}

	filename := fmt.Sprintf("escalas-%s.csv", mesSel)
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Header("Content-Type", "text/csv; charset=utf-8")

	w := csv.NewWriter(c.Writer)
	w.Comma = ';' // ponto-e-vírgula: padrão Excel pt-BR

	// BOM UTF-8 para que o Excel reconheça acentos ao abrir diretamente
	c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	// Cabeçalho
	w.Write([]string{
		"ID", "Navio", "IMO", "Bandeira", "Tipo", "Ano Const.",
		"Terminal", "ETA", "ETD", "Atracação", "Suspensão",
		"Status", "Prioridade", "Inspecionado", "Resultado Insp.", "Data Insp.", "Dias em Porto",
	})

	for _, r := range rows {
		e := buildEntry(r)
		insp := "Não"
		if e.Inspected {
			insp = "Sim"
		}
		w.Write([]string{
			strconv.FormatInt(e.ID, 10),
			e.VesselName, e.VesselIMO, e.VesselFlag, e.VesselType, e.YearBuilt,
			e.Terminal, e.ETADate, e.ETDDate, e.BerthingDate, e.DepartureDate,
			e.StatusLabel, e.PrioritySnapshot, insp, e.InspectionResult, e.InspectionDate, e.DaysInPort,
		})
	}
	w.Flush()
}

// ── helpers ──────────────────────────────────────────────────────────────────

// parseMes interpreta o parâmetro "mes" (formato "YYYY-MM").
// Retorna year, month e o valor normalizado para o seletor.
func parseMes(mes string) (year, month int, mesSel string) {
	now := time.Now()
	year = now.Year()
	month = int(now.Month())

	if len(mes) == 7 {
		parts := strings.SplitN(mes, "-", 2)
		if y, err := strconv.Atoi(parts[0]); err == nil {
			if m, err := strconv.Atoi(parts[1]); err == nil && m >= 1 && m <= 12 {
				year, month = y, m
			}
		}
	}
	mesSel = fmt.Sprintf("%04d-%02d", year, month)
	return
}

// buildEntry converte uma linha do banco em ReportEntry formatada para exibição.
func buildEntry(r dbsqlc.ListReportEntriesRow) ReportEntry {
	e := ReportEntry{
		ID:           r.ID,
		VesselID:     r.VesselID,
		VesselName:   r.VesselName,
		VesselIMO:    derefStr(r.VesselImo),
		VesselFlag:   derefStr(r.VesselFlag),
		IsAfretado:   r.VesselAfretado,
		Status:       r.PortCallStatus,
		StatusLabel:  statusLabel(r.PortCallStatus),
		PrioritySnapshot: derefStr(r.PrioritySnapshot),
		Inspected:    r.InspectionID != nil,
		InspectionResult: derefStr(r.InspectionResult),
	}

	if r.VesselType != nil {
		e.VesselType = *r.VesselType
	}
	if r.VesselYearBuilt != nil {
		e.YearBuilt = strconv.Itoa(int(*r.VesselYearBuilt))
	}
	if r.Terminal != nil {
		e.Terminal = *r.Terminal
	}
	if r.EtaDate.Valid {
		e.ETADate = r.EtaDate.Time.Format("02/01/2006")
	}
	if r.EtdDate.Valid {
		e.ETDDate = r.EtdDate.Time.Format("02/01/2006")
	}
	if r.ActualArrival.Valid {
		e.BerthingDate = r.ActualArrival.Time.Format("02/01/2006")
	}
	if r.ActualDeparture.Valid {
		e.DepartureDate = r.ActualDeparture.Time.Format("02/01/2006")
	}
	if r.InspectionDate.Valid {
		e.InspectionDate = r.InspectionDate.Time.Format("02/01/2006")
	}

	// Dias em porto: só calcula se tiver atracação e suspensão
	if r.ActualArrival.Valid && r.ActualDeparture.Valid {
		days := int(r.ActualDeparture.Time.Sub(r.ActualArrival.Time).Hours() / 24)
		if days < 0 {
			days = 0
		}
		e.DaysInPort = strconv.Itoa(days)
	} else {
		e.DaysInPort = "—"
	}

	// Classificação: brasileiro se flag contém "brazil"/"brasil"
	flag := strings.ToLower(strings.TrimSpace(e.VesselFlag))
	e.IsBrazilian = flag == "brazil" || flag == "brasil"

	if e.PrioritySnapshot == "" {
		e.PrioritySnapshot = "—"
	}

	return e
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func statusLabel(s string) string {
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

// buildMonthOptions gera os últimos 12 meses para o seletor.
func buildMonthOptions(selected string) []monthOption {
	now := time.Now()
	opts := make([]monthOption, 12)
	for i := 0; i < 12; i++ {
		t := now.AddDate(0, -i, 0)
		val := fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month()))
		opts[i] = monthOption{
			Value:    val,
			Label:    mesLabel(t.Year(), int(t.Month())),
			Selected: val == selected,
		}
	}
	return opts
}

var meses = [...]string{
	"", "Janeiro", "Fevereiro", "Março", "Abril", "Maio", "Junho",
	"Julho", "Agosto", "Setembro", "Outubro", "Novembro", "Dezembro",
}

func mesLabel(year, month int) string {
	if month < 1 || month > 12 {
		return fmt.Sprintf("%04d-%02d", year, month)
	}
	return fmt.Sprintf("%s %d", meses[month], year)
}
