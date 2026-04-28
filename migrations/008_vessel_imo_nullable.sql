-- +goose Up
-- -----------------------------------------------------------------------------
-- IMO passa a ser opcional no cadastro manual.
-- Fluxo real: ZP-21 encontra o navio pelo nome → equasys descobre o IMO.
-- Portanto, um vessel pode existir sem IMO até que o scraper do equasys rode.
-- O constraint UNIQUE permanece: PostgreSQL trata múltiplos NULLs como
-- valores distintos, então vários navios sem IMO não conflitam entre si.
-- -----------------------------------------------------------------------------
ALTER TABLE vessels
    ALTER COLUMN imo DROP NOT NULL;

-- +goose Down
-- Atenção: falha se houver navios com imo NULL no banco.
ALTER TABLE vessels
    ALTER COLUMN imo SET NOT NULL;
