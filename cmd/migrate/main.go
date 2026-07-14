// Command migrate applies the Postgres and ClickHouse schemas.
//
//	migrate            # both databases
//	migrate -target=postgres
//	migrate -target=clickhouse
//	migrate -status    # report without applying anything
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ebnsina/sabab-api/internal/config"
	"github.com/ebnsina/sabab-api/internal/logging"
	"github.com/ebnsina/sabab-api/internal/migrate"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
	"github.com/ebnsina/sabab-api/migrations"
)

func main() {
	var (
		target = flag.String("target", "all", "which database to migrate: all|postgres|clickhouse")
		status = flag.Bool("status", false, "report pending migrations without applying them")
	)
	flag.Parse()

	// Ctrl-C cancels in-flight work; the runner still releases its lock.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *target, *status); err != nil {
		slog.Default().Error("migrate failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context, target string, statusOnly bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logging.Service(logging.New(cfg.LogLevel, cfg.Env), "migrate")

	switch target {
	case "all", "postgres", "clickhouse":
	default:
		return fmt.Errorf("unknown -target %q: want all, postgres or clickhouse", target)
	}

	var errs []error
	if target == "all" || target == "postgres" {
		errs = append(errs, migratePostgres(ctx, cfg, log, statusOnly))
	}
	if target == "all" || target == "clickhouse" {
		errs = append(errs, migrateClickHouse(ctx, cfg, log, statusOnly))
	}
	return errors.Join(errs...)
}

func migratePostgres(ctx context.Context, cfg *config.Config, log *slog.Logger, statusOnly bool) error {
	db, err := postgres.Connect(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer db.Close()

	loaded, err := migrate.Load(migrations.FS, migrations.PostgresDir)
	if err != nil {
		return err
	}
	driver := migrate.NewPostgres(db)
	if statusOnly {
		return reportStatus(ctx, driver, loaded, log)
	}
	return migrate.Run(ctx, driver, loaded, log)
}

func migrateClickHouse(ctx context.Context, cfg *config.Config, log *slog.Logger, statusOnly bool) error {
	db, err := clickhouse.Connect(ctx, cfg.ClickHouse)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	loaded, err := migrate.Load(migrations.FS, migrations.ClickHouseDir)
	if err != nil {
		return err
	}
	driver := migrate.NewClickHouse(db)
	if statusOnly {
		return reportStatus(ctx, driver, loaded, log)
	}
	return migrate.Run(ctx, driver, loaded, log)
}

// reportStatus prints which migrations are applied and which are pending,
// without touching the schema.
func reportStatus(ctx context.Context, d migrate.Driver, loaded []migrate.Migration, log *slog.Logger) error {
	if err := d.EnsureVersionTable(ctx); err != nil {
		return fmt.Errorf("%s: ensure version table: %w", d.Name(), err)
	}
	applied, err := d.AppliedVersions(ctx)
	if err != nil {
		return fmt.Errorf("%s: read applied versions: %w", d.Name(), err)
	}
	for _, m := range loaded {
		state := "pending"
		if checksum, done := applied[m.Version]; done {
			state = "applied"
			if checksum != m.Checksum {
				state = "CHANGED SINCE APPLIED"
			}
		}
		log.Info("migration",
			slog.String("target", d.Name()),
			slog.String("migration", m.Filename),
			slog.String("state", state),
		)
	}
	return nil
}
