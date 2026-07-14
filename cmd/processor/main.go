// Command processor drains the ingest queue: normalize, scrub, symbolicate,
// enrich, fingerprint, group, write.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ebnsina/sabab-api/internal/config"
	"github.com/ebnsina/sabab-api/internal/health"
	"github.com/ebnsina/sabab-api/internal/logging"
	"github.com/ebnsina/sabab-api/internal/processor"
	"github.com/ebnsina/sabab-api/internal/queue"
	"github.com/ebnsina/sabab-api/internal/scrub"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
	"github.com/ebnsina/sabab-api/internal/store/objects"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
	"github.com/ebnsina/sabab-api/internal/symbolicate"
	"github.com/ebnsina/sabab-api/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Default().Error("processor failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logging.Service(logging.New(cfg.LogLevel, cfg.Env), "processor")

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

	q, err := queue.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer func() { _ = q.Close() }()

	consumer, err := q.WithGroup(ctx, cfg.Processor.ConsumerGroup, cfg.Processor.ConsumerName)
	if err != nil {
		return err
	}

	artifacts, err := objects.Connect(ctx, cfg.S3)
	if err != nil {
		return err
	}

	// Symbolication is best-effort inside the pipeline: a missing map costs us a
	// readable stack, never the event.
	symbolicator := symbolicate.New(pg, artifacts, log)
	pipeline := processor.NewPipeline(scrub.Default(), symbolicator, pg)

	opts := processor.DefaultOptions()
	opts.BatchSize = cfg.Processor.BatchSize

	// A health server, because an orchestrator needs somewhere to probe even for
	// a service that serves no traffic.
	checker := health.New("processor", version.Version)
	checker.Register("postgres", pg.Ping)
	checker.Register("clickhouse", ch.Ping)
	checker.Register("redis", q.Ping)
	checker.Register("objects", artifacts.Ping)
	go serveHealth(ctx, checker, log)

	log.Info("draining queue",
		slog.String("group", cfg.Processor.ConsumerGroup),
		slog.String("consumer", cfg.Processor.ConsumerName),
		slog.Int("batch_size", opts.BatchSize))

	return processor.New(consumer, pipeline, ch, opts, log).Run(ctx)
}

// serveHealth exposes /healthz and /readyz on the port after the gateway's.
func serveHealth(ctx context.Context, checker *health.Checker, log *slog.Logger) {
	mux := http.NewServeMux()
	checker.Routes(mux)

	server := &http.Server{
		Addr:              ":8082",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		// A health port that is already taken must not stop the processor from
		// doing its actual job.
		log.Warn("health server stopped", slog.Any("error", err))
	}
}
