package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ebnsina/sabab-api/internal/query"
	"github.com/google/uuid"
)

// IssueStats are the aggregates behind one row of the issue stream.
type IssueStats struct {
	GroupHash uint64
	TimesSeen uint64
	Users     uint64
	// Sparkline is a bucketed count per hour, oldest first — the little chart
	// that tells you at a glance whether this is spiking or trailing off, which
	// is usually the only thing you want to know from a list.
	Sparkline []uint64
}

// StatsFor reads the pre-aggregated counts for a set of issues.
//
// It reads issue_stats_1h, not `errors`: the materialized view exists precisely
// so the issue list does not scan billions of raw events on every page load
// (query-mv-incremental).
func (db *DB) StatsFor(ctx context.Context, projectID uint64, groupHashes []uint64, from, to time.Time) (map[uint64]IssueStats, error) {
	if len(groupHashes) == 0 {
		return map[uint64]IssueStats{}, nil
	}

	// Two queries rather than one nested aggregate: the totals and the per-hour
	// buckets are different shapes, and expressing both in one statement means a
	// subquery that is harder to read than it is fast. Both hit the same handful
	// of pre-aggregated rows.
	stats, err := db.totals(ctx, projectID, groupHashes, from, to)
	if err != nil {
		return nil, err
	}
	if err := db.sparklines(ctx, projectID, groupHashes, from, to, stats); err != nil {
		return nil, err
	}
	return stats, nil
}

func (db *DB) totals(ctx context.Context, projectID uint64, groupHashes []uint64, from, to time.Time) (map[uint64]IssueStats, error) {
	const q = `
		SELECT group_hash,
		       countMerge(times_seen) AS seen,
		       uniqMerge(users_state) AS users
		FROM issue_stats_1h
		WHERE project_id = ? AND group_hash IN (?) AND hour >= ? AND hour < ?
		GROUP BY group_hash`

	rows, err := db.Query(ctx, q, projectID, groupHashes, from, to)
	if err != nil {
		return nil, fmt.Errorf("read issue totals: %w", err)
	}
	defer rows.Close()

	stats := make(map[uint64]IssueStats, len(groupHashes))
	for rows.Next() {
		var s IssueStats
		if err := rows.Scan(&s.GroupHash, &s.TimesSeen, &s.Users); err != nil {
			return nil, fmt.Errorf("scan issue totals: %w", err)
		}
		stats[s.GroupHash] = s
	}
	return stats, rows.Err()
}

func (db *DB) sparklines(ctx context.Context, projectID uint64, groupHashes []uint64, from, to time.Time, into map[uint64]IssueStats) error {
	const q = `
		SELECT group_hash, hour, countMerge(times_seen) AS seen
		FROM issue_stats_1h
		WHERE project_id = ? AND group_hash IN (?) AND hour >= ? AND hour < ?
		GROUP BY group_hash, hour
		ORDER BY hour`

	rows, err := db.Query(ctx, q, projectID, groupHashes, from, to)
	if err != nil {
		return fmt.Errorf("read sparklines: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			hash uint64
			hour time.Time
			seen uint64
		)
		if err := rows.Scan(&hash, &hour, &seen); err != nil {
			return fmt.Errorf("scan sparkline: %w", err)
		}
		s := into[hash]
		s.GroupHash = hash
		s.Sparkline = append(s.Sparkline, seen)
		into[hash] = s
	}
	return rows.Err()
}

// MatchingGroups returns the group hashes whose events match a search.
//
// This is how the search DSL reaches the issue stream. The event-level fields a
// user searches on — browser, tag, user id — live only in ClickHouse, while the
// issue *state* lives in Postgres. So the search runs here, and the resulting
// hashes are handed to the control plane as a filter.
func (db *DB) MatchingGroups(ctx context.Context, sql query.SQL, limit int) ([]uint64, error) {
	q := fmt.Sprintf(`
		SELECT DISTINCT group_hash
		FROM errors
		WHERE %s
		LIMIT %d`, sql.Where, min(max(limit, 1), 1000))

	rows, err := db.Query(ctx, q, sql.Args...)
	if err != nil {
		return nil, fmt.Errorf("search events: %w", err)
	}
	defer rows.Close()

	var hashes []uint64
	for rows.Next() {
		var hash uint64
		if err := rows.Scan(&hash); err != nil {
			return nil, fmt.Errorf("scan group hash: %w", err)
		}
		hashes = append(hashes, hash)
	}
	return hashes, rows.Err()
}

// Event is one occurrence, as the issue-detail page shows it.
type Event struct {
	EventID     uuid.UUID         `json:"event_id"`
	Timestamp   time.Time         `json:"timestamp"`
	Level       string            `json:"level"`
	Environment string            `json:"environment"`
	Release     string            `json:"release"`
	Platform    string            `json:"platform"`
	Type        string            `json:"exception_type"`
	Value       string            `json:"exception_value"`
	Culprit     string            `json:"culprit"`
	TraceID     uuid.UUID         `json:"trace_id"`
	UserID      string            `json:"user_id,omitempty"`
	UserEmail   string            `json:"user_email,omitempty"`
	Browser     string            `json:"browser,omitempty"`
	OS          string            `json:"os,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	// Raw JSON, passed through to the client, which renders them.
	Stacktrace  string `json:"stacktrace"`
	Breadcrumbs string `json:"breadcrumbs"`
	Contexts    string `json:"contexts"`
}

const selectEvent = `
	SELECT event_id, timestamp, level, environment, release, platform,
	       exception_type, exception_value, culprit, trace_id,
	       user_id, user_email, browser, os, tags,
	       stacktrace, breadcrumbs, contexts
	FROM errors`

// LatestEvent returns the most recent occurrence of an issue — what the detail
// page opens on, because the newest example of a bug is almost always the one
// you want to look at.
func (db *DB) LatestEvent(ctx context.Context, projectID, groupHash uint64) (Event, error) {
	q := selectEvent + `
		WHERE project_id = ? AND group_hash = ?
		ORDER BY timestamp DESC
		LIMIT 1`

	rows, err := db.Query(ctx, q, projectID, groupHash)
	if err != nil {
		return Event{}, fmt.Errorf("read latest event: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return Event{}, ErrNotFound
	}
	e, err := scanEvent(rows)
	if err != nil {
		return Event{}, err
	}
	return e, rows.Err()
}

// SearchEvents returns matching events, newest first.
func (db *DB) SearchEvents(ctx context.Context, sql query.SQL, limit int) ([]Event, error) {
	q := fmt.Sprintf(selectEvent+`
		WHERE %s
		ORDER BY timestamp DESC
		LIMIT %d`, sql.Where, min(max(limit, 1), 200))

	rows, err := db.Query(ctx, q, sql.Args...)
	if err != nil {
		return nil, fmt.Errorf("search events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// rowScanner is satisfied by driver.Rows.
type rowScanner interface{ Scan(dest ...any) error }

func scanEvent(rows rowScanner) (Event, error) {
	var e Event
	err := rows.Scan(
		&e.EventID, &e.Timestamp, &e.Level, &e.Environment, &e.Release, &e.Platform,
		&e.Type, &e.Value, &e.Culprit, &e.TraceID,
		&e.UserID, &e.UserEmail, &e.Browser, &e.OS, &e.Tags,
		&e.Stacktrace, &e.Breadcrumbs, &e.Contexts,
	)
	if err != nil {
		return Event{}, fmt.Errorf("scan event: %w", err)
	}
	return e, nil
}

// FrequentGroup is one group that crossed a frequency threshold.
type FrequentGroup struct {
	GroupHash uint64
	Count     uint64
}

// FrequentGroups returns the groups in a project whose event count since `since`
// is at least `threshold`.
//
// This backs frequency alert rules ("page me when a bug happens more than N
// times in M minutes"). It reads the raw `errors` table rather than the hourly
// MV because alert windows are minutes, finer than the MV's 1h buckets — and the
// window is short, so the scan is bounded by the leading (project_id, hour) sort
// key rather than being a full scan.
func (db *DB) FrequentGroups(ctx context.Context, projectID uint64, since time.Time, threshold uint64) ([]FrequentGroup, error) {
	const q = `
		SELECT group_hash, count() AS c
		FROM errors
		WHERE project_id = ? AND timestamp >= ?
		GROUP BY group_hash
		HAVING c >= ?
		ORDER BY c DESC
		LIMIT 100`

	rows, err := db.Query(ctx, q, projectID, since, threshold)
	if err != nil {
		return nil, fmt.Errorf("query frequent groups: %w", err)
	}
	defer rows.Close()

	var out []FrequentGroup
	for rows.Next() {
		var g FrequentGroup
		if err := rows.Scan(&g.GroupHash, &g.Count); err != nil {
			return nil, fmt.Errorf("scan frequent group: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
