-- +goose Up
-- -----------------------------------------------------------------------------
-- Tabela: vessels
--
-- Cadastro permanente de navios. Equivale à classe Vessel do Java legado.
--
-- O campo mais importante é o IMO — é o identificador único MUNDIAL de um
-- navio, emitido pela International Maritime Organization. Imutável.
-- É como o CPF do navio: muda o nome, muda a bandeira, o IMO permanece.
--
-- Por que isso importa para nós:
--   - O scraper do ZP-21 retorna apenas o NOME do navio
--   - Buscamos o IMO no equasys (1x por navio, nunca repete)
--   - Com o IMO, consultamos o histórico de inspeções no CIALA
-- -----------------------------------------------------------------------------
CREATE TABLE vessels (
    id        BIGSERIAL   PRIMARY KEY,

    -- Identificador IMO: formato "IMO1234567" (7 dígitos após o prefixo).
    -- UNIQUE garante que cada navio aparece uma única vez no cadastro.
    imo       VARCHAR(10) NOT NULL UNIQUE,

    -- Nome do navio como aparece no ZP-21 (ex: "MSC MARINA").
    -- Pode mudar ao longo da vida do navio (renomeação), o IMO não.
    name      VARCHAR(100) NOT NULL,

    -- Bandeira do país de registro (ex: "Panama", "Bahamas", "Brazil").
    -- NULL permitido: pode ser descoberto depois via equasys/CIALA.
    flag      VARCHAR(50),

    -- Ano de construção. Usado para calcular a idade e, portanto, o risk_level.
    -- NULL permitido: pode ser desconhecido inicialmente.
    year_built INT,

    -- Tipo do navio (ex: "Container", "Bulk Carrier", "Tanker", "General Cargo").
    vessel_type VARCHAR(50),

    -- Dimensões em metros. DECIMAL(6,1) = até 99999.9 metros (mais que suficiente).
    -- Comprimento e boca são usados em cálculos de berço disponível.
    length_m  DECIMAL(6,1),
    beam_m    DECIMAL(5,1),

    -- Nível de risco calculado pela IDADE do navio (regra do Java GrauRisco):
    --   high     → mais de 20 anos → inspeção obrigatória a cada 2 meses
    --   standard → 10 a 20 anos   → inspeção a cada 5 meses
    --   low      → menos de 10    → inspeção a cada 9 meses
    risk_level VARCHAR(20) NOT NULL
               CHECK (risk_level IN ('high', 'standard', 'low')),

    -- Data da última inspeção PSC registrada no CIALA.
    -- NULL = nunca inspecionado → prioridade automática P1 (máxima).
    last_inspection_date DATE,

    -- Navio inativo não aparece nas listagens mas preserva histórico.
    active     BOOLEAN     NOT NULL DEFAULT true,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Índice para buscas por nome (autocomplete, busca manual).
CREATE INDEX idx_vessels_name ON vessels (name);

-- Índice para filtrar navios ativos (query mais comum na listagem).
CREATE INDEX idx_vessels_active ON vessels (active);

-- +goose Down
DROP TABLE IF EXISTS vessels;
