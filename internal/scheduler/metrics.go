package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	dbsqlc "github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
)

// RunResult resume o resultado de uma execução automática (auto-IMO ou auto-CIALA).
type RunResult struct {
	Processed int
	Failed    int
	Err       error // non-nil = falha total do scraper
}

// RecordRun persiste o resultado de um ciclo na tabela scraper_runs.
// Erros de persistência são apenas logados — não interrompem o ciclo.
func RecordRun(ctx context.Context, q *dbsqlc.Queries, scraperName string, start time.Time, rowsFound, processed, failed int, totalErr error) {
	finished := time.Now()
	durationMs := int32(finished.Sub(start).Milliseconds())

	status := "success"
	if totalErr != nil {
		status = "error"
	} else if failed > 0 {
		status = "partial"
	}

	var errMsg *string
	if totalErr != nil {
		s := totalErr.Error()
		errMsg = &s
	}

	var startedTS, finishedTS pgtype.Timestamptz
	_ = startedTS.Scan(start)
	_ = finishedTS.Scan(finished)

	if err := q.InsertScraperRun(ctx, dbsqlc.InsertScraperRunParams{
		Scraper:        scraperName,
		StartedAt:      startedTS,
		FinishedAt:     finishedTS,
		DurationMs:     durationMs,
		Status:         status,
		RowsFound:      int32(rowsFound),
		ItemsProcessed: int32(processed),
		ItemsFailed:    int32(failed),
		ErrorMessage:   errMsg,
	}); err != nil {
		log.Printf("[metrics] erro ao registrar run de %s: %v", scraperName, err)
	}
}
