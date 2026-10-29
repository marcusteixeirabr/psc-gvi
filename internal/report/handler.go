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
	ID               int64
	VesselID         int64
	VesselName       string
	VesselIMO        string
	VesselFlag       string
	BerthingDate     string // atracação efetiva (actual_arrival)
	RiskSnapshot     string // risco da escala de maior precedência do mês (regras.md §15)
	PrioritySnapshot string // prioridade da escala de maior precedência do mês (P1 > P2 > P3)
	Inspected        bool   // true se o GVI inspecionou o navio no mês
	// Inspections: inspeções do GVI no mês, em ordem cronológica. Normalmente
	// uma; pode haver duas (ex.: inspeção P1 + perícia de acidente no retorno).
	Inspections []InspectionView
	// LastInspection: última inspeção PSC conhecida (CIALA, qualquer porto) no
	// snapshot da atracação — exibida só quando o GVI não inspecionou no mês.
	LastInspection   string
	IsBrazilian      bool
	IsAfretado       bool
	MonthOverridden  bool // true se o mês do relatório foi ajustado manualmente (regras.md §13)
}

// InspectionView é uma inspeção do GVI pronta para exibir na célula "Inspeção".
type InspectionView struct {
	Result string
	Date   string
	// OutOfWindow: a escala inspecionada estava fora da janela (P3/N/A) —
	// perícia, clandestinos, denúncia. Conta como inspeção, não como sujeito (regras.md §14).
	OutOfWindow bool
}

// KPI resume os números do mês para os cards do relatório.
type KPI struct {
	TotalPorto   int64
	Estrangeiros int64
	Sujeitos     int64
	Inspected    int64
	Percent      float64 // Inspected / Sujeitos * 100, limitado a 100 (ver KPIPercent)
}

// KPIPercent calcula o % de inspeção do mês: inspecionados / sujeitos.
//
// Por que pode passar de 100%: inspeções FORA da janela (perícia de acidente,
// clandestinos, denúncia — escala em P3/N/A) contam no numerador como inspeção
// realizada, mas o navio NÃO entra no denominador "sujeitos" (regras.md §14).
// Ex.: 1 inspeção fora da janela num mês sem nenhum navio sujeito = 1/0.
// "Inspected" conta INSPEÇÕES, não navios (regras.md §15): um navio P1
// inspecionado que volta e sofre uma perícia no mesmo mês soma 2.
//
// Decisão do Marcus: o app não é ferramenta de auditoria — nesses casos exibe
// 100% em vez de um número absurdo ou divisão por zero. Função exportada para
// o dashboard usar a MESMA regra (equivalente a um método static utilitário em Java).
func KPIPercent(inspected, sujeitos int64) float64 {
	if inspected <= 0 {
		return 0
	}
	if sujeitos <= 0 || inspected >= sujeitos {
		return 100
	}
	return float64(inspected) / float64(sujeitos) * 100
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

	inspByVessel := h.loadInspections(c, year, month)

	kpiRow, err := h.q.CountReportKPI(c.Request.Context(), dbsqlc.CountReportKPIParams{
		Year:  int32(year),
		Month: int32(month),
	})
	if err != nil {
		kpiRow = dbsqlc.CountReportKPIRow{}
	}

	kpi := KPI{
		TotalPorto:   kpiRow.TotalPorto,
		Estrangeiros: kpiRow.Estrangeiros,
		Sujeitos:     kpiRow.Sujeitos,
		Inspected:    kpiRow.Inspected,
		Percent:      KPIPercent(kpiRow.Inspected, kpiRow.Sujeitos),
	}

	entries := make([]ReportEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, buildEntry(r, inspByVessel[r.VesselID]))
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
		"ID", "Navio", "IMO", "Bandeira", "Atracação",
		"Risco", "Prioridade", "Inspecionado", "Resultado Insp.", "Data Insp.",
		"Última insp. PSC (CIALA)",
	})

	inspByVessel := h.loadInspections(c, year, month)
	for _, r := range rows {
		e := buildEntry(r, inspByVessel[r.VesselID])
		// Uma linha por navio: com 2+ inspeções do GVI no mês, os campos são
		// unidos com " / " na mesma ordem (cronológica).
		insp := "Não"
		var results, dates []string
		for _, iv := range e.Inspections {
			results = append(results, iv.Result)
			dates = append(dates, iv.Date)
			if insp == "Não" {
				insp = "Sim"
			}
			if iv.OutOfWindow {
				insp = "Sim (fora da janela)"
			}
		}
		w.Write([]string{
			strconv.FormatInt(e.ID, 10),
			e.VesselName, e.VesselIMO, e.VesselFlag, e.BerthingDate,
			e.RiskSnapshot, e.PrioritySnapshot, insp,
			strings.Join(results, " / "), strings.Join(dates, " / "),
			e.LastInspection,
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

// loadInspections busca as inspeções do GVI no mês e agrupa por navio
// (equivalente a um Map<Long, List<InspectionView>> em Java). Erro → mapa vazio:
// o relatório ainda abre, só sem a coluna de inspeção preenchida.
func (h *Handler) loadInspections(c *gin.Context, year, month int) map[int64][]InspectionView {
	out := map[int64][]InspectionView{}
	rows, err := h.q.ListReportInspections(c.Request.Context(), dbsqlc.ListReportInspectionsParams{
		Year:  int32(year),
		Month: int32(month),
	})
	if err != nil {
		return out
	}
	for _, r := range rows {
		iv := InspectionView{Result: inspResultLabel(r.Result), OutOfWindow: r.OutOfWindow}
		if r.InspectionDate.Valid {
			iv.Date = r.InspectionDate.Time.Format("02/01/2006")
		}
		out[r.VesselID] = append(out[r.VesselID], iv)
	}
	return out
}

// buildEntry converte uma linha do banco (+ inspeções do GVI do navio no mês)
// em ReportEntry formatada para exibição.
func buildEntry(r dbsqlc.ListReportEntriesRow, insps []InspectionView) ReportEntry {
	e := ReportEntry{
		ID:               r.ID,
		VesselID:         r.VesselID,
		VesselName:       r.VesselName,
		VesselIMO:        derefStr(r.VesselImo),
		VesselFlag:       derefStr(r.VesselFlag),
		IsAfretado:       r.VesselAfretado,
		RiskSnapshot:     riskLabel(derefStr(r.RiskLevelSnapshot)),
		PrioritySnapshot: derefStr(r.PrioritySnapshot),
		Inspected:        len(insps) > 0,
		Inspections:      insps,
		MonthOverridden:  r.MonthOverridden,
	}

	if r.ActualArrival.Valid {
		e.BerthingDate = r.ActualArrival.Time.Format("02/01/2006")
	}
	// Última inspeção PSC (CIALA) congelada na atracação: só faz sentido
	// quando o GVI não inspecionou no mês (senão a célula mostra a nossa).
	if !e.Inspected && r.LastInspectionSnapshot.Valid {
		e.LastInspection = r.LastInspectionSnapshot.Time.Format("02/01/2006")
	}

	flag := strings.ToLower(strings.TrimSpace(e.VesselFlag))
	e.IsBrazilian = flag == "brazil" || flag == "brasil"

	if e.PrioritySnapshot == "" {
		e.PrioritySnapshot = "—"
	}

	return e
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

func inspResultLabel(r string) string {
	switch r {
	case "no_deficiencies":
		return "Sem deficiências"
	case "deficiencies":
		return "Com deficiências"
	case "detained":
		return "Detido"
	}
	return r
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
