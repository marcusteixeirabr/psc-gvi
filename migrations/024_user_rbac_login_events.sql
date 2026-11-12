-- +goose Up
-- -----------------------------------------------------------------------------
-- RBAC de usuários + log de login
--
-- 1) users.must_change_password
--    Quando o admin reseta a senha de alguém, ele digita uma senha PROVISÓRIA.
--    Esta flag obriga o usuário a trocá-la no próximo login: enquanto for true,
--    o middleware só deixa acessar a tela "Minha senha" (e o logout).
--
-- 2) users.session_version
--    A sessão é um cookie assinado (gorilla/sessions CookieStore), ou seja, não
--    existe tabela de sessões para "apagar". Para derrubar sessões abertas
--    (ex.: o admin resetou a senha de alguém que estava logado), guardamos um
--    número de versão no cookie e no banco. Mudou a senha → incrementa a versão
--    → todo cookie antigo passa a ser recusado no próximo request.
--    Analogia Java: é o mesmo papel do "token version" que se usa com JWT,
--    já que um token assinado não pode ser revogado individualmente.
--
-- 3) login_events
--    Log de login: sucesso, falha e logout, com data/hora, IP e navegador.
--    - user_id SEM foreign key, de propósito: o admin pode EXCLUIR um usuário
--      (DELETE real) e o histórico de login dele tem de continuar existindo.
--      Uma FK com ON DELETE RESTRICT impediria a exclusão; com ON DELETE CASCADE
--      apagaria o histórico. Por isso guardamos também o username como texto.
--    - Em tentativa falha com usuário inexistente, user_id fica NULL e username
--      guarda o que foi digitado (útil para ver tentativas de adivinhação).
--    - Retenção: para sempre (decisão do Marcus, ~5 usuários, volume desprezível).
-- -----------------------------------------------------------------------------
ALTER TABLE users
    ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN session_version      INTEGER NOT NULL DEFAULT 1;

CREATE TABLE login_events (
    id          BIGSERIAL    PRIMARY KEY,
    occurred_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    -- login_success  → entrou
    -- login_failure  → senha errada, usuário inexistente ou desativado
    -- logout         → clicou em "Sair"
    event       VARCHAR(20)  NOT NULL
                CHECK (event IN ('login_success', 'login_failure', 'logout')),

    user_id     BIGINT,                 -- sem FK (ver cabeçalho)
    username    VARCHAR(100) NOT NULL,  -- o que foi digitado / o login do usuário
    ip          VARCHAR(64)  NOT NULL DEFAULT '',
    user_agent  VARCHAR(512) NOT NULL DEFAULT '',

    -- Motivo da falha, só para o admin (nunca mostrado na tela de login):
    -- 'senha incorreta', 'usuário inexistente', 'usuário desativado'.
    reason      VARCHAR(100) NOT NULL DEFAULT ''
);

-- A tela do log lista do mais recente para o mais antigo e filtra por usuário.
CREATE INDEX idx_login_events_occurred_at ON login_events (occurred_at DESC);
CREATE INDEX idx_login_events_username    ON login_events (username);

-- +goose Down
DROP TABLE IF EXISTS login_events;
ALTER TABLE users
    DROP COLUMN IF EXISTS session_version,
    DROP COLUMN IF EXISTS must_change_password;
