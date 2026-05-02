-- +goose Up
CREATE TABLE scraper_runs (
    id             BIGSERIAL PRIMARY KEY,
    scraper        VARCHAR(50)  NOT NULL,
    started_at     TIMESTAMPTZ  NOT NULL,
    finished_at    TIMESTAMPTZ  NOT NULL,
    duration_ms    INTEGER      NOT NULL,
    status         VARCHAR(20)  NOT NULL CHECK (status IN ('success', 'partial', 'error')),
    rows_found     INTEGER      NOT NULL DEFAULT 0,
    items_processed INTEGER     NOT NULL DEFAULT 0,
    items_failed   INTEGER      NOT NULL DEFAULT 0,
    error_message  TEXT
);

CREATE INDEX idx_scraper_runs_scraper_started ON scraper_runs (scraper, started_at DESC);

-- +goose Down
DROP TABLE scraper_runs;
