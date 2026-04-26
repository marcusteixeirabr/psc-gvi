package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidCredentials é retornado quando usuário ou senha estão errados.
// Usamos um erro sentinela para que o handler possa distinguir "credenciais
// erradas" de "erro interno" sem fazer string matching.
var ErrInvalidCredentials = errors.New("usuário ou senha inválidos")

// Authenticate valida as credenciais e retorna a sessão do usuário.
//
// Por que buscamos pelo username e depois comparamos a senha em Go?
// O bcrypt não pode ser comparado com SQL — o hash precisa ser
// processado pela função bcrypt.CompareHashAndPassword em Go.
// Buscar pelo username e comparar em Go é a abordagem correta.
func Authenticate(ctx context.Context, pool *pgxpool.Pool, username, password string) (*UserSession, error) {
	var user struct {
		id           int64
		username     string
		displayName  string
		passwordHash string
		role         string
	}

	// $1 é o placeholder do pgx para evitar SQL injection.
	// Nunca concatene strings em queries SQL — use placeholders.
	err := pool.QueryRow(ctx,
		`SELECT id, username, display_name, password_hash, role
		 FROM users
		 WHERE username = $1 AND active = true`,
		username,
	).Scan(&user.id, &user.username, &user.displayName, &user.passwordHash, &user.role)

	if err != nil {
		// pgx.ErrNoRows significa "nenhuma linha encontrada" — usuário não existe.
		// Retornamos ErrInvalidCredentials (não "usuário não encontrado") para não
		// revelar ao atacante se o usuário existe ou não.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("erro ao consultar usuário: %w", err)
	}

	// bcrypt.CompareHashAndPassword compara a senha fornecida com o hash armazenado.
	// Retorna nil se a senha for correta, erro caso contrário.
	// Esta operação é intencionalmente lenta (~100ms) para dificultar ataques de força bruta.
	if err := bcrypt.CompareHashAndPassword([]byte(user.passwordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	return &UserSession{
		ID:          user.id,
		Username:    user.username,
		DisplayName: user.displayName,
		Role:        user.role,
	}, nil
}
