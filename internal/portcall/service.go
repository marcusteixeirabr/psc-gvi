// Package portcall gerencia a lógica de port calls: criação, atualização e merge
// de registros vindos do scraper ZP-21.
package portcall

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/scraper"
	"github.com/marcusteixeirabr/psc-gvi/internal/vessel"
)

// CIALARefresher busca dados atualizados do CIALA para o navio e persiste o
// resultado em vessels (risk_level, last_inspection_date, etc.). nil quando o
// CIALA não está configurado — nesse caso a reconsulta é pulada (ver regras.md §4 R9).
type CIALARefresher func(ctx context.Context, vesselID int64, imo string) error

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
func ProcessManobras(ctx context.Context, q *sqlc.Queries, rows []scraper.ManobrasRow, refresher CIALARefresher) ScrapeResult {
	result := ScrapeResult{RowsFound: len(rows)}
	scrapeTime := time.Now() // timestamp único para todo o ciclo — identifica o lote no dashboard

	for _, group := range groupRows(rows) {
		if err := processGroup(ctx, q, group, &result, scrapeTime, refresher); err != nil {
			name := group[0].VesselName
			slog.Error("erro processando vessel", "component", "portcall", "vessel", name, "error", err)
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", name, err))
		}
	}

	var scrapeTS pgtype.Timestamptz
	_ = scrapeTS.Scan(scrapeTime)

	// Cancela escalas ZP-21 planejadas que não apareceram neste ciclo (status → aborted).
	if err := q.AbortStaleZP21PortCalls(ctx, scrapeTS); err != nil {
		slog.Error("AbortStaleZP21PortCalls falhou", "component", "portcall", "error", err)
	}

	// R5: conclui escalas ativas+atracadas que sumiram do ZP-21 E têm outro navio
	// confirmado no mesmo terminal — evidência direta de que o berço foi liberado.
	// Escalas sem terminal ou sem outro navio no berço permanecem atracadas (R4).
	staleBerthed, err := q.GetStaleBerthedPortCalls(ctx, scrapeTS)
	if err != nil {
		slog.Error("GetStaleBerthedPortCalls falhou", "component", "portcall", "error", err)
	} else {
		for _, row := range staleBerthed {
			if err := autoCompleteDeparture(ctx, q, row.ID); err != nil {
				slog.Error("R5: auto-conclusão falhou", "component", "portcall", "vessel", row.VesselName, "error", err)
			} else {
				slog.Info("R5: vessel suspendeu (sumiu do ZP-21, terminal ocupado por outro navio)", "component", "portcall", "vessel", row.VesselName)
			}
		}
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
func processGroup(ctx context.Context, q *sqlc.Queries, group []scraper.ManobrasRow, result *ScrapeResult, scrapeTime time.Time, refresher CIALARefresher) error {
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
		// R8: navio desatracou do mesmo terminal (normalizado) há menos de 2 dias —
		// trata como ruído/incorreção do ZP-21 e ignora a criação automática.
		// Só se aplica a R1 (automático); registro manual não é afetado.
		if blockedByR8(ctx, q, v.ID, terminalOf(entrada, saida)) {
			return nil
		}
		// Nenhum port call ativo — cria novo.
		return createPortCallEntry(ctx, q, v, entrada, saida, result, scrapeTime, refresher)
	}

	// ── R7: conclusão automática por partida confirmada (active+berthed) ────────
	// Gatilho: ZP-21 mostra APENAS 'saida' (entrada=nil) E situação ≠ "atracado".
	// Quando o navio está partindo, o ZP-21 remove a linha de chegada e mantém só a de saída.
	// Exceção: situação "atracado" com só saída = ETS prevista (suspensão planejada) —
	//   navio ainda está no berço; cai no fluxo de atualização de ETD abaixo, sem concluir.
	// 'entrada+saída' = navio atracado normal (atualiza ETD abaixo, não conclui).
	// 'só entrada'    = atualiza ETA abaixo, não conclui.
	// Conclusão por desaparecimento total é tratada por GetStaleBerthedPortCalls (R4/R5).
	if pc.PortCallStatus == "active" && pc.VesselStatus == "berthed" && saida != nil && entrada == nil {
		if !strings.Contains(saida.Situation, "atrac") {
			// Situação diferente de "atracado" (ou ausente) → navio está partindo. Conclui.
			if err := autoCompleteDeparture(ctx, q, pc.ID); err != nil {
				slog.Error("R7: auto-conclusão falhou", "component", "portcall", "vessel", v.Name, "error", err)
			} else {
				slog.Info("R7: vessel suspendeu (só saida)", "component", "portcall", "vessel", v.Name, "situacao", saida.Situation)
			}
			return nil
		}
		slog.Info("R7 excluído: ETS prevista, não conclui", "component", "portcall", "vessel", v.Name, "situacao", saida.Situation)
	}

	// 4. Atualiza o port call existente.
	result.PortCallsUpdated++
	markZP21Seen(ctx, q, pc.ID, scrapeTime)

	// ── Auto-atracação ──────────────────────────────────────────────────────────
	// Hierarquia: se actual_arrival já está preenchido, ZP-21 NÃO sobrescreve.
	if !pc.ActualArrival.Valid && pc.PortCallStatus == "planned" {
		if entrada == nil && saida != nil {
			// Apenas saída visível → navio atracou entre ciclos de scraping.
			// Melhor data: eta_date armazenado (chegada planejada) ou data corrente.
			berthDate := time.Now()
			if pc.EtaDate.Valid {
				berthDate = pc.EtaDate.Time
			}
			if err := autoRegisterBerthing(ctx, q, pc.ID, berthDate, v, refresher); err != nil {
				slog.Error("auto-atracação falhou", "component", "portcall", "vessel", v.Name, "error", err)
			} else {
				slog.Info("auto-atracação registrada", "component", "portcall", "vessel", v.Name, "data", berthDate.Format("02/01/2006"))
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

// r8CooldownWindow é a janela de bloqueio de re-atracação no mesmo terminal (R8).
const r8CooldownWindow = 48 * time.Hour

// blockedByR8 verifica se o navio desatracou do mesmo terminal (normalizado)
// dentro do cooldown de 2 dias — nesse caso a criação automática de escala (R1)
// deve ser ignorada silenciosamente, pois é sinal de dado desatualizado/duplicado
// do ZP-21, não uma nova escala real. Ver regras.md §4 R8.
func blockedByR8(ctx context.Context, q *sqlc.Queries, vesselID int64, incomingTerminal string) bool {
	terminal := normalizeTerminal(incomingTerminal)
	if terminal == "" {
		return false
	}
	var since pgtype.Timestamptz
	_ = since.Scan(time.Now().Add(-r8CooldownWindow))
	recent, err := q.GetRecentDeparturesByVessel(ctx, sqlc.GetRecentDeparturesByVesselParams{
		VesselID:        vesselID,
		ActualDeparture: since,
	})
	if err != nil {
		slog.Error("R8: falha ao verificar cooldown de terminal", "component", "portcall", "vessel_id", vesselID, "error", err)
		return false
	}
	for _, rc := range recent {
		if rc.Terminal != nil && normalizeTerminal(*rc.Terminal) == terminal {
			return true
		}
	}
	return false
}

// normalizeTerminal normaliza terminal para comparação: minúsculas, sem espaços
// e sem acentos — mesmo padrão usado para normalizar a coluna Situação do ZP-21.
func normalizeTerminal(s string) string {
	return strings.Join(strings.Fields(scraper.StripAccents(strings.ToLower(s))), "")
}

// createPortCallEntry cria um novo port call para o vessel com os dados ZP-21.
// Detecta auto-atracação quando apenas linha de saída está visível.
func createPortCallEntry(ctx context.Context, q *sqlc.Queries, v sqlc.Vessel, entrada, saida *scraper.ManobrasRow, result *ScrapeResult, scrapeTime time.Time, refresher CIALARefresher) error {
	initialStatus := "navigating"
	if entrada == nil && saida != nil {
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

	if initialStatus == "berthed" {
		if err := autoRegisterBerthing(ctx, q, newPC.ID, time.Now(), v, refresher); err != nil {
			slog.Error("auto-atracação falhou na criação", "component", "portcall", "vessel", v.Name, "error", err)
		} else {
			slog.Info("auto-atracação registrada na criação (data corrente)", "component", "portcall", "vessel", v.Name)
		}
	}
	return nil
}

// autoCompleteDeparture conclui automaticamente uma escala ativa.
// Usa data corrente como actual_departure. O snapshot já foi capturado na atracação.
func autoCompleteDeparture(ctx context.Context, q *sqlc.Queries, portCallID int64) error {
	var ts pgtype.Timestamptz
	_ = ts.Scan(midnightOf(time.Now()))
	return q.RegisterDeparture(ctx, sqlc.RegisterDepartureParams{
		ID:              portCallID,
		ActualDeparture: ts,
	})
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
		slog.Error("markZP21Seen falhou", "component", "portcall", "port_call_id", portCallID, "error", err)
	}
}

// autoRegisterBerthing registra a atracação automática via ZP-21 com snapshot de risco/prioridade.
// Só age se actual_arrival ainda estiver vazio (hierarquia: dado consolidado > automático).
func autoRegisterBerthing(ctx context.Context, q *sqlc.Queries, portCallID int64, date time.Time, v sqlc.Vessel, refresher CIALARefresher) error {
	riskLevel := v.RiskLevel
	lastInspDate := v.LastInspectionDate

	// R9: para navios cuja prioridade calculada com o dado em cache já é P1/P2,
	// reconsulta o CIALA antes de congelar o snapshot — o Acordo de Viña del Mar
	// permite lançar uma inspeção feita em outro porto até 5 dias depois, janela
	// maior que o intervalo entre a previsão do ZP-21 e a atracação real. Só se
	// aplica à atracação automática (ver regras.md §4 R9).
	if refresher != nil && v.Imo != nil {
		cached := vessel.CalcPriority(riskLevel, lastInspDate)
		if cached == vessel.P1 || cached == vessel.P2 {
			if err := refresher(ctx, v.ID, *v.Imo); err != nil {
				slog.Warn("R9: reconsulta CIALA na atracação falhou, usando dado em cache", "component", "portcall", "vessel", v.Name, "error", err)
			} else if fresh, gErr := q.GetVessel(ctx, v.ID); gErr == nil {
				riskLevel = fresh.RiskLevel
				lastInspDate = fresh.LastInspectionDate
			}
		}
	}

	var ts pgtype.Timestamptz
	_ = ts.Scan(midnightOf(date))
	priority := vessel.CalcPriority(riskLevel, lastInspDate)
	priorityStr := string(priority)
	return q.RegisterBerthing(ctx, sqlc.RegisterBerthingParams{
		ID:                portCallID,
		ActualArrival:     ts,
		RiskLevelSnapshot: riskLevel,
		PrioritySnapshot:  &priorityStr,
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

// midnightOf retorna meia-noite do dia de t na mesma timezone de t.
//
// Substitui t.Truncate(24*time.Hour) que sempre trunca para meia-noite UTC,
// causando data errada entre 00h00 e 02h59 no fuso de São Paulo (UTC-3):
//   - 01h00 SP = 04h00 UTC → Truncate → 00h00 UTC = 21h00 SP do dia anterior ✗
//   - midnightOf → 00h00 SP do dia correto ✓
//
// Funciona corretamente tanto para time.Now() (timezone SP após time.Local = SP)
// quanto para datas vindas do PostgreSQL DATE (timezone UTC, meia-noite UTC já correta).
func midnightOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// floatNumeric converte float64 para pgtype.Numeric. Retorna inválido para zero.
func floatNumeric(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	if f > 0 {
		_ = n.Scan(strconv.FormatFloat(f, 'f', 2, 64))
	}
	return n
}
