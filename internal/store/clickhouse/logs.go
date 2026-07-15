package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ebnsina/sabab-api/internal/query"
	"github.com/google/uuid"
)

// LogRow is one row of the `logs` table.
type LogRow struct {
	ProjectID  uint64
	Severity   string // trace|debug|info|warn|error|fatal — matches the Enum8
	Timestamp  time.Time
	ReceivedAt time.Time

	Service     string
	Environment string
	Release     string

	Body     string
	Template string

	TraceID uuid.UUID
	SpanID  uint64

	Attributes map[string]string
}

// insertLogs is the explicit column list, so adding a column cannot silently
// shift every value one place over.
const insertLogs = `INSERT INTO logs (
	project_id, severity, timestamp, received_at,
	service, environment, release,
	body, template,
	trace_id, span_id,
	attributes
)`

// InsertLogs writes a batch of log rows.
//
// A batch, never row-by-row: logs arrive at far higher volume than errors, so
// the insert-batch-size rule (impact CRITICAL) matters even more here — per-row
// inserts would bury ClickHouse in tiny parts. The processor decides when to
// flush; this just sends what it is given.
func (db *DB) InsertLogs(ctx context.Context, rows []LogRow) error {
	if len(rows) == 0 {
		return nil
	}

	batch, err := db.PrepareBatch(ctx, insertLogs)
	if err != nil {
		return fmt.Errorf("prepare logs batch: %w", err)
	}
	defer func() { _ = batch.Close() }()

	for i, row := range rows {
		if err := batch.Append(
			row.ProjectID, row.Severity, row.Timestamp, row.ReceivedAt,
			row.Service, row.Environment, row.Release,
			row.Body, row.Template,
			row.TraceID, row.SpanID,
			row.Attributes,
		); err != nil {
			return fmt.Errorf("append log row %d/%d: %w", i+1, len(rows), err)
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("send logs batch of %d: %w", len(rows), err)
	}
	return nil
}

// LogEntry is one log line as the log view renders it.
type LogEntry struct {
	Timestamp   time.Time         `json:"timestamp"`
	Severity    string            `json:"severity"`
	Service     string            `json:"service"`
	Body        string            `json:"body"`
	Template    string            `json:"template,omitempty"`
	TraceID     uuid.UUID         `json:"trace_id"`
	Environment string            `json:"environment,omitempty"`
	Release     string            `json:"release,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

const selectLog = `
	SELECT timestamp, severity, service, body, template, trace_id,
	       environment, release, attributes
	FROM logs`

// SearchLogs returns matching log lines, newest first.
//
// The DSL compiler produces the WHERE clause; this only frames it with ordering
// and a bounded limit. Newest-first because a log view is read top-down from the
// most recent — the opposite of the SDK's send order.
// SearchLogs returns one page of logs, newest first. `before` is the keyset
// cursor — nil for the first page, else the timestamp of the last row seen, so
// the next page starts strictly older than it. It fetches one extra row to tell
// the caller whether a further page exists; hasMore is that answer.
func (db *DB) SearchLogs(ctx context.Context, sql query.SQL, limit int, before *time.Time) (logs []LogEntry, hasMore bool, err error) {
	n := min(max(limit, 1), 500)

	where := sql.Where
	args := append([]any{}, sql.Args...) // copy: never mutate the caller's args
	if before != nil {
		where += " AND timestamp < ?"
		args = append(args, *before)
	}

	q := fmt.Sprintf(selectLog+`
		WHERE %s
		ORDER BY timestamp DESC
		LIMIT %d`, where, n+1)

	logs, err = db.scanLogs(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	if len(logs) > n {
		return logs[:n], true, nil
	}
	return logs, false, nil
}

// LogsForTrace returns every log emitted inside a trace, oldest first.
//
// This is the correlation the whole product is built around: from an error you
// have its trace_id, and this turns it into "the logs around that error", in the
// order they were printed. Oldest-first here because you read a single request's
// story forwards.
func (db *DB) LogsForTrace(ctx context.Context, projectID uint64, traceID uuid.UUID, limit int) ([]LogEntry, error) {
	q := selectLog + `
		WHERE project_id = ? AND trace_id = ?
		ORDER BY timestamp ASC
		LIMIT ?`

	return db.scanLogs(ctx, q, projectID, traceID, min(max(limit, 1), 500))
}

// TailLogs returns log lines newer than `after` for the live tail. The caller
// passes the newest timestamp it has seen and gets only what arrived since, so
// the tail streams forward without re-sending the screen.
func (db *DB) TailLogs(ctx context.Context, sql query.SQL, after time.Time, limit int) ([]LogEntry, error) {
	// The compiled WHERE already scopes project and (a wide) time range; the
	// after-cursor narrows to just-arrived rows. Ascending, because a tail shows
	// new lines appended at the bottom in arrival order.
	q := fmt.Sprintf(selectLog+`
		WHERE %s AND timestamp > ?
		ORDER BY timestamp ASC
		LIMIT %d`, sql.Where, min(max(limit, 1), 500))

	args := append(append([]any{}, sql.Args...), after)
	return db.scanLogs(ctx, q, args...)
}

func (db *DB) scanLogs(ctx context.Context, q string, args ...any) ([]LogEntry, error) {
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.Timestamp, &e.Severity, &e.Service, &e.Body, &e.Template,
			&e.TraceID, &e.Environment, &e.Release, &e.Attributes); err != nil {
			return nil, fmt.Errorf("scan log: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
