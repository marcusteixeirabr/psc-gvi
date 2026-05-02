// Package scraper busca e parseia dados do site da praticagem ZP-21.
package scraper

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// timeRe encontra padrões de hora dentro de texto misto (ex: "ETB 14H30", "ATB\n14:30").
var timeRe = regexp.MustCompile(`\b(\d{1,2})[Hh:\.](\d{2})\b`)

// ManobrasRow é uma linha parseada da tabela "Manobras Previstas" do ZP-21.
type ManobrasRow struct {
	VesselName   string
	LOA          float64 // comprimento em metros; 0 = não informado
	Beam         float64 // boca em metros; 0 = não informado
	Terminal     string
	RawDate      string // data no formato original do site (ex: "26/04/2026")
	RawTime      string // hora no formato original; pode ser "TBC" ou similar
	ManeuverType string // "entrada" | "saida" | texto original se não reconhecido
	Situation    string // situação do navio normalizada (ex: "atracado", "fundeado", "navegando")
}

// FetchManobras busca e retorna as linhas da tabela "Manobras Previstas" do ZP-21.
// Retorna slice vazio (sem erro) se a tabela estiver vazia.
func FetchManobras(ctx context.Context, url string) ([]ManobrasRow, error) {
	if url == "" {
		return nil, fmt.Errorf("ZP21_URL não configurada no .env")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("criando request ZP-21: %w", err)
	}
	// User-Agent real para evitar bloqueio por bot detection.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept-Language", "pt-BR,pt;q=0.9")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("buscando ZP-21: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ZP-21 retornou HTTP %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parseando HTML do ZP-21: %w", err)
	}

	return parseManobrasTable(doc), nil
}

// parseManobrasTable extrai as linhas da primeira tabela da página.
// O ZP-21 apresenta "Manobras Previstas" como primeira tabela.
func parseManobrasTable(doc *goquery.Document) []ManobrasRow {
	var rows []ManobrasRow

	table := doc.Find("table").First()
	if table.Length() == 0 {
		log.Println("[zp21] nenhuma tabela encontrada na página")
		return rows
	}

	var cols map[string]int
	table.Find("tr").Each(func(i int, tr *goquery.Selection) {
		cells := extractCells(tr)
		if len(cells) == 0 {
			return
		}

		if i == 0 {
			// Linha de cabeçalho — detecta quais colunas são quais.
			cols = detectColumns(cells)
			log.Printf("[zp21] colunas detectadas: %v | headers: %v", cols, cells)
			return
		}

		if cols == nil {
			return
		}

		r := parseRow(cells, cols)
		if r != nil {
			rows = append(rows, *r)
		}
	})

	log.Printf("[zp21] %d linhas encontradas na tabela", len(rows))
	return rows
}

// extractCells retorna o texto de cada célula (th ou td) de uma linha.
func extractCells(tr *goquery.Selection) []string {
	var cells []string
	tr.Find("th, td").Each(func(_ int, cell *goquery.Selection) {
		cells = append(cells, strings.TrimSpace(cell.Text()))
	})
	return cells
}

// detectColumns mapeia nomes de campo para índices de coluna usando o cabeçalho.
// Usa normalização de texto para lidar com variações de acentuação e capitalização.
func detectColumns(headers []string) map[string]int {
	cols := map[string]int{
		"name":      -1,
		"loa":       -1,
		"beam":      -1,
		"terminal":  -1,
		"date":      -1,
		"time":      -1,
		"maneuver":  -1,
		"situation": -1,
	}
	for i, h := range headers {
		n := norm(h)
		switch {
		case has(n, "navio", "nome", "embarcac", "vessel"):
			cols["name"] = i
		case has(n, "loa", "comprimento", "comp."):
			cols["loa"] = i
		case has(n, "boca", "beam", "manga", "largura"):
			cols["beam"] = i
		case has(n, "terminal", "berco", "berço", "atracadouro"):
			cols["terminal"] = i
		case has(n, "data", "date"):
			cols["date"] = i
		case has(n, "hora", "time", "horario"):
			cols["time"] = i
		case has(n, "manobra", "operac", "tipo", "movimento", "mov."):
			cols["maneuver"] = i
		case has(n, "situac", "situação", "estado", "condicao"):
			cols["situation"] = i
		}
	}
	return cols
}

// parseRow converte uma linha da tabela em ManobrasRow, ou nil se linha vazia/inválida.
func parseRow(cells []string, cols map[string]int) *ManobrasRow {
	get := func(field string) string {
		idx := cols[field]
		if idx < 0 || idx >= len(cells) {
			return ""
		}
		return strings.TrimSpace(cells[idx])
	}

	// Remove acentos antes de armazenar: ZP-21 envia nomes com diacríticos
	// (ex: "LOG-IN JATOBÁ"), mas buscas externas como VesselFinder não os aceitam.
	name := strings.ToUpper(StripAccents(strings.TrimSpace(get("name"))))
	if name == "" {
		return nil
	}

	maneuverRaw := get("maneuver")
	rawTime := get("time")

	// Extrai hora do campo "time" se tiver sigla misturada (ex: "ATB\n14H30").
	// Se o campo "time" estiver vazio, tenta extrair do campo "maneuver".
	rawTime = extractTime(rawTime)
	if rawTime == "" {
		rawTime = extractTime(maneuverRaw)
	}

	return &ManobrasRow{
		VesselName:   name,
		LOA:          parseDecimal(get("loa")),
		Beam:         parseDecimal(get("beam")),
		Terminal:     get("terminal"),
		RawDate:      get("date"),
		RawTime:      rawTime,
		ManeuverType: classifyManeuver(maneuverRaw),
		Situation:    norm(get("situation")), // normalizado: minúsculas, sem acentos
	}
}

// extractTime retorna "HH:MM" extraído de um texto livre, ou "" se não encontrar.
// Exemplos: "ETB 14H30" → "14:30", "14:30 (ATB)" → "14:30", "TBC" → "".
func extractTime(s string) string {
	m := timeRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	if h > 23 || min > 59 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", h, min)
}

// classifyManeuver normaliza o tipo de manobra para "entrada" ou "saida".
func classifyManeuver(s string) string {
	n := norm(s)
	if has(n, "entrada", "atraca", "chegada", "arriv", "etb", "atb") {
		return "entrada"
	}
	if has(n, "saida", "saída", "desatraca", "partida", "depart", "ets", "ats") {
		return "saida"
	}
	// Retorna o texto original normalizado para facilitar debug.
	return strings.ToLower(strings.TrimSpace(s))
}

// TimeIsKnown retorna false para TBC, vazio ou outros marcadores de "hora indefinida".
func TimeIsKnown(s string) bool {
	s = strings.ToUpper(strings.TrimSpace(s))
	return s != "" && s != "TBC" && s != "A CONFIRMAR" && s != "-" && s != "—" && s != "?"
}

// WithinTolerance retorna true se dois valores de dimensão estiverem dentro de pct% um do outro.
// Se qualquer um for zero (dado ausente), retorna true para não descartarmos o navio.
func WithinTolerance(a, b, pct float64) bool {
	if a == 0 || b == 0 {
		return true
	}
	return math.Abs(a-b)/math.Max(a, b) <= pct
}

// parseDecimal converte decimais em formato brasileiro ("189,50") ou internacional ("189.50").
func parseDecimal(s string) float64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	s = strings.TrimSuffix(s, "m") // remove unidade se presente
	if s == "" || s == "-" || s == "—" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// StripAccents remove diacríticos de uma string preservando o case original.
// Usada para normalizar nomes de navios antes de pesquisas externas (ex: VesselFinder)
// que não aceitam caracteres acentuados.
func StripAccents(s string) string {
	r := strings.NewReplacer(
		"Ç", "C", "Ã", "A", "Á", "A", "Â", "A", "À", "A",
		"É", "E", "Ê", "E", "Í", "I", "Ó", "O", "Ô", "O",
		"Õ", "O", "Ú", "U", "Ü", "U",
		"ç", "c", "ã", "a", "á", "a", "â", "a", "à", "a",
		"é", "e", "ê", "e", "í", "i", "ó", "o", "ô", "o",
		"õ", "o", "ú", "u", "ü", "u",
	)
	return r.Replace(s)
}

// norm converte para minúsculas e remove diacríticos para comparação fuzzy.
func norm(s string) string {
	return StripAccents(strings.ToLower(strings.TrimSpace(s)))
}

// has reporta se s contém algum dos substrings.
func has(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
