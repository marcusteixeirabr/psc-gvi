package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	dbsqlc "github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/scraper"
)

const autoIMOMaxPerCycle = 10

// RunAutoIMO busca IMO no VesselFinder para navios recém-criados pelo ZP-21 sem IMO.
// Limite de 10 navios por ciclo com delay de 500ms entre chamadas (anti-bot).
// Retorna RunResult para que o scheduler possa persistir métricas via RecordRun.
func RunAutoIMO(ctx context.Context, q *dbsqlc.Queries, vesselFinderURL string) RunResult {
	vessels, err := q.ListVesselsWithoutIMO(ctx)
	if err != nil {
		slog.Error("erro ao listar navios sem IMO", "component", "auto-imo", "error", err)
		return RunResult{Err: err}
	}
	if len(vessels) == 0 {
		return RunResult{}
	}

	if len(vessels) > autoIMOMaxPerCycle {
		slog.Warn("navios sem IMO acima do limite do ciclo", "component", "auto-imo", "total", len(vessels), "processando", autoIMOMaxPerCycle)
		vessels = vessels[:autoIMOMaxPerCycle]
	}

	found, failed := 0, 0
	for _, v := range vessels {
		if ctx.Err() != nil {
			break
		}

		// Normaliza o nome antes de enviar ao VesselFinder: acentos causam zero resultados.
		searchName := scraper.StripAccents(v.Name)
		imo, err := scraper.FindIMO(ctx, vesselFinderURL, searchName,
			numericToFloat64(v.LengthM), numericToFloat64(v.BeamM))
		if err != nil {
			slog.Warn("navio não encontrado no VesselFinder", "component", "auto-imo", "vessel", v.Name, "error", err)
			failed++
		} else {
			saveErr := q.UpdateVesselIMO(ctx, dbsqlc.UpdateVesselIMOParams{
				ID:  v.ID,
				Imo: &imo,
			})
			if saveErr != nil {
				var pgErr *pgconn.PgError
				if errors.As(saveErr, &pgErr) && pgErr.Code == "23505" {
					slog.Warn("IMO já existe em outro cadastro — requer merge manual", "component", "auto-imo", "vessel", v.Name, "imo", imo)
				} else {
					slog.Error("erro ao salvar IMO", "component", "auto-imo", "vessel", v.Name, "error", saveErr)
				}
				failed++
			} else {
				slog.Info("IMO registrado", "component", "auto-imo", "vessel", v.Name, "imo", imo)
				found++
			}
		}

		// Delay respeitoso entre requisições ao VesselFinder.
		select {
		case <-ctx.Done():
			goto done
		case <-time.After(500 * time.Millisecond):
		}
	}

done:
	slog.Info("ciclo concluído", "component", "auto-imo", "encontrados", found, "nao_encontrados", failed)
	return RunResult{Processed: found, Failed: failed}
}
