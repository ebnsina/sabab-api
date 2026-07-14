// Package logging builds the structured logger every Sabab service uses.
//
// Production emits JSON so the platform's own logs are machine-readable;
// development emits text for human eyes. Callers pass the logger explicitly
// rather than reaching for a package-level global.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// ctxKey is the private key under which a logger is carried on a context.
type ctxKey struct{}

// New builds a logger for the given level and environment. An unrecognised
// level falls back to info — logging must never be the reason a process
// refuses to boot (config.Load already rejects bad levels loudly).
func New(level, env string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var handler slog.Handler
	if env == "production" || env == "staging" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}

// Service returns a logger tagged with the service name, so a single log
// stream from docker compose stays attributable.
func Service(base *slog.Logger, name string) *slog.Logger {
	return base.With(slog.String("service", name))
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithContext stores logger on ctx so request-scoped fields (project, trace)
// travel with the request instead of being threaded through every signature.
func WithContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// FromContext returns the logger stored by WithContext, or the default logger
// when the context carries none. It never returns nil.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return slog.Default()
}
