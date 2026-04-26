package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ContextKeyUser é a chave usada para armazenar o usuário no contexto do Gin.
// Usar uma constante tipada evita colisão com outras chaves no contexto.
const ContextKeyUser = "auth_user"

// RequireAuth é um middleware Gin que exige usuário autenticado.
// Se não houver sessão válida, redireciona para /login.
//
// Middlewares no Gin são funções gin.HandlerFunc que chamam c.Next() para
// passar o controle para o próximo handler, ou c.Abort() para interromper.
func RequireAuth(c *gin.Context) {
	user := getSessionUser(c.Request)
	if user == nil {
		c.Redirect(http.StatusFound, "/login")
		// c.Abort() impede que os handlers seguintes da cadeia sejam executados.
		c.Abort()
		return
	}
	// Coloca o usuário no contexto para que os handlers seguintes possam acessá-lo
	// sem precisar ler o cookie de novo.
	c.Set(ContextKeyUser, user)
	c.Next()
}

// RequireRole retorna um middleware que exige que o usuário tenha um dos roles informados.
// Deve ser usado APÓS RequireAuth, pois depende do usuário já estar no contexto.
//
// Exemplo de uso:
//
//	admin := router.Group("/admin")
//	admin.Use(auth.RequireAuth, auth.RequireRole("admin"))
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
