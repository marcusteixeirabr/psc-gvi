-- +goose Up
-- -----------------------------------------------------------------------------
-- Dois ajustes necessários para a Story 2.1 (scraping ZP-21):
--
-- 1. Adiciona terminal a port_calls
--    O terminal (ex: "Portonave", "TCP") vem do ZP-21 e é apenas texto —
--    não vira entidade, só serve para os inspetores saberem o local físico.
--
-- 2. Corrige o CHECK de priority_snapshot
--    Estava com 'P0' (nomenclatura antiga). Passa para 'P3' (sem prioridade)
--    e inclui 'N/A' (aguardando CIALA).
-- -----------------------------------------------------------------------------

-- 1. Terminal: campo de texto livre, nullable.
ALTER TABLE port_calls
    ADD COLUMN terminal VARCHAR(100);

-- 2. Corrige a constraint de priority_snapshot.
ALTER TABLE port_calls
    DROP CONSTRAINT IF EXISTS port_calls_priority_snapshot_check;

ALTER TABLE port_calls
    ADD CONSTRAINT port_calls_priority_snapshot_check
        CHECK (priority_snapshot IN ('P1', 'P2', 'P3', 'N/A'));

-- +goose Down
ALTER TABLE port_calls DROP COLUMN IF EXISTS terminal;

ALTER TABLE port_calls
    DROP CONSTRAINT IF EXISTS port_calls_priority_snapshot_check;

ALTER TABLE port_calls
    ADD CONSTRAINT port_calls_priority_snapshot_check
        CHECK (priority_snapshot IN ('P0', 'P1', 'P2'));
