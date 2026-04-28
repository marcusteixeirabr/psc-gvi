-- +goose Up
-- Adiciona 'not_found' ao check constraint de risk_level.
-- Semântica: CIALA foi consultado mas o navio não está na base do Acordo.
-- Distingue de NULL (ainda não consultado) e de high/standard/low (risco conhecido).
ALTER TABLE vessels
    DROP CONSTRAINT IF EXISTS vessels_risk_level_check,
    ADD CONSTRAINT vessels_risk_level_check
        CHECK (risk_level IN ('high', 'standard', 'low', 'not_found'));

-- port_calls.risk_level_snapshot armazena o risco no momento da atracação — não precisa de 'not_found'.
-- Se eventualmente necessário, pode ser adicionado numa migration futura.

-- +goose Down
ALTER TABLE vessels
    DROP CONSTRAINT IF EXISTS vessels_risk_level_check,
    ADD CONSTRAINT vessels_risk_level_check
        CHECK (risk_level IN ('high', 'standard', 'low'));
