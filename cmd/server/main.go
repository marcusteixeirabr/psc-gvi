// Ponto de entrada da aplicação psc-gvi.
//
// Em Go, todo executável começa pela função main() no pacote main.
// A convenção é colocar o main.go dentro de cmd/server/ — isso permite
// que o projeto tenha múltiplos executáveis no futuro (ex: cmd/worker/ para scraping).
//
// Sequência de inicialização:
//   1. Carrega configuração (variáveis de ambiente)
//   2. Conecta ao banco de dados
//   3. Monta o roteador Gin com as rotas
//   4. Inicia o servidor HTTP
package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marcusteixeirabr/psc-gvi/internal/config"
	"github.com/marcusteixeirabr/psc-gvi/internal/db"
)

func main() {
	// ── 1. Configuração ────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		// log.Fatal imprime a mensagem e chama os.Exit(1) — encerra o processo.
		// Fail fast: se não temos configuração, não adianta continuar.
		log.Fatal("ERRO de configuração: ", err)
	}

	// ── 2. Banco de dados ──────────────────────────────────────────────────
	pool, err := db.New(cfg.DSN())
	if err != nil {
		log.Fatal("ERRO ao conectar ao banco de dados: ", err)
	}
	// defer garante que o pool seja fechado quando main() terminar —
	// mesmo que ocorra um panic. É o equivalente Go ao try/finally do Java.
	defer pool.Close()

	log.Println("Conectado ao PostgreSQL com sucesso.")

	// ── 3. Roteador ────────────────────────────────────────────────────────
	// gin.Default() cria um roteador com dois middlewares padrão:
	//   - Logger: imprime cada requisição no terminal (método, rota, status, tempo)
	//   - Recovery: captura panics e retorna 500 em vez de derrubar o servidor
	//
	// Em produção trocaremos por gin.New() com middlewares customizados,
	// mas para desenvolvimento o Default é perfeito.
	router := gin.Default()

	// Rota de health check: permite que ferramentas externas (GCP, Docker)
	// verifiquem se a aplicação está viva sem autenticação.
	router.GET("/health", func(c *gin.Context) {
		// c.JSON serializa a struct/map para JSON e define o Content-Type automaticamente.
		// http.StatusOK = 200
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	// ── 4. Servidor HTTP ────────────────────────────────────────────────────
	addr := ":" + cfg.Port
	log.Printf("Servidor iniciado em http://localhost%s (env: %s)\n", addr, cfg.Env)

	// router.Run() inicia o servidor e bloqueia — só retorna se houver erro.
	if err := router.Run(addr); err != nil {
		log.Fatal("ERRO ao iniciar servidor: ", err)
	}
}
