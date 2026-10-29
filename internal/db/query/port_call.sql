-- Queries de PortCall para o sqlc.

-- name: GetVesselsByName :many
-- Retorna todos os vessels com o nome dado (case-insensitive), incluindo não-acompanhados.
-- O critério de identidade do scraper é nome + dimensões — não o status acompanhado.
-- Navios de estado (Marinha, etc.) não têm IMO e ficam com acompanhado=FALSE, mas devem
-- ser reconhecidos para evitar duplicatas. ORDER BY id garante preferência pelo registro
-- mais antigo (manual) quando há múltiplos candidatos com o mesmo nome.
SELECT * FROM vessels WHERE name ILIKE $1 ORDER BY id;

-- name: CreatePortCall :one
-- Cria um novo port call a partir do scraper ZP-21.
-- risk_level_snapshot e priority_snapshot são preenchidos depois (via CIALA).
INSERT INTO port_calls (
    vessel_id, terminal, vessel_status, port_call_status,
    eta_date, eta_time, etd_date, etd_time, zp21_sourced
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
) RETURNING *;

-- name: GetActivePortCallByVessel :one
-- Retorna o port call mais recente com status planned ou active para um vessel.
-- Usado pelo scraper para decidir se deve criar ou atualizar.
SELECT id, vessel_id, berth, vessel_status, port_call_status,
       eta_date, eta_time, etd_date, etd_time,
       actual_arrival, actual_departure,
       risk_level_snapshot, priority_snapshot,
       zp21_sourced, created_at, updated_at, terminal, last_zp21_seen_at
FROM port_calls
WHERE vessel_id = $1
  AND (port_call_status = 'planned' OR port_call_status = 'active')
ORDER BY created_at DESC
LIMIT 1;

-- name: GetRecentDeparturesByVessel :many
-- R8: escalas concluídas do navio com desatracação a partir do timestamp dado.
-- Usado para bloquear a criação automática (R1) de uma nova escala no mesmo
-- terminal dentro do cooldown de 2 dias. Ver regras.md §4 R8.
SELECT id, terminal, actual_departure
FROM port_calls
WHERE vessel_id = $1
  AND port_call_status = 'completed'
  AND actual_departure IS NOT NULL
  AND actual_departure >= $2
ORDER BY actual_departure DESC;

-- name: GetRecentAbortedByVessel :many
-- R10: escalas 'planned' abortadas (R6) do navio, atualizadas dentro da janela.
-- Usado para reverter o cancelamento em vez de criar uma escala nova quando o
-- navio reaparece no ZP-21 no mesmo terminal pouco depois — sinal de sumiço
-- transitório de um ciclo do ZP-21, não um cancelamento real. Ver regras.md §4 R10.
SELECT id, terminal, updated_at
FROM port_calls
WHERE vessel_id = $1
  AND port_call_status = 'aborted'
  AND zp21_sourced = TRUE
  AND updated_at >= $2
ORDER BY updated_at DESC;

-- name: RevertAbortedPortCall :exec
-- R10: reverte uma escala abortada (R6) de volta para 'planned', reaproveitando
-- a linha existente em vez de criar uma nova, com os dados atualizados do ZP-21.
UPDATE port_calls
SET port_call_status = 'planned',
    terminal          = $2,
    vessel_status     = $3,
    eta_date          = $4,
    eta_time          = $5,
    etd_date          = $6,
    etd_time          = $7,
    updated_at        = NOW()
WHERE id = $1;

-- name: UpdatePortCallZP21Seen :exec
-- Registra o timestamp do ciclo de scraping em que o port call foi visto.
-- Chamado para todo port call processado pelo scraper ZP-21.
UPDATE port_calls SET last_zp21_seen_at = $2 WHERE id = $1;

-- name: GetLastZP21ScrapeTime :one
-- Retorna o timestamp do ciclo de scraping ZP-21 mais recente.
-- Usado pelo dashboard para filtrar navios da última consulta.
SELECT last_zp21_seen_at
FROM port_calls
WHERE zp21_sourced = TRUE
  AND last_zp21_seen_at IS NOT NULL
ORDER BY last_zp21_seen_at DESC
LIMIT 1;

-- name: AbortStaleZP21PortCalls :exec
-- Cancela escalas ZP-21 planejadas que não apareceram na última consulta.
-- Executado ao final de cada ciclo: port calls com last_zp21_seen_at anterior ao
-- ciclo atual ainda estão 'planned' → sumiu do ZP-21 → status = 'aborted'.
UPDATE port_calls
SET port_call_status = 'aborted', updated_at = NOW()
WHERE zp21_sourced = TRUE
  AND port_call_status = 'planned'
  AND (last_zp21_seen_at IS NULL OR last_zp21_seen_at < $1);

-- name: GetStaleBerthedPortCalls :many
-- Retorna escalas ativas+atracadas que sumiram do ZP-21 E têm outro navio confirmado
-- no mesmo terminal (R5). Essa combinação prova que o berço foi liberado pelo navio original.
-- Escalas sem terminal ou sem outro navio no mesmo terminal permanecem atracadas (R4).
SELECT pc.id, pc.vessel_id,
       v.name AS vessel_name,
       v.risk_level, v.last_inspection_date
FROM port_calls pc
JOIN vessels v ON v.id = pc.vessel_id
WHERE pc.zp21_sourced = TRUE
  AND pc.port_call_status = 'active'
  AND pc.vessel_status = 'berthed'
  AND (pc.last_zp21_seen_at IS NULL OR pc.last_zp21_seen_at < $1)
  AND pc.terminal IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM port_calls pc2
      WHERE pc2.id != pc.id
        AND pc2.vessel_status = 'berthed'
        AND pc2.port_call_status = 'active'
        AND pc2.terminal = pc.terminal
  );

-- name: UpdatePortCallETA :exec
-- Atualiza a previsão de chegada e o terminal a partir de uma linha de entrada do ZP-21.
-- COALESCE mantém o terminal existente se o novo for NULL.
UPDATE port_calls
SET eta_date  = $2,
    eta_time  = $3,
    terminal  = COALESCE($4, terminal),
    updated_at = NOW()
WHERE id = $1;

-- name: ListDashboardEntries :many
-- Retorna navios acompanhados com port calls ativos para avaliação no dashboard.
-- Regra de exibição (aplicada no handler Go):
--   • Estrangeiros não afretados: sempre incluídos (filtro de prioridade no handler).
--   • Nacionais (brasil/brazil) e afretados: incluídos apenas se tiverem
--     deficiências CIALA registradas — sem inspeção na escala atual.
-- O filtro SQL inclui a segunda categoria para que o handler possa avaliá-la.
-- Ordenado por data (ETA ou ETD) — inspetores decidem com base no horário disponível.
SELECT
    v.id, v.name, v.imo, v.flag, v.length_m, v.beam_m, v.year_built,
    v.risk_level, v.last_inspection_date, v.last_inspection_deficiencies,
    v.afretado, v.acompanhado,
    pc.id          AS port_call_id,
    pc.terminal,
    pc.eta_date,
    pc.eta_time,
    pc.etd_date,
    pc.etd_time,
    pc.vessel_status,
    pc.port_call_status,
    pc.zp21_sourced,
    pc.last_zp21_seen_at,
    ins.id          AS inspection_id,
    ins.result      AS inspection_result
FROM vessels v
JOIN port_calls pc ON pc.vessel_id = v.id
LEFT JOIN inspections ins ON ins.port_call_id = pc.id
WHERE v.acompanhado = TRUE
  AND (pc.port_call_status = 'planned' OR pc.port_call_status = 'active')
  AND (
    -- Estrangeiros não afretados: sempre candidatos ao dashboard
    (v.afretado = FALSE AND (v.flag IS NULL OR LOWER(TRIM(v.flag)) NOT IN ('brazil', 'brasil')))
    OR
    -- Nacionais e afretados: apenas se tiverem deficiências CIALA (filtro fino no handler)
    (v.last_inspection_deficiencies IS NOT NULL AND v.last_inspection_deficiencies != '')
  )
ORDER BY COALESCE(pc.eta_date, pc.etd_date) ASC NULLS LAST, v.name ASC;

-- name: ReassignPortCalls :exec
-- Transfere todos os port calls de um vessel para outro.
-- Usado na operação de mesclar duplicatas.
UPDATE port_calls
SET vessel_id = sqlc.arg(target_id)
WHERE vessel_id = sqlc.arg(source_id);

-- name: UpdatePortCallETD :exec
-- Atualiza a previsão de saída a partir de uma linha de saída do ZP-21.
-- COALESCE mantém o terminal existente se o novo for NULL.
UPDATE port_calls
SET etd_date  = $2,
    etd_time  = $3,
    terminal  = COALESCE($4, terminal),
    updated_at = NOW()
WHERE id = $1;

-- name: ListEscalas :many
-- Lista todas as escalas com dados do navio e inspeção, ordenadas por data desc.
-- Filtragem opcional por mês (0 = todos) e status ('' = todos).
SELECT
    pc.id, pc.vessel_id, pc.terminal,
    pc.eta_date, pc.etd_date,
    pc.actual_arrival, pc.actual_departure,
    pc.vessel_status, pc.port_call_status,
    pc.risk_level_snapshot, pc.priority_snapshot,
    v.name AS vessel_name, v.imo AS vessel_imo,
    v.flag AS vessel_flag, v.afretado AS vessel_afretado, v.acompanhado AS vessel_acompanhado,
    v.risk_level AS vessel_risk_level,
    v.last_inspection_date AS vessel_last_inspection_date,
    ins.id     AS inspection_id,
    ins.result AS inspection_result
FROM port_calls pc
JOIN vessels v ON v.id = pc.vessel_id
LEFT JOIN inspections ins ON ins.port_call_id = pc.id
WHERE (sqlc.arg(status)::text = '' OR pc.port_call_status = sqlc.arg(status)::text)
  AND (sqlc.arg(vessel_id)::bigint = 0 OR pc.vessel_id = sqlc.arg(vessel_id)::bigint)
  AND (sqlc.arg(year)::int = 0
       OR EXTRACT(YEAR FROM COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date)) = sqlc.arg(year)::int)
  AND (sqlc.arg(month)::int = 0
       OR EXTRACT(MONTH FROM COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date)) = sqlc.arg(month)::int)
ORDER BY COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date) ASC NULLS LAST,
         pc.created_at ASC;

-- name: GetEscala :one
-- Retorna uma escala pelo ID com dados do navio.
SELECT
    pc.id, pc.vessel_id, pc.terminal,
    pc.eta_date, pc.etd_date,
    pc.actual_arrival, pc.actual_departure,
    pc.vessel_status, pc.port_call_status,
    pc.risk_level_snapshot, pc.priority_snapshot,
    v.name AS vessel_name, v.imo AS vessel_imo,
    v.flag AS vessel_flag, v.afretado AS vessel_afretado, v.acompanhado AS vessel_acompanhado,
    v.risk_level AS vessel_risk_level,
    v.last_inspection_date AS vessel_last_inspection_date
FROM port_calls pc
JOIN vessels v ON v.id = pc.vessel_id
WHERE pc.id = $1;

-- name: RegisterBerthing :exec
-- Registra a atracação efetiva: data real de chegada + snapshot de risco/prioridade + muda status para ativo.
-- O snapshot é capturado aqui (estado do navio na chegada) e só pode ser alterado por uma inspeção.
-- last_inspection_snapshot: última inspeção PSC conhecida (CIALA) na chegada — regras.md §15.
UPDATE port_calls
SET actual_arrival           = $2,
    vessel_status            = 'berthed',
    port_call_status         = 'active',
    risk_level_snapshot      = $3,
    priority_snapshot        = $4,
    last_inspection_snapshot = $5,
    updated_at               = NOW()
WHERE id = $1;

-- name: UpdatePortCallSnapshot :exec
-- Salva o snapshot de risco/prioridade no momento da inspeção (estado pré-inspeção).
-- last_inspection_snapshot também é o valor PRÉ-inspeção (a inspeção do GVI
-- em si aparece pela tabela inspections, não por este campo).
UPDATE port_calls
SET risk_level_snapshot      = $2,
    priority_snapshot        = $3,
    last_inspection_snapshot = $4,
    updated_at               = NOW()
WHERE id = $1;

-- name: RegisterDeparture :exec
-- Registra a partida efetiva: data real de saída + conclui a escala.
-- O snapshot (risco/prioridade) já foi capturado na atracação — não é alterado aqui.
UPDATE port_calls
SET actual_departure  = $2,
    port_call_status  = 'completed',
    updated_at        = NOW()
WHERE id = $1;

-- name: GetPortCallByID :one
SELECT id, vessel_id, berth, vessel_status, port_call_status,
       eta_date, eta_time, etd_date, etd_time,
       actual_arrival, actual_departure,
       risk_level_snapshot, priority_snapshot,
       zp21_sourced, created_at, updated_at, terminal, last_zp21_seen_at,
       report_month_override
FROM port_calls WHERE id = $1;

-- name: UpdatePortCallFull :exec
-- Edição completa de uma escala pelo usuário.
-- vessel_status é derivado automaticamente: actual_arrival preenchido → berthed, senão navigating.
-- report_month_override: ver regras.md §13 — NULL = usa actual_arrival (padrão).
UPDATE port_calls
SET terminal              = $2,
    eta_date              = $3,
    eta_time              = $4,
    etd_date              = $5,
    etd_time              = $6,
    actual_arrival        = $7,
    actual_departure      = $8,
    port_call_status      = $9,
    risk_level_snapshot   = $10,
    priority_snapshot     = $11,
    report_month_override = $12,
    -- Edição manual não mexe no snapshot de última inspeção (regras.md §15), exceto:
    -- sem atracação → limpa; atracação registrada pela 1ª vez aqui → captura a
    -- data atual do navio, desde que não seja posterior à atracação informada.
    last_inspection_snapshot = CASE
        WHEN $7::timestamptz IS NULL THEN NULL
        ELSE COALESCE(last_inspection_snapshot,
                      (SELECT v.last_inspection_date FROM vessels v
                       JOIN port_calls p2 ON p2.vessel_id = v.id
                       WHERE p2.id = $1 AND v.last_inspection_date <= $7::timestamptz::date))
    END,
    vessel_status         = CASE WHEN $7::timestamptz IS NOT NULL THEN 'berthed' ELSE 'navigating' END,
    updated_at            = NOW()
WHERE id = $1;

-- name: DeletePortCall :exec
-- Remove uma escala. Falha com FK error se houver inspeção vinculada.
DELETE FROM port_calls WHERE id = $1;

-- name: ListEscalasByVessel :many
-- Histórico de escalas de um navio específico, mais recentes primeiro.
SELECT
    pc.id, pc.vessel_id, pc.terminal,
    pc.eta_date, pc.etd_date,
    pc.actual_arrival, pc.actual_departure,
    pc.vessel_status, pc.port_call_status,
    pc.risk_level_snapshot, pc.priority_snapshot,
    v.name AS vessel_name, v.imo AS vessel_imo,
    v.flag AS vessel_flag, v.afretado AS vessel_afretado, v.acompanhado AS vessel_acompanhado,
    v.risk_level AS vessel_risk_level,
    v.last_inspection_date AS vessel_last_inspection_date,
    ins.id     AS inspection_id,
    ins.result AS inspection_result
FROM port_calls pc
JOIN vessels v ON v.id = pc.vessel_id
LEFT JOIN inspections ins ON ins.port_call_id = pc.id
WHERE pc.vessel_id = $1
ORDER BY COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date) DESC NULLS LAST,
         pc.created_at DESC;

-- name: UpdateBerthingDate :exec
-- Corrige a data de atracação manualmente (editores — sobrescreve dado automático).
UPDATE port_calls
SET actual_arrival   = $2,
    vessel_status    = 'berthed',
    port_call_status = 'active',
    updated_at       = NOW()
WHERE id = $1;

-- name: UpdateDepartureDate :exec
-- Corrige a data de partida manualmente (editores — sobrescreve dado automático).
UPDATE port_calls
SET actual_departure = $2,
    updated_at       = NOW()
WHERE id = $1;

-- name: CancelBerthing :exec
-- Cancela a atracação: limpa actual_arrival, actual_departure e snapshots.
-- Alerta: também apaga dados de suspensão — o front-end deve avisar o usuário.
-- report_month_override também é limpo: sem actual_departure, o override (que
-- se baseia no mês da desatracação) fica órfão/sem sentido. Ver regras.md §13.
UPDATE port_calls
SET actual_arrival         = NULL,
    actual_departure       = NULL,
    vessel_status          = 'navigating',
    port_call_status       = 'planned',
    risk_level_snapshot    = NULL,
    priority_snapshot      = NULL,
    last_inspection_snapshot = NULL,
    report_month_override  = NULL,
    updated_at             = NOW()
WHERE id = $1;

-- name: CancelSuspension :exec
-- Cancela a suspensão: limpa actual_departure e reverte escala para ativa.
-- report_month_override também é limpo (ver comentário em CancelBerthing).
UPDATE port_calls
SET actual_departure       = NULL,
    port_call_status       = 'active',
    risk_level_snapshot    = NULL,
    priority_snapshot      = NULL,
    report_month_override  = NULL,
    updated_at             = NOW()
WHERE id = $1;

-- name: ListReportEntries :many
-- Uma linha por navio/mês. Estrangeiros não-afretados primeiro, ordenados pela
-- primeira atracação do navio no período; afretados e nacionais por último.
-- Dados de exibição (terminal, datas, status) vêm da escala MAIS RECENTE do mês.
-- Risco/prioridade vêm da escala de MAIOR PRECEDÊNCIA do mês (P1 > P2 > P3 >
-- N/A > N/D), para a linha refletir a janela que o navio de fato ocupou —
-- ex.: P1 inspecionado + retorno P3 no mesmo mês mostra P1 (regras.md §15).
-- As inspeções do GVI vêm de ListReportInspections (pode haver mais de uma).
-- last_inspection_snapshot: última inspeção PSC (CIALA) congelada na atracação
-- da escala mais recente — exibida só quando o GVI não inspecionou no mês.
WITH period_vessels AS (
    -- Apenas escalas com atracação registrada — planejadas ficam de fora.
    -- Bucketiza por report_month_override quando definido (escala de fronteira
    -- marcada manualmente para contar no mês da desatracação — ver regras.md
    -- §13), senão pelo mês real de actual_arrival.
    SELECT pc.id, pc.vessel_id,
           pc.actual_arrival::date AS arrival_date
    FROM port_calls pc
    JOIN vessels v ON v.id = pc.vessel_id
    WHERE v.acompanhado = TRUE
      AND pc.actual_arrival IS NOT NULL
      AND EXTRACT(YEAR  FROM COALESCE(pc.report_month_override, pc.actual_arrival::date)) = sqlc.arg(year)::int
      AND EXTRACT(MONTH FROM COALESCE(pc.report_month_override, pc.actual_arrival::date)) = sqlc.arg(month)::int
),
vessel_first_arrival AS (
    SELECT vessel_id, MIN(arrival_date) AS first_arrival
    FROM period_vessels GROUP BY vessel_id
),
-- Escala de maior precedência de prioridade do navio no mês (desempate: a mais recente).
vessel_best_priority AS (
    SELECT DISTINCT ON (pv.vessel_id)
        pv.vessel_id, pc.risk_level_snapshot, pc.priority_snapshot
    FROM period_vessels pv
    JOIN port_calls pc ON pc.id = pv.id
    ORDER BY pv.vessel_id,
             CASE pc.priority_snapshot
                 WHEN 'P1' THEN 1 WHEN 'P2' THEN 2 WHEN 'P3' THEN 3
                 WHEN 'N/A' THEN 4 WHEN 'N/D' THEN 5 ELSE 6 END,
             pc.actual_arrival DESC
),
ranked AS (
    SELECT
        pc.id, pc.vessel_id, pc.terminal,
        pc.eta_date, pc.etd_date,
        pc.actual_arrival, pc.actual_departure,
        pc.port_call_status, pc.last_inspection_snapshot,
        (pc.report_month_override IS NOT NULL)::bool AS month_overridden,
        v.name AS vessel_name, v.imo AS vessel_imo, v.flag AS vessel_flag,
        v.afretado AS vessel_afretado, v.year_built AS vessel_year_built,
        v.vessel_type AS vessel_type,
        vfa.first_arrival,
        ROW_NUMBER() OVER (
            -- Última atracação real do navio no período (sem fallback para planejadas).
            PARTITION BY pc.vessel_id
            ORDER BY pc.actual_arrival DESC NULLS LAST
        ) AS rn
    FROM period_vessels pv
    JOIN port_calls pc ON pc.id = pv.id
    JOIN vessels v ON v.id = pc.vessel_id
    JOIN vessel_first_arrival vfa ON vfa.vessel_id = pc.vessel_id
)
SELECT r.id, r.vessel_id, r.terminal, r.eta_date, r.etd_date,
       r.actual_arrival, r.actual_departure, r.port_call_status,
       bp.risk_level_snapshot, bp.priority_snapshot, r.month_overridden,
       r.vessel_name, r.vessel_imo, r.vessel_flag, r.vessel_afretado,
       r.vessel_year_built, r.vessel_type,
       r.last_inspection_snapshot
FROM ranked r
JOIN vessel_best_priority bp ON bp.vessel_id = r.vessel_id
WHERE r.rn = 1
ORDER BY
    -- Estrangeiros não afretados primeiro; brasileiros e afretados no final.
    CASE WHEN r.vessel_afretado = FALSE
              AND (r.vessel_flag IS NULL OR LOWER(TRIM(r.vessel_flag)) NOT IN ('brazil','brasil'))
         THEN 0 ELSE 1 END ASC,
    r.actual_arrival ASC NULLS LAST,
    r.vessel_name ASC;

-- name: ListReportInspections :many
-- Todas as inspeções do GVI nas escalas do mês (mesmo bucketing de
-- ListReportEntries), para a célula "Inspeção" do relatório. Um navio pode ter
-- mais de uma (ex.: inspeção P1 + perícia de acidente no retorno — regras.md §15).
-- out_of_window: a escala inspecionada NÃO estava em P1/P2 (regras.md §14).
SELECT pc.vessel_id,
       ins.id AS inspection_id,
       ins.inspection_date,
       ins.result,
       (pc.priority_snapshot IS NULL OR pc.priority_snapshot NOT IN ('P1','P2'))::bool AS out_of_window
FROM inspections ins
JOIN port_calls pc ON pc.id = ins.port_call_id
JOIN vessels v ON v.id = pc.vessel_id
WHERE v.acompanhado = TRUE
  AND pc.actual_arrival IS NOT NULL
  AND EXTRACT(YEAR  FROM COALESCE(pc.report_month_override, pc.actual_arrival::date)) = sqlc.arg(year)::int
  AND EXTRACT(MONTH FROM COALESCE(pc.report_month_override, pc.actual_arrival::date)) = sqlc.arg(month)::int
ORDER BY pc.vessel_id, ins.inspection_date ASC, ins.id ASC;

-- name: CountReportKPI :one
-- KPI do mês.
-- total_porto   = navios únicos com atracação no mês
-- estrangeiros  = estrangeiros não-afretados (únicos)
-- sujeitos      = estrangeiros (únicos) com a escala mais recente do mês em P1/P2
--                 OU inspecionados por nós numa escala do mês que estava em P1/P2
-- inspected     = número de INSPEÇÕES do GVI em estrangeiros no mês (não de
--                 navios) — inclusive fora da janela (perícia, clandestinos,
--                 denúncia). Ex.: inspeção P1 + perícia no retorno = 2 sobre 1
--                 sujeito (regras.md §15).
-- Inspeção fora da janela conta no numerador mas NÃO torna o navio "sujeito"
-- (regras.md §14). Por isso o % (inspected/sujeitos) pode passar de 100%; o
-- handler limita a 100% (report.KPIPercent).
-- Por que "mais recente" e não "qualquer escala" para P1/P2: um P1/P2 que NÃO foi
-- inspecionado por nós normalmente significa que o navio foi inspecionado em outro
-- porto (ou o CIALA recalculou o risco) — a escala mais recente do mês já reflete
-- isso. Só uma inspeção NOSSA em escala P1/P2 "vence" sobre uma escala posterior
-- do mesmo mês (regras.md §10/§12).
WITH period_calls AS (
    -- Bucketiza por report_month_override quando definido (ver regras.md §13),
    -- senão pelo mês real de actual_arrival.
    SELECT pc.id, pc.vessel_id, pc.priority_snapshot,
           ROW_NUMBER() OVER (PARTITION BY pc.vessel_id ORDER BY pc.actual_arrival DESC) AS rn
    FROM port_calls pc
    JOIN vessels v ON v.id = pc.vessel_id
    WHERE v.acompanhado = TRUE
      AND pc.actual_arrival IS NOT NULL
      AND EXTRACT(YEAR  FROM COALESCE(pc.report_month_override, pc.actual_arrival::date)) = sqlc.arg(year)::int
      AND EXTRACT(MONTH FROM COALESCE(pc.report_month_override, pc.actual_arrival::date)) = sqlc.arg(month)::int
),
vessel_agg AS (
    SELECT
        pcs.vessel_id,
        BOOL_OR(pcs.rn = 1 AND pcs.priority_snapshot IN ('P1','P2')) AS latest_in_window,
        -- Inspeção nossa numa escala que estava NA janela (P1/P2): só esta
        -- "vence" e mantém o navio como sujeito (regras.md §10/§12/§14).
        BOOL_OR(ins.id IS NOT NULL AND pcs.priority_snapshot IN ('P1','P2')) AS inspected_in_window,
        -- Cada escala tem no máximo 1 inspeção (uq_inspections_port_call).
        COUNT(ins.id) AS n_inspections
    FROM period_calls pcs
    LEFT JOIN inspections ins ON ins.port_call_id = pcs.id
    GROUP BY pcs.vessel_id
),
foreign_agg AS (
    SELECT va.*,
           (v.afretado = FALSE
            AND (v.flag IS NULL OR LOWER(TRIM(v.flag)) NOT IN ('brazil','brasil'))) AS is_foreign
    FROM vessel_agg va
    JOIN vessels v ON v.id = va.vessel_id
)
SELECT
    COUNT(*) AS total_porto,
    COUNT(*) FILTER (WHERE is_foreign) AS estrangeiros,
    COUNT(*) FILTER (WHERE is_foreign AND (latest_in_window OR inspected_in_window)) AS sujeitos,
    COALESCE(SUM(n_inspections) FILTER (WHERE is_foreign), 0)::bigint AS inspected
FROM foreign_agg;

-- name: CountEscalas :one
-- Conta escalas com os mesmos filtros de ListEscalasPaged — usado para paginação.
-- Sem status: mês selecionado (year != 0) mostra só aquele mês; navio selecionado
-- sem mês mostra o histórico completo do navio; default (sem mês, sem navio)
-- limita aos últimos 30 dias (só piso, sem teto — ver regras.md e escala_handler.go).
SELECT COUNT(*)::int
FROM port_calls pc
JOIN vessels v ON v.id = pc.vessel_id
WHERE (sqlc.arg(vessel_id)::bigint = 0 OR pc.vessel_id = sqlc.arg(vessel_id)::bigint)
  AND (
    (sqlc.arg(year)::int != 0
        AND EXTRACT(YEAR  FROM COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date)) = sqlc.arg(year)::int
        AND EXTRACT(MONTH FROM COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date)) = sqlc.arg(month)::int)
    OR (sqlc.arg(year)::int = 0 AND sqlc.arg(vessel_id)::bigint != 0)
    OR (sqlc.arg(year)::int = 0 AND sqlc.arg(vessel_id)::bigint = 0
        AND COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date) >= CURRENT_DATE - INTERVAL '30 days')
  );

-- name: ListEscalasPaged :many
-- Lista escalas com paginação (LIMIT 50 OFFSET). NÃO modifica ListEscalas.
-- Mesmos 3 casos de filtro de CountEscalas (sem status).
-- Ordenação: com mês selecionado, cronológica simples (asc). Sem mês selecionado
-- (default ou navio específico), agrupa por status — Atracados/Planejados (asc)
-- antes de Concluídas/Canceladas (desc) — via status_rank computado na CTE.
WITH filtered AS (
    SELECT
        pc.id, pc.vessel_id, pc.terminal,
        pc.eta_date, pc.etd_date,
        pc.actual_arrival, pc.actual_departure,
        pc.vessel_status, pc.port_call_status,
        pc.risk_level_snapshot, pc.priority_snapshot,
        v.name AS vessel_name, v.imo AS vessel_imo,
        v.flag AS vessel_flag, v.afretado AS vessel_afretado, v.acompanhado AS vessel_acompanhado,
        v.risk_level AS vessel_risk_level,
        v.last_inspection_date AS vessel_last_inspection_date,
        ins.id     AS inspection_id,
        ins.result AS inspection_result,
        COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date) AS sort_date,
        CASE WHEN sqlc.arg(year)::int != 0 THEN NULL ELSE
            CASE pc.port_call_status
                WHEN 'active'    THEN 0
                WHEN 'planned'   THEN 1
                WHEN 'completed' THEN 2
                WHEN 'closed'    THEN 2
                WHEN 'aborted'   THEN 3
                ELSE 4
            END
        END AS status_rank
    FROM port_calls pc
    JOIN vessels v ON v.id = pc.vessel_id
    LEFT JOIN inspections ins ON ins.port_call_id = pc.id
    WHERE (sqlc.arg(vessel_id)::bigint = 0 OR pc.vessel_id = sqlc.arg(vessel_id)::bigint)
      AND (
        (sqlc.arg(year)::int != 0
            AND EXTRACT(YEAR  FROM COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date)) = sqlc.arg(year)::int
            AND EXTRACT(MONTH FROM COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date)) = sqlc.arg(month)::int)
        OR (sqlc.arg(year)::int = 0 AND sqlc.arg(vessel_id)::bigint != 0)
        OR (sqlc.arg(year)::int = 0 AND sqlc.arg(vessel_id)::bigint = 0
            AND COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date) >= CURRENT_DATE - INTERVAL '30 days')
      )
)
SELECT id, vessel_id, terminal, eta_date, etd_date, actual_arrival, actual_departure,
       vessel_status, port_call_status, risk_level_snapshot, priority_snapshot,
       vessel_name, vessel_imo, vessel_flag, vessel_afretado, vessel_acompanhado,
       vessel_risk_level, vessel_last_inspection_date, inspection_id, inspection_result
FROM filtered
ORDER BY
    status_rank ASC NULLS FIRST,
    CASE WHEN status_rank IS NULL OR status_rank IN (0,1) THEN sort_date END ASC NULLS LAST,
    CASE WHEN status_rank IN (2,3,4) THEN sort_date END DESC NULLS LAST
LIMIT 50 OFFSET sqlc.arg(page_offset)::int;
