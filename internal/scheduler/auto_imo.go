package scheduler

import (
	"context"
	"errors"
	"log"
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
		log.Printf("[auto-imo] erro ao listar navios sem IMO: %v", err)
		return RunResult{Err: err}
	}
	if len(vessels) == 0 {
		return RunResult{}
	}

	if len(vessels) > autoIMOMaxPerCycle {
		log.Printf("[auto-imo] %d navios sem IMO — processando apenas os primeiros %d",
			len(vessels), autoIMOMaxPerCycle)
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
			log.Printf("[auto-imo] %q: não encontrado — %v", v.Name, err)
			failed++
		} else {
			saveErr := q.UpdateVesselIMO(ctx, dbsqlc.UpdateVesselIMOParams{
				ID:  v.ID,
				Imo: &imo,
			})
			if saveErr != nil {
				var pgErr *pgconn.PgError
				if errors.As(saveErr, &pgErr) && pgErr.Code == "23505" {
					log.Printf("[auto-imo] %q: IMO %s já existe em outro cadastro — requer merge manual", v.Name, imo)
				} else {
					log.Printf("[auto-imo] %q: erro ao salvar IMO: %v", v.Name, saveErr)
				}
				failed++
			} else {
				log.Printf("[auto-imo] %q: IMO %s registrado", v.Name, imo)
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
	log.Printf("[auto-imo] ciclo concluído — %d encontrado(s), %d não encontrado(s)", found, failed)
	return RunResult{Processed: found, Failed: failed}
}
