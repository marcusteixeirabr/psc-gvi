package main

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/config"
	"github.com/marcusteixeirabr/psc-gvi/internal/dashboard"
	"github.com/marcusteixeirabr/psc-gvi/internal/db"
	"github.com/marcusteixeirabr/psc-gvi/internal/inspection"
	dbsqlc "github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/health"
	"github.com/marcusteixeirabr/psc-gvi/internal/portcall"
	"github.com/marcusteixeirabr/psc-gvi/internal/report"
	"github.com/marcusteixeirabr/psc-gvi/internal/scheduler"
	"github.com/marcusteixeirabr/psc-gvi/internal/vessel"
)

func main() {
	// ── 0. Timezone ────────────────────────────────────────────────────────
	// Fixa o fuso do processo como America/Sao_Paulo.
	// Sem isso, time.Now() retorna UTC, e Format/Year produzem valores errados
	// para qualquer hora entre 21h00 e 23h59 SP (= dia seguinte em UTC).
	spLoc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		log.Fatal("ERRO ao carregar timezone America/Sao_Paulo: ", err)
	}
	time.Local = spLoc

	// ── 1. Configuração ────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("ERRO de configuração: ", err)
	}

	// ── 2. Banco de dados ──────────────────────────────────────────────────
	pool, err := db.New(cfg.DSN())
	if err != nil {
		log.Fatal("ERRO ao conectar ao banco de dados: ", err)
	}
	defer pool.Close()
	log.Println("Conectado ao PostgreSQL com sucesso.")

	// ── 3. Queries (sqlc) ──────────────────────────────────────────────────
	queries := dbsqlc.New(pool)

	// ── 4. Sessões ─────────────────────────────────────────────────────────
	auth.InitStore(cfg.SessionSecret)

	// ── 5. Scheduler automático ────────────────────────────────────────────
	alerter := scheduler.NewAlerter(scheduler.SMTPConfig{
		Host:       cfg.SMTPHost,
		Port:       cfg.SMTPPort,
		User:       cfg.SMTPUser,
		Password:   cfg.SMTPPassword,
		AlertEmail: cfg.AlertEmail,
	})

	sched := scheduler.New(queries, cfg.ZP21URL).
		WithVesselFinder(cfg.VesselFinderURL).
		WithCIALA(cfg.CIALAURL, cfg.CIALAUsername, cfg.CIALAPassword).
		WithAlerter(alerter)
	sched.Start()

	// ── 6. Roteador ────────────────────────────────────────────────────────
	router := gin.Default()

	// Funções auxiliares para os templates Go.
	router.SetFuncMap(template.FuncMap{
		"sub":  func(a, b int) int { return a - b },
		"add1": func(i int) int { return i + 1 },
		"todayISO": func() string { return time.Now().Format("2006-01-02") },
		"derefStr": func(s *string) string {
			if s == nil { return "" }
			return *s
		},
		"formatTS":       health.FormatTS,
		"formatDuration": health.FormatDuration,
	})

	router.LoadHTMLGlob("web/templates/*.html")

	// ── Rotas públicas ─────────────────────────────────────────────────────
	authHandler := auth.NewHandler(pool)

	router.GET("/login", authHandler.ShowLogin)
	router.POST("/login", authHandler.HandleLogin)
	router.POST("/logout", authHandler.HandleLogout)

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// ── Handlers ───────────────────────────────────────────────────────────
	vesselHandler     := vessel.NewHandler(queries)
	imoFinder         := vessel.NewIMOFinder(queries, cfg.VesselFinderURL)
	cialaHandler      := vessel.NewCIALAHandler(queries, cfg.CIALAURL, cfg.CIALAUsername, cfg.CIALAPassword, cfg.VesselFinderURL)
	escalaHandler     := portcall.NewEscalaHandler(queries)
	dashboardHandler  := dashboard.NewHandler(queries)
	inspectionHandler := inspection.NewHandler(queries)
	reportHandler     := report.NewHandler(queries)
	healthHandler := health.NewHandler(queries)
	if alerter != nil {
		healthHandler = healthHandler.WithMailer(alerter)
	}

	// ── Rotas protegidas ───────────────────────────────────────────────────
	protected := router.Group("/")
	protected.Use(auth.RequireAuth)
	{
		protected.GET("/", dashboardHandler.Index)

		// Relatório mensal — leitura (todos os usuários autenticados)
		protected.GET("/relatorio", reportHandler.Show)
		protected.GET("/relatorio/export", reportHandler.ExportCSV)

		// Escalas — leitura (todos os usuários autenticados)
		protected.GET("/escalas", escalaHandler.List)

		// Vessels — leitura (todos os usuários autenticados)
		protected.GET("/vessels", vesselHandler.List)
		protected.GET("/vessels/:id", vesselHandler.Detail)

		// Vessels — escrita (editor e admin)
		editors := protected.Group("/")
		editors.Use(auth.RequireRole("admin", "editor"))
		{
			editors.GET("/vessels/new", vesselHandler.NewForm)
			editors.POST("/vessels", vesselHandler.Create)
			editors.GET("/vessels/:id/edit", vesselHandler.EditForm)
			editors.POST("/vessels/:id", vesselHandler.Update)
			editors.POST("/scrape/zp21", sched.TriggerCycle)
			editors.GET("/escalas/new", escalaHandler.NewForm)
			editors.POST("/escalas/new", escalaHandler.NewSave)
			editors.POST("/escalas/:id/berthing", escalaHandler.RegisterBerthing)
			editors.POST("/escalas/:id/suspension", escalaHandler.RegisterSuspension)
			editors.POST("/escalas/:id/edit-berthing", escalaHandler.EditBerthing)
			editors.POST("/escalas/:id/edit-suspension", escalaHandler.EditSuspension)
			editors.POST("/escalas/:id/cancel-berthing", escalaHandler.CancelBerthing)
			editors.POST("/escalas/:id/cancel-suspension", escalaHandler.CancelSuspension)
			editors.GET("/escalas/:id/edit", escalaHandler.EditForm)
			editors.POST("/escalas/:id/edit", escalaHandler.EditSave)
			editors.POST("/escalas/:id/delete", escalaHandler.DeleteEscala)
			editors.GET("/vessels/:id/inspect", inspectionHandler.ShowForm)
			editors.POST("/vessels/:id/inspect", inspectionHandler.Register)
			editors.POST("/vessels/:id/find-imo", imoFinder.FindForVessel)
			editors.GET("/imo-search", imoFinder.BatchProgressPage)
			editors.GET("/imo-search/stream", imoFinder.BatchStream)
			editors.POST("/vessels/:id/ciala", cialaHandler.LookupVessel)
			editors.POST("/vessels/:id/atualizar", cialaHandler.AtualizarDados)
			editors.GET("/ciala-search", cialaHandler.BatchProgressPage)
			editors.GET("/ciala-search/stream", cialaHandler.BatchStream)
		}

		// Operações admin
		admins := protected.Group("/")
		admins.Use(auth.RequireRole("admin"))
		{
			admins.POST("/vessels/:id/deactivate", vesselHandler.Deactivate)
			admins.POST("/vessels/:id/delete", vesselHandler.Delete)
			admins.POST("/vessels/:id/merge", vesselHandler.MergeVessel)
			admins.GET("/admin/health", healthHandler.Show)
			admins.POST("/admin/health/test-email", healthHandler.TestEmail)
		}
	}

	// ── 7. Servidor HTTP com graceful shutdown ─────────────────────────────
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	// Canal para capturar SIGINT/SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Servidor iniciado em http://localhost:%s (env: %s)\n", cfg.Port, cfg.Env)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("ERRO ao iniciar servidor: ", err)
		}
	}()

	// Aguarda sinal de encerramento.
	<-quit
	log.Println("Encerrando servidor...")

	// Para o scheduler antes de fechar o servidor.
	sched.Stop()

	// Drain das conexões HTTP ativas (timeout de 10s).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Shutdown forçado: %v", err)
	}

	log.Println("Servidor encerrado.")
}
