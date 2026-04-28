-- +goose Up
-- Armazena o texto de deficiências da última inspeção PSC, conforme retornado pelo CIALA.
-- O campo "Inspeccionado" do CIALA às vezes traz deficiências em vez de (ou além de) uma data.
-- Exemplos: "3 deficiencias", "Sí 2 def.", "Sin deficiencias".
--
-- Regra de negócio (implementar em Fase 6 — filtros do dashboard):
--   Navio com last_inspection_deficiencies IS NOT NULL deve sempre aparecer no dashboard,
--   independente da prioridade calculada (mesmo que P3).
ALTER TABLE vessels
    ADD COLUMN last_inspection_deficiencies TEXT;

-- +goose Down
ALTER TABLE vessels DROP COLUMN IF EXISTS last_inspection_deficiencies;
