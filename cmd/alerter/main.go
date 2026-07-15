// Command alerter evaluates alert rules and fires notifications.
//
// It does two things at once: consumes the alert-signal stream for immediate
// new-issue and regression alerts, and runs a timer for frequency rules.
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

	"github.com/ebnsina/sabab-api/internal/alert"
	"github.com/ebnsina/sabab-api/internal/config"
	"github.com/ebnsina/sabab-api/internal/health"
	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/logging"
	"github.com/ebnsina/sabab-api/internal/notify"
	"github.com/ebnsina/sabab-api/internal/queue"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
	"github.com/ebnsina/sabab-api/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Default().Error("alerter failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logging.Service(logging.New(cfg.LogLevel, cfg.Env), "alerter")

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

	// The alert stream, as a consumer group so multiple alerter replicas share
	// the load and each signal is handled once.
	consumer, err := q.OnStream(queue.AlertStream, queue.AlertStreamMaxLen).
		WithGroup(ctx, cfg.Alerter.ConsumerGroup, cfg.Alerter.ConsumerName)
	if err != nil {
		return err
	}

	// The channels. The email sender falls back to logging when SMTP is not
	// configured, so a fresh self-host can still create and test email rules.
	dispatcher := notify.NewDispatcher(
		notify.SlackSender{},
		notify.DiscordSender{},
		notify.WebhookSender{},
		notify.NewEmailSender(cfg.SMTP, log),
	)

	engine := alert.NewEngine(pg, dispatcher, cfg.DashboardURL, log)

	checker := health.New("alerter", version.Version)
	checker.Register("postgres", pg.Ping)
	checker.Register("clickhouse", ch.Ping)
	checker.Register("redis", q.Ping)
	go serveHealth(ctx, checker, log)

	log.Info("alerter running",
		slog.String("group", cfg.Alerter.ConsumerGroup),
		slog.Duration("frequency_interval", cfg.Alerter.FrequencyInterval))

	// The frequency timer runs alongside the signal loop.
	go runFrequency(ctx, engine, pg, ch, cfg.Alerter.FrequencyInterval, log)

	return consumeSignals(ctx, consumer, engine, log)
}

// consumeSignals drains the alert-signal stream: each new-issue or regression
// signal is handled the moment it arrives.
func consumeSignals(ctx context.Context, consumer *queue.Redis, engine *alert.Engine, log *slog.Logger) error {
	for {
		if ctx.Err() != nil {
			return nil
		}

		messages, err := consumer.Consume(ctx, 64, time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Error("consume alert signals", slog.Any("error", err))
			time.Sleep(time.Second)
			continue
		}

		var acked []string
		for _, msg := range messages {
			sig, err := ingest.DecodeAlert(msg.Body)
			if err != nil {
				// Undecodable: it can never succeed, so ack it rather than
				// redeliver forever.
				log.Error("dropping undecodable alert signal", slog.Any("error", err))
				acked = append(acked, msg.ID)
				continue
			}
			if err := engine.HandleSignal(ctx, sig); err != nil {
				// A transient failure (Postgres blipped): leave it unacked so it
				// is redelivered. A duplicate alert is far better than a missed
				// regression.
				log.Error("handle alert signal",
					slog.Uint64("issue_id", sig.IssueID), slog.Any("error", err))
				continue
			}
			acked = append(acked, msg.ID)
		}

		if len(acked) > 0 {
			if err := consumer.Ack(ctx, acked...); err != nil {
				log.Error("ack alert signals", slog.Any("error", err))
			}
		}
	}
}

// runFrequency evaluates frequency rules on a timer.
func runFrequency(ctx context.Context, engine *alert.Engine, pg *postgres.DB, ch *clickhouse.DB, interval time.Duration, log *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	counter := clickhouseCounter{ch}

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := engine.EvaluateFrequency(ctx, pg, counter, now.UTC()); err != nil {
				log.Error("frequency evaluation", slog.Any("error", err))
			}
		}
	}
}

// clickhouseCounter adapts *clickhouse.DB to alert.EventCounter, translating the
// store's FrequentGroup into the alert package's own type so the engine does not
// import the store.
type clickhouseCounter struct{ db *clickhouse.DB }

func (c clickhouseCounter) FrequentGroups(ctx context.Context, projectID uint64, since time.Time, threshold uint64) ([]alert.FrequentGroup, error) {
	groups, err := c.db.FrequentGroups(ctx, projectID, since, threshold)
	if err != nil {
		return nil, err
	}
	out := make([]alert.FrequentGroup, len(groups))
	for i, g := range groups {
		out[i] = alert.FrequentGroup{GroupHash: g.GroupHash, Count: g.Count}
	}
	return out, nil
}

func serveHealth(ctx context.Context, checker *health.Checker, log *slog.Logger) {
	mux := http.NewServeMux()
	checker.Routes(mux)

	server := &http.Server{Addr: ":8083", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Warn("health server stopped", slog.Any("error", err))
	}
}
