-- +goose Up
ALTER TABLE vessels
    ADD COLUMN last_ciala_checked_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE vessels
    DROP COLUMN last_ciala_checked_at;
