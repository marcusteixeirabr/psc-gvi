package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
)

// Handler agrupa os handlers de auth e sua dependência (queries do sqlc).
// Em vez de variáveis globais, injetamos as dependências via struct — mais testável.
type Handler struct {
	q *sqlc.Queries
}

// NewHandler cria um Handler com as queries injetadas.
func NewHandler(q *sqlc.Queries) *Handler {
	return &Handler{q: q}
}

// Tipos de evento do log de login (login_events.event).
const (
	EventLoginSuccess = "login_success"
	EventLoginFailure = "login_failure"
	EventLogout       = "logout"
)

// ShowLogin exibe o formulário de login (GET /login).
// Se já houver cookie de sessão, manda para a raiz; o RequireAuth decide se
// a sessão ainda vale (se não valer, apaga o cookie e devolve para cá).
func (h *Handler) ShowLogin(c *gin.Context) {
	if _, _, ok := readSession(c.Request); ok {
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
// Toda tentativa, certa ou errada, vai para o log de login.
func (h *Handler) HandleLogin(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")

	user, reason, err := Authenticate(c.Request.Context(), h.q, username, password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			var uid *int64
			if reason != reasonUnknownUser {
				// Usuário existe (senha errada ou desativado): guarda o id também.
				if u, e := h.q.GetUserByUsername(c.Request.Context(), username); e == nil {
					uid = &u.ID
				}
			}
			h.logEvent(c, EventLoginFailure, uid, username, reason)
			slog.Info("[auth] login recusado", "username", username, "motivo", reason, "ip", c.ClientIP())
			// Credenciais erradas → volta para o formulário com mensagem genérica.
			// Usamos redirect para evitar o problema de "reenvio de formulário"
			// ao pressionar F5 — o navegador não resubmete um GET.
			c.Redirect(http.StatusFound, "/login?error=credenciais+inválidas")
			return
		}
		// Erro inesperado do banco — não expõe detalhes ao usuário.
		slog.Error("[auth] erro no login", "username", username, "error", err)
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{
			"error": "erro interno, tente novamente",
		})
		return
	}

	if err := saveSession(c.Writer, c.Request, user.ID, user.SessionVersion); err != nil {
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{
			"error": "erro ao criar sessão, tente novamente",
		})
		return
	}

	h.logEvent(c, EventLoginSuccess, &user.ID, user.Username, "")
	slog.Info("[auth] login ok", "username", user.Username, "role", user.Role, "ip", c.ClientIP())

	// Senha provisória → direto para a troca obrigatória.
	if user.MustChangePassword {
		c.Redirect(http.StatusFound, PathChangePassword)
		return
	}
	c.Redirect(http.StatusFound, "/")
}

// HandleLogout destrói a sessão e redireciona para o login (POST /logout).
func (h *Handler) HandleLogout(c *gin.Context) {
	if userID, _, ok := readSession(c.Request); ok {
		username := ""
		if u, err := h.q.GetUserByID(c.Request.Context(), userID); err == nil {
			username = u.Username
		}
		if username != "" {
			h.logEvent(c, EventLogout, &userID, username, "")
		}
	}
	_ = clearSession(c.Writer, c.Request)
	c.Redirect(http.StatusFound, "/login")
}

// ── Minha senha ──────────────────────────────────────────────────────────────
// Cada usuário troca SOMENTE a própria senha: a rota não recebe id; o alvo é
// sempre o usuário da sessão. Senha de outra pessoa só o admin, e só por reset
// (internal/user).

// ShowChangePassword — GET /conta/senha
func (h *Handler) ShowChangePassword(c *gin.Context) {
	user := GetUser(c)
	c.HTML(http.StatusOK, "account_password.html", gin.H{
		"User":   user,
		"Forced": user.MustChangePassword,
		"Error":  c.Query("error"),
		"Flash":  c.Query("flash"),
	})
}

// ChangePassword — POST /conta/senha
func (h *Handler) ChangePassword(c *gin.Context) {
	user := GetUser(c)
	ctx := c.Request.Context()
	fail := func(msg string) {
		c.Redirect(http.StatusFound, PathChangePassword+"?error="+url.QueryEscape(msg))
	}

	current := c.PostForm("current_password")
	newPw := c.PostForm("new_password")
	confirm := c.PostForm("confirm_password")

	u, err := h.q.GetUserByID(ctx, user.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.Redirect(http.StatusFound, "/login")
			return
		}
		fail("erro interno, tente novamente")
		return
	}

	// Exige a senha atual, inclusive na troca obrigatória (é a provisória que
	// o admin passou). Protege contra alguém usando um navegador deixado logado.
	if !checkPassword(u.PasswordHash, current) {
		fail("senha atual incorreta")
		return
	}
	if newPw != confirm {
		fail("a confirmação não confere com a nova senha")
		return
	}
	if newPw == current {
		fail("a nova senha precisa ser diferente da atual")
		return
	}
	hash, err := HashPassword(newPw)
	if err != nil {
		fail(err.Error())
		return
	}

	newVersion, err := h.q.ChangeOwnPassword(ctx, sqlc.ChangeOwnPasswordParams{ID: u.ID, PasswordHash: hash})
	if err != nil {
		slog.Error("[auth] erro ao trocar a própria senha", "username", u.Username, "error", err)
		fail("erro interno, tente novamente")
		return
	}
	// A versão subiu (derruba as outras sessões dele); regrava o cookie desta
	// sessão com a versão nova para ela continuar valendo.
	if err := saveSession(c.Writer, c.Request, u.ID, newVersion); err != nil {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	slog.Info("[auth] senha trocada pelo próprio usuário", "username", u.Username,
		"era_provisoria", u.MustChangePassword)

	if u.MustChangePassword {
		c.Redirect(http.StatusFound, "/?flash="+url.QueryEscape("Senha definida. Bem-vindo!"))
		return
	}
	c.Redirect(http.StatusFound, PathChangePassword+"?flash="+url.QueryEscape("Senha alterada."))
}

// ── Log de login ─────────────────────────────────────────────────────────────

// logEvent grava um evento em login_events. Falha ao gravar o log nunca impede
// o login/logout: só fica registrada no journal.
func (h *Handler) logEvent(c *gin.Context, event string, userID *int64, username, reason string) {
	err := h.q.InsertLoginEvent(c.Request.Context(), sqlc.InsertLoginEventParams{
		Event:     event,
		UserID:    userID,
		Username:  truncate(username, 100),
		Ip:        truncate(c.ClientIP(), 64),
		UserAgent: truncate(c.Request.UserAgent(), 512),
		Reason:    reason,
	})
	if err != nil {
		slog.Error("[auth] erro ao gravar log de login", "event", event, "username", username, "error", err)
	}
}

// truncate corta s em no máximo max caracteres, sem partir um caractere UTF-8
// ao meio (VARCHAR(n) do Postgres conta caracteres, não bytes).
func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max])
}
