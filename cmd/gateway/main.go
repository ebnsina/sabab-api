// Command gateway is the ingest edge: authenticate, limit, enqueue, answer.
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

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/config"
	"github.com/ebnsina/sabab-api/internal/envelope"
	"github.com/ebnsina/sabab-api/internal/gateway"
	"github.com/ebnsina/sabab-api/internal/health"
	"github.com/ebnsina/sabab-api/internal/logging"
	"github.com/ebnsina/sabab-api/internal/queue"
	"github.com/ebnsina/sabab-api/internal/ratelimit"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
	"github.com/ebnsina/sabab-api/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Default().Error("gateway failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logging.Service(logging.New(cfg.LogLevel, cfg.Env), "gateway")

	db, err := postgres.Connect(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer db.Close()

	q, err := queue.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer func() { _ = q.Close() }()

	limiter := ratelimit.New(q.Client(), ratelimit.DefaultLimit)
	keys := auth.NewIngestKeys(db)

	checker := health.New("gateway", version.Version)
	// Readiness, not liveness: if Postgres or Redis is down the gateway cannot
	// accept events and should be taken out of the load balancer — but it must
	// not be killed and restarted, which would fix nothing.
	checker.Register("postgres", db.Ping)
	checker.Register("redis", q.Ping)

	gw := gateway.New(keys, limiter, q, envelope.DefaultLimits(), checker, log)

	server := &http.Server{
		Addr:         cfg.Gateway.Addr,
		Handler:      gw.Handler(),
		ReadTimeout:  cfg.Gateway.ReadTimeout,
		WriteTimeout: cfg.Gateway.WriteTimeout,
		IdleTimeout:  cfg.Gateway.IdleTimeout,
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

	// Drain in-flight requests. A request we already accepted has not been
	// enqueued yet, and killing it here would lose events we have effectively
	// promised to keep.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.Gateway.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("stopped")
	return nil
}
