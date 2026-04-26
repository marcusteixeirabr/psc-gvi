-- +goose Up
-- -----------------------------------------------------------------------------
-- Tabela: users
--
-- Unifica autenticação e dados profissionais em uma única tabela.
-- No Java havia duas entidades separadas (Usuario e Inspetor), mas com apenas
-- 5 usuários fixos não há motivo para essa complexidade.
--
-- Campos de auth:   username, password_hash, role
-- Campos de perfil: display_name (nome exibido na UI)
-- Campos de controle: active, created_at
-- -----------------------------------------------------------------------------
CREATE TABLE users (
    id            BIGSERIAL    PRIMARY KEY,

    -- Login do usuário. UNIQUE garante que não existam dois "marcus".
    username      VARCHAR(50)  NOT NULL UNIQUE,

    -- Nome exibido na interface (ex: "Marcus Teixeira").
    display_name  VARCHAR(100) NOT NULL,

    -- Senha com hash bcrypt. Nunca armazenamos senha em texto puro.
    -- Bcrypt gera hashes de ~60 caracteres, mas usamos 255 por segurança futura.
    password_hash VARCHAR(255) NOT NULL,

    -- Papel do usuário no sistema (controle de acesso RBAC):
    --   admin  → tudo: gerenciar usuários, configurações, todos os dados
    --   editor → registrar atracações, inspeções, editar navios
    --   reader → apenas visualizar e exportar relatórios
    -- CHECK é uma constraint de banco: rejeita qualquer valor fora da lista.
    role          VARCHAR(20)  NOT NULL
                  CHECK (role IN ('admin', 'editor', 'reader')),

    -- Usuário desativado não consegue logar, mas seus registros históricos
    -- são preservados. Preferimos isso a deletar o registro.
    active        BOOLEAN      NOT NULL DEFAULT true,

    -- TIMESTAMPTZ = timestamp WITH time zone.
    -- Sempre armazene datas com fuso horário — evita problemas com
    -- servidores em timezones diferentes.
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Índice para acelerar buscas por username (login faz isso frequentemente).
-- O banco já cria um índice para UNIQUE, mas deixamos explícito para clareza.
CREATE INDEX idx_users_username ON users (username);

-- +goose Down
-- Desfaz a migration acima. Executado ao rodar "goose down".
DROP TABLE IF EXISTS users;
