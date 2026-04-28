-- +goose Up
-- -----------------------------------------------------------------------------
-- Substitui vessels.active (boolean) por vessels.category (VARCHAR).
--
-- O boolean active era insuficiente: só distinguia "existe" de "não existe".
-- Na realidade, um navio pode ser estrangeiro (dashboard + relatório),
-- nacional (só relatório), afretado por empresa BR (idem nacional, mas manual),
-- apoio (rebocadores, pesquisa — não entra em nada) ou desativado.
-- -----------------------------------------------------------------------------

ALTER TABLE vessels
    ADD COLUMN category VARCHAR(20) NOT NULL DEFAULT 'estrangeiro'
        CHECK (category IN ('estrangeiro', 'nacional', 'afretado', 'apoio', 'desativado'));

-- Migra navios já marcados como inativos para a categoria 'desativado'.
UPDATE vessels SET category = 'desativado' WHERE active = false;

ALTER TABLE vessels DROP COLUMN active;

-- +goose Down
ALTER TABLE vessels ADD COLUMN active BOOLEAN NOT NULL DEFAULT true;
UPDATE vessels SET active = false WHERE category = 'desativado';
ALTER TABLE vessels DROP COLUMN category;
