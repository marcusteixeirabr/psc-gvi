-- Queries do log de login (login_events) para o sqlc.
-- Leitura só para admin (rota /admin/logins).

-- name: InsertLoginEvent :exec
INSERT INTO login_events (event, user_id, username, ip, user_agent, reason)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: CountLoginEvents :one
-- Filtros opcionais: string vazia = sem filtro.
SELECT COUNT(*)::int FROM login_events
WHERE (sqlc.arg(username)::text = '' OR username = sqlc.arg(username)::text)
  AND (sqlc.arg(event)::text    = '' OR event    = sqlc.arg(event)::text);

-- name: ListLoginEvents :many
-- Mais recentes primeiro, paginado (internal/pagination).
SELECT * FROM login_events
WHERE (sqlc.arg(username)::text = '' OR username = sqlc.arg(username)::text)
  AND (sqlc.arg(event)::text    = '' OR event    = sqlc.arg(event)::text)
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::int;

-- name: ListLoginEventUsernames :many
-- Alimenta o filtro por usuário: inclui quem já foi excluído e logins
-- digitados que nunca existiram (tentativas de adivinhação).
SELECT DISTINCT username FROM login_events ORDER BY username;
