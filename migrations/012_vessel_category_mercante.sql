-- +goose Up
-- -----------------------------------------------------------------------------
-- Renomeia a categoria 'estrangeiro' para 'mercante' e adiciona 'estado'.
--
-- Motivação: "estrangeiro" era ambíguo — afretados e navios de estado também
-- têm bandeira estrangeira mas têm tratamento diferente. "mercante" é o termo
-- correto para os navios alvo de inspeção PSC (navios mercantes estrangeiros).
-- 'estado' cobre navios de guerra, pesquisa e oceanográficos.
-- -----------------------------------------------------------------------------

-- 1. Remove o constraint antigo para poder renomear o valor.
ALTER TABLE vessels DROP CONSTRAINT IF EXISTS vessels_category_check;

-- 2. Renomeia registros existentes.
UPDATE vessels SET category = 'mercante' WHERE category = 'estrangeiro';

-- 3. Recria o constraint com os novos valores.
ALTER TABLE vessels
    ADD CONSTRAINT vessels_category_check
        CHECK (category IN ('mercante', 'nacional', 'afretado', 'estado', 'apoio', 'desativado'));

-- +goose Down
ALTER TABLE vessels DROP CONSTRAINT IF EXISTS vessels_category_check;
UPDATE vessels SET category = 'estrangeiro' WHERE category = 'mercante';
DELETE FROM vessels WHERE category = 'estado'; -- sem equivalente antigo
ALTER TABLE vessels
    ADD CONSTRAINT vessels_category_check
        CHECK (category IN ('estrangeiro', 'nacional', 'afretado', 'apoio', 'desativado'));
