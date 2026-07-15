package clickhouse

import (
	"context"
	"fmt"

	"github.com/ebnsina/sabab-api/internal/query"
)

// The list endpoints paginate with keyset cursors, which by design do not know
// how many rows match in total. These counts answer the "of N" a paginator shows
// — one bounded COUNT over the same filter and time window the search ran, no
// cursor and no ordering, so ClickHouse answers it from marks cheaply.

// CountLogs counts the log lines matching a compiled search.
func (db *DB) CountLogs(ctx context.Context, sql query.SQL) (uint64, error) {
	return db.count(ctx, "logs", sql.Where, sql.Args)
}

// CountEvents counts the error events matching a compiled search.
func (db *DB) CountEvents(ctx context.Context, sql query.SQL) (uint64, error) {
	return db.count(ctx, "errors", sql.Where, sql.Args)
}

// CountSegments counts the traces (segment/root spans) matching a compiled
// search — the same is_segment restriction SearchSegments applies, so the total
// matches the rows the list actually returns.
func (db *DB) CountSegments(ctx context.Context, sql query.SQL) (uint64, error) {
	return db.count(ctx, "spans", sql.Where+" AND is_segment = true", sql.Args)
}

func (db *DB) count(ctx context.Context, table, where string, args []any) (uint64, error) {
	q := fmt.Sprintf("SELECT count() FROM %s WHERE %s", table, where)
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("count %s: %w", table, err)
	}
	defer rows.Close()

	var n uint64
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, fmt.Errorf("scan count %s: %w", table, err)
		}
	}
	return n, rows.Err()
}
