-- +goose Up
-- -----------------------------------------------------------------------------
-- Correções no schema após revisão de domínio.
--
-- Correção 1: vessels.risk_level → nullable
--   O risk_level não é calculado pela aplicação (ao contrário do que o Java
--   legado sugeria com GrauRisco por idade). Ele vem exclusivamente do site
--   do CIALA via scraping. Fluxo real:
--     ZP-21 → nome do navio → port_call criado (sem IMO, sem risk_level)
--     equasys → busca por nome → descobre o IMO
--     CIALA → busca por IMO → retorna risk_level + histórico de inspeções
--   Portanto, risk_level é NULL até que o CIALA seja consultado.
--
-- Correção 2: inspections.inspector_id → removido
--   Cada port_call pode ter 0 ou 1 inspeção (constraint UNIQUE abaixo).
--   A inspeção pertence ao port_call — não precisamos saber quem registrou.
--   Simplifica o modelo sem perder informação relevante para o negócio.
-- -----------------------------------------------------------------------------

-- Correção 1: permite NULL em risk_level (desconhecido até o CIALA ser consultado).
ALTER TABLE vessels
    ALTER COLUMN risk_level DROP NOT NULL;

-- Correção 2: remove o vínculo com o usuário que registrou a inspeção.
ALTER TABLE inspections
    DROP COLUMN inspector_id;

-- Garante a cardinalidade 0..1: um port_call não pode ter mais de uma inspeção.
-- UNIQUE em uma FK é a forma SQL de expressar "no máximo um".
ALTER TABLE inspections
    ADD CONSTRAINT uq_inspections_port_call UNIQUE (port_call_id);

-- Remove o índice separado que criamos — o UNIQUE já cria um índice internamente.
DROP INDEX IF EXISTS idx_inspections_inspector_id;

-- +goose Down
ALTER TABLE inspections
    DROP CONSTRAINT IF EXISTS uq_inspections_port_call;

ALTER TABLE inspections
    ADD COLUMN inspector_id BIGINT REFERENCES users(id) ON DELETE RESTRICT;

CREATE INDEX idx_inspections_inspector_id ON inspections (inspector_id);

ALTER TABLE vessels
    ALTER COLUMN risk_level SET NOT NULL;
