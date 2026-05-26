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
UPDATE port_calls
SET actual_arrival      = $2,
    vessel_status       = 'berthed',
    port_call_status    = 'active',
    risk_level_snapshot = $3,
    priority_snapshot   = $4,
    updated_at          = NOW()
WHERE id = $1;

-- name: UpdatePortCallSnapshot :exec
-- Salva o snapshot de risco/prioridade no momento da inspeção (estado pré-inspeção).
UPDATE port_calls
SET risk_level_snapshot = $2,
    priority_snapshot   = $3,
    updated_at          = NOW()
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
       zp21_sourced, created_at, updated_at, terminal, last_zp21_seen_at
FROM port_calls WHERE id = $1;

-- name: UpdatePortCallFull :exec
-- Edição completa de uma escala pelo usuário.
-- vessel_status é derivado automaticamente: actual_arrival preenchido → berthed, senão navigating.
UPDATE port_calls
SET terminal            = $2,
    eta_date            = $3,
    eta_time            = $4,
    etd_date            = $5,
    etd_time            = $6,
    actual_arrival      = $7,
    actual_departure    = $8,
    port_call_status    = $9,
    risk_level_snapshot = $10,
    priority_snapshot   = $11,
    vessel_status       = CASE WHEN $7::timestamptz IS NOT NULL THEN 'berthed' ELSE 'navigating' END,
    updated_at          = NOW()
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
UPDATE port_calls
SET actual_arrival      = NULL,
    actual_departure    = NULL,
    vessel_status       = 'navigating',
    port_call_status    = 'planned',
    risk_level_snapshot = NULL,
    priority_snapshot   = NULL,
    updated_at          = NOW()
WHERE id = $1;

-- name: CancelSuspension :exec
-- Cancela a suspensão: limpa actual_departure e reverte escala para ativa.
UPDATE port_calls
SET actual_departure    = NULL,
    port_call_status    = 'active',
    risk_level_snapshot = NULL,
    priority_snapshot   = NULL,
    updated_at          = NOW()
WHERE id = $1;

-- name: ListReportEntries :many
-- Uma linha por navio (última escala do período).
-- Estrangeiros não-afretados primeiro, ordenados pela primeira atracação do navio no período.
-- Afretados e nacionais sempre por último.
WITH period_vessels AS (
    -- Apenas escalas com atracação registrada — planejadas ficam de fora.
    SELECT pc.id, pc.vessel_id,
           pc.actual_arrival::date AS arrival_date
    FROM port_calls pc
    JOIN vessels v ON v.id = pc.vessel_id
    WHERE v.acompanhado = TRUE
      AND pc.actual_arrival IS NOT NULL
      AND EXTRACT(YEAR  FROM pc.actual_arrival::date) = sqlc.arg(year)::int
      AND EXTRACT(MONTH FROM pc.actual_arrival::date) = sqlc.arg(month)::int
),
vessel_first_arrival AS (
    SELECT vessel_id, MIN(arrival_date) AS first_arrival
    FROM period_vessels GROUP BY vessel_id
),
ranked AS (
    SELECT
        pc.id, pc.vessel_id, pc.terminal,
        pc.eta_date, pc.etd_date,
        pc.actual_arrival, pc.actual_departure,
        pc.port_call_status, pc.risk_level_snapshot, pc.priority_snapshot,
        v.name AS vessel_name, v.imo AS vessel_imo, v.flag AS vessel_flag,
        v.afretado AS vessel_afretado, v.year_built AS vessel_year_built,
        v.vessel_type AS vessel_type,
        ins.id AS inspection_id, ins.result AS inspection_result, ins.inspection_date,
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
    LEFT JOIN inspections ins ON ins.port_call_id = pc.id
)
SELECT id, vessel_id, terminal, eta_date, etd_date,
       actual_arrival, actual_departure, port_call_status,
       risk_level_snapshot, priority_snapshot,
       vessel_name, vessel_imo, vessel_flag, vessel_afretado,
       vessel_year_built, vessel_type,
       inspection_id, inspection_result, inspection_date
FROM ranked
WHERE rn = 1
ORDER BY
    -- Estrangeiros não afretados primeiro; brasileiros e afretados no final.
    CASE WHEN vessel_afretado = FALSE
              AND (vessel_flag IS NULL OR LOWER(TRIM(vessel_flag)) NOT IN ('brazil','brasil'))
         THEN 0 ELSE 1 END ASC,
    actual_arrival ASC NULLS LAST,
    vessel_name ASC;

-- name: CountReportKPI :one
-- KPI do mês: cada navio conta uma única vez (última escala com atracação no período).
-- total_porto   = navios únicos com atracação no mês
-- estrangeiros  = estrangeiros não-afretados (únicos)
-- sujeitos      = estrangeiros com P1/P2 no snapshot OU já inspecionados
-- inspected     = estrangeiros com inspeção registrada
WITH last_per_vessel AS (
    SELECT DISTINCT ON (pc.vessel_id)
        pc.id, pc.vessel_id, pc.priority_snapshot,
        v.afretado AS vessel_afretado, v.flag AS vessel_flag
    FROM port_calls pc
    JOIN vessels v ON v.id = pc.vessel_id
    WHERE v.acompanhado = TRUE
      AND pc.actual_arrival IS NOT NULL
      AND EXTRACT(YEAR  FROM pc.actual_arrival::date) = sqlc.arg(year)::int
      AND EXTRACT(MONTH FROM pc.actual_arrival::date) = sqlc.arg(month)::int
    ORDER BY pc.vessel_id, pc.actual_arrival DESC
)
SELECT
    COUNT(*) AS total_porto,
    COUNT(*) FILTER (
        WHERE lpv.vessel_afretado = FALSE
          AND (lpv.vessel_flag IS NULL OR LOWER(TRIM(lpv.vessel_flag)) NOT IN ('brazil','brasil'))
    ) AS estrangeiros,
    COUNT(*) FILTER (
        WHERE lpv.vessel_afretado = FALSE
          AND (lpv.vessel_flag IS NULL OR LOWER(TRIM(lpv.vessel_flag)) NOT IN ('brazil','brasil'))
          AND (lpv.priority_snapshot IN ('P1','P2') OR ins.id IS NOT NULL)
    ) AS sujeitos,
    COUNT(ins.id) FILTER (
        WHERE lpv.vessel_afretado = FALSE
          AND (lpv.vessel_flag IS NULL OR LOWER(TRIM(lpv.vessel_flag)) NOT IN ('brazil','brasil'))
    ) AS inspected
FROM last_per_vessel lpv
LEFT JOIN inspections ins ON ins.port_call_id = lpv.id;

-- name: CountEscalas :one
-- Conta escalas com os mesmos filtros de ListEscalas — usado para paginação.
SELECT COUNT(*)::int
FROM port_calls pc
JOIN vessels v ON v.id = pc.vessel_id
WHERE (sqlc.arg(status)::text = '' OR pc.port_call_status = sqlc.arg(status)::text)
  AND (sqlc.arg(vessel_id)::bigint = 0 OR pc.vessel_id = sqlc.arg(vessel_id)::bigint)
  AND (sqlc.arg(year)::int = 0
       OR EXTRACT(YEAR FROM COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date)) = sqlc.arg(year)::int)
  AND (sqlc.arg(month)::int = 0
       OR EXTRACT(MONTH FROM COALESCE(pc.actual_departure::date, pc.actual_arrival::date, pc.eta_date)) = sqlc.arg(month)::int);

-- name: ListEscalasPaged :many
-- Lista escalas com paginação (LIMIT 50 OFFSET). NÃO modifica ListEscalas.
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
         pc.created_at ASC
LIMIT 50 OFFSET sqlc.arg(page_offset)::int;
