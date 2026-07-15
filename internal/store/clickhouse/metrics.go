package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MetricRow is one raw metric observation, written to metrics_raw.
type MetricRow struct {
	ProjectID uint64
	Timestamp time.Time
	Name      string
	Type      string // counter | gauge | distribution | set
	Unit      string
	Tags      map[string]string
	Value     float64
}

const insertMetrics = `INSERT INTO metrics_raw (
	project_id, timestamp, name, type, unit, tags, value
)`

// InsertMetrics writes raw observations. The rollup MVs aggregate them at insert
// time, so the dashboard never reads this table — it reads metrics_1m/1h.
func (db *DB) InsertMetrics(ctx context.Context, rows []MetricRow) error {
	if len(rows) == 0 {
		return nil
	}

	batch, err := db.PrepareBatch(ctx, insertMetrics)
	if err != nil {
		return fmt.Errorf("prepare metrics batch: %w", err)
	}
	defer func() { _ = batch.Close() }()

	for i, row := range rows {
		if err := batch.Append(
			row.ProjectID, row.Timestamp, row.Name, row.Type, row.Unit,
			nonNilStrMap(row.Tags), row.Value,
		); err != nil {
			return fmt.Errorf("append metric row %d/%d: %w", i+1, len(rows), err)
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("send metrics batch of %d: %w", len(rows), err)
	}
	return nil
}

// MetricPoint is one bucket of a metric time series.
type MetricPoint struct {
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
	// Group is the tag value when the query is split by a tag; empty otherwise.
	Group string `json:"group,omitempty"`
}

// MetricQuery selects and shapes a time series.
type MetricQuery struct {
	ProjectID uint64
	Name      string
	// Agg is how the value is reduced per bucket: sum | avg | min | max | count |
	// p50 | p95 | p99 | uniq. The right default depends on the metric type, which
	// the caller picks.
	Agg string
	// GroupBy splits the series by a tag key: one line per distinct value.
	GroupBy string
	From    time.Time
	To      time.Time
	// Rollup chooses the resolution table: "1m" or "1h".
	Rollup string
}

// QueryMetric reads a metric time series from the rollups.
//
// It never touches metrics_raw — the whole point of the rollup MVs is that a
// chart reads pre-aggregated minute or hour buckets. The aggregate columns are
// AggregateFunction states, so every read merges them (sumMerge, quantilesMerge)
// rather than re-scanning raw values.
func (db *DB) QueryMetric(ctx context.Context, q MetricQuery) ([]MetricPoint, error) {
	table, timeCol := "metrics_1m", "minute"
	if q.Rollup == "1h" {
		table, timeCol = "metrics_1h", "hour"
	}

	// The value expression is chosen from a fixed set — never interpolated from
	// user input — so the merge function matches the requested aggregation. Only
	// its column list is templated; every value stays a bound parameter.
	valueExpr, ok := metricAgg(strings.ToLower(strings.TrimSpace(q.Agg)))
	if !ok {
		return nil, fmt.Errorf("unknown aggregation %q", q.Agg)
	}

	// Build SELECT/GROUP BY and the args in one pass, so placeholder order and
	// argument order cannot drift apart.
	var (
		selectCols = valueExpr + " AS v"
		groupCols  = "t"
		args       []any
		grouped    = q.GroupBy != ""
	)
	if grouped {
		// The tag KEY is user input, so it is bound into the subscript, not
		// interpolated.
		selectCols = "tags[?] AS grp, " + selectCols
		groupCols = "grp, t"
		args = append(args, q.GroupBy) // for tags[?]
	}
	args = append(args, q.ProjectID, q.Name)
	tagFilter := ""
	if grouped {
		tagFilter = "AND has(mapKeys(tags), ?)"
		args = append(args, q.GroupBy) // for has(mapKeys(tags), ?)
	}
	args = append(args, q.From, q.To)

	sql := fmt.Sprintf(`
		SELECT %s AS t, %s
		FROM %s
		WHERE project_id = ? AND name = ? %s AND %s >= ? AND %s < ?
		GROUP BY %s
		ORDER BY t`,
		timeCol, selectCols, table, tagFilter, timeCol, timeCol, groupCols)

	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query metric: %w", err)
	}
	defer rows.Close()

	var out []MetricPoint
	for rows.Next() {
		var p MetricPoint
		if grouped {
			if err := rows.Scan(&p.Time, &p.Group, &p.Value); err != nil {
				return nil, fmt.Errorf("scan metric point: %w", err)
			}
		} else {
			if err := rows.Scan(&p.Time, &p.Value); err != nil {
				return nil, fmt.Errorf("scan metric point: %w", err)
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AggregateMetric reduces a metric to ONE value over the whole window, merging
// the rollup's aggregate states across every bucket with no per-bucket grouping.
// That makes "p95 over the last 5 minutes" the true window percentile — not an
// average of per-minute p95s, which would be wrong — which is exactly what a
// metric alert must compare against its threshold. The bool is false when the
// window held no data (so a rule does not fire on an absent metric).
func (db *DB) AggregateMetric(ctx context.Context, projectID uint64, name, agg string, from, to time.Time, rollup string) (float64, bool, error) {
	table, timeCol := "metrics_1m", "minute"
	if rollup == "1h" {
		table, timeCol = "metrics_1h", "hour"
	}

	valueExpr, ok := metricAgg(strings.ToLower(strings.TrimSpace(agg)))
	if !ok {
		return 0, false, fmt.Errorf("unknown aggregation %q", agg)
	}

	// count() rides along so an empty window (aggregates default to 0) is
	// distinguishable from a genuine zero.
	sql := fmt.Sprintf(`
		SELECT %s AS v, count() AS n
		FROM %s
		WHERE project_id = ? AND name = ? AND %s >= ? AND %s < ?`,
		valueExpr, table, timeCol, timeCol)

	rows, err := db.Query(ctx, sql, projectID, name, from, to)
	if err != nil {
		return 0, false, fmt.Errorf("aggregate metric: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return 0, false, rows.Err()
	}
	var (
		v float64
		n uint64
	)
	if err := rows.Scan(&v, &n); err != nil {
		return 0, false, fmt.Errorf("scan metric aggregate: %w", err)
	}
	return v, n > 0, rows.Err()
}

// metricAgg maps a requested aggregation to its ClickHouse merge expression over
// the rollup's AggregateFunction state columns. A percentile pulls the single
// quantile out of the merged quantiles state.
func metricAgg(agg string) (string, bool) {
	switch agg {
	case "sum", "":
		return "sumMerge(sum)", true
	case "count":
		return "toFloat64(countMerge(count))", true
	case "avg":
		return "sumMerge(sum) / countMerge(count)", true
	case "min":
		return "min(min)", true
	case "max":
		return "max(max)", true
	case "uniq":
		return "toFloat64(uniqMerge(unique))", true
	case "p50":
		return "arrayElement(quantilesMerge(0.5,0.75,0.95,0.99)(quantiles), 1)", true
	case "p75":
		return "arrayElement(quantilesMerge(0.5,0.75,0.95,0.99)(quantiles), 2)", true
	case "p95":
		return "arrayElement(quantilesMerge(0.5,0.75,0.95,0.99)(quantiles), 3)", true
	case "p99":
		return "arrayElement(quantilesMerge(0.5,0.75,0.95,0.99)(quantiles), 4)", true
	default:
		return "", false
	}
}

// MetricName is a metric the project has emitted, for the chart builder's picker.
type MetricName struct {
	Name string   `json:"name"`
	Type string   `json:"type"`
	Unit string   `json:"unit"`
	Tags []string `json:"tags"`
}

// MetricNames lists the distinct metric names (and their tag keys) a project has
// sent — what the chart builder offers to chart.
func (db *DB) MetricNames(ctx context.Context, projectID uint64) ([]MetricName, error) {
	const q = `
		SELECT name, any(type), any(unit), groupUniqArrayArray(mapKeys(tags))
		FROM metrics_1m
		WHERE project_id = ?
		GROUP BY name
		ORDER BY name
		LIMIT 500`

	rows, err := db.Query(ctx, q, projectID)
	if err != nil {
		return nil, fmt.Errorf("list metric names: %w", err)
	}
	defer rows.Close()

	var out []MetricName
	for rows.Next() {
		var m MetricName
		if err := rows.Scan(&m.Name, &m.Type, &m.Unit, &m.Tags); err != nil {
			return nil, fmt.Errorf("scan metric name: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
