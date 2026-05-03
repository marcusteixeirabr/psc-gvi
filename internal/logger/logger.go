package logger

import (
	"log/slog"
	"os"
)

// Init configura o logger global baseado no ambiente.
// Produção: JSON (para ingestão por ferramentas de log).
// Desenvolvimento: texto legível.
func Init(env string) {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}

	var handler slog.Handler
	if env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	slog.SetDefault(slog.New(handler))
}
