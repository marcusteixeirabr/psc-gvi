package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// VesselFinderResult é o resultado de uma busca de IMO no VesselFinder.
type VesselFinderResult struct {
	IMO      string
	Name     string
	LOA      float64
	Beam     float64
	DetailURL string
}

// FindIMO busca o número IMO de um navio pelo nome no VesselFinder.
//
// Retorna o IMO se encontrar uma correspondência única ou unívoca por LOA/Beam.
// Retorna erro se não encontrar, ou se houver ambiguidade irresolúvel.
//
// loa e beam são usados para desambiguação quando há múltiplos resultados.
// Se forem zero (desconhecidos), aceita o primeiro resultado com nome exato.
func FindIMO(ctx context.Context, baseURL, vesselName string, loa, beam float64) (string, error) {
	if baseURL == "" {
		return "", fmt.Errorf("VESSEL_FINDER_URL não configurada")
	}

	results, err := SearchVesselFinder(ctx, baseURL, vesselName, loa)
	if err != nil {
		return "", err
	}

	if len(results) == 0 {
		return "", fmt.Errorf("nenhum resultado para %q no VesselFinder", vesselName)
	}

	// 1 resultado → retorna direto. VesselFinder já filtrou por nome.
	if len(results) == 1 {
		slog.Info("IMO encontrado", "component", "vesselfinder", "imo", results[0].IMO, "vessel", vesselName)
		return results[0].IMO, nil
	}

	// Múltiplos resultados → desambigua por dimensões.
	return disambiguateByDimensions(vesselName, results, loa, beam)
}

// SearchVesselFinder busca na página de resultados do VesselFinder e extrai candidatos.
// Pode ser chamado com o nome do navio OU com o número IMO como termo de busca.
//
// loa, quando > 0, filtra a busca por minLength/maxLength (±5%) diretamente na URL —
// reduz a paginação/desambiguação manual para nomes comuns de navio. Ver regras.md §9.
// Passe 0 para não aplicar o filtro (ex: busca por IMO, já unívoca por natureza).
func SearchVesselFinder(ctx context.Context, baseURL, vesselName string, loa float64) ([]VesselFinderResult, error) {
	searchURL := baseURL + "/vessels?name=" + url.QueryEscape(vesselName)
	if loa > 0 {
		minLen, maxLen := loaSearchRange(loa)
		searchURL += fmt.Sprintf("&minLength=%d&maxLength=%d", minLen, maxLen)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("criando request VesselFinder: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept-Language", "pt-BR,pt;q=0.9,en;q=0.8")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	// Delay respeitoso — evita rate limiting do VesselFinder.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(2 * time.Second):
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("buscando VesselFinder: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("VesselFinder retornou HTTP %d para %q", resp.StatusCode, vesselName)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parseando HTML do VesselFinder: %w", err)
	}

	results := parseVesselFinderResults(doc)
	dims := make([]string, len(results))
	for i, r := range results {
		dims[i] = fmt.Sprintf("IMO%s LOA%.1f Beam%.1f", r.IMO, r.LOA, r.Beam)
	}
	slog.Info("resultados da busca", "component", "vesselfinder", "total", len(results), "vessel", vesselName, "dims", dims)
	return results, nil
}

// parseVesselFinderResults extrai resultados da página de busca do VesselFinder.
// O IMO vem diretamente do path do link de detalhe: /vessels/details/{IMO}
func parseVesselFinderResults(doc *goquery.Document) []VesselFinderResult {
	var results []VesselFinderResult

	// Procura todos os links com o padrão /vessels/details/{número}
	doc.Find("a[href*='/vessels/details/']").Each(func(_ int, a *goquery.Selection) {
		href, exists := a.Attr("href")
		if !exists {
			return
		}

		// Extrai o IMO do path (último segmento da URL).
		imo := path.Base(href)
		if _, err := strconv.ParseInt(imo, 10, 64); err != nil {
			return // não é um número → não é um IMO válido
		}

		// Tenta extrair nome e dimensões do contexto ao redor do link.
		// O texto do próprio link costuma ser o nome do navio.
		name := strings.TrimSpace(a.Text())
		if name == "" {
			// Tenta o elemento pai ou avô.
			name = strings.TrimSpace(a.Parent().Text())
		}

		// Procura LOA/Beam na linha da tabela.
		// VesselFinder usa "v5 hidden-mobile" para o campo de dimensões: "300 / 48".
		// Estratégia: escaneia cada <td> individualmente para evitar falsos positivos
		// de outros campos numéricos na linha (GT, ano de construção, etc.).
		var loa, beam float64
		tr := a.Closest("tr")
		if tr.Length() > 0 {
			tr.Find("td").Each(func(_ int, td *goquery.Selection) {
				if loa > 0 {
					return // já encontrou
				}
				if l, b := extractDimensions(td.Text()); l > 0 {
					loa, beam = l, b
				}
			})
		}

		// Evita duplicatas pelo mesmo IMO.
		for _, r := range results {
			if r.IMO == imo {
				return
			}
		}

		results = append(results, VesselFinderResult{
			IMO:       imo,
			Name:      name,
			LOA:       loa,
			Beam:      beam,
			DetailURL: href,
		})
	})

	return results
}

// disambiguateByDimensions escolhe o melhor candidato entre múltiplos navios com o mesmo nome.
//
// Estratégia em cascata:
//  1. Match dentro de ±5% em LOA e Beam (tolerância rígida)
//  2. Menor diferença proporcional combinada (±15% de folga)
//  3. Se nenhum candidato tem dimensões extraídas → retorna o primeiro (nome exato é suficiente)
func disambiguateByDimensions(vesselName string, candidates []VesselFinderResult, loa, beam float64) (string, error) {
	// Verifica se algum candidato tem dimensões extraídas.
	anyHasDims := false
	for _, r := range candidates {
		if r.LOA > 0 || r.Beam > 0 {
			anyHasDims = true
			break
		}
	}

	// Nenhum candidato com dimensões → VesselFinder não retornou o campo size.
	// Se o vessel local também não tem dimensões, aceita o primeiro resultado.
	if !anyHasDims {
		slog.Info("sem dimensões extraídas, usando primeiro resultado", "component", "vesselfinder", "vessel", vesselName, "imo", candidates[0].IMO)
		return candidates[0].IMO, nil
	}

	// Sem dimensões no vessel local → não conseguimos desambiguar.
	if loa == 0 && beam == 0 {
		return "", fmt.Errorf("múltiplos navios chamados %q mas vessel local não tem LOA/Beam para desambiguar", vesselName)
	}

	// Passo 1: tolerância rígida ±5%.
	for _, r := range candidates {
		if WithinTolerance(loa, r.LOA, 0.05) && WithinTolerance(beam, r.Beam, 0.05) {
			slog.Info("IMO encontrado por match ±5%", "component", "vesselfinder", "imo", r.IMO, "vessel", vesselName)
			return r.IMO, nil
		}
	}

	// Passo 2: melhor match por menor diferença proporcional (até ±15%).
	bestIdx := -1
	bestDiff := math.MaxFloat64
	for i, r := range candidates {
		if r.LOA == 0 && r.Beam == 0 {
			continue
		}
		diffLOA := 0.0
		if loa > 0 && r.LOA > 0 {
			diffLOA = math.Abs(loa-r.LOA) / math.Max(loa, r.LOA)
		}
		diffBeam := 0.0
		if beam > 0 && r.Beam > 0 {
			diffBeam = math.Abs(beam-r.Beam) / math.Max(beam, r.Beam)
		}
		diff := diffLOA + diffBeam
		if diff < bestDiff {
			bestDiff = diff
			bestIdx = i
		}
	}
	if bestIdx >= 0 && bestDiff <= 0.30 { // até 30% combinado (15% por dimensão)
		slog.Info("IMO encontrado por melhor match", "component", "vesselfinder", "imo", candidates[bestIdx].IMO, "vessel", vesselName, "diff", bestDiff)
		return candidates[bestIdx].IMO, nil
	}

	return "", fmt.Errorf("ambíguo: %d resultados para %q — nenhuma dimensão bateu (LOA %.1f, Beam %.1f)",
		len(candidates), vesselName, loa, beam)
}

// loaSearchRange calcula minLength/maxLength (±5% do LOA, arredondado para
// inteiro) para o filtro de comprimento na URL de busca do VesselFinder — o
// campo do site só aceita dígitos, sem casas decimais. Ver regras.md §9.
func loaSearchRange(loa float64) (min, max int) {
	return int(math.Round(loa * 0.95)), int(math.Round(loa * 1.05))
}

// dimPattern encontra "LOA / Beam" do VesselFinder: ex "300 / 48" ou "189.5 / 32.0".
// LOA de navios: 50–400m (2-3 dígitos). Beam: 5–70m (1-2 dígitos).
// Limitar a 2-3 dígitos evita confundir com GT (5 dígitos) ou ano de construção (4 dígitos).
var dimPattern = regexp.MustCompile(`(\d{2,3}(?:\.\d+)?)\s*/\s*(\d{1,2}(?:\.\d+)?)`)

// extractDimensions extrai LOA e Beam de um texto com padrão "300 / 48" (VesselFinder).
// Retorna (0, 0) se não encontrar o padrão esperado.
func extractDimensions(text string) (loa, beam float64) {
	text = strings.ReplaceAll(text, ",", ".")
	if m := dimPattern.FindStringSubmatch(text); m != nil {
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[2], 64)
		// Sanidade: LOA deve ser maior que Beam e razoável para um navio.
		if a > 0 && b > 0 && a > b && a >= 50 && b >= 5 {
			return a, b
		}
	}
	return 0, 0
}
