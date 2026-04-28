// Package portcall gerencia a lógica de port calls: criação, atualização e merge
// de registros vindos do scraper ZP-21.
package portcall

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/scraper"
)

// ScrapeResult resume o resultado de um ciclo de scraping ZP-21.
type ScrapeResult struct {
	RowsFound        int
	VesselsCreated   int
	PortCallsCreated int
	PortCallsUpdated int
	Errors           []string
}

// Summary retorna uma string legível para exibir como flash message.
func (r ScrapeResult) Summary() string {
	s := fmt.Sprintf(
		"%d linha(s) · %d navio(s) criado(s) · %d port call(s) criado(s) · %d atualizado(s)",
		r.RowsFound, r.VesselsCreated, r.PortCallsCreated, r.PortCallsUpdated,
	)
	if len(r.Errors) > 0 {
		s += fmt.Sprintf(" · %d erro(s)", len(r.Errors))
	}
	return s
}

// ProcessManobras processa as linhas parseadas do ZP-21 e atualiza o banco de dados.
func ProcessManobras(ctx context.Context, q *sqlc.Queries, rows []scraper.ManobrasRow) ScrapeResult {
	result := ScrapeResult{RowsFound: len(rows)}
	scrapeTime := time.Now() // timestamp único para todo o ciclo — identifica o lote no dashboard

	for _, group := range groupRows(rows) {
		if err := processGroup(ctx, q, group, &result, scrapeTime); err != nil {
			name := group[0].VesselName
			log.Printf("[portcall] erro processando %q: %v", name, err)
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", name, err))
		}
	}

	// Cancela automaticamente escalas ZP-21 planejadas que não apareceram neste ciclo.
	// Condição: zp21_sourced=TRUE + status=planned + last_zp21_seen_at < scrapeTime.
	var scrapeTS pgtype.Timestamptz
	_ = scrapeTS.Scan(scrapeTime)
	if err := q.AbortStaleZP21PortCalls(ctx, scrapeTS); err != nil {
		log.Printf("[portcall] AbortStaleZP21PortCalls falhou: %v", err)
	}

	return result
}

// groupRows agrupa linhas que se referem ao mesmo vessel físico
// (mesmo nome + dimensões dentro de ±5% de tolerância).
func groupRows(rows []scraper.ManobrasRow) [][]scraper.ManobrasRow {
	var groups [][]scraper.ManobrasRow
	used := make([]bool, len(rows))

	for i := range rows {
		if used[i] {
			continue
		}
		group := []scraper.ManobrasRow{rows[i]}
		used[i] = true
		for j := i + 1; j < len(rows); j++ {
			if !used[j] && sameVessel(rows[i], rows[j]) {
				group = append(group, rows[j])
				used[j] = true
			}
		}
		groups = append(groups, group)
	}
	return groups
}

// sameVessel retorna true se duas linhas se referem ao mesmo navio.
// Critério: nome igual (case-insensitive) E dimensões dentro de ±5%.
func sameVessel(a, b scraper.ManobrasRow) bool {
	return strings.EqualFold(a.VesselName, b.VesselName) &&
		scraper.WithinTolerance(a.LOA, b.LOA, 0.05) &&
		scraper.WithinTolerance(a.Beam, b.Beam, 0.05)
}

// processGroup trata todas as linhas de um mesmo vessel em um único ciclo.
func processGroup(ctx context.Context, q *sqlc.Queries, group []scraper.ManobrasRow, result *ScrapeResult, scrapeTime time.Time) error {
	ref := group[0]

	// 1. Identifica ou cria o vessel no banco.
	v, created, err := findOrCreateVessel(ctx, q, ref)
	if err != nil {
		return err
	}
	if created {
		result.VesselsCreated++
	}

	// 2. Separa linhas de entrada e saída.
	var entrada, saida *scraper.ManobrasRow
	for i := range group {
		switch group[i].ManeuverType {
		case "entrada":
			entrada = &group[i]
		case "saida":
			saida = &group[i]
		}
	}

	// 3. Busca port call ativo existente para este vessel.
	pc, err := q.GetActivePortCallByVessel(ctx, v.ID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("buscando port call ativo: %w", err)
		}

		// Nenhum port call ativo — cria um novo com todos os dados disponíveis.
		initialStatus := "navigating"
		if entrada == nil && saida != nil {
			// Apenas saída: o navio já está atracado.
			initialStatus = "berthed"
		}

		newPC, err := q.CreatePortCall(ctx, sqlc.CreatePortCallParams{
			VesselID:       v.ID,
			Terminal:       optStr(terminalOf(entrada, saida)),
			VesselStatus:   initialStatus,
			PortCallStatus: "planned",
			EtaDate:        parsePgDate(strIf(entrada, func(r *scraper.ManobrasRow) string { return r.RawDate })),
			EtaTime:        parsePgTime(strIf(entrada, func(r *scraper.ManobrasRow) string { return r.RawTime })),
			EtdDate:        parsePgDate(strIf(saida, func(r *scraper.ManobrasRow) string { return r.RawDate })),
			EtdTime:        parsePgTime(strIf(saida, func(r *scraper.ManobrasRow) string { return r.RawTime })),
			Zp21Sourced:    true,
		})
		if err != nil {
			return fmt.Errorf("criando port call: %w", err)
		}
		result.PortCallsCreated++

		markZP21Seen(ctx, q, newPC.ID, scrapeTime)

		// Auto-atracação: novo port call criado já com status berthed (app estava fora).
		// Usa data corrente — não sabemos quando exatamente atracou.
		if initialStatus == "berthed" {
			if err := autoRegisterBerthing(ctx, q, newPC.ID, time.Now()); err != nil {
				log.Printf("[portcall] auto-atracação falhou para %s: %v", v.Name, err)
			} else {
				log.Printf("[portcall] auto-atracação registrada para %s (data corrente)", v.Name)
			}
		}
		return nil
	}

	// 4. Atualiza o port call existente.
	result.PortCallsUpdated++
	markZP21Seen(ctx, q, pc.ID, scrapeTime)

	// ── Auto-atracação ──────────────────────────────────────────────────────────
	// Hierarquia: se actual_arrival já está preenchido, ZP-21 NÃO sobrescreve.
	// Se ainda não está, e detectamos que o navio já atracou, registra automaticamente.
	if !pc.ActualArrival.Valid && pc.PortCallStatus == "planned" {
		if entrada == nil && saida != nil {
			// Apenas saída visível → navio atracou entre ciclos de scraping.
			// Melhor data: eta_date armazenado (chegada planejada) ou data corrente.
			berthDate := time.Now()
			if pc.EtaDate.Valid {
				berthDate = pc.EtaDate.Time
			}
			if err := autoRegisterBerthing(ctx, q, pc.ID, berthDate); err != nil {
				log.Printf("[portcall] auto-atracação falhou para %s: %v", v.Name, err)
			} else {
				log.Printf("[portcall] auto-atracação: %s → data %s", v.Name, berthDate.Format("02/01/2006"))
			}
		}
	}

	// ── Atualiza previsões ZP-21 (só se dados não estiverem consolidados) ───────
	if entrada != nil && !pc.ActualArrival.Valid {
		if err := q.UpdatePortCallETA(ctx, sqlc.UpdatePortCallETAParams{
			ID:       pc.ID,
			EtaDate:  parsePgDate(entrada.RawDate),
			EtaTime:  parsePgTime(entrada.RawTime),
			Terminal: optStr(entrada.Terminal),
		}); err != nil {
			return fmt.Errorf("atualizando ETA: %w", err)
		}
	}

	// ETD pode ser atualizado até a partida ser registrada.
	if saida != nil && !pc.ActualDeparture.Valid {
		if err := q.UpdatePortCallETD(ctx, sqlc.UpdatePortCallETDParams{
			ID:       pc.ID,
			EtdDate:  parsePgDate(saida.RawDate),
			EtdTime:  parsePgTime(saida.RawTime),
			Terminal: optStr(saida.Terminal),
		}); err != nil {
			return fmt.Errorf("atualizando ETD: %w", err)
		}
	}

	return nil
}

// markZP21Seen atualiza last_zp21_seen_at para o port call dado.
// Erros são apenas logados — falha aqui não deve interromper o ciclo de scraping.
func markZP21Seen(ctx context.Context, q *sqlc.Queries, portCallID int64, t time.Time) {
	var ts pgtype.Timestamptz
	_ = ts.Scan(t)
	if err := q.UpdatePortCallZP21Seen(ctx, sqlc.UpdatePortCallZP21SeenParams{
		ID:             portCallID,
		LastZp21SeenAt: ts,
	}); err != nil {
		log.Printf("[portcall] markZP21Seen falhou para port call %d: %v", portCallID, err)
	}
}

// autoRegisterBerthing registra a atracação automática via ZP-21.
// Só age se actual_arrival ainda estiver vazio (hierarquia: dado consolidado > automático).
func autoRegisterBerthing(ctx context.Context, q *sqlc.Queries, portCallID int64, date time.Time) error {
	var ts pgtype.Timestamptz
	_ = ts.Scan(date.Truncate(24 * time.Hour))
	return q.RegisterBerthing(ctx, sqlc.RegisterBerthingParams{
		ID:            portCallID,
		ActualArrival: ts,
	})
}

// findOrCreateVessel busca um vessel por nome + dimensões (±5%).
// Se não encontrado, cria um novo com category='estrangeiro'.
func findOrCreateVessel(ctx context.Context, q *sqlc.Queries, row scraper.ManobrasRow) (sqlc.Vessel, bool, error) {
	candidates, err := q.GetVesselsByName(ctx, row.VesselName)
	if err != nil {
		return sqlc.Vessel{}, false, fmt.Errorf("buscando vessel por nome: %w", err)
	}

	for _, v := range candidates {
		if dimensionsMatch(row.LOA, row.Beam, pgNumericF(v.LengthM), pgNumericF(v.BeamM)) {
			return v, false, nil
		}
	}

	// Navio não encontrado ou dimensões fora da tolerância — cria novo.
	newV, err := q.CreateVessel(ctx, sqlc.CreateVesselParams{
		Name:    row.VesselName,
		LengthM: floatNumeric(row.LOA),
		BeamM:   floatNumeric(row.Beam),
		// Category nil → NULL → operador classifica depois (scraper não presume)
	})
	if err != nil {
		return sqlc.Vessel{}, false, fmt.Errorf("criando vessel: %w", err)
	}
	return newV, true, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// parsePgDate converte "DD/MM/YYYY" em pgtype.Date. Retorna inválido para strings vazias.
func parsePgDate(s string) pgtype.Date {
	var d pgtype.Date
	s = strings.TrimSpace(s)
	if s == "" {
		return d
	}
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	t, err := time.ParseInLocation("02/01/2006", s, loc)
	if err != nil {
		return d
	}
	_ = d.Scan(t)
	return d
}

// parsePgTime converte horário em pgtype.Time.
// Aceita "HH:MM", "HHhMM", "HHhMM", "HH.MM", "HHMM" — o ZP-21 varia o separador.
// Retorna inválido para TBC, vazio ou texto sem hora reconhecível.
func parsePgTime(s string) pgtype.Time {
	s = strings.TrimSpace(s)
	if !scraper.TimeIsKnown(s) {
		return pgtype.Time{}
	}
	// Normaliza separadores: h, H, . → :
	normalized := strings.Map(func(r rune) rune {
		if r == 'h' || r == 'H' || r == '.' {
			return ':'
		}
		return r
	}, s)
	// Tenta HH:MM
	parts := strings.SplitN(normalized, ":", 2)
	if len(parts) == 2 {
		h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 == nil && err2 == nil && h >= 0 && h <= 23 && m >= 0 && m <= 59 {
			return pgtype.Time{Microseconds: int64(h*3600+m*60) * 1_000_000, Valid: true}
		}
	}
	// Tenta HHMM (4 dígitos sem separador)
	if len(s) == 4 {
		h, err1 := strconv.Atoi(s[:2])
		m, err2 := strconv.Atoi(s[2:])
		if err1 == nil && err2 == nil && h >= 0 && h <= 23 && m >= 0 && m <= 59 {
			return pgtype.Time{Microseconds: int64(h*3600+m*60) * 1_000_000, Valid: true}
		}
	}
	return pgtype.Time{}
}

// optStr converte string vazia em nil para campos nullable.
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// terminalOf retorna o terminal de entrada ou, se vazio, de saída.
func terminalOf(entrada, saida *scraper.ManobrasRow) string {
	if entrada != nil && entrada.Terminal != "" {
		return entrada.Terminal
	}
	if saida != nil {
		return saida.Terminal
	}
	return ""
}

// strIf retorna o resultado de fn(r) se r não for nil, ou "" se for nil.
func strIf(r *scraper.ManobrasRow, fn func(*scraper.ManobrasRow) string) string {
	if r == nil {
		return ""
	}
	return fn(r)
}

// dimensionsMatch verifica se as dimensões do ZP-21 batem com o vessel do BD.
//
// Regras:
//   - ZP-21 não fornece dimensões (0) → aceita qualquer vessel pelo nome
//   - ZP-21 fornece dimensões mas BD não tem (0) → NÃO bate (criar novo vessel)
//   - Ambos têm dimensões → comparar com tolerância de ±5%
func dimensionsMatch(rowLOA, rowBeam, vesselLOA, vesselBeam float64) bool {
	if rowLOA == 0 || rowBeam == 0 {
		return true // ZP-21 sem dimensões → não descarta por dimensão
	}
	if vesselLOA == 0 || vesselBeam == 0 {
		return false // ZP-21 tem dimensões, BD não → incerto, cria novo
	}
	return scraper.WithinTolerance(rowLOA, vesselLOA, 0.05) &&
		scraper.WithinTolerance(rowBeam, vesselBeam, 0.05)
}

// pgNumericF converte pgtype.Numeric para float64, retornando 0 se inválido.
func pgNumericF(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

// floatNumeric converte float64 para pgtype.Numeric. Retorna inválido para zero.
func floatNumeric(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	if f > 0 {
		_ = n.Scan(strconv.FormatFloat(f, 'f', 2, 64))
	}
	return n
}
