package migrate

import (
	"context"
	"fmt"

	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

// pgAdvisoryLockKey is an arbitrary but fixed key. Every Sabab migration
// runner contends on it, so two deploys racing to migrate serialise instead of
// both applying 0007.
const pgAdvisoryLockKey int64 = 8_242_119_001

// PostgresDriver applies migrations to the control plane.
type PostgresDriver struct {
	db *postgres.DB
}

// NewPostgres returns a Driver backed by the given control-plane pool.
func NewPostgres(db *postgres.DB) *PostgresDriver { return &PostgresDriver{db: db} }

func (d *PostgresDriver) Name() string { return "postgres" }

// Lock takes a session-level advisory lock on a dedicated connection. The
// connection is held until unlock, because an advisory lock belongs to the
// session that took it — releasing it from a different pooled connection is a
// no-op that would silently defeat the whole mechanism.
func (d *PostgresDriver) Lock(ctx context.Context) (func(context.Context) error, error) {
	conn, err := d.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", pgAdvisoryLockKey); err != nil {
		conn.Release()
		return nil, err
	}
	return func(ctx context.Context) error {
		defer conn.Release()
		_, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", pgAdvisoryLockKey)
		return err
	}, nil
}

func (d *PostgresDriver) EnsureVersionTable(ctx context.Context) error {
	_, err := d.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    text        PRIMARY KEY,
			name       text        NOT NULL,
			checksum   text        NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`)
	return err
}

func (d *PostgresDriver) AppliedVersions(ctx context.Context) (map[string]string, error) {
	rows, err := d.db.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]string)
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, err
		}
		applied[version] = checksum
	}
	return applied, rows.Err()
}

// Apply runs the migration and records it in one transaction: Postgres gives us
// transactional DDL, so a failing migration leaves no partial schema behind.
func (d *PostgresDriver) Apply(ctx context.Context, m Migration) error {
	tx, err := d.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() // no-op after Commit

	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
		m.Version, m.Name, m.Checksum,
	); err != nil {
		return fmt.Errorf("record version: %w", err)
	}
	return tx.Commit(ctx)
}

var _ Driver = (*PostgresDriver)(nil)
