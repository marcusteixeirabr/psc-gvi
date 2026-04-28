// Package scraper — cliente HTTP para o portal CIALA (Acordo Latino-Americano).
// Portal: https://ciala.acuerdolatinoamericano.org
// Busca: /Arrival/GrillaArribos (formulário de busca por IMO)
package scraper

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/publicsuffix"
)

// dateRe extrai padrões de data de dentro de textos — aceita ano com 2 ou 4 dígitos.
// Ex: "Sí 15/01/2024" → "15/01/2024", "21/04/26" → "21/04/26"
var dateRe = regexp.MustCompile(`\b(\d{1,2}[/\-]\d{1,2}[/\-]\d{2,4}|\d{4}[/\-]\d{1,2}[/\-]\d{1,2})\b`)

// CIALAResult contém os dados extraídos do portal CIALA para um navio.
type CIALAResult struct {
	RiskLevel              string     // "high" | "standard" | "low" | "not_found" | ""
	LastInspectionDate     *time.Time // nil = sem registro ou nunca inspecionado
	LastInspectionDeficiencies string // texto de deficiências da última inspeção (ex: "3 deficiencias")
	Flag                   string
	VesselType             string
	YearBuilt              int
}

// CIALAClient gerencia a sessão HTTP com o portal CIALA.
// A sessão é mantida pelo cookie jar — sem flag loggedIn.
// Se o CIALA redirecionar para o login, o cliente refaz o login automaticamente.
type CIALAClient struct {
	baseURL  string
	username string
	password string
	client   *http.Client
}

// NewCIALAClient cria um cliente com cookie jar para manter a sessão.
func NewCIALAClient(baseURL, username, password string) (*CIALAClient, error) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}
	return &CIALAClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Jar:     jar,
		},
	}, nil
}

// Login autentica no portal CIALA via formulário ASP.NET.
// O CIALA usa sessão persistente — após login, o cookie dura indefinidamente.
func (c *CIALAClient) Login(ctx context.Context) error {
	if c.username == "" || c.password == "" {
		return fmt.Errorf("CIALA_USERNAME e CIALA_PASSWORD devem estar configurados no .env")
	}

	// CIALA usa ASP.NET Core Identity com Razor Pages → URL real é /Identity/Account/Login.
	loginURL := c.baseURL + "/Identity/Account/Login"

	// Passo 1: GET para obter o token CSRF e o action do formulário.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, loginURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "es-AR,es;q=0.9,pt-BR;q=0.8")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("acessando login CIALA: %w", err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return fmt.Errorf("parseando página de login: %w", err)
	}

	// Log de todos os campos do formulário para facilitar o debug.
	log.Printf("[ciala] campos do formulário de login:")
	doc.Find("form input").Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		typ, _ := s.Attr("type")
		log.Printf("[ciala]   input name=%q type=%q", name, typ)
	})

	csrfToken, _ := doc.Find("input[name='__RequestVerificationToken']").Attr("value")
	log.Printf("[ciala] CSRF token: len=%d", len(csrfToken))

	// Detecta campos de email e senha no formulário.
	// ASP.NET Core Razor Pages usa prefixo "Input." nos campos de modelo.
	emailField := "Input.Email"
	passField := "Input.Password"
	for _, candidate := range []string{"Input.Email", "Email", "UserName", "Login", "Usuario"} {
		if doc.Find(fmt.Sprintf("input[name='%s']", candidate)).Length() > 0 {
			emailField = candidate
			break
		}
	}
	for _, candidate := range []string{"Input.Password", "Password", "Senha", "Contrasena"} {
		if doc.Find(fmt.Sprintf("input[name='%s']", candidate)).Length() > 0 {
			passField = candidate
			break
		}
	}
	log.Printf("[ciala] campos detectados: email=%q senha=%q", emailField, passField)

	// Extrai o action do formulário que contém o campo de e-mail (login local).
	// A página tem múltiplos forms — o primeiro costuma ser ExternalLogin (OAuth).
	postURL := loginURL
	doc.Find("form").Each(func(_ int, f *goquery.Selection) {
		if f.Find(fmt.Sprintf("input[name='%s']", emailField)).Length() > 0 {
			if action, exists := f.Attr("action"); exists && action != "" {
				if strings.HasPrefix(action, "/") {
					postURL = c.baseURL + action
				} else if strings.HasPrefix(action, "http") {
					postURL = action
				}
			}
		}
	})
	// Segurança extra: nunca postar para ExternalLogin.
	if strings.Contains(postURL, "ExternalLogin") {
		postURL = loginURL
	}
	log.Printf("[ciala] POST para: %s", postURL)

	form := url.Values{
		emailField:                   {c.username},
		passField:                    {c.password},
		"__RequestVerificationToken": {csrfToken},
		"Input.RememberMe":           {"true"},
	}

	postReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	postReq.Header.Set("Referer", loginURL)
	postReq.Header.Set("Accept-Language", "es-AR,es;q=0.9,pt-BR;q=0.8")

	loginResp, err := c.client.Do(postReq)
	if err != nil {
		return fmt.Errorf("enviando credenciais: %w", err)
	}
	defer loginResp.Body.Close()

	finalURL := loginResp.Request.URL.String()
	log.Printf("[ciala] após login: status=%d URL=%s", loginResp.StatusCode, finalURL)

	if strings.Contains(finalURL, "/Account/Login") || strings.Contains(finalURL, "/Identity/Account/Login") {
		return fmt.Errorf("login falhou (URL final: %s) — verifique as credenciais no .env", finalURL)
	}

	log.Printf("[ciala] login bem-sucedido")
	return nil
}

// LookupIMO busca um navio pelo IMO na página GrillaArribos do CIALA.
// O CIALA carrega a tabela completa no GET — o campo "buscar" é filtro JavaScript client-side.
// A estratégia: GET com query params → se redirecionar para login, faz login e tenta de novo.
func (c *CIALAClient) LookupIMO(ctx context.Context, imo string) (*CIALAResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(1 * time.Second):
	}

	doc, err := c.fetchGrillaArribos(ctx, imo)
	if err != nil {
		return nil, err
	}

	// Se a resposta for a página de login (sessão expirou), faz login e tenta de novo.
	if isLoginPage(doc) {
		log.Printf("[ciala] sessão expirou — refazendo login")
		if err := c.Login(ctx); err != nil {
			return nil, fmt.Errorf("re-login falhou: %w", err)
		}
		doc, err = c.fetchGrillaArribos(ctx, imo)
		if err != nil {
			return nil, err
		}
		if isLoginPage(doc) {
			return nil, fmt.Errorf("sessão inválida após re-login — verifique credenciais")
		}
	}

	imoCount := doc.Find("input[name='Imo']").Length()
	rowCount := doc.Find("table tr").Length()
	log.Printf("[ciala] resultado: %d linhas, %d inputs Imo", rowCount, imoCount)

	result := parseCIALAResults(doc, imo)
	if result == nil {
		// Navio não encontrado na base do CIALA — registra como "not_found"
		// para distinguir de "ainda não consultado" (risk_level NULL).
		log.Printf("[ciala] IMO %s não encontrado no CIALA — registrando como not_found", imo)
		return &CIALAResult{RiskLevel: "not_found"}, nil
	}

	log.Printf("[ciala] IMO %s → risco=%q inspeção=%v bandeira=%q", imo, result.RiskLevel, result.LastInspectionDate, result.Flag)
	return result, nil
}

// fetchGrillaArribos faz o GET filtrado pelo IMO.
func (c *CIALAClient) fetchGrillaArribos(ctx context.Context, imo string) (*goquery.Document, error) {
	imoNum := strings.TrimPrefix(strings.ToUpper(imo), "IMO")
	params := url.Values{
		"buscar":   {imoNum},
		"userName": {c.username},
	}
	searchURL := c.baseURL + "/Arrival/GrillaArribos?" + params.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept-Language", "es-AR,es;q=0.9,pt-BR;q=0.8")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("acessando GrillaArribos: %w", err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parseando GrillaArribos: %w", err)
	}
	return doc, nil
}

// isLoginPage detecta se o documento retornado é a página de login (sessão expirada).
func isLoginPage(doc *goquery.Document) bool {
	return doc.Find("input[name='Input.Email'], input[name='Input.Password']").Length() > 0
}

// parseCIALAResults localiza o navio pelo IMO na tabela do GrillaArribos e extrai seus dados.
// O CIALA usa input[name='Imo'][value='{imo}'] em cada linha — a tabela já está completa no GET.
func parseCIALAResults(doc *goquery.Document, imo string) *CIALAResult {
	imoNum := strings.TrimPrefix(strings.ToUpper(imo), "IMO")

	// Tenta localizar a linha pelo input hidden Imo.
	var row *goquery.Selection
	doc.Find("input[name='Imo']").Each(func(_ int, s *goquery.Selection) {
		if row != nil {
			return
		}
		val, _ := s.Attr("value")
		if strings.TrimSpace(val) == imoNum || strings.TrimSpace(val) == imo {
			row = s.Closest("tr")
		}
	})

	// Fallback: busca filtrada já retornou só o navio — pega a primeira linha de dados.
	if (row == nil || row.Length() == 0) && doc.Find("input[name='Imo']").Length() == 1 {
		row = doc.Find("table tbody tr").First()
	}

	// Fallback 2: procura a linha cujas células contêm o IMO como texto visível.
	if row == nil || row.Length() == 0 {
		doc.Find("table tr").Each(func(_ int, tr *goquery.Selection) {
			if row != nil {
				return
			}
			if strings.Contains(tr.Text(), imoNum) {
				row = tr
			}
		})
	}

	if row == nil || row.Length() == 0 {
		return nil
	}

	// Log das células da linha para diagnóstico.
	log.Printf("[ciala] linha encontrada para IMO %s:", imo)
	row.Find("td").Each(func(i int, td *goquery.Selection) {
		log.Printf("[ciala]   td[%d]=%q", i, strings.TrimSpace(td.Text()))
	})

	result := &CIALAResult{}

	// Extrai cabeçalhos da tabela para mapear colunas por nome.
	headers := []string{}
	doc.Find("table thead th, table tr:first-child th").Each(func(_ int, th *goquery.Selection) {
		headers = append(headers, strings.ToLower(strings.TrimSpace(th.Text())))
	})
	log.Printf("[ciala] cabeçalhos: %v", headers)

	// Mapeia células da linha pelos cabeçalhos.
	// Extrai células usando cellText (remove img, faz fallback em alt/title).
	cells := []string{}
	row.Find("td").Each(func(_ int, td *goquery.Selection) {
		cells = append(cells, cellText(td))
	})

	for i, h := range headers {
		if i >= len(cells) {
			break
		}
		v := cells[i]
		vLower := strings.ToLower(v)
		switch {
		case has(h, "riesgo", "risco", "risk"):
			switch {
			case has(vLower, "alto"):
				result.RiskLevel = "high"
			case has(vLower, "bajo"):
				result.RiskLevel = "low"
			case has(vLower, "estandar", "estándar", "standard", "srs", "normal"):
				result.RiskLevel = "standard"
			}
		case has(h, "bandera", "bandeira", "flag", "pabellón", "pabellon", "pais", "país"):
			result.Flag = v
		case has(h, "tipo", "type", "buque"):
			if !has(h, "buque") { // "Buque" = nome do navio, não tipo
				result.VesselType = v
			}
		case has(h, "año constr", "año de constr", "year built", "construccion", "construc", "año"):
			year := 0
			fmt.Sscanf(v, "%d", &year)
			if year > 1900 && year <= time.Now().Year() {
				result.YearBuilt = year
			}
		case has(h, "inspeccionado", "inspecc", "inspeç", "inspection", "última", "ultima", "fecha insp"):
			if t := extractDateFromText(v); t != nil {
				result.LastInspectionDate = t
			}
			// Extrai deficiências: qualquer texto além da data (ex: "15/01/24 3 deficiencias").
			// "Sin Inspección previa en AVM" → sem data e sem deficiências contabilizáveis.
			if def := extractDeficiencies(v); def != "" {
				result.LastInspectionDeficiencies = def
			}
		}
	}

	// Fallback de bandeira: cabeçalho "Bandera" pode ser só ícone CSS (span.fi), resultando em
	// header vazio após remoção. Detecta pelo posicionamento: coluna vazia entre "embarcación" e "tipo".
	if result.Flag == "" {
		for i, h := range headers {
			if h != "" || i >= len(cells) {
				continue
			}
			v := cells[i]
			if v == "" || v == "-" {
				continue
			}
			prevH := ""
			if i > 0 {
				prevH = headers[i-1]
			}
			nextH := ""
			if i < len(headers)-1 {
				nextH = headers[i+1]
			}
			if has(prevH, "embarcación", "embarcacion", "buque", "vessel", "navio") ||
				has(nextH, "tipo", "type") {
				result.Flag = v
				log.Printf("[ciala] bandeira detectada por posição (col %d): %q", i, v)
				break
			}
		}
	}

	// Fallback sem cabeçalhos: varre células por padrão.
	if len(headers) == 0 {
		row.Find("td").Each(func(_ int, td *goquery.Selection) {
			v := cellText(td)
			vLower := strings.ToLower(v)
			switch {
			case result.RiskLevel == "" && has(vLower, "alto"):
				result.RiskLevel = "high"
			case result.RiskLevel == "" && has(vLower, "bajo"):
				result.RiskLevel = "low"
			case result.RiskLevel == "" && has(vLower, "estandar", "estándar", "standard"):
				result.RiskLevel = "standard"
			}
			if result.LastInspectionDate == nil {
				result.LastInspectionDate = extractDateFromText(v)
			}
		})
	}

	// Fallback de risco por classe CSS da linha.
	if result.RiskLevel == "" {
		cls, _ := row.Attr("class")
		clsLower := strings.ToLower(cls)
		switch {
		case strings.Contains(clsLower, "alto") || strings.Contains(clsLower, "danger") || strings.Contains(clsLower, "red"):
			result.RiskLevel = "high"
		case strings.Contains(clsLower, "bajo") || strings.Contains(clsLower, "success") || strings.Contains(clsLower, "green"):
			result.RiskLevel = "low"
		case strings.Contains(clsLower, "estandar") || strings.Contains(clsLower, "warning") || strings.Contains(clsLower, "standard"):
			result.RiskLevel = "standard"
		}
	}

	return result
}

// cellText extrai texto de uma célula removendo ícones (img e span.fi do flag-icons).
// O CIALA usa <span class="fi fi-sg"> para bandeiras — apenas CSS, sem texto útil.
func cellText(td *goquery.Selection) string {
	clone := td.Clone()

	// Guarda fallbacks antes de remover elementos.
	img := clone.Find("img")
	imgFallback := strings.TrimSpace(img.AttrOr("title", img.AttrOr("alt", "")))

	// Remove img e spans de ícone (flag-icons usa classe "fi").
	img.Remove()
	clone.Find("span[class*='fi '], span.fi").Remove()

	// Normaliza espaços — flag-icons pode deixar &nbsp; ( ).
	raw := clone.Text()
	raw = strings.ReplaceAll(raw, " ", " ")
	text := strings.TrimSpace(raw)

	if text == "" {
		return imgFallback
	}
	return text
}

// extractDateFromText extrai uma data de dentro de um texto livre.
// Útil quando a célula traz "Sí 15/01/2024" ou "15-01-2024 (confirmado)".
func extractDateFromText(s string) *time.Time {
	if t := parseDate(s); t != nil {
		return t
	}
	if m := dateRe.FindString(s); m != "" {
		return parseDate(m)
	}
	return nil
}

// parseDate converte uma string exata em data usando formatos comuns do CIALA.
// Inclui formatos com ano de 2 dígitos (DD/MM/AA) — padrão frequente no portal.
func parseDate(s string) *time.Time {
	formats := []string{
		"02/01/2006", "2/1/2006",   // DD/MM/YYYY
		"02/01/06", "2/1/06",       // DD/MM/YY
		"2006-01-02", "06-01-02",   // ISO e variante
		"02-01-2006", "02-01-06",   // DD-MM-YYYY e DD-MM-YY
		"01/02/2006", "1/2/2006",   // MM/DD/YYYY (fallback)
	}
	now := time.Now()
	for _, f := range formats {
		t, err := time.Parse(f, s)
		if err != nil {
			continue
		}
		// Ano de 2 dígitos: Go interpreta 00-68 como 2000-2068, 69-99 como 1969-1999.
		if t.Year() > 1990 && t.Before(now.AddDate(0, 1, 0)) {
			tt := t
			return &tt
		}
	}
	return nil
}

// extractDeficiencies extrai apenas o texto de deficiências da célula "Inspeccionado".
// A data vai para last_inspection_date separadamente — aqui não entra.
// "Seguimiento" é um link de acompanhamento fora do escopo — também não entra.
// Exemplo: "11/11/25 2 deficiencias seguimiento" → "2 deficiencias"
func extractDeficiencies(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	// Nunca inspecionado — sem deficiências contabilizáveis.
	if strings.Contains(lower, "sin inspecc") || strings.Contains(lower, "sin inspección") {
		return ""
	}
	// Remove a data (vai para last_inspection_date).
	remaining := dateRe.ReplaceAllString(s, "")
	// Remove sufixo "seguimiento" (link de acompanhamento, fora do escopo).
	if idx := strings.Index(strings.ToLower(remaining), "seguimiento"); idx >= 0 {
		remaining = remaining[:idx]
	}
	// Normaliza espaços e newlines — o CIALA usa muito whitespace na mesma célula.
	remaining = strings.Join(strings.Fields(remaining), " ")
	// Remove prefixos como "Sí", "Si", "/", "-".
	remaining = strings.TrimLeft(remaining, "SsíiÍI/ -")
	remaining = strings.TrimSpace(remaining)
	if len(remaining) < 2 {
		return ""
	}
	// Ignora se o restante for apenas outra data.
	if extractDateFromText(remaining) != nil && len(remaining) <= 12 {
		return ""
	}
	return remaining
}
