package migrate

import (
	"context"
	"fmt"

	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
)

// ClickHouseDriver applies migrations to the event plane.
//
// Two properties of ClickHouse shape this driver, and both are worked around
// rather than hidden:
//
//   - No multi-statement Exec. Migration files are split on semicolons and each
//     statement is sent on its own.
//   - No transactional DDL. A file that fails halfway leaves the statements
//     before the failure in place. Event-plane migrations are therefore
//     required to be idempotent (CREATE TABLE IF NOT EXISTS, ADD COLUMN IF NOT
//     EXISTS) so that re-running a partially applied file converges.
type ClickHouseDriver struct {
	db *clickhouse.DB
}

// NewClickHouse returns a Driver backed by the given event-plane connection.
func NewClickHouse(db *clickhouse.DB) *ClickHouseDriver { return &ClickHouseDriver{db: db} }

func (d *ClickHouseDriver) Name() string { return "clickhouse" }

// Lock is a no-op: ClickHouse has no advisory locks, and adding a Keeper-based
// distributed lock is not worth it while migrations run as a single job (make
// migrate locally, one init container in deploy). If migrations ever run from
// multiple replicas concurrently, this is the function to fix.
func (d *ClickHouseDriver) Lock(context.Context) (func(context.Context) error, error) {
	return func(context.Context) error { return nil }, nil
}

func (d *ClickHouseDriver) EnsureVersionTable(ctx context.Context) error {
	return d.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    String,
			name       String,
			checksum   String,
			applied_at DateTime64(3, 'UTC') DEFAULT now64(3)
		) ENGINE = ReplacingMergeTree(applied_at)
		ORDER BY version`)
}

func (d *ClickHouseDriver) AppliedVersions(ctx context.Context) (map[string]string, error) {
	// FINAL collapses any duplicate rows a retried Apply may have written.
	rows, err := d.db.Query(ctx, `SELECT version, checksum FROM schema_migrations FINAL`)
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

func (d *ClickHouseDriver) Apply(ctx context.Context, m Migration) error {
	statements := splitStatements(m.SQL)
	if len(statements) == 0 {
		return fmt.Errorf("no executable statements")
	}
	for i, stmt := range statements {
		if err := d.db.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("statement %d/%d: %w", i+1, len(statements), err)
		}
	}
	return d.db.Exec(ctx,
		`INSERT INTO schema_migrations (version, name, checksum) VALUES (?, ?, ?)`,
		m.Version, m.Name, m.Checksum,
	)
}

var _ Driver = (*ClickHouseDriver)(nil)
