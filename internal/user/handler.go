// Pacote user: área administrativa de usuários (só admin).
//
// O que o admin pode fazer com OUTROS usuários:
//   - criar (com senha provisória → troca obrigatória no 1º login);
//   - alterar o nome exibido;
//   - alterar o papel (admin / editor / reader);
//   - resetar a senha (senha provisória → troca obrigatória no próximo login);
//   - excluir (DELETE real; o log de login do usuário é preservado);
//   - consultar o log de login.
//
// Travas de segurança (o porquê de cada uma está no handler):
//   - o sistema nunca fica sem admin ativo;
//   - o admin não exclui nem rebaixa a si mesmo;
//   - o admin não reseta a própria senha por aqui: a própria senha cada um
//     troca em /conta/senha (internal/auth), sabendo a senha atual.
//
// Quem garante que só admin chega aqui é o main.go (grupo com
// auth.RequireRole("admin")). Este pacote confia nisso, como um @Controller
// atrás de um @PreAuthorize("hasRole('ADMIN')") no Spring.
package user

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
	"github.com/marcusteixeirabr/psc-gvi/internal/pagination"
)

// Papéis válidos, na ordem em que aparecem no <select>.
// Precisa bater com o CHECK da coluna users.role (migration 001).
var roles = []RoleOption{
	{Value: "admin", Label: "Administrador"},
	{Value: "editor", Label: "Editor"},
	{Value: "reader", Label: "Leitor"},
}

// RoleOption é um item do <select> de papel.
type RoleOption struct {
	Value string
	Label string
}

// usernameRe: login em minúsculas, sem espaço nem acento (3 a 50 caracteres).
var usernameRe = regexp.MustCompile(`^[a-z0-9._-]{3,50}$`)

// UserRow é a view struct usada no template de listagem/edição.
type UserRow struct {
	ID                 int64
	Username           string
	DisplayName        string
	Role               string
	RoleLabel          string
	Active             bool
	MustChangePassword bool
	CreatedAt          string
	IsSelf             bool // é o admin logado (esconde ações perigosas)
}

// LoginEventRow é uma linha do log de login já formatada para a tela.
type LoginEventRow struct {
	When       string
	Event      string
	EventLabel string
	Username   string
	Deleted    bool // usuário já não existe (excluído ou nunca existiu)
	IP         string
	UserAgent  string
	Reason     string
}

// Handler agrupa as dependências do gerenciamento de usuários.
type Handler struct {
	q *sqlc.Queries
}

// NewHandler cria o handler com as queries do sqlc.
func NewHandler(q *sqlc.Queries) *Handler {
	return &Handler{q: q}
}

// ── Listagem ─────────────────────────────────────────────────────────────────

// List — GET /admin/users
func (h *Handler) List(c *gin.Context) {
	me := auth.GetUser(c)
	users, err := h.q.ListUsers(c.Request.Context())
	if err != nil {
		c.HTML(http.StatusInternalServerError, "admin_users.html", gin.H{
			"User":  me,
			"Error": "Erro ao carregar usuários: " + err.Error(),
			"Users": []UserRow{},
			"Roles": roles,
		})
		return
	}

	rows := make([]UserRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, toRow(u, me.ID))
	}

	c.HTML(http.StatusOK, "admin_users.html", gin.H{
		"User":       me,
		"Users":      rows,
		"Roles":      roles,
		"Flash":      c.Query("flash"),
		"Error":      c.Query("error"),
		"MinPassLen": auth.MinPasswordLen,
	})
}

// ── Criar ────────────────────────────────────────────────────────────────────

// Create — POST /admin/users
func (h *Handler) Create(c *gin.Context) {
	me := auth.GetUser(c)
	username := strings.ToLower(strings.TrimSpace(c.PostForm("username")))
	displayName := strings.TrimSpace(c.PostForm("display_name"))
	role := c.PostForm("role")
	password := c.PostForm("password")

	if !usernameRe.MatchString(username) {
		backToList(c, "", "Login inválido: use de 3 a 50 caracteres entre a-z, 0-9, ponto, hífen ou sublinhado.")
		return
	}
	if msg := validateDisplayName(displayName); msg != "" {
		backToList(c, "", msg)
		return
	}
	if !validRole(role) {
		backToList(c, "", "Papel inválido.")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		backToList(c, "", "Senha provisória: "+err.Error()+".")
		return
	}

	u, err := h.q.CreateUser(c.Request.Context(), sqlc.CreateUserParams{
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: hash,
		Role:         role,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			backToList(c, "", "Já existe um usuário com o login \""+username+"\".")
			return
		}
		slog.Error("[user] erro ao criar usuário", "username", username, "error", err)
		backToList(c, "", "Erro ao criar usuário.")
		return
	}

	slog.Info("[user] usuário criado", "admin", me.Username, "username", u.Username, "role", u.Role)
	backToList(c, "Usuário \""+u.Username+"\" criado. Passe a senha provisória a ele: a troca é obrigatória no primeiro login.", "")
}

// ── Editar ───────────────────────────────────────────────────────────────────

// EditForm — GET /admin/users/:id
// Uma página por usuário, com um formulário para cada ação (nome, papel,
// reset de senha, exclusão). Ações separadas = cada POST faz uma coisa só,
// com sua própria validação e mensagem.
func (h *Handler) EditForm(c *gin.Context) {
	me := auth.GetUser(c)
	u, ok := h.loadTarget(c)
	if !ok {
		return
	}
	c.HTML(http.StatusOK, "admin_user_edit.html", gin.H{
		"User":       me,
		"Target":     toRow(u, me.ID),
		"Roles":      roles,
		"Flash":      c.Query("flash"),
		"Error":      c.Query("error"),
		"MinPassLen": auth.MinPasswordLen,
	})
}

// UpdateName — POST /admin/users/:id/name
func (h *Handler) UpdateName(c *gin.Context) {
	me := auth.GetUser(c)
	u, ok := h.loadTarget(c)
	if !ok {
		return
	}
	name := strings.TrimSpace(c.PostForm("display_name"))
	if msg := validateDisplayName(name); msg != "" {
		backToEdit(c, u.ID, "", msg)
		return
	}
	if err := h.q.UpdateUserDisplayName(c.Request.Context(), sqlc.UpdateUserDisplayNameParams{ID: u.ID, DisplayName: name}); err != nil {
		slog.Error("[user] erro ao alterar nome", "username", u.Username, "error", err)
		backToEdit(c, u.ID, "", "Erro ao alterar o nome.")
		return
	}
	slog.Info("[user] nome alterado", "admin", me.Username, "username", u.Username,
		"de", u.DisplayName, "para", name)
	backToEdit(c, u.ID, "Nome alterado.", "")
}

// UpdateRole — POST /admin/users/:id/role
func (h *Handler) UpdateRole(c *gin.Context) {
	me := auth.GetUser(c)
	u, ok := h.loadTarget(c)
	if !ok {
		return
	}
	role := c.PostForm("role")
	if !validRole(role) {
		backToEdit(c, u.ID, "", "Papel inválido.")
		return
	}
	if role == u.Role {
		backToEdit(c, u.ID, "", "O usuário já tem esse papel.")
		return
	}
	// Rebaixar a si mesmo tiraria o acesso a esta tela no mesmo clique.
	// Se for preciso, outro admin faz.
	if u.ID == me.ID {
		backToEdit(c, u.ID, "", "Você não pode alterar o seu próprio papel. Peça a outro administrador.")
		return
	}
	if u.Role == "admin" && u.Active {
		if n, err := h.q.CountActiveAdmins(c.Request.Context()); err != nil || n <= 1 {
			backToEdit(c, u.ID, "", "Este é o único administrador ativo: o sistema não pode ficar sem admin.")
			return
		}
	}
	if err := h.q.UpdateUserRole(c.Request.Context(), sqlc.UpdateUserRoleParams{ID: u.ID, Role: role}); err != nil {
		slog.Error("[user] erro ao alterar papel", "username", u.Username, "error", err)
		backToEdit(c, u.ID, "", "Erro ao alterar o papel.")
		return
	}
	// Não precisa derrubar a sessão: o RequireAuth relê o papel do banco a
	// cada request, então a mudança vale já no próximo clique do usuário.
	slog.Info("[user] papel alterado", "admin", me.Username, "username", u.Username,
		"de", u.Role, "para", role)
	backToEdit(c, u.ID, "Papel alterado para "+roleLabel(role)+". Vale a partir do próximo clique do usuário.", "")
}

// ResetPassword — POST /admin/users/:id/password
func (h *Handler) ResetPassword(c *gin.Context) {
	me := auth.GetUser(c)
	u, ok := h.loadTarget(c)
	if !ok {
		return
	}
	// A própria senha o admin troca em /conta/senha, como qualquer usuário
	// (requisito: cada um altera só a sua, sabendo a atual).
	if u.ID == me.ID {
		backToEdit(c, u.ID, "", "Para a sua própria senha use \"Minha senha\".")
		return
	}
	password := c.PostForm("password")
	if password != c.PostForm("confirm_password") {
		backToEdit(c, u.ID, "", "A confirmação não confere com a senha provisória.")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		backToEdit(c, u.ID, "", "Senha provisória: "+err.Error()+".")
		return
	}
	if err := h.q.ResetUserPassword(c.Request.Context(), sqlc.ResetUserPasswordParams{ID: u.ID, PasswordHash: hash}); err != nil {
		slog.Error("[user] erro ao resetar senha", "username", u.Username, "error", err)
		backToEdit(c, u.ID, "", "Erro ao resetar a senha.")
		return
	}
	slog.Info("[user] senha resetada pelo admin (sessões do usuário derrubadas)",
		"admin", me.Username, "username", u.Username)
	backToEdit(c, u.ID, "Senha resetada. As sessões abertas de \""+u.Username+
		"\" foram encerradas; no próximo login ele será obrigado a trocar a senha provisória.", "")
}

// Delete — POST /admin/users/:id/delete
func (h *Handler) Delete(c *gin.Context) {
	me := auth.GetUser(c)
	u, ok := h.loadTarget(c)
	if !ok {
		return
	}
	if u.ID == me.ID {
		backToEdit(c, u.ID, "", "Você não pode excluir a sua própria conta.")
		return
	}
	// Confirmação digitada: o admin precisa redigitar o login. É uma exclusão
	// real, sem desfazer.
	if strings.TrimSpace(c.PostForm("confirm_username")) != u.Username {
		backToEdit(c, u.ID, "", "Para excluir, digite exatamente o login \""+u.Username+"\" na confirmação.")
		return
	}
	if u.Role == "admin" && u.Active {
		if n, err := h.q.CountActiveAdmins(c.Request.Context()); err != nil || n <= 1 {
			backToEdit(c, u.ID, "", "Este é o único administrador ativo: o sistema não pode ficar sem admin.")
			return
		}
	}
	if _, err := h.q.DeleteUser(c.Request.Context(), u.ID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
			backToEdit(c, u.ID, "", "Não foi possível excluir: há registros ligados a este usuário.")
			return
		}
		slog.Error("[user] erro ao excluir usuário", "username", u.Username, "error", err)
		backToEdit(c, u.ID, "", "Erro ao excluir o usuário.")
		return
	}
	// A sessão aberta dele cai sozinha: o RequireAuth não acha mais o id.
	slog.Info("[user] usuário excluído (log de login preservado)", "admin", me.Username,
		"username", u.Username, "role", u.Role)
	backToList(c, "Usuário \""+u.Username+"\" excluído. O histórico de login dele continua no log.", "")
}

// ── Log de login ─────────────────────────────────────────────────────────────

// eventLabels traduz login_events.event para a tela.
var eventLabels = map[string]string{
	auth.EventLoginSuccess: "Login",
	auth.EventLoginFailure: "Falha",
	auth.EventLogout:       "Logout",
}

const loginPageSize = 50

// Logins — GET /admin/logins?username=&event=&page=
func (h *Handler) Logins(c *gin.Context) {
	ctx := c.Request.Context()
	username := c.Query("username")
	event := c.Query("event")
	if _, ok := eventLabels[event]; !ok {
		event = ""
	}

	total, err := h.q.CountLoginEvents(ctx, sqlc.CountLoginEventsParams{Username: username, Event: event})
	if err != nil {
		slog.Error("[user] erro ao contar log de login", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	page := pagination.FromQuery(c, int(total), loginPageSize)

	events, err := h.q.ListLoginEvents(ctx, sqlc.ListLoginEventsParams{
		Username:   username,
		Event:      event,
		PageLimit:  int32(page.PageSize),
		PageOffset: int32(page.Offset),
	})
	if err != nil {
		slog.Error("[user] erro ao listar log de login", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	usernames, _ := h.q.ListLoginEventUsernames(ctx)

	// Quem ainda existe (para marcar "excluído/inexistente" no log).
	existing := map[string]bool{}
	if users, err := h.q.ListUsers(ctx); err == nil {
		for _, u := range users {
			existing[u.Username] = true
		}
	}

	rows := make([]LoginEventRow, 0, len(events))
	for _, e := range events {
		rows = append(rows, LoginEventRow{
			When:       e.OccurredAt.Time.In(time.Local).Format("02/01/2006 15:04:05"),
			Event:      e.Event,
			EventLabel: eventLabels[e.Event],
			Username:   e.Username,
			Deleted:    !existing[e.Username],
			IP:         e.Ip,
			UserAgent:  e.UserAgent,
			Reason:     e.Reason,
		})
	}

	c.HTML(http.StatusOK, "admin_logins.html", gin.H{
		"User":      auth.GetUser(c),
		"Events":    rows,
		"Page":      page,
		"Usernames": usernames,
		"FUsername": username,
		"FEvent":    event,
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// loadTarget lê o :id da URL e carrega o usuário alvo. Responde 404 se não existir.
func (h *Handler) loadTarget(c *gin.Context) (sqlc.User, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return sqlc.User{}, false
	}
	u, err := h.q.GetUserByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			backToList(c, "", "Usuário não encontrado (talvez já excluído).")
			return sqlc.User{}, false
		}
		slog.Error("[user] erro ao carregar usuário", "id", id, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return sqlc.User{}, false
	}
	return u, true
}

func toRow(u sqlc.User, meID int64) UserRow {
	created := ""
	if u.CreatedAt.Valid {
		created = u.CreatedAt.Time.In(time.Local).Format("02/01/2006")
	}
	return UserRow{
		ID:                 u.ID,
		Username:           u.Username,
		DisplayName:        u.DisplayName,
		Role:               u.Role,
		RoleLabel:          roleLabel(u.Role),
		Active:             u.Active,
		MustChangePassword: u.MustChangePassword,
		CreatedAt:          created,
		IsSelf:             u.ID == meID,
	}
}

func validateDisplayName(name string) string {
	n := utf8.RuneCountInString(name)
	if n == 0 {
		return "O nome não pode ficar vazio."
	}
	if n > 100 {
		return "O nome pode ter no máximo 100 caracteres."
	}
	return ""
}

func validRole(r string) bool {
	for _, o := range roles {
		if o.Value == r {
			return true
		}
	}
	return false
}

func roleLabel(r string) string {
	for _, o := range roles {
		if o.Value == r {
			return o.Label
		}
	}
	return r
}

// backToList e backToEdit: padrão Post/Redirect/Get, como no resto do
// sistema. A mensagem vai na query string e o F5 não reenvia o formulário.
func backToList(c *gin.Context, flash, errMsg string) {
	c.Redirect(http.StatusFound, "/admin/users"+msgQuery(flash, errMsg))
}

func backToEdit(c *gin.Context, id int64, flash, errMsg string) {
	c.Redirect(http.StatusFound, "/admin/users/"+strconv.FormatInt(id, 10)+msgQuery(flash, errMsg))
}

func msgQuery(flash, errMsg string) string {
	v := url.Values{}
	if flash != "" {
		v.Set("flash", flash)
	}
	if errMsg != "" {
		v.Set("error", errMsg)
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}
