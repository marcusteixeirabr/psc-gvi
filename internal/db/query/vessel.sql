-- Queries de Vessel para o sqlc.

-- name: ListVessels :many
-- Lista navios acompanhados (exclui estado/apoio/desativado), ordenados por nome.
SELECT * FROM vessels
WHERE acompanhado = TRUE
ORDER BY name;

-- name: ListAllVessels :many
-- Lista TODOS os navios incluindo não-acompanhados — para uso exclusivo do admin.
SELECT * FROM vessels ORDER BY name;

-- name: GetVessel :one
-- Busca um navio pelo ID (qualquer status — usado em detalhes e edição).
SELECT * FROM vessels WHERE id = $1;

-- name: GetVesselByIMO :one
-- Busca por IMO — usado pelo scraper para evitar cadastros duplicados.
SELECT * FROM vessels WHERE imo = $1;

-- name: GetVesselsByNameExcludingSelf :many
-- Retorna vessels com o mesmo nome, excluindo apenas o próprio vessel.
-- Inclui não-acompanhados porque podem ser duplicatas a resolver via Mesclar.
SELECT * FROM vessels
WHERE name ILIKE sqlc.arg(name)
  AND id != sqlc.arg(self_id)
ORDER BY name;

-- name: UpdateVesselIMO :exec
-- Registra o IMO descoberto via VesselFinder.
-- IMO é imutável após definido — UPDATE só quando imo IS NULL.
UPDATE vessels SET imo = $2, updated_at = NOW() WHERE id = $1 AND imo IS NULL;

-- name: ListVesselsWithoutIMO :many
-- Navios acompanhados, de bandeira não-brasileira, sem IMO e com port call ativo.
-- Candidatos para busca de IMO no VesselFinder.
SELECT v.* FROM vessels v
JOIN port_calls pc ON pc.vessel_id = v.id
WHERE v.imo IS NULL
  AND v.acompanhado = TRUE
  AND (v.flag IS NULL OR LOWER(TRIM(v.flag)) NOT IN ('brazil', 'brasil'))
  AND (pc.port_call_status = 'planned' OR pc.port_call_status = 'active')
GROUP BY v.id
ORDER BY v.name;

-- name: UpdateVesselCIALA :exec
-- Atualiza os dados vindos do CIALA.
-- last_inspection_date: protegido por 10 dias após inspeção GVI.
-- flag, vessel_type, year_built: COALESCE — só sobrescreve se CIALA trouxer valor.
UPDATE vessels
SET risk_level                   = sqlc.narg('risk_level'),
    last_inspection_date         = CASE
        WHEN last_inspection_date IS NOT NULL
             AND last_inspection_date > (CURRENT_DATE - INTERVAL '10 days')
        THEN last_inspection_date
        ELSE sqlc.narg('last_inspection_date')
    END,
    last_inspection_deficiencies = sqlc.narg('last_inspection_deficiencies'),
    flag                         = COALESCE(sqlc.narg('flag'), flag),
    vessel_type                  = COALESCE(sqlc.narg('vessel_type'), vessel_type),
    year_built                   = COALESCE(sqlc.narg('year_built'), year_built),
    updated_at                   = NOW()
WHERE id = sqlc.arg('id');

-- name: ListVesselsForCIALA :many
-- Navios acompanhados com IMO mas sem risk_level — candidatos para consulta no CIALA.
-- Inclui nacionais e afretados (também aparecem no relatório mensal).
SELECT * FROM vessels
WHERE imo IS NOT NULL
  AND risk_level IS NULL
  AND acompanhado = TRUE
ORDER BY name;

-- name: ListAllVesselsWithIMO :many
-- Todos os navios acompanhados com IMO — para atualização completa do CIALA.
SELECT * FROM vessels
WHERE imo IS NOT NULL AND acompanhado = TRUE
ORDER BY name;

-- name: UpdateVesselDimensions :exec
-- Persiste LOA e Beam descobertos via VesselFinder.
UPDATE vessels SET length_m = $2, beam_m = $3, updated_at = NOW() WHERE id = $1;

-- name: UpdateVesselLastInspectionDate :exec
-- Atualiza a data da última inspeção PSC após registro de nova inspeção pelo GVI.
UPDATE vessels SET last_inspection_date = $2, updated_at = NOW() WHERE id = $1;

-- name: DeleteVessel :exec
-- Hard delete de um vessel sem histórico.
DELETE FROM vessels WHERE id = $1;

-- name: CreateVessel :one
-- Cria novo navio.
INSERT INTO vessels (imo, name, flag, year_built, vessel_type, length_m, beam_m, afretado)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateVessel :one
-- Atualiza campos editáveis pelo usuário.
-- imo: COALESCE($10, imo) → deixe vazio para manter o IMO existente.
UPDATE vessels
SET name        = $2,
    flag        = $3,
    year_built  = $4,
    vessel_type = $5,
    length_m    = $6,
    beam_m      = $7,
    afretado    = $8,
    acompanhado = $9,
    imo         = COALESCE($10, imo),
    updated_at  = NOW()
WHERE id = $1
RETURNING *;

-- name: DeactivateVessel :exec
-- Marca o navio como não-acompanhado sem deletar o registro.
UPDATE vessels
SET acompanhado = FALSE, updated_at = NOW()
WHERE id = $1;
