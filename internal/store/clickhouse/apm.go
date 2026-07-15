package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// APM turns the raw `spans` table into the numbers a performance view needs:
// per-endpoint throughput and latency percentiles, failure rate, Apdex, the
// slowest sample traces, and the database queries costing the most time. There
// is no new ingest — it is all aggregation over spans already written.
//
// A "transaction" is a segment span (is_segment): the root of a service's slice
// of a trace, i.e. one served request. Grouping those by their parameterised
// name ("GET /users/:id") is what turns millions of spans into a short,
// readable endpoint list.

// TransactionSummary is one endpoint's row in the performance list.
type TransactionSummary struct {
	Name    string `json:"name"`
	Op      string `json:"op"`
	Service string `json:"service"`
	// Count is requests in the window; Throughput is that as requests/minute, the
	// figure that stays comparable as the window changes.
	Count      uint64  `json:"count"`
	Throughput float64 `json:"throughput"`
	P50MS      float64 `json:"p50_ms"`
	P75MS      float64 `json:"p75_ms"`
	P95MS      float64 `json:"p95_ms"`
	P99MS      float64 `json:"p99_ms"`
	// FailureRate is 0..1 (errored requests / total).
	FailureRate float64 `json:"failure_rate"`
	// Apdex is 0..1: satisfied + tolerating/2 over total, against the T threshold.
	Apdex float64 `json:"apdex"`
}

// transactionSort maps a requested sort to a safe ORDER BY expression. The value
// is chosen from this fixed set — never interpolated from user input.
func transactionSort(key string) (string, bool) {
	switch key {
	case "throughput", "count", "":
		return "count()", true
	case "p95", "slowest":
		return "quantile(0.95)(duration_ns)", true
	case "p99":
		return "quantile(0.99)(duration_ns)", true
	case "failure_rate":
		return "countIf(status = 'error') / count()", true
	case "apdex":
		// Lowest Apdex first is what a sort here means — the worst-served
		// endpoints — so the caller flips this one to ASC.
		return "apdexScore", true
	case "impact":
		// Time the endpoint is responsible for: slow AND frequent rises to the top.
		return "quantile(0.95)(duration_ns) * count()", true
	default:
		return "", false
	}
}

// Transactions lists endpoints with their latency, throughput, failure rate and
// Apdex over the window. apdexT is the Apdex threshold T (satisfied at <= T,
// tolerating up to 4T).
func (db *DB) Transactions(ctx context.Context, projectID uint64, from, to time.Time, apdexT time.Duration, sortBy string, limit int) ([]TransactionSummary, error) {
	orderExpr, ok := transactionSort(sortBy)
	if !ok {
		return nil, fmt.Errorf("unknown sort %q", sortBy)
	}
	dir := "DESC"
	if orderExpr == "apdexScore" {
		dir = "ASC" // worst-served first
	}

	tNS := apdexT.Nanoseconds()
	windowMin := to.Sub(from).Minutes()
	if windowMin <= 0 {
		windowMin = 1
	}

	// Apdex is computed from two counts (satisfied, tolerating) so the row scan
	// stays simple and the division happens in Go; the query still exposes an
	// `apdexScore` alias for ORDER BY apdex.
	q := fmt.Sprintf(`
		SELECT
			name, any(op) AS op, any(service) AS service,
			count() AS c,
			quantiles(0.5, 0.75, 0.95, 0.99)(duration_ns) AS qs,
			countIf(status = 'error') AS errors,
			countIf(duration_ns <= ?) AS satisfied,
			countIf(duration_ns > ? AND duration_ns <= ?) AS tolerating,
			(satisfied + tolerating / 2) / c AS apdexScore
		FROM spans
		WHERE project_id = ? AND is_segment AND timestamp >= ? AND timestamp < ?
		GROUP BY name
		ORDER BY %s %s
		LIMIT ?`, orderExpr, dir)

	rows, err := db.Query(ctx, q,
		tNS, tNS, 4*tNS, // apdex thresholds, in SELECT order
		projectID, from, to,
		min(max(limit, 1), 200),
	)
	if err != nil {
		return nil, fmt.Errorf("query transactions: %w", err)
	}
	defer rows.Close()

	var out []TransactionSummary
	for rows.Next() {
		var (
			t                             TransactionSummary
			qs                            []float64
			errors, satisfied, tolerating uint64
			apdexScore                    float64
		)
		if err := rows.Scan(&t.Name, &t.Op, &t.Service, &t.Count, &qs,
			&errors, &satisfied, &tolerating, &apdexScore); err != nil {
			return nil, fmt.Errorf("scan transaction: %w", err)
		}
		if len(qs) == 4 {
			t.P50MS, t.P75MS, t.P95MS, t.P99MS = ns(qs[0]), ns(qs[1]), ns(qs[2]), ns(qs[3])
		}
		if t.Count > 0 {
			t.FailureRate = float64(errors) / float64(t.Count)
		}
		t.Apdex = apdexScore
		t.Throughput = float64(t.Count) / windowMin
		out = append(out, t)
	}
	return out, rows.Err()
}

// TransactionSample is one slow trace to open from an endpoint's detail — the
// evidence behind a p95.
type TransactionSample struct {
	TraceID    uuid.UUID `json:"trace_id"`
	Timestamp  time.Time `json:"timestamp"`
	DurationNS uint64    `json:"duration_ns"`
	Status     string    `json:"status"`
	HTTPStatus uint16    `json:"http_status,omitempty"`
}

// TransactionSamples returns the slowest traces for one endpoint — the ones
// worth opening in the waterfall to see where the time went.
func (db *DB) TransactionSamples(ctx context.Context, projectID uint64, name string, from, to time.Time, limit int) ([]TransactionSample, error) {
	const q = `
		SELECT trace_id, timestamp, duration_ns, status, http_status
		FROM spans
		WHERE project_id = ? AND is_segment AND name = ?
		  AND timestamp >= ? AND timestamp < ?
		ORDER BY duration_ns DESC
		LIMIT ?`

	rows, err := db.Query(ctx, q, projectID, name, from, to, min(max(limit, 1), 50))
	if err != nil {
		return nil, fmt.Errorf("query transaction samples: %w", err)
	}
	defer rows.Close()

	var out []TransactionSample
	for rows.Next() {
		var s TransactionSample
		if err := rows.Scan(&s.TraceID, &s.Timestamp, &s.DurationNS, &s.Status, &s.HTTPStatus); err != nil {
			return nil, fmt.Errorf("scan sample: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SlowQuery is one database statement's cost, aggregated across every trace.
type SlowQuery struct {
	Statement string  `json:"statement"`
	DBSystem  string  `json:"db_system"`
	Count     uint64  `json:"count"`
	P95MS     float64 `json:"p95_ms"`
	AvgMS     float64 `json:"avg_ms"`
	// TotalMS is the sum of time in this statement — what makes a fast-but-frequent
	// query rank above a slow-but-rare one.
	TotalMS float64 `json:"total_ms"`
}

// SlowQueries ranks database statements by the total time spent in them. It
// groups by the statement text, which the SDK sends parameterised ("SELECT ...
// WHERE id = $1"), so the same query shape aggregates into one row.
func (db *DB) SlowQueries(ctx context.Context, projectID uint64, from, to time.Time, limit int) ([]SlowQuery, error) {
	const q = `
		SELECT
			db_statement, any(db_system) AS db_system,
			count() AS c,
			quantile(0.95)(duration_ns) AS p95,
			avg(duration_ns) AS mean,
			toFloat64(sum(duration_ns)) AS total
		FROM spans
		WHERE project_id = ? AND op = 'db.query' AND db_statement != ''
		  AND timestamp >= ? AND timestamp < ?
		GROUP BY db_statement
		ORDER BY total DESC
		LIMIT ?`

	rows, err := db.Query(ctx, q, projectID, from, to, min(max(limit, 1), 100))
	if err != nil {
		return nil, fmt.Errorf("query slow queries: %w", err)
	}
	defer rows.Close()

	var out []SlowQuery
	for rows.Next() {
		var (
			s                SlowQuery
			p95, mean, total float64
		)
		if err := rows.Scan(&s.Statement, &s.DBSystem, &s.Count, &p95, &mean, &total); err != nil {
			return nil, fmt.Errorf("scan slow query: %w", err)
		}
		s.P95MS, s.AvgMS, s.TotalMS = ns(p95), ns(mean), ns(total)
		out = append(out, s)
	}
	return out, rows.Err()
}

// NPlusOne is one repeated-query pattern: a statement run many times under a
// single parent span in the same trace — the classic N+1, where code loads a
// list and then queries once per row instead of once for all.
type NPlusOne struct {
	Statement string `json:"statement"`
	DBSystem  string `json:"db_system"`
	// Occurrences is how many (trace, parent) groups exhibited the pattern.
	Occurrences uint64 `json:"occurrences"`
	// MaxRepeats/AvgRepeats are the worst and typical fan-out — 50 identical
	// queries under one parent is a worse smell than 6.
	MaxRepeats uint64  `json:"max_repeats"`
	AvgRepeats float64 `json:"avg_repeats"`
	// SampleTrace is the worst offender, to open in the waterfall.
	SampleTrace uuid.UUID `json:"sample_trace"`
}

// NPlusOneQueries finds repeated-query patterns: within one trace, a parent span
// with at least `threshold` child db.query spans running the identical
// statement. That fan-out is the signature of an N+1, and it is invisible in a
// per-query average — each query is fast; it is the count that hurts.
func (db *DB) NPlusOneQueries(ctx context.Context, projectID uint64, from, to time.Time, threshold, limit int) ([]NPlusOne, error) {
	if threshold < 2 {
		threshold = 2
	}
	// Inner: how many times each statement repeats under one parent in one trace.
	// Outer: roll those groups up per statement across all traces.
	const q = `
		SELECT
			db_statement,
			any(db_system) AS db_system,
			count() AS occurrences,
			max(repeats) AS max_repeats,
			avg(repeats) AS avg_repeats,
			argMax(trace_id, repeats) AS sample_trace
		FROM (
			SELECT trace_id, parent_span_id, db_statement,
			       any(db_system) AS db_system, count() AS repeats
			FROM spans
			WHERE project_id = ? AND op = 'db.query' AND db_statement != '' AND parent_span_id != 0
			  AND timestamp >= ? AND timestamp < ?
			GROUP BY trace_id, parent_span_id, db_statement
			HAVING repeats >= ?
		)
		GROUP BY db_statement
		ORDER BY max_repeats DESC, occurrences DESC
		LIMIT ?`

	rows, err := db.Query(ctx, q, projectID, from, to, threshold, min(max(limit, 1), 50))
	if err != nil {
		return nil, fmt.Errorf("query n+1: %w", err)
	}
	defer rows.Close()

	var out []NPlusOne
	for rows.Next() {
		var n NPlusOne
		if err := rows.Scan(&n.Statement, &n.DBSystem, &n.Occurrences, &n.MaxRepeats, &n.AvgRepeats, &n.SampleTrace); err != nil {
			return nil, fmt.Errorf("scan n+1: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ns converts nanoseconds to milliseconds for the wire — the dashboard thinks in
// ms, and JSON floats keep the fractional part a percentile needs.
func ns(nanos float64) float64 { return nanos / 1e6 }
