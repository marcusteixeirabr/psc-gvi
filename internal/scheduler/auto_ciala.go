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

	found, failed := 0, 0
	for _, v := range vessels {
		if ctx.Err() != nil {
			break
		}

		result, err := lookupAndSaveCIALA(ctx, q, session, v.ID, *v.Imo)
		if err != nil {
			slog.Error("CIALA indisponível", "component", "auto-ciala", "vessel", v.Name, "error", err)
			failed++
			continue
		}

		slog.Info("navio atualizado", "component", "auto-ciala", "vessel", v.Name, "risk_level", result.RiskLevel)
		found++
	}

	slog.Info("ciclo concluído", "component", "auto-ciala", "atualizados", found, "falhas", failed)
	return RunResult{Processed: found, Failed: failed}
}

// lookupAndSaveCIALA consulta o CIALA para um navio (com retry de sessão uma vez
// em caso de falha — o token pode ter expirado) e persiste o resultado em vessels
// (risk_level, last_inspection_date, etc.) + last_ciala_checked_at.
// Reaproveitado por RunAutoCIALA e pela reconsulta na atracação automática
// (ver portcall.CIALARefresher, regras.md §4 R9).
func lookupAndSaveCIALA(ctx context.Context, q *dbsqlc.Queries, session *cialaSession, vesselID int64, imo string) (*scraper.CIALAResult, error) {
	client, err := session.get()
	if err != nil {
		return nil, err
	}

	result, err := client.LookupIMO(ctx, imo)
	if err != nil {
		session.reset()
		client, err = session.get()
		if err == nil {
			result, err = client.LookupIMO(ctx, imo)
		}
	}
	if err != nil {
		return nil, err
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
		ID:                         vesselID,
		RiskLevel:                  riskLevel,
		LastInspectionDate:         inspDate,
		LastInspectionDeficiencies: deficiencies,
		Flag:                       flag,
		VesselType:                 vesselType,
		YearBuilt:                  yearBuilt,
	}); err != nil {
		return nil, err
	}

	if err := q.UpdateVesselLastCIALACheck(ctx, vesselID); err != nil {
		slog.Warn("erro ao atualizar last_ciala_checked_at", "component", "auto-ciala", "vessel_id", vesselID, "error", err)
	}

	return result, nil
}
