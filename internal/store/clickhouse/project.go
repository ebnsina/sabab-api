package clickhouse

import (
	"context"
	"fmt"
)

// purgeTables are every event-plane table keyed by project_id — everything a
// deleted project's data lives in, including the metric rollups.
var purgeTables = []string{"errors", "logs", "spans", "metrics_raw", "metrics_1m", "metrics_1h"}

// PurgeProject deletes all of a project's event data. Each ALTER … DELETE is a
// ClickHouse mutation: it is applied asynchronously in the background, so this
// enqueues the purge rather than waiting for every part to rewrite. The project
// is already gone from the control plane, so the data is unreachable meanwhile.
func (db *DB) PurgeProject(ctx context.Context, projectID uint64) error {
	for _, t := range purgeTables {
		// project_id is a validated uint64 — safe to format directly, and ALTER
		// mutations do not reliably accept a bound parameter in the WHERE.
		q := fmt.Sprintf("ALTER TABLE %s DELETE WHERE project_id = %d", t, projectID)
		if err := db.Exec(ctx, q); err != nil {
			return fmt.Errorf("purge %s: %w", t, err)
		}
	}
	return nil
}
