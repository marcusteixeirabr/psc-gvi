package auth

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler agrupa os handlers de auth e sua dependência (pool de banco).
// Em vez de variáveis globais, injetamos o pool via struct — mais testável.
type Handler struct {
	pool *pgxpool.Pool
}

// NewHandler cria um Handler com o pool injetado.
func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

// ShowLogin exibe o formulário de login (GET /login).
// Se o usuário já estiver autenticado, redireciona para a raiz.
func (h *Handler) ShowLogin(c *gin.Context) {
	if getSessionUser(c.Request) != nil {
		c.Redirect(http.StatusFound, "/")
		return
	}
	// c.HTML renderiza um template Go HTML.
	// gin.H é um atalho para map[string]any — passa variáveis para o template.
	c.HTML(http.StatusOK, "login.html", gin.H{
		"error": c.Query("error"),
	})
}

// HandleLogin processa o formulário de login (POST /login).
func (h *Handler) HandleLogin(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")

	user, err := Authenticate(c.Request.Context(), h.pool, username, password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			// Credenciais erradas → volta para o formulário com mensagem de erro.
			// Usamos redirect para evitar o problema de "reenvio de formulário"
			// ao pressionar F5 — o navegador não resubmete um GET.
			c.Redirect(http.StatusFound, "/login?error=credenciais+inválidas")
			return
		}
		// Erro inesperado do banco — não expõe detalhes ao usuário.
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{
			"error": "erro interno, tente novamente",
		})
		return
	}

	if err := saveSession(c.Writer, c.Request, *user); err != nil {
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{
			"error": "erro ao criar sessão, tente novamente",
		})
		return
	}

	c.Redirect(http.StatusFound, "/")
}

// HandleLogout destrói a sessão e redireciona para o login (POST /logout).
func (h *Handler) HandleLogout(c *gin.Context) {
	_ = clearSession(c.Writer, c.Request)
	c.Redirect(http.StatusFound, "/login")
}
