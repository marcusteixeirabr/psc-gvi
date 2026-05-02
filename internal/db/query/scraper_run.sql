-- Queries para scraper_runs — histórico de execuções do scheduler.

-- name: InsertScraperRun :exec
INSERT INTO scraper_runs (scraper, started_at, finished_at, duration_ms, status,
                          rows_found, items_processed, items_failed, error_message)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ListRecentScraperRuns :many
-- Últimas 50 execuções de um scraper específico, mais recentes primeiro.
SELECT * FROM scraper_runs
WHERE scraper = $1
ORDER BY started_at DESC
LIMIT 50;

-- name: CountConsecutiveErrors :one
-- Conta falhas consecutivas mais recentes para um scraper.
-- Retorna o número de execuções com status='error' no início da sequência mais recente.
SELECT COUNT(*)::int FROM (
    SELECT status FROM scraper_runs
    WHERE scraper = $1
    ORDER BY started_at DESC
    LIMIT 10
) recent
WHERE status = 'error';

-- name: GetLatestRunPerScraper :many
-- Última execução de cada scraper conhecido, mais recente primeiro.
SELECT DISTINCT ON (scraper) *
FROM scraper_runs
ORDER BY scraper, started_at DESC;

-- name: ListRecentRuns :many
-- Últimas 20 execuções de todos os scrapers, ordenadas por data desc.
SELECT * FROM scraper_runs
ORDER BY started_at DESC
LIMIT 20;

-- name: CountZP21SilentCycles :one
-- Conta quantas das últimas 3 execuções do zp21 retornaram 0 navios.
SELECT COUNT(*)::int FROM (
    SELECT rows_found FROM scraper_runs
    WHERE scraper = 'zp21'
    ORDER BY started_at DESC
    LIMIT 3
) last3
WHERE rows_found = 0;
