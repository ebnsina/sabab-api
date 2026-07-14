package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

// Project is a project as the ingest path needs to know it.
type Project struct {
	ID       uint64
	OrgID    uint64
	Slug     string
	Name     string
	Platform string
}

// ProjectByIngestKey resolves a public ingest key to its project.
//
// Revoked keys do not match: revocation has to take effect immediately, because
// the only reason to revoke a key that ships in a browser bundle is that it is
// being abused.
func (db *DB) ProjectByIngestKey(ctx context.Context, publicKey string) (Project, error) {
	const query = `
		SELECT p.id, p.org_id, p.slug, p.name, p.platform
		FROM ingest_keys k
		JOIN projects p ON p.id = k.project_id
		WHERE k.public_key = $1 AND k.revoked_at IS NULL`

	var p Project
	err := db.QueryRow(ctx, query, publicKey).Scan(&p.ID, &p.OrgID, &p.Slug, &p.Name, &p.Platform)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Project{}, ErrNotFound
		}
		return Project{}, fmt.Errorf("lookup ingest key: %w", err)
	}
	return p, nil
}

// CreateProject inserts a project and returns it.
func (db *DB) CreateProject(ctx context.Context, orgID uint64, slug, name, platform string) (Project, error) {
	const query = `
		INSERT INTO projects (org_id, slug, name, platform)
		VALUES ($1, $2, $3, $4)
		RETURNING id, org_id, slug, name, platform`

	var p Project
	err := db.QueryRow(ctx, query, orgID, slug, name, platform).
		Scan(&p.ID, &p.OrgID, &p.Slug, &p.Name, &p.Platform)
	if err != nil {
		return Project{}, fmt.Errorf("create project: %w", err)
	}
	return p, nil
}

// CreateOrganization inserts an organization and returns its id.
func (db *DB) CreateOrganization(ctx context.Context, slug, name string) (uint64, error) {
	const query = `INSERT INTO organizations (slug, name) VALUES ($1, $2) RETURNING id`

	var id uint64
	if err := db.QueryRow(ctx, query, slug, name).Scan(&id); err != nil {
		return 0, fmt.Errorf("create organization: %w", err)
	}
	return id, nil
}

// CreateIngestKey stores a public key for a project.
func (db *DB) CreateIngestKey(ctx context.Context, projectID uint64, publicKey, label string) error {
	const query = `INSERT INTO ingest_keys (project_id, public_key, label) VALUES ($1, $2, $3)`

	if _, err := db.Exec(ctx, query, projectID, publicKey, label); err != nil {
		return fmt.Errorf("create ingest key: %w", err)
	}
	return nil
}
