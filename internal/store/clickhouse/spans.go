package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/ebnsina/sabab-api/internal/query"
	"github.com/google/uuid"
)

// SpanRow is one row of the `spans` table.
type SpanRow struct {
	ProjectID    uint64
	TraceID      uuid.UUID
	SpanID       uint64
	ParentSpanID uint64
	SegmentID    uint64
	IsSegment    bool

	Name    string
	Op      string
	Service string

	Timestamp  time.Time
	ReceivedAt time.Time
	DurationNS uint64
	Status     string // ok | error | cancelled

	Environment string
	Release     string

	HTTPMethod string
	HTTPStatus uint16
	HTTPRoute  string

	DBSystem    string
	DBStatement string

	Tags         map[string]string
	Measurements map[string]float64

	UserID     string
	GeoCountry string
}

const insertSpans = `INSERT INTO spans (
	project_id, trace_id, span_id, parent_span_id, segment_id, is_segment,
	name, op, service,
	timestamp, received_at, duration_ns, status,
	environment, release,
	http_method, http_status, http_route,
	db_system, db_statement,
	tags, measurements,
	user_id, geo_country
)`

// InsertSpans writes a batch of spans. Spans are the highest-volume signal — one
// request can be dozens — so batching (insert-batch-size, CRITICAL) matters most
// here.
func (db *DB) InsertSpans(ctx context.Context, rows []SpanRow) error {
	if len(rows) == 0 {
		return nil
	}

	batch, err := db.PrepareBatch(ctx, insertSpans)
	if err != nil {
		return fmt.Errorf("prepare spans batch: %w", err)
	}
	defer func() { _ = batch.Close() }()

	for i, row := range rows {
		if err := batch.Append(
			row.ProjectID, row.TraceID, row.SpanID, row.ParentSpanID, row.SegmentID, row.IsSegment,
			row.Name, row.Op, row.Service,
			row.Timestamp, row.ReceivedAt, row.DurationNS, row.Status,
			row.Environment, row.Release,
			row.HTTPMethod, row.HTTPStatus, row.HTTPRoute,
			row.DBSystem, row.DBStatement,
			nonNilStrMap(row.Tags), nonNilFloatMap(row.Measurements),
			row.UserID, row.GeoCountry,
		); err != nil {
			return fmt.Errorf("append span row %d/%d: %w", i+1, len(rows), err)
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("send spans batch of %d: %w", len(rows), err)
	}
	return nil
}

// Span is one span as the waterfall renders it.
//
// span_id and parent_span_id are 64-bit ids that exceed JavaScript's safe
// integer range, so they are marshalled as strings (see MarshalJSON) — a JSON
// number would silently lose precision and let two spans collide.
type Span struct {
	SpanID       uint64            `json:"-"`
	ParentSpanID uint64            `json:"-"`
	Name         string            `json:"name"`
	Op           string            `json:"op"`
	Service      string            `json:"service"`
	Timestamp    time.Time         `json:"timestamp"`
	DurationNS   uint64            `json:"duration_ns"`
	Status       string            `json:"status"`
	HTTPMethod   string            `json:"http_method,omitempty"`
	HTTPStatus   uint16            `json:"http_status,omitempty"`
	DBStatement  string            `json:"db_statement,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
}

// MarshalJSON emits the 64-bit span ids as decimal strings, so they survive the
// trip to a JavaScript client intact.
func (s Span) MarshalJSON() ([]byte, error) {
	type alias Span // avoid recursion
	return json.Marshal(struct {
		alias
		SpanID       string `json:"span_id"`
		ParentSpanID string `json:"parent_span_id"`
	}{
		alias:        alias(s),
		SpanID:       strconv.FormatUint(s.SpanID, 10),
		ParentSpanID: strconv.FormatUint(s.ParentSpanID, 10),
	})
}

// SpansForTrace returns every span of one trace, ordered by start time — the
// waterfall. Served by the by_trace projection, so it is a key lookup.
func (db *DB) SpansForTrace(ctx context.Context, projectID uint64, traceID uuid.UUID) ([]Span, error) {
	const q = `
		SELECT span_id, parent_span_id, name, op, service, timestamp, duration_ns,
		       status, http_method, http_status, db_statement, tags
		FROM spans
		WHERE project_id = ? AND trace_id = ?
		ORDER BY timestamp ASC
		LIMIT 2000`

	rows, err := db.Query(ctx, q, projectID, traceID)
	if err != nil {
		return nil, fmt.Errorf("read trace spans: %w", err)
	}
	defer rows.Close()

	var out []Span
	for rows.Next() {
		var s Span
		if err := rows.Scan(&s.SpanID, &s.ParentSpanID, &s.Name, &s.Op, &s.Service,
			&s.Timestamp, &s.DurationNS, &s.Status, &s.HTTPMethod, &s.HTTPStatus,
			&s.DBStatement, &s.Tags); err != nil {
			return nil, fmt.Errorf("scan span: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// TraceSummary is one row of the trace-search list: the root span of a trace.
type TraceSummary struct {
	TraceID    uuid.UUID `json:"trace_id"`
	Name       string    `json:"name"`
	Op         string    `json:"op"`
	Service    string    `json:"service"`
	Timestamp  time.Time `json:"timestamp"`
	DurationNS uint64    `json:"duration_ns"`
	Status     string    `json:"status"`
	HTTPStatus uint16    `json:"http_status,omitempty"`
}

// SearchSegments returns matching segment (root) spans — one per trace — so a
// trace search lists traces, not the thousands of child spans within them.
// SearchSegments returns one page of traces (segment/root spans, one per trace),
// newest first. The keyset uses (timestamp, trace_id) — trace_id is unique per
// segment row — so paging is stable across microsecond ties. before is nil for
// the first page.
func (db *DB) SearchSegments(ctx context.Context, sql query.SQL, limit int, before *time.Time, beforeID uuid.UUID) (out []TraceSummary, hasMore bool, err error) {
	n := min(max(limit, 1), 200)

	where := sql.Where + " AND is_segment = true"
	args := append([]any{}, sql.Args...)
	if before != nil {
		where += " AND (timestamp, trace_id) < (?, ?)"
		args = append(args, *before, beforeID)
	}

	q := fmt.Sprintf(`
		SELECT trace_id, name, op, service, timestamp, duration_ns, status, http_status
		FROM spans
		WHERE %s
		ORDER BY timestamp DESC, trace_id DESC
		LIMIT %d`, where, n+1)

	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, fmt.Errorf("search segments: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var t TraceSummary
		if err := rows.Scan(&t.TraceID, &t.Name, &t.Op, &t.Service, &t.Timestamp,
			&t.DurationNS, &t.Status, &t.HTTPStatus); err != nil {
			return nil, false, fmt.Errorf("scan trace summary: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > n {
		return out[:n], true, nil
	}
	return out, false, nil
}

func nonNilStrMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func nonNilFloatMap(m map[string]float64) map[string]float64 {
	if m == nil {
		return map[string]float64{}
	}
	return m
}
