package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

// Project is auth.Project. The type is declared in internal/auth so that auth
// depends on nothing and the store depends on auth — the only arrangement in
// which the two do not import each other.
type Project = auth.Project

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
			// auth distinguishes "no such key" (401) from "the database is
			// down" (503). Returning a generic not-found here would collapse
			// that distinction and make an outage look like a bad key.
			return Project{}, auth.ErrProjectNotFound
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

// IngestKey is one live public key for a project.
type IngestKey struct {
	PublicKey string    `json:"public_key"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

// IngestKeysForProject returns a project's live (non-revoked) keys, so the setup
// screen can show the user their DSN.
func (db *DB) IngestKeysForProject(ctx context.Context, projectID uint64) ([]IngestKey, error) {
	const query = `
		SELECT public_key, label, created_at
		FROM ingest_keys
		WHERE project_id = $1 AND revoked_at IS NULL
		ORDER BY created_at`

	rows, err := db.Query(ctx, query, projectID)
	if err != nil {
		return nil, fmt.Errorf("list ingest keys: %w", err)
	}
	defer rows.Close()

	var out []IngestKey
	for rows.Next() {
		var k IngestKey
		if err := rows.Scan(&k.PublicKey, &k.Label, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan ingest key: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ProjectsForUser lists the projects a user can see, through their org
// memberships. It is the list the dashboard's project switcher shows, and it is
// scoped by membership rather than filtered client-side for the obvious reason.
func (db *DB) ProjectsForUser(ctx context.Context, userID uint64) ([]Project, error) {
	const query = `
		SELECT p.id, p.org_id, p.slug, p.name, p.platform
		FROM projects p
		JOIN org_members m ON m.org_id = p.org_id
		WHERE m.user_id = $1
		ORDER BY p.name`

	rows, err := db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	projects := make([]Project, 0)
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Slug, &p.Name, &p.Platform); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// ProjectByID loads a project by id. Used by the alerter to name a project in a
// notification.
func (db *DB) ProjectByID(ctx context.Context, projectID uint64) (Project, error) {
	const query = `SELECT id, org_id, slug, name, platform FROM projects WHERE id = $1`
	var p Project
	err := db.QueryRow(ctx, query, projectID).Scan(&p.ID, &p.OrgID, &p.Slug, &p.Name, &p.Platform)
	if err != nil {
		if isNoRows(err) {
			return Project{}, ErrNotFound
		}
		return Project{}, fmt.Errorf("get project: %w", err)
	}
	return p, nil
}

// ProjectIDsWithEnabledRules returns the projects that have at least one enabled
// rule of a kind. The frequency evaluator uses it to skip projects with no
// frequency rules rather than scanning every project's events.
func (db *DB) ProjectIDsWithEnabledRules(ctx context.Context, kind string) ([]uint64, error) {
	const query = `
		SELECT DISTINCT project_id FROM alert_rules WHERE kind = $1 AND enabled = true`
	rows, err := db.Query(ctx, query, kind)
	if err != nil {
		return nil, fmt.Errorf("list projects with rules: %w", err)
	}
	defer rows.Close()

	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan project id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
