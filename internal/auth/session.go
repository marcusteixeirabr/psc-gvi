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
//
// O cookie guarda só QUEM é o usuário (id) e a VERSÃO da sessão.
// Nome, papel e "precisa trocar a senha" são relidos do banco a cada request
// (ver RequireAuth). Antes o papel ficava no cookie: se o admin rebaixasse
// alguém, a pessoa continuava com o papel antigo até o cookie expirar (8 dias).
// Analogia Spring: é como recarregar o UserDetails a cada request em vez de
// confiar nas authorities gravadas no token.
const (
	sessionKeyUserID  = "user_id"
	sessionKeyVersion = "session_version"
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
		// SameSiteLaxMode protege contra CSRF na maioria dos casos: o navegador
		// não envia o cookie em POST vindo de outro site. Todas as ações de
		// escrita (inclusive as do admin) são POST, então ficam cobertas.
		SameSite: http.SameSiteLaxMode,
	}
}

// UserSession contém os dados do usuário autenticado, montados pelo
// RequireAuth a partir do banco. É o que os handlers recebem via GetUser(c).
type UserSession struct {
	ID                 int64
	Username           string
	DisplayName        string
	Role               string
	MustChangePassword bool
}

// saveSession grava no cookie o id do usuário e a versão atual da sessão.
func saveSession(w http.ResponseWriter, r *http.Request, userID int64, version int32) error {
	session, err := store.Get(r, sessionName)
	if err != nil {
		// Cookie antigo/ilegível (ex.: SESSION_SECRET trocado): começa um novo.
		// store.Get já devolve uma sessão nova nesse caso; só ignoramos o erro.
		session, _ = store.New(r, sessionName)
	}
	// Limpa chaves do formato antigo (username, role...) que ainda existam.
	for k := range session.Values {
		delete(session.Values, k)
	}
	session.Values[sessionKeyUserID] = userID
	session.Values[sessionKeyVersion] = version
	return session.Save(r, w)
}

// clearSession destrói a sessão atual definindo MaxAge = -1.
// O navegador remove o cookie imediatamente ao receber a resposta.
func clearSession(w http.ResponseWriter, r *http.Request) error {
	session, err := store.Get(r, sessionName)
	if err != nil {
		session, _ = store.New(r, sessionName)
	}
	session.Options.MaxAge = -1
	return session.Save(r, w)
}

// readSession lê o id do usuário e a versão da sessão do cookie.
// ok=false se não houver sessão válida.
//
// Cookies do formato antigo (sem session_version) são recusados de propósito:
// depois do deploy todo mundo entra de novo uma vez, já no formato novo.
func readSession(r *http.Request) (userID int64, version int32, ok bool) {
	session, err := store.Get(r, sessionName)
	if err != nil || session.IsNew {
		return 0, 0, false
	}
	userID, okID := session.Values[sessionKeyUserID].(int64)
	version, okVer := session.Values[sessionKeyVersion].(int32)
	if !okID || !okVer {
		return 0, 0, false
	}
	return userID, version, true
}
