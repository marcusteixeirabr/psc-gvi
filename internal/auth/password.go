package auth

import (
	"errors"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// Regras de senha, usadas em três lugares: criação de usuário e reset (admin)
// e troca da própria senha (qualquer usuário).
const (
	MinPasswordLen = 8
	// bcrypt só considera os primeiros 72 BYTES; o resto seria ignorado em
	// silêncio. Melhor recusar do que aceitar uma senha que não vale inteira.
	maxPasswordBytes = 72
)

var (
	ErrPasswordTooShort = errors.New("a senha precisa ter pelo menos 8 caracteres")
	ErrPasswordTooLong  = errors.New("a senha pode ter no máximo 72 bytes")
)

// ValidatePassword confere o tamanho mínimo e máximo da senha.
func ValidatePassword(pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLen {
		return ErrPasswordTooShort
	}
	if len(pw) > maxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}

// HashPassword valida e gera o hash bcrypt da senha.
// O salt fica dentro do próprio hash, então basta gravar o resultado no banco.
func HashPassword(pw string) (string, error) {
	if err := ValidatePassword(pw); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// checkPassword compara a senha digitada com o hash gravado.
func checkPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}
