-- Queries de usuários (RBAC) para o sqlc.
--
-- Regras de acesso NÃO ficam aqui: quem pode chamar cada query é decidido nos
-- handlers (internal/user = só admin; internal/auth = login e "minha senha").

-- name: GetUserByUsername :one
-- Usada no login. Traz também os desativados: o handler precisa distinguir
-- "usuário desativado" de "inexistente" para registrar o motivo no log
-- (a tela de login continua mostrando só "credenciais inválidas").
SELECT * FROM users WHERE username = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY id;

-- name: CountActiveAdmins :one
-- Usada para nunca deixar o sistema sem administrador
-- (rebaixar ou excluir o último admin é recusado).
SELECT COUNT(*)::int FROM users WHERE role = 'admin' AND active = true;

-- name: CreateUser :one
-- Todo usuário novo nasce com senha provisória definida pelo admin
-- (must_change_password = true → troca obrigatória no primeiro login).
INSERT INTO users (username, display_name, password_hash, role, must_change_password)
VALUES ($1, $2, $3, $4, true)
RETURNING *;

-- name: UpdateUserDisplayName :exec
UPDATE users SET display_name = $2 WHERE id = $1;

-- name: UpdateUserRole :exec
UPDATE users SET role = $2 WHERE id = $1;

-- name: ResetUserPassword :exec
-- Reset pelo admin: senha provisória + troca obrigatória no próximo login.
-- session_version + 1 derruba as sessões abertas desse usuário.
UPDATE users
SET password_hash        = $2,
    must_change_password = true,
    session_version      = session_version + 1
WHERE id = $1;

-- name: ChangeOwnPassword :one
-- Troca feita pelo próprio usuário. Limpa a obrigação de troca e incrementa
-- session_version (derruba as OUTRAS sessões abertas dele). Devolve a nova
-- versão para o handler regravar o cookie da sessão atual, que continua válida.
UPDATE users
SET password_hash        = $2,
    must_change_password = false,
    session_version      = session_version + 1
WHERE id = $1
RETURNING session_version;

-- name: DeleteUser :execrows
-- Exclusão real. O histórico em login_events é preservado (sem FK).
DELETE FROM users WHERE id = $1;
