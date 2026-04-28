-- +goose Up
-- Renomeia 'mercante' de volta para 'estrangeiro'.
-- Decisão de domínio: o termo correto é "estrangeiro" (bandeira estrangeira),
-- e o label exibido na UI já era "Estrangeiro". Alinhamos o valor do BD.
ALTER TABLE vessels DROP CONSTRAINT IF EXISTS vessels_category_check;
UPDATE vessels SET category = 'estrangeiro' WHERE category = 'mercante';
ALTER TABLE vessels ADD CONSTRAINT vessels_category_check
    CHECK (category IN ('estrangeiro', 'nacional', 'afretado', 'estado', 'apoio', 'desativado'));

-- +goose Down
ALTER TABLE vessels DROP CONSTRAINT IF EXISTS vessels_category_check;
UPDATE vessels SET category = 'mercante' WHERE category = 'estrangeiro';
ALTER TABLE vessels ADD CONSTRAINT vessels_category_check
    CHECK (category IN ('mercante', 'nacional', 'afretado', 'estado', 'apoio', 'desativado'));
