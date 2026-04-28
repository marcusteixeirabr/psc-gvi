-- +goose Up
-- Registra quando cada port call foi visto pela última vez na consulta ao ZP-21.
-- O dashboard filtra por este campo para exibir apenas navios da última consulta.
ALTER TABLE port_calls ADD COLUMN last_zp21_seen_at TIMESTAMPTZ NULL;

-- +goose Down
ALTER TABLE port_calls DROP COLUMN last_zp21_seen_at;
