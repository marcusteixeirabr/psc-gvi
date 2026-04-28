-- Queries de Inspection para o sqlc.

-- name: CreateInspection :one
-- Registra uma inspeção PSC vinculada a um port call.
-- A constraint UNIQUE em port_call_id garante no máximo uma inspeção por escala.
INSERT INTO inspections (port_call_id, inspection_date, result, notes)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetInspectionByPortCall :one
-- Retorna a inspeção de um port call, se existir.
-- Retorna pgx.ErrNoRows se o port call ainda não foi inspecionado.
SELECT * FROM inspections WHERE port_call_id = $1;
