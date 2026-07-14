// Command api serves the dashboard: issues, events, search, issue state.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ebnsina/sabab-api/internal/api"
	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/config"
	"github.com/ebnsina/sabab-api/internal/health"
	"github.com/ebnsina/sabab-api/internal/logging"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
	"github.com/ebnsina/sabab-api/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Default().Error("api failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logging.Service(logging.New(cfg.LogLevel, cfg.Env), "api")

	pg, err := postgres.Connect(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer pg.Close()

	ch, err := clickhouse.Connect(ctx, cfg.ClickHouse)
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()

	// Secure cookies in production only: localhost is not HTTPS, and a Secure
	// cookie there is simply never sent — which looks exactly like broken login.
	sessions := auth.NewSessions(pg, cfg.IsProduction())

	checker := health.New("api", version.Version)
	checker.Register("postgres", pg.Ping)
	checker.Register("clickhouse", ch.Ping)

	devOrigin := ""
	if !cfg.IsProduction() {
		devOrigin = "http://localhost:5173" // the SvelteKit dev server
	}

	a := api.New(pg, ch, sessions, checker, devOrigin, log)
	go a.SweepSessions(ctx, time.Hour)

	server := &http.Server{
		Addr:         cfg.API.Addr,
		Handler:      a.Handler(),
		ReadTimeout:  cfg.API.ReadTimeout,
		WriteTimeout: cfg.API.WriteTimeout,
		IdleTimeout:  cfg.API.IdleTimeout,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", slog.String("addr", server.Addr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.API.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("stopped")
	return nil
}
