// Package scheduler gerencia o ciclo automático de scraping ZP-21.
package scheduler

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"

	dbsqlc "github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/portcall"
	"github.com/marcusteixeirabr/psc-gvi/internal/scraper"
)

// Scheduler encapsula o cron e o estado necessário para os jobs automáticos.
type Scheduler struct {
	cron            *cron.Cron
	queries         *dbsqlc.Queries
	zp21URL         string
	vesselFinderURL string
	cialaSession    *cialaSession
	alerter         *Alerter
}

// New cria o scheduler. Ainda não inicia — chame Start().
func New(queries *dbsqlc.Queries, zp21URL string) *Scheduler {
	return &Scheduler{
		cron:    cron.New(),
		queries: queries,
		zp21URL: zp21URL,
	}
}

// WithVesselFinder configura a URL do VesselFinder para auto-IMO (Story 7.2).
func (s *Scheduler) WithVesselFinder(url string) *Scheduler {
	s.vesselFinderURL = url
	return s
}

// WithCIALA configura as credenciais do CIALA para auto-CIALA (Story 7.3).
func (s *Scheduler) WithCIALA(cialaURL, username, password string) *Scheduler {
	if cialaURL != "" && username != "" && password != "" {
		s.cialaSession = newCIALASession(cialaURL, username, password)
	}
	return s
}

// WithAlerter configura o alerter de e-mail (Story 8.3). nil = alertas desativados.
func (s *Scheduler) WithAlerter(a *Alerter) *Scheduler {
	s.alerter = a
	return s
}

// Start registra o job horário e agenda o primeiro disparo em 1 minuto.
func (s *Scheduler) Start() {
	s.cron.AddFunc("@every 1h", s.runZP21Cycle)
	s.cron.Start()

	// Primeiro disparo após 1 minuto — não espera o ciclo completo de 1 hora.
	time.AfterFunc(1*time.Minute, s.runZP21Cycle)

	slog.Info("iniciado — primeiro ciclo ZP-21 em 1 minuto, depois a cada hora", "component", "scheduler")
}

// Stop aguarda o término do ciclo em andamento e para o scheduler.
func (s *Scheduler) Stop() {
	slog.Info("aguardando término do ciclo em andamento...", "component", "scheduler")
	ctx := s.cron.Stop()
	<-ctx.Done()
	slog.Info("parado", "component", "scheduler")
}

// TriggerCycle dispara o ciclo completo em background e redireciona imediatamente.
// Equivalente ao disparo automático: ZP-21 → AutoIMO → AutoCIALA → Alertas.
func (s *Scheduler) TriggerCycle(c *gin.Context) {
	back := c.Request.Referer()
	if back == "" {
		back = "/"
	}
	go s.runZP21Cycle()
	c.Redirect(http.StatusFound, back+"?flash="+url.QueryEscape("Ciclo iniciado — acompanhe o console"))
}

// runZP21Cycle executa um ciclo completo: busca ZP-21 → processa → loga resultado.
// Erros são logados mas nunca sobem — o servidor não deve cair por falha de scraping.
func (s *Scheduler) runZP21Cycle() {
	start := time.Now()
	slog.Info("iniciando ciclo ZP-21", "component", "scheduler")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	rows, fetchErr := scraper.FetchManobras(ctx, s.zp21URL)
	if fetchErr != nil {
		slog.Error("FetchManobras falhou", "component", "scheduler", "error", fetchErr)
		RecordRun(ctx, s.queries, "zp21", start, 0, 0, 0, fetchErr)
		return
	}

	result := portcall.ProcessManobras(ctx, s.queries, rows)

	slog.Info("ciclo ZP-21 concluído", "component", "scheduler", "duracao", time.Since(start).Round(time.Millisecond), "resultado", result.Summary())

	for _, e := range result.Errors {
		slog.Error("erro no ciclo", "component", "scheduler", "error", e)
	}

	RecordRun(ctx, s.queries, "zp21", start, result.RowsFound, result.PortCallsUpdated+result.PortCallsCreated, len(result.Errors), nil)

	// Auto-IMO: busca IMO para navios novos criados neste ciclo.
	if s.vesselFinderURL != "" {
		imoStart := time.Now()
		imoResult := RunAutoIMO(ctx, s.queries, s.vesselFinderURL)
		RecordRun(ctx, s.queries, "auto_imo", imoStart, 0, imoResult.Processed, imoResult.Failed, imoResult.Err)
	}

	// Auto-CIALA: atualiza risco/inspeção para navios com nova escala.
	if s.cialaSession != nil {
		cialaStart := time.Now()
		cialaResult := RunAutoCIALA(ctx, s.queries, s.cialaSession)
		RecordRun(ctx, s.queries, "auto_ciala", cialaStart, 0, cialaResult.Processed, cialaResult.Failed, cialaResult.Err)
	}

	// Alertas: verifica condições de alerta após o ciclo completo.
	if s.alerter != nil {
		s.alerter.CheckAndAlert(ctx, s.queries)
	}
}
