// Package config lê variáveis de ambiente e as expõe como uma struct tipada.
//
// Por que usar uma struct em vez de chamar os.Getenv() diretamente?
// Se você chamar os.Getenv("DB_HOST") em 10 lugares diferentes e o nome
// da variável mudar, você precisa corrigir 10 pontos. Com uma struct,
// corrige em um único lugar — aqui.
package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// Config agrupa todas as configurações da aplicação.
// Os campos são exportados (letra maiúscula) para que outros pacotes possam lê-los.
type Config struct {
	// Configurações do servidor HTTP
	Port string // porta onde o Gin vai escutar (ex: "8080")
	Env  string // ambiente: "development" ou "production"

	// Configurações do banco de dados
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string

	// Chave para assinar e criptografar o cookie de sessão.
	// Deve ter no mínimo 32 bytes. Use um valor aleatório longo em produção.
	SessionSecret string

	// URL da página de Manobras Previstas do ZP-21.
	// Configurável porque pode mudar sem aviso prévio.
	ZP21URL string

	// URL base do VesselFinder — usado para buscar IMO por nome de navio.
	VesselFinderURL string

	// CIALA — Acordo de Viña del Mar (histórico PSC + grau de risco).
	CIALAURL      string
	CIALAUsername string
	CIALAPassword string

	// SMTP — alertas por e-mail (todos opcionais; se vazios, alertas desativados).
	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPassword string
	AlertEmail   string
}

// DSN retorna a string de conexão no formato que o pgx espera.
// DSN = Data Source Name — é o "endereço completo" do banco de dados.
//
// Formato: "host=X port=X dbname=X user=X password=X sslmode=X options=X"
// Em produção (GCP), usaremos sslmode=require. Em dev, disable.
//
// options='-c timezone=America/Sao_Paulo' força o timezone da SESSÃO do
// Postgres a bater com o timezone de negócio já fixado no Go (time.Local
// em cmd/server/main.go). Sem isso, ::date/EXTRACT/CURRENT_DATE nas queries
// rodam no timezone padrão do servidor (UTC) enquanto a exibição usa SP —
// uma atracação registrada entre 21h-23h59 (horário de SP) já virou o dia
// seguinte em UTC, o que empurra a escala para o mês errado no relatório.
func (c Config) DSN() string {
	sslmode := "disable"
	if c.Env == "production" {
		sslmode = "require"
	}
	return fmt.Sprintf(
		"host=%s port=%s dbname=%s user=%s password=%s sslmode=%s options='-c timezone=America/Sao_Paulo'",
		c.DBHost, c.DBPort, c.DBName, c.DBUser, c.DBPassword, sslmode,
	)
}

// Load tenta ler o arquivo .env (se existir) e então monta a struct Config
// a partir das variáveis de ambiente.
//
// godotenv.Load() não retorna erro se o arquivo .env não existir —
// em produção as variáveis já estão no ambiente, sem precisar de arquivo.
func Load() (Config, error) {
	// Tenta carregar .env — falha silenciosa se não existir (comportamento correto).
	_ = godotenv.Load()

	cfg := Config{
		Port:          getEnv("PORT", "8080"),
		Env:           getEnv("ENV", "development"),
		DBHost:        getEnv("DB_HOST", "localhost"),
		DBPort:        getEnv("DB_PORT", "5432"),
		DBName:        getEnv("DB_NAME", "pscgvi"),
		DBUser:        getEnv("DB_USER", "pscgvi"),
		DBPassword:    getEnv("DB_PASSWORD", ""),
		SessionSecret: getEnv("SESSION_SECRET", ""),
		ZP21URL:          getEnv("ZP21_URL", ""),
		VesselFinderURL:  getEnv("VESSEL_FINDER_URL", "https://www.vesselfinder.com"),
		CIALAURL:         getEnv("CIALA_URL", "https://ciala.acuerdolatinoamericano.org"),
		CIALAUsername:    getEnv("CIALA_USERNAME", ""),
		CIALAPassword:    getEnv("CIALA_PASSWORD", ""),
		SMTPHost:         getEnv("SMTP_HOST", ""),
		SMTPPort:         getEnv("SMTP_PORT", "587"),
		SMTPUser:         getEnv("SMTP_USER", ""),
		SMTPPassword:     getEnv("SMTP_PASSWORD", ""),
		AlertEmail:       getEnv("ALERT_EMAIL", ""),
	}

	if cfg.DBPassword == "" {
		return Config{}, fmt.Errorf("variável de ambiente DB_PASSWORD não definida")
	}
	if cfg.SessionSecret == "" {
		return Config{}, fmt.Errorf("variável de ambiente SESSION_SECRET não definida")
	}

	return cfg, nil
}

// getEnv lê uma variável de ambiente. Se não existir, retorna o valor padrão.
// É uma função auxiliar privada (letra minúscula = só usada neste pacote).
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
