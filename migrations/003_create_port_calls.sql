-- +goose Up
-- -----------------------------------------------------------------------------
-- Tabela: port_calls
--
-- Representa uma escala de navio no porto — o evento central do sistema.
-- Equivale à classe PortCall do Java legado, com algumas diferenças:
--
--   Java: IDs gerados em memória (AtomicInteger) → perdidos ao reiniciar
--   Go:   IDs persistidos no banco (BIGSERIAL) → correto e permanente
--
-- Origem dos registros:
--   - Automática: scraper do ZP-21 detecta navio novo → cria port_call
--   - Manual: editor cadastra manualmente via formulário
--
-- Ciclo de vida (port_call_status):
--   planned → active → closed
--                    → aborted (cancelado antes de atracar)
-- -----------------------------------------------------------------------------
CREATE TABLE port_calls (
    id        BIGSERIAL   PRIMARY KEY,

    -- Chave estrangeira: qual navio é este port call?
    -- REFERENCES cria um link entre tabelas — o banco garante que
    -- não existirá um port_call sem um vessel correspondente.
    -- ON DELETE RESTRICT impede deletar um vessel que tem port_calls.
    vessel_id BIGINT      NOT NULL REFERENCES vessels(id) ON DELETE RESTRICT,

    -- Berço de atracação (ex: "201", "TVIP", "BTC").
    -- NULL permitido: pode ser desconhecido no momento do planejamento.
    berth     VARCHAR(20),

    -- Status operacional do navio NESTE MOMENTO (do Java VesselStatus):
    --   navigating  → navegando em direção ao porto
    --   drifting    → derivando (aguardando berço)
    --   anchored    → ancorado na barra
    --   maneuvring  → em manobra com praticagem
    --   berthed     → atracado no berço
    vessel_status VARCHAR(20) NOT NULL
                  CHECK (vessel_status IN (
                      'navigating', 'drifting', 'anchored', 'maneuvring', 'berthed'
                  )),

    -- Status do port call como registro administrativo (do Java PortCallStatus):
    --   planned  → agendado, ainda não chegou
    --   active   → navio está no porto (berthed)
    --   closed   → navio saiu, port call encerrado
    --   aborted  → cancelado (navio não veio)
    port_call_status VARCHAR(20) NOT NULL DEFAULT 'planned'
                     CHECK (port_call_status IN ('planned', 'active', 'closed', 'aborted')),

    -- Datas e horários ESTIMADOS (vindos do ZP-21).
    -- O ZP-21 usa DATE e TIME separados, então mantemos assim.
    eta_date  DATE,
    eta_time  TIME,
    etd_date  DATE,
    etd_time  TIME,

    -- Chegada e saída REAIS (registradas manualmente ou confirmadas pelo ZP-21).
    -- NULL = ainda não aconteceu.
    actual_arrival   TIMESTAMPTZ,
    actual_departure TIMESTAMPTZ,

    -- Snapshots do risco e prioridade NO MOMENTO da escala.
    -- Importante: o risk_level do navio pode mudar com o tempo (ele envelhece),
    -- mas o snapshot preserva o contexto histórico da inspeção.
    -- Do Java: riskLevelSnapshot e prioritySnapshot no construtor de PortCall.
    risk_level_snapshot VARCHAR(20) CHECK (risk_level_snapshot IN ('high', 'standard', 'low')),
    priority_snapshot   VARCHAR(5)  CHECK (priority_snapshot IN ('P0', 'P1', 'P2')),

    -- Indica se este registro foi criado automaticamente pelo scraper do ZP-21
    -- (true) ou manualmente por um editor (false).
    -- Útil para rastrear a origem dos dados e depurar problemas de scraping.
    zp21_sourced BOOLEAN NOT NULL DEFAULT false,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Índice para a query mais comum: "port calls ativos de um navio específico".
CREATE INDEX idx_port_calls_vessel_id ON port_calls (vessel_id);

-- Índice para filtrar por status (listagem principal do dashboard).
CREATE INDEX idx_port_calls_status ON port_calls (port_call_status);

-- Índice para filtrar por data de chegada estimada (ordenação cronológica).
CREATE INDEX idx_port_calls_eta ON port_calls (eta_date);

-- +goose Down
DROP TABLE IF EXISTS port_calls;
