-- +goose Up
-- -----------------------------------------------------------------------------
-- Tabela: inspections
--
-- Registros de inspeções PSC (Port State Control) realizadas.
--
-- No Java legado, Inspecao tinha apenas dois campos: nome_navio e data.
-- Aqui modelamos corretamente: a inspeção está vinculada a um port_call
-- (não diretamente ao navio) porque o contexto importa — a mesma inspeção
-- não se repete no mesmo port_call.
--
-- Resultados possíveis (campo result):
--   no_deficiencies → navio aprovado sem ressalvas
--   deficiencies    → foram encontradas deficiências, mas navio pode sair
--   detained        → navio detido até corrigir as deficiências
--
-- Uma inspeção realizada no port_call atualiza o last_inspection_date do
-- vessel correspondente — essa atualização será feita pela aplicação Go,
-- não por trigger SQL (mais explícito e testável).
-- -----------------------------------------------------------------------------
CREATE TABLE inspections (
    id           BIGSERIAL   PRIMARY KEY,

    -- FK para o port_call inspecionado.
    -- Uma inspeção só existe no contexto de uma escala.
    port_call_id BIGINT      NOT NULL REFERENCES port_calls(id) ON DELETE RESTRICT,

    -- FK para o usuário que realizou a inspeção (role editor ou admin).
    -- O inspetor é um usuário do sistema — unificamos as duas entidades
    -- Java (Usuario + Inspetor) em uma única tabela users.
    inspector_id BIGINT      NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- Data em que a inspeção foi realizada.
    inspection_date DATE      NOT NULL,

    -- Resultado da inspeção:
    --   no_deficiencies → Aprovado
    --   deficiencies    → Deficiências anotadas, navio liberado
    --   detained        → Navio detido
    result       VARCHAR(20) NOT NULL
                 CHECK (result IN ('no_deficiencies', 'deficiencies', 'detained')),

    -- Notas livres do inspetor (observações, número do relatório IMO, etc.).
    notes        TEXT,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Índice para buscar inspeções de um port_call específico.
CREATE INDEX idx_inspections_port_call_id ON inspections (port_call_id);

-- Índice para buscar inspeções realizadas por um inspetor.
CREATE INDEX idx_inspections_inspector_id ON inspections (inspector_id);

-- Índice para relatórios mensais (filtro por data).
CREATE INDEX idx_inspections_date ON inspections (inspection_date);

-- +goose Down
DROP TABLE IF EXISTS inspections;
