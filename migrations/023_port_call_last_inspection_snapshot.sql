-- +goose Up
-- Snapshot da última inspeção PSC conhecida (CIALA, em qualquer porto do Acordo
-- de Viña del Mar) no momento da atracação. É congelado junto com
-- risk_level_snapshot/priority_snapshot e nunca recalculado depois.
-- O relatório mensal usa este campo para mostrar a "última inspeção" quando
-- o GVI não inspecionou o navio no mês (ver regras.md §15).
-- Por que snapshot: vessels.last_inspection_date guarda só o valor ATUAL. Se
-- um navio for inspecionado em outro porto em setembro, o relatório de julho
-- mostraria setembro.
ALTER TABLE port_calls
    ADD COLUMN last_inspection_snapshot DATE;

-- Backfill das escalas já atracadas. Melhor estimativa possível sem histórico:
--   a) a data atual do CIALA, se não for posterior à atracação (ainda valia);
--   b) senão, a inspeção mais recente do próprio GVI antes da atracação;
--   c) senão, NULL (o relatório mostra "—").
-- Fuso explícito: o goose do deploy.sh conecta SEM o timezone de SP do app
-- (regras.md §11), então ::date direto daria o dia em UTC.
UPDATE port_calls pc
SET last_inspection_snapshot = COALESCE(
        (SELECT v.last_inspection_date FROM vessels v
          WHERE v.id = pc.vessel_id
            AND v.last_inspection_date <= (pc.actual_arrival AT TIME ZONE 'America/Sao_Paulo')::date),
        (SELECT MAX(i.inspection_date)
           FROM inspections i
           JOIN port_calls pc2 ON pc2.id = i.port_call_id
          WHERE pc2.vessel_id = pc.vessel_id
            AND i.inspection_date <= (pc.actual_arrival AT TIME ZONE 'America/Sao_Paulo')::date)
    )
WHERE pc.actual_arrival IS NOT NULL;

-- +goose Down
ALTER TABLE port_calls
    DROP COLUMN last_inspection_snapshot;
