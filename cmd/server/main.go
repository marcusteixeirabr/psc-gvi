package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/config"
	"github.com/marcusteixeirabr/psc-gvi/internal/db"
	dbsqlc "github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/vessel"
)

func main() {
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
	// dbsqlc.New() recebe o pool e retorna *Queries — o objeto com todos os
	// métodos de banco gerados pelo sqlc. Passamos este objeto para os handlers.
	queries := dbsqlc.New(pool)

	// ── 4. Sessões ─────────────────────────────────────────────────────────
	// InitStore configura o cookie store com a chave secreta.
	// Deve ser chamado antes de qualquer handler que use sessão.
	auth.InitStore(cfg.SessionSecret)

	// ── 5. Roteador ────────────────────────────────────────────────────────
	router := gin.Default()

	// LoadHTMLGlob carrega todos os .html do diretório de templates.
	// O nome do template é o basename do arquivo (ex: "vessel_list.html").
	router.LoadHTMLGlob("web/templates/*.html")

	// ── Rotas públicas (sem autenticação) ──────────────────────────────────
	authHandler := auth.NewHandler(pool)

	router.GET("/login", authHandler.ShowLogin)
	router.POST("/login", authHandler.HandleLogin)
	router.POST("/logout", authHandler.HandleLogout)

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// ── Handlers ───────────────────────────────────────────────────────────
	vesselHandler := vessel.NewHandler(queries)

	// ── Rotas protegidas ───────────────────────────────────────────────────
	protected := router.Group("/")
	protected.Use(auth.RequireAuth)
	{
		protected.GET("/", func(c *gin.Context) {
			user := auth.GetUser(c)
			c.JSON(http.StatusOK, gin.H{"message": "Dashboard em construção", "user": user.DisplayName})
		})

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
		}

		// Vessels — desativação (admin apenas)
		admins := protected.Group("/")
		admins.Use(auth.RequireRole("admin"))
		{
			admins.POST("/vessels/:id/deactivate", vesselHandler.Deactivate)
		}
	}

	// ── 6. Servidor HTTP ────────────────────────────────────────────────────
	addr := ":" + cfg.Port
	log.Printf("Servidor iniciado em http://localhost%s (env: %s)\n", addr, cfg.Env)

	if err := router.Run(addr); err != nil {
		log.Fatal("ERRO ao iniciar servidor: ", err)
	}
}
