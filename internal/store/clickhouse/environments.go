package clickhouse

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Environments lists the distinct environments a project has sent data to
// recently, across errors, logs and spans. It powers the environment switcher —
// so the filter offers exactly the environments that exist, never a free-text
// guess. Blank environments (data sent without one) are dropped rather than
// shown as an empty option.
func (db *DB) Environments(ctx context.Context, projectID uint64, since time.Time) ([]string, error) {
	// One scan per signal table, unioned. Each table carries `environment` as a
	// LowCardinality(String), so a DISTINCT over a recent window is cheap.
	const q = `
		SELECT DISTINCT environment FROM (
			SELECT environment FROM errors WHERE project_id = ? AND timestamp >= ?
			UNION ALL
			SELECT environment FROM logs   WHERE project_id = ? AND timestamp >= ?
			UNION ALL
			SELECT environment FROM spans  WHERE project_id = ? AND timestamp >= ?
		)
		WHERE environment != ''`

	rows, err := db.Query(ctx, q, projectID, since, projectID, since, projectID, since)
	if err != nil {
		return nil, fmt.Errorf("environments: %w", err)
	}
	defer rows.Close()

	var envs []string
	for rows.Next() {
		var env string
		if err := rows.Scan(&env); err != nil {
			return nil, fmt.Errorf("scan environment: %w", err)
		}
		envs = append(envs, env)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("environments rows: %w", err)
	}

	// Stable order so the switcher does not reshuffle between loads; "production"
	// first when present, since it is what people watch most.
	sort.Slice(envs, func(i, j int) bool {
		if p := envs[i] == "production"; p != (envs[j] == "production") {
			return p
		}
		return envs[i] < envs[j]
	})
	return envs, nil
}
