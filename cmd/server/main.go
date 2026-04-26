package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/config"
	"github.com/marcusteixeirabr/psc-gvi/internal/db"
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

	// ── 3. Sessões ─────────────────────────────────────────────────────────
	// InitStore configura o cookie store com a chave secreta.
	// Deve ser chamado antes de qualquer handler que use sessão.
	auth.InitStore(cfg.SessionSecret)

	// ── 4. Roteador ────────────────────────────────────────────────────────
	router := gin.Default()

	// Carrega todos os arquivos .html de web/templates/ para memória.
	// O Gin compila os templates uma vez na inicialização — eficiente.
	// O path é relativo ao diretório de execução (raiz do projeto).
	router.LoadHTMLGlob("web/templates/*.html")

	// ── Rotas públicas (sem autenticação) ──────────────────────────────────
	authHandler := auth.NewHandler(pool)

	router.GET("/login", authHandler.ShowLogin)
	router.POST("/login", authHandler.HandleLogin)
	router.POST("/logout", authHandler.HandleLogout)

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// ── Rotas protegidas (exigem autenticação) ─────────────────────────────
	// router.Group cria um grupo de rotas que compartilham middlewares.
	// Todas as rotas dentro de "protected" passarão por auth.RequireAuth primeiro.
	protected := router.Group("/")
	protected.Use(auth.RequireAuth)
	{
		protected.GET("/", func(c *gin.Context) {
			user := auth.GetUser(c)
			c.JSON(http.StatusOK, gin.H{
				"message":      "Dashboard em construção",
				"user":         user.DisplayName,
				"role":         user.Role,
			})
		})
	}

	// ── 5. Servidor HTTP ────────────────────────────────────────────────────
	addr := ":" + cfg.Port
	log.Printf("Servidor iniciado em http://localhost%s (env: %s)\n", addr, cfg.Env)

	if err := router.Run(addr); err != nil {
		log.Fatal("ERRO ao iniciar servidor: ", err)
	}
}
