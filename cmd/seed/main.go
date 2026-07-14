// Command seed creates an organization, a project and an ingest key, and prints
// the ingest URL an SDK would be configured with.
//
// It is idempotent: running it twice reuses the existing org and project and
// mints a fresh key, so it is safe to re-run against a stack you already seeded.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/config"
	"github.com/ebnsina/sabab-api/internal/logging"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
	"github.com/jackc/pgx/v5"
)

func main() {
	var (
		orgSlug     = flag.String("org", "acme", "organization slug")
		projectSlug = flag.String("project", "web", "project slug")
		platform    = flag.String("platform", "javascript", "project platform")
	)
	flag.Parse()

	if err := run(context.Background(), *orgSlug, *projectSlug, *platform); err != nil {
		slog.Default().Error("seed failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context, orgSlug, projectSlug, platform string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logging.Service(logging.New(cfg.LogLevel, cfg.Env), "seed")

	db, err := postgres.Connect(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer db.Close()

	orgID, err := upsertOrg(ctx, db, orgSlug)
	if err != nil {
		return err
	}
	project, err := upsertProject(ctx, db, orgID, projectSlug, platform)
	if err != nil {
		return err
	}

	key, err := auth.NewIngestKey()
	if err != nil {
		return err
	}
	if err := db.CreateIngestKey(ctx, project.ID, key, "seed"); err != nil {
		return err
	}

	log.Info("seeded",
		slog.String("org", orgSlug),
		slog.String("project", projectSlug),
		slog.Uint64("project_id", project.ID),
	)

	// The whole SDK configuration, in one string — see docs/wire-format.md.
	fmt.Printf("\n  Ingest key: %s\n", key)
	fmt.Printf("  Project ID: %d\n", project.ID)
	fmt.Printf("  Ingest URL: http://%s@localhost:8080/%d\n\n", key, project.ID)
	return nil
}

func upsertOrg(ctx context.Context, db *postgres.DB, slug string) (uint64, error) {
	var id uint64
	err := db.QueryRow(ctx, `SELECT id FROM organizations WHERE slug = $1`, slug).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("look up organization: %w", err)
	}
	return db.CreateOrganization(ctx, slug, slug)
}

func upsertProject(ctx context.Context, db *postgres.DB, orgID uint64, slug, platform string) (postgres.Project, error) {
	var p postgres.Project
	err := db.QueryRow(ctx,
		`SELECT id, org_id, slug, name, platform FROM projects WHERE org_id = $1 AND slug = $2`,
		orgID, slug,
	).Scan(&p.ID, &p.OrgID, &p.Slug, &p.Name, &p.Platform)
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return postgres.Project{}, fmt.Errorf("look up project: %w", err)
	}
	return db.CreateProject(ctx, orgID, slug, slug, platform)
}
