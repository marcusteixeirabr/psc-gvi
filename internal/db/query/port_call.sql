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
-- Retorna o timestamp do ciclo de scraping mais recente.
-- Usado pelo dashboard para filtrar navios da última consulta.
SELECT MAX(last_zp21_seen_at) FROM port_calls WHERE zp21_sourced = TRUE;

-- name: AbortStaleZP21PortCalls :exec
-- Cancela escalas ZP-21 planejadas que não apareceram na última consulta.
-- Executado ao final de cada ciclo: port calls com last_zp21_seen_at anterior ao
-- ciclo atual ainda estão 'planned' → sumiu do ZP-21 → status = 'aborted'.
UPDATE port_calls
SET port_call_status = 'aborted', updated_at = NOW()
WHERE zp21_sourced = TRUE
  AND port_call_status = 'planned'
  AND (last_zp21_seen_at IS NULL OR last_zp21_seen_at < $1);

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
-- Retorna navios mercantes (ou sem categoria) com port calls ativos.
-- NULL category aparece para alertar que o cadastro precisa ser completado.
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
  AND v.afretado = FALSE
  AND (v.flag IS NULL OR LOWER(TRIM(v.flag)) NOT IN ('brazil', 'brasil'))
  AND (pc.port_call_status = 'planned' OR pc.port_call_status = 'active')
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
-- Registra a atracação efetiva: data real de chegada + muda status para ativo.
UPDATE port_calls
SET actual_arrival   = $2,
    vessel_status    = 'berthed',
    port_call_status = 'active',
    updated_at       = NOW()
WHERE id = $1;

-- name: RegisterDeparture :exec
-- Registra a partida efetiva: data real de saída + snapshot + conclui a escala.
UPDATE port_calls
SET actual_departure      = $2,
    port_call_status      = 'completed',
    risk_level_snapshot   = $3,
    priority_snapshot     = $4,
    updated_at            = NOW()
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
SET terminal         = $2,
    eta_date         = $3,
    etd_date         = $4,
    actual_arrival   = $5,
    actual_departure = $6,
    port_call_status = $7,
    vessel_status    = CASE WHEN $5::timestamptz IS NOT NULL THEN 'berthed' ELSE 'navigating' END,
    updated_at       = NOW()
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
-- Retorna escalas que tocaram o porto no mês selecionado, para o relatório mensal.
-- "Tocou o porto" = actual_arrival, actual_departure ou eta_date caiu no mês.
-- Filtra apenas navios acompanhados (excluindo apoio/estado).
-- Ordenado por data efetiva de atracação (ou eta como fallback).
SELECT
    pc.id,
    pc.vessel_id,
    pc.terminal,
    pc.eta_date,
    pc.etd_date,
    pc.actual_arrival,
    pc.actual_departure,
    pc.port_call_status,
    pc.risk_level_snapshot,
    pc.priority_snapshot,
    v.name        AS vessel_name,
    v.imo         AS vessel_imo,
    v.flag        AS vessel_flag,
    v.afretado    AS vessel_afretado,
    v.year_built  AS vessel_year_built,
    v.vessel_type AS vessel_type,
    ins.id        AS inspection_id,
    ins.result    AS inspection_result,
    ins.inspection_date
FROM port_calls pc
JOIN vessels v ON v.id = pc.vessel_id
LEFT JOIN inspections ins ON ins.port_call_id = pc.id
WHERE v.acompanhado = TRUE
  AND EXTRACT(YEAR  FROM COALESCE(pc.actual_arrival::date, pc.actual_departure::date, pc.eta_date)) = sqlc.arg(year)::int
  AND EXTRACT(MONTH FROM COALESCE(pc.actual_arrival::date, pc.actual_departure::date, pc.eta_date)) = sqlc.arg(month)::int
ORDER BY COALESCE(pc.actual_arrival::date, pc.eta_date) ASC NULLS LAST, v.name ASC;

-- name: CountReportKPI :one
-- Calcula KPI do mês: total de escalas de estrangeiros acompanhados e quantas foram inspecionadas.
-- Usado no card de resumo do relatório mensal.
SELECT
    COUNT(pc.id)                                              AS total,
    COUNT(ins.id)                                             AS inspected,
    COUNT(*) FILTER (WHERE pc.port_call_status = 'completed') AS completed
FROM port_calls pc
JOIN vessels v ON v.id = pc.vessel_id
LEFT JOIN inspections ins ON ins.port_call_id = pc.id
WHERE v.acompanhado = TRUE
  AND v.afretado = FALSE
  AND (v.flag IS NULL OR LOWER(TRIM(v.flag)) NOT IN ('brazil', 'brasil'))
  AND EXTRACT(YEAR  FROM COALESCE(pc.actual_arrival::date, pc.actual_departure::date, pc.eta_date)) = sqlc.arg(year)::int
  AND EXTRACT(MONTH FROM COALESCE(pc.actual_arrival::date, pc.actual_departure::date, pc.eta_date)) = sqlc.arg(month)::int;
