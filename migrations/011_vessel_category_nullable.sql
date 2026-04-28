-- +goose Up
-- -----------------------------------------------------------------------------
-- Torna vessels.category nullable.
-- Navios criados pelo scraper ZP-21 entram sem categoria (NULL = não classificado).
-- Um editor ou admin deve classificar manualmente após a criação.
-- -----------------------------------------------------------------------------
ALTER TABLE vessels ALTER COLUMN category DROP NOT NULL;
ALTER TABLE vessels ALTER COLUMN category DROP DEFAULT;

-- +goose Down
-- Atenção: falha se houver vessels com category NULL.
UPDATE vessels SET category = 'estrangeiro' WHERE category IS NULL;
ALTER TABLE vessels ALTER COLUMN category SET DEFAULT 'estrangeiro';
ALTER TABLE vessels ALTER COLUMN category SET NOT NULL;
