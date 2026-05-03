package scheduler

import (
	"context"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
	dbsqlc "github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/scraper"
)

// cialaSession gerencia o cliente CIALA compartilhado entre ciclos do scheduler.
// Login é feito uma única vez e reutilizado — reset automático em caso de expiração.
type cialaSession struct {
	mu       sync.Mutex
	client   *scraper.CIALAClient
	cialaURL string
	username string
	password string
}

func newCIALASession(cialaURL, username, password string) *cialaSession {
	return &cialaSession{cialaURL: cialaURL, username: username, password: password}
}

func (s *cialaSession) get() (*scraper.CIALAClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		c, err := scraper.NewCIALAClient(s.cialaURL, s.username, s.password)
		if err != nil {
			return nil, err
		}
		s.client = c
	}
	return s.client, nil
}

func (s *cialaSession) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = nil
}

// RunAutoCIALA consulta o CIALA para navios com escala ativa não consultados desde a criação da escala.
// Retorna RunResult para que o scheduler possa persistir métricas via RecordRun.
func RunAutoCIALA(ctx context.Context, q *dbsqlc.Queries, session *cialaSession) RunResult {
	vessels, err := q.ListVesselsForAutoCIALA(ctx)
	if err != nil {
		slog.Error("erro ao listar navios", "component", "auto-ciala", "error", err)
		return RunResult{Err: err}
	}
	if len(vessels) == 0 {
		return RunResult{}
	}

	client, err := session.get()
	if err != nil {
		slog.Error("erro ao criar cliente CIALA", "component", "auto-ciala", "error", err)
		return RunResult{Err: err}
	}

	found, failed := 0, 0
	for _, v := range vessels {
		if ctx.Err() != nil {
			break
		}

		imo := *v.Imo
		result, err := client.LookupIMO(ctx, imo)
		if err != nil {
			// Tenta reset de sessão uma vez (token pode ter expirado).
			slog.Warn("falha no CIALA, tentando reset de sessão", "component", "auto-ciala", "vessel", v.Name, "error", err)
			session.reset()
			client, err = session.get()
			if err == nil {
				result, err = client.LookupIMO(ctx, imo)
			}
		}
		if err != nil {
			slog.Error("CIALA indisponível", "component", "auto-ciala", "vessel", v.Name, "error", err)
			failed++
			continue
		}

		var riskLevel *string
		if result.RiskLevel != "" {
			riskLevel = &result.RiskLevel
		}
		var inspDate pgtype.Date
		if result.LastInspectionDate != nil {
			_ = inspDate.Scan(*result.LastInspectionDate)
		}
		var flag *string
		if result.Flag != "" {
			flag = &result.Flag
		}
		var vesselType *string
		if result.VesselType != "" {
			vesselType = &result.VesselType
		}
		var yearBuilt *int32
		if result.YearBuilt > 0 {
			y := int32(result.YearBuilt)
			yearBuilt = &y
		}
		var deficiencies *string
		if result.LastInspectionDeficiencies != "" {
			deficiencies = &result.LastInspectionDeficiencies
		}

		if err := q.UpdateVesselCIALA(ctx, dbsqlc.UpdateVesselCIALAParams{
			ID:                         v.ID,
			RiskLevel:                  riskLevel,
			LastInspectionDate:         inspDate,
			LastInspectionDeficiencies: deficiencies,
			Flag:                       flag,
			VesselType:                 vesselType,
			YearBuilt:                  yearBuilt,
		}); err != nil {
			slog.Error("erro ao salvar dados CIALA", "component", "auto-ciala", "vessel", v.Name, "error", err)
			failed++
			continue
		}

		if err := q.UpdateVesselLastCIALACheck(ctx, v.ID); err != nil {
			slog.Warn("erro ao atualizar last_ciala_checked_at", "component", "auto-ciala", "vessel", v.Name, "error", err)
		}

		slog.Info("navio atualizado", "component", "auto-ciala", "vessel", v.Name, "risk_level", riskLevel)
		found++
	}

	slog.Info("ciclo concluído", "component", "auto-ciala", "atualizados", found, "falhas", failed)
	return RunResult{Processed: found, Failed: failed}
}
