-- +goose Up
-- Substitui category por dois campos booleanos mais simples e sem inconsistências.
--
-- afretado (bool): navio de bandeira estrangeira operado por empresa brasileira.
--   Se true → comporta como nacional (excluído do dashboard, incluído no relatório).
--
-- acompanhado (bool, default TRUE): se false → excluído de tudo.
--   Equivale aos antigos: estado, apoio, desativado.
--   Label na UI: "Não acompanhado (Estado, Apoio, Rebocador...)"
--
-- nacional/estrangeiro passa a ser calculado automaticamente:
--   flag = 'Brazil'/'Brasil' → nacional
--   afretado = true          → trata como nacional
--   default                  → estrangeiro

ALTER TABLE vessels
    ADD COLUMN afretado    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN acompanhado BOOLEAN NOT NULL DEFAULT TRUE;

-- Migra dados do antigo campo category.
UPDATE vessels SET afretado    = TRUE  WHERE category = 'afretado';
UPDATE vessels SET acompanhado = FALSE WHERE category IN ('estado', 'apoio', 'desativado');

-- Remove constraint e coluna category.
ALTER TABLE vessels DROP CONSTRAINT IF EXISTS vessels_category_check;
ALTER TABLE vessels DROP COLUMN IF EXISTS category;

-- +goose Down
ALTER TABLE vessels
    ADD COLUMN category VARCHAR(20);

UPDATE vessels SET category = 'afretado'    WHERE afretado = TRUE;
UPDATE vessels SET category = 'desativado'  WHERE acompanhado = FALSE AND afretado = FALSE;
UPDATE vessels SET category = 'estrangeiro' WHERE category IS NULL;

ALTER TABLE vessels
    ADD CONSTRAINT vessels_category_check
        CHECK (category IN ('estrangeiro', 'nacional', 'afretado', 'estado', 'apoio', 'desativado'));

ALTER TABLE vessels DROP COLUMN IF EXISTS afretado;
ALTER TABLE vessels DROP COLUMN IF EXISTS acompanhado;
