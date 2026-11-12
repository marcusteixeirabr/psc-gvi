package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
)

// ErrInvalidCredentials é retornado quando usuário ou senha estão errados.
// Usamos um erro sentinela para que o handler possa distinguir "credenciais
// erradas" de "erro interno" sem fazer string matching.
var ErrInvalidCredentials = errors.New("usuário ou senha inválidos")

// Motivos de falha gravados em login_events.reason. Só o admin vê no log;
// a tela de login mostra sempre a mesma mensagem genérica, para não revelar
// a um estranho se o login existe.
const (
	reasonWrongPassword = "senha incorreta"
	reasonUnknownUser   = "usuário inexistente"
	reasonInactiveUser  = "usuário desativado"
)

// dummyHash é um hash bcrypt qualquer (custo 10, o mesmo do seed 005).
// Quando o usuário não existe, comparamos a senha com ele mesmo assim, para a
// resposta demorar o mesmo tempo (~100ms) nos dois casos. Sem isso, um
// atacante descobriria quais logins existem só medindo o tempo de resposta.
const dummyHash = "$2a$10$Rdw7FdqlZ3hlqwFsTkyde.c8AnrrWC633oUWcVbGPPHXZzo.3yiFq"

// Authenticate valida as credenciais.
//
// Sucesso: devolve o usuário. Falha de credencial: ErrInvalidCredentials e o
// motivo (para o log de login). Qualquer outro erro é falha do banco.
//
// Por que buscamos pelo username e depois comparamos a senha em Go?
// O bcrypt não pode ser comparado com SQL — o hash precisa ser
// processado pela função bcrypt.CompareHashAndPassword em Go.
func Authenticate(ctx context.Context, q *sqlc.Queries, username, password string) (*sqlc.User, string, error) {
	u, err := q.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			checkPassword(dummyHash, password) // só para igualar o tempo
			return nil, reasonUnknownUser, ErrInvalidCredentials
		}
		return nil, "", fmt.Errorf("erro ao consultar usuário: %w", err)
	}

	if !checkPassword(u.PasswordHash, password) {
		return nil, reasonWrongPassword, ErrInvalidCredentials
	}
	// Senha certa, mas conta desativada. Checado DEPOIS da senha para não
	// revelar a um estranho que a conta existe e está desativada.
	if !u.Active {
		return nil, reasonInactiveUser, ErrInvalidCredentials
	}
	return &u, "", nil
}
