// Package auth implementa autenticação por sessão e controle de acesso por role.
package auth

import (
	"net/http"

	"github.com/gorilla/sessions"
)

// sessionName é o nome do cookie no navegador.
// Um nome específico evita conflito com cookies de outros sistemas.
const sessionName = "psc-gvi-session"

// Chaves de sessão — constantes evitam erros de digitação ao acessar valores.
const (
	sessionKeyUserID      = "user_id"
	sessionKeyUsername    = "username"
	sessionKeyDisplayName = "display_name"
	sessionKeyRole        = "role"
)

// store é o repositório de sessões. É privado — acessado apenas pelas funções abaixo.
// Gorilla usa uma variável de pacote aqui porque o store é configurado uma vez
// na inicialização e reutilizado em todas as requisições.
var store *sessions.CookieStore

// InitStore configura o store de sessões com a chave secreta.
// Deve ser chamado uma única vez no main.go antes de registrar as rotas.
//
// CookieStore armazena a sessão no próprio cookie do navegador.
// A sessão é assinada (não pode ser forjada) e criptografada (não pode ser lida).
// Isso elimina a necessidade de uma tabela `sessions` no banco de dados.
func InitStore(secret string) {
	store = sessions.NewCookieStore([]byte(secret))
	store.Options = &sessions.Options{
		Path:   "/",
		MaxAge: 86400 * 8, // 8 dias em segundos
		// HttpOnly impede que JavaScript acesse o cookie — protege contra XSS.
		HttpOnly: true,
		// SameSiteLaxMode protege contra CSRF na maioria dos casos.
		SameSite: http.SameSiteLaxMode,
	}
}

// UserSession contém os dados do usuário autenticado que ficam na sessão.
// É a "identidade" carregada do cookie em cada requisição.
type UserSession struct {
	ID          int64
	Username    string
	DisplayName string
	Role        string
}

// saveSession persiste os dados do usuário no cookie de sessão.
func saveSession(w http.ResponseWriter, r *http.Request, user UserSession) error {
	session, err := store.Get(r, sessionName)
	if err != nil {
		return err
	}
	session.Values[sessionKeyUserID] = user.ID
	session.Values[sessionKeyUsername] = user.Username
	session.Values[sessionKeyDisplayName] = user.DisplayName
	session.Values[sessionKeyRole] = user.Role
	return session.Save(r, w)
}

// clearSession destrói a sessão atual definindo MaxAge = -1.
// O navegador remove o cookie imediatamente ao receber a resposta.
func clearSession(w http.ResponseWriter, r *http.Request) error {
	session, err := store.Get(r, sessionName)
	if err != nil {
		return err
	}
	session.Options.MaxAge = -1
	return session.Save(r, w)
}

// getSessionUser lê os dados do usuário do cookie de sessão.
// Retorna nil se não houver sessão válida (usuário não autenticado).
func getSessionUser(r *http.Request) *UserSession {
	session, err := store.Get(r, sessionName)
	if err != nil || session.IsNew {
		return nil
	}

	id, ok := session.Values[sessionKeyUserID].(int64)
	if !ok {
		return nil
	}

	return &UserSession{
		ID:          id,
		Username:    session.Values[sessionKeyUsername].(string),
		DisplayName: session.Values[sessionKeyDisplayName].(string),
		Role:        session.Values[sessionKeyRole].(string),
	}
}
