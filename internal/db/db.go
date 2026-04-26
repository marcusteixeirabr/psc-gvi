// Package db gerencia o pool de conexões com o PostgreSQL.
//
// Por que um pacote separado para o banco?
// Separar a "como conectar ao banco" da lógica de negócio mantém o código
// organizado e facilita testes — em testes podemos substituir o pool real
// por um pool de teste sem mudar nada na lógica de negócio.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// New cria e valida um pool de conexões com o PostgreSQL.
//
// pgxpool.New cria um POOL — um grupo de conexões abertas e reutilizáveis.
// Isso é muito mais eficiente do que abrir uma conexão nova a cada requisição.
//
// context.Background() é um contexto sem timeout — adequado para inicialização.
// Em operações dentro de handlers HTTP usaremos o contexto da requisição (com timeout).
//
// Retorna erro se não conseguir conectar — o main.go deve tratar isso com fail fast.
func New(dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar pool de conexões: %w", err)
	}

	// Ping valida que a conexão funciona de verdade.
	// pgxpool.New por si só não abre conexão — apenas configura o pool.
	// O Ping é necessário para garantir que o banco está acessível na inicialização.
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("banco de dados inacessível: %w", err)
	}

	return pool, nil
}
