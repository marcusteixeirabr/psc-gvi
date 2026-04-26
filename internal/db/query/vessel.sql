-- Queries de Vessel para o sqlc.
--
-- O sqlc lê estes comentários especiais para saber o nome da função Go
-- e o tipo de retorno que deve gerar:
--
--   :one   → retorna uma linha (Row) ou erro pgx.ErrNoRows
--   :many  → retorna um slice []Vessel
--   :exec  → não retorna linhas (UPDATE/DELETE sem RETURNING)

-- name: ListVessels :many
-- Lista todos os navios ativos ordenados por nome.
SELECT id, imo, name, flag, year_built, vessel_type, length_m, beam_m,
       risk_level, last_inspection_date, active, created_at, updated_at
FROM vessels
WHERE active = true
ORDER BY name;

-- name: GetVessel :one
-- Busca um navio pelo ID (ativo ou não — usado em detalhes e edição).
SELECT id, imo, name, flag, year_built, vessel_type, length_m, beam_m,
       risk_level, last_inspection_date, active, created_at, updated_at
FROM vessels
WHERE id = $1;

-- name: GetVesselByIMO :one
-- Busca por IMO — usado pelo scraper para evitar cadastros duplicados.
SELECT id, imo, name, flag, year_built, vessel_type, length_m, beam_m,
       risk_level, last_inspection_date, active, created_at, updated_at
FROM vessels
WHERE imo = $1;

-- name: CreateVessel :one
-- Cria novo navio. risk_level e last_inspection_date não são incluídos —
-- chegam posteriormente via scraping do CIALA.
INSERT INTO vessels (imo, name, flag, year_built, vessel_type, length_m, beam_m)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateVessel :one
-- Atualiza campos editáveis pelo usuário. risk_level e last_inspection_date
-- são atualizados apenas pelo scraper (Story 2.x), não aqui.
UPDATE vessels
SET name        = $2,
    flag        = $3,
    year_built  = $4,
    vessel_type = $5,
    length_m    = $6,
    beam_m      = $7,
    updated_at  = NOW()
WHERE id = $1
RETURNING *;

-- name: DeactivateVessel :exec
-- Soft delete: marca como inativo sem deletar o registro.
-- Preserva histórico de port_calls e inspeções vinculadas.
UPDATE vessels
SET active = false, updated_at = NOW()
WHERE id = $1;
