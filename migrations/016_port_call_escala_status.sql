-- +goose Up
-- Prepara port_calls para o fluxo de escalas (Story 6.1).
--
-- Mudanças:
-- 1. port_call_status: adiciona 'completed' (escala concluída, partida registrada)
-- 2. priority_snapshot: adiciona 'N/D' (renomeado de 'N/A')
-- 3. risk_level_snapshot: adiciona 'not_found' (CIALA não tem registro do navio)

ALTER TABLE port_calls
    DROP CONSTRAINT IF EXISTS port_calls_port_call_status_check;
ALTER TABLE port_calls
    ADD CONSTRAINT port_calls_port_call_status_check
        CHECK (port_call_status IN ('planned', 'active', 'completed', 'closed', 'aborted'));

ALTER TABLE port_calls
    DROP CONSTRAINT IF EXISTS port_calls_priority_snapshot_check;
ALTER TABLE port_calls
    ADD CONSTRAINT port_calls_priority_snapshot_check
        CHECK (priority_snapshot IN ('P1', 'P2', 'P3', 'N/A', 'N/D'));

ALTER TABLE port_calls
    DROP CONSTRAINT IF EXISTS port_calls_risk_level_snapshot_check;
ALTER TABLE port_calls
    ADD CONSTRAINT port_calls_risk_level_snapshot_check
        CHECK (risk_level_snapshot IN ('high', 'standard', 'low', 'not_found'));

-- +goose Down
ALTER TABLE port_calls
    DROP CONSTRAINT IF EXISTS port_calls_port_call_status_check;
ALTER TABLE port_calls
    ADD CONSTRAINT port_calls_port_call_status_check
        CHECK (port_call_status IN ('planned', 'active', 'closed', 'aborted'));

ALTER TABLE port_calls
    DROP CONSTRAINT IF EXISTS port_calls_priority_snapshot_check;
ALTER TABLE port_calls
    ADD CONSTRAINT port_calls_priority_snapshot_check
        CHECK (priority_snapshot IN ('P1', 'P2', 'P3', 'N/A'));

ALTER TABLE port_calls
    DROP CONSTRAINT IF EXISTS port_calls_risk_level_snapshot_check;
ALTER TABLE port_calls
    ADD CONSTRAINT port_calls_risk_level_snapshot_check
        CHECK (risk_level_snapshot IN ('high', 'standard', 'low'));
