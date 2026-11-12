package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ContextKeyUser é a chave usada para armazenar o usuário no contexto do Gin.
// Usar uma constante tipada evita colisão com outras chaves no contexto.
const ContextKeyUser = "auth_user"

// PathChangePassword é a tela "Minha senha". Enquanto o usuário estiver com
// senha provisória (must_change_password), é a única página liberada.
const PathChangePassword = "/conta/senha"

// RequireAuth é um middleware Gin que exige usuário autenticado.
//
// A cada request:
//  1. lê do cookie o id do usuário e a versão da sessão;
//  2. relê o usuário no banco (1 SELECT por chave primária, custo desprezível);
//  3. recusa a sessão se o usuário foi excluído, desativado, ou se a versão do
//     cookie não bate com a do banco (senha trocada/resetada depois do login);
//  4. com senha provisória, só deixa abrir a tela de troca de senha.
//
// Middlewares no Gin são funções gin.HandlerFunc que chamam c.Next() para
// passar o controle para o próximo handler, ou c.Abort() para interromper.
func (h *Handler) RequireAuth(c *gin.Context) {
	userID, version, ok := readSession(c.Request)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		c.Abort()
		return
	}

	u, err := h.q.GetUserByID(c.Request.Context(), userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		// Falha do banco: não derruba a sessão (pode ser só um soluço).
		slog.Error("[auth] erro ao carregar usuário da sessão", "user_id", userID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if err != nil || !u.Active || u.SessionVersion != version {
		motivo := "versão de sessão antiga (senha trocada/resetada)"
		if err != nil {
			motivo = "usuário excluído"
		} else if !u.Active {
			motivo = "usuário desativado"
		}
		slog.Info("[auth] sessão recusada", "user_id", userID, "motivo", motivo)
		_ = clearSession(c.Writer, c.Request)
		c.Redirect(http.StatusFound, "/login")
		c.Abort()
		return
	}

	user := &UserSession{
		ID:                 u.ID,
		Username:           u.Username,
		DisplayName:        u.DisplayName,
		Role:               u.Role,
		MustChangePassword: u.MustChangePassword,
	}

	// Senha provisória (criada ou resetada pelo admin): troca obrigatória antes
	// de qualquer outra página. O logout fica fora deste grupo, então "Sair"
	// continua funcionando.
	if user.MustChangePassword && c.Request.URL.Path != PathChangePassword {
		c.Redirect(http.StatusFound, PathChangePassword)
		c.Abort()
		return
	}

	// Coloca o usuário no contexto para que os handlers seguintes possam acessá-lo.
	c.Set(ContextKeyUser, user)
	c.Next()
}

// RequireRole retorna um middleware que exige que o usuário tenha um dos roles informados.
// Deve ser usado APÓS RequireAuth, pois depende do usuário já estar no contexto.
//
// Exemplo de uso:
//
//	admin := router.Group("/admin")
//	admin.Use(authHandler.RequireAuth, auth.RequireRole("admin"))
func RequireRole(roles ...string) gin.HandlerFunc {
	// Convertemos a lista em um map para busca O(1) em vez de O(n).
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}

	return func(c *gin.Context) {
		user, exists := c.Get(ContextKeyUser)
		if !exists {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		userSession := user.(*UserSession)
		if !allowed[userSession.Role] {
			// 403 Forbidden: o usuário está autenticado, mas não tem permissão.
			// Diferente de 401 Unauthorized (não autenticado).
			slog.Info("[auth] acesso negado por papel", "username", userSession.Username,
				"role", userSession.Role, "path", c.Request.URL.Path)
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		c.Next()
	}
}

// GetUser recupera o usuário autenticado do contexto do Gin.
// Retorna nil se não houver usuário (rota pública ou middleware não configurado).
func GetUser(c *gin.Context) *UserSession {
	user, exists := c.Get(ContextKeyUser)
	if !exists {
		return nil
	}
	return user.(*UserSession)
}
