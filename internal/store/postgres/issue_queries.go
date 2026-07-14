package postgres

import (
	"context"
	"fmt"
	"time"
)

// Issue is a row of the issue stream.
type Issue struct {
	ID                uint64     `json:"id"`
	ProjectID         uint64     `json:"project_id"`
	GroupHash         string     `json:"group_hash"`
	Title             string     `json:"title"`
	Culprit           string     `json:"culprit"`
	Level             string     `json:"level"`
	Status            string     `json:"status"`
	FirstSeen         time.Time  `json:"first_seen"`
	LastSeen          time.Time  `json:"last_seen"`
	TimesSeen         uint64     `json:"times_seen"`
	UsersAffected     uint64     `json:"users_affected"`
	FirstRelease      string     `json:"first_release,omitempty"`
	ResolvedInRelease string     `json:"resolved_in_release,omitempty"`
	AssigneeID        *uint64    `json:"assignee_id,omitempty"`
	SnoozeUntil       *time.Time `json:"snooze_until,omitempty"`
	Components        []string   `json:"group_components,omitempty"`
}

// IssueFilter is a request for a page of the issue stream.
type IssueFilter struct {
	ProjectID uint64
	// Status filters the stream. Empty means every status.
	Status string
	// GroupHashes restricts the result to these groups. It is how the search DSL
	// participates: the DSL runs against ClickHouse (which knows browsers, tags
	// and users), and the matching group hashes are fed back in here.
	//
	// nil means "no event-level filter was applied". An EMPTY, non-nil slice
	// means "the search matched nothing" — and those two must not be confused,
	// or a search with no results would silently show every issue instead.
	GroupHashes []string
	Sort        string // last_seen | first_seen | times_seen | users
	Limit       int
	Offset      int
}

// ListIssues returns a page of the issue stream.
func (db *DB) ListIssues(ctx context.Context, f IssueFilter) ([]Issue, error) {
	// The sort column is chosen from a fixed set, never taken from user input:
	// it cannot be a bound parameter, so an allowlist is the only safe way.
	orderBy := "last_seen DESC"
	switch f.Sort {
	case "first_seen":
		orderBy = "first_seen DESC"
	case "times_seen", "frequency":
		orderBy = "times_seen DESC"
	case "users":
		orderBy = "users_affected DESC"
	}

	limit := min(max(f.Limit, 1), 100)

	query := fmt.Sprintf(`
		SELECT id, project_id, group_hash, title, culprit, level, status,
		       first_seen, last_seen, times_seen, users_affected,
		       COALESCE(first_release, ''), COALESCE(resolved_in_release, ''),
		       assignee_id, snooze_until
		FROM issues
		WHERE project_id = $1
		  AND ($2 = '' OR status = $2)
		  AND ($3::text[] IS NULL OR group_hash = ANY($3))
		ORDER BY %s
		LIMIT $4 OFFSET $5`, orderBy)

	// nil stays nil (no filter); an empty slice must stay an empty array so that
	// "= ANY('{}')" matches nothing.
	var hashes any
	if f.GroupHashes != nil {
		hashes = f.GroupHashes
	}

	rows, err := db.Query(ctx, query, f.ProjectID, f.Status, hashes, limit, max(f.Offset, 0))
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	defer rows.Close()

	var issues []Issue
	for rows.Next() {
		var i Issue
		if err := rows.Scan(
			&i.ID, &i.ProjectID, &i.GroupHash, &i.Title, &i.Culprit, &i.Level, &i.Status,
			&i.FirstSeen, &i.LastSeen, &i.TimesSeen, &i.UsersAffected,
			&i.FirstRelease, &i.ResolvedInRelease, &i.AssigneeID, &i.SnoozeUntil,
		); err != nil {
			return nil, fmt.Errorf("scan issue: %w", err)
		}
		issues = append(issues, i)
	}
	return issues, rows.Err()
}

// GetIssue returns one issue, including the components that explain its grouping.
func (db *DB) GetIssue(ctx context.Context, issueID uint64) (Issue, error) {
	const query = `
		SELECT id, project_id, group_hash, title, culprit, level, status,
		       first_seen, last_seen, times_seen, users_affected,
		       COALESCE(first_release, ''), COALESCE(resolved_in_release, ''),
		       assignee_id, snooze_until, group_components
		FROM issues WHERE id = $1`

	var i Issue
	err := db.QueryRow(ctx, query, issueID).Scan(
		&i.ID, &i.ProjectID, &i.GroupHash, &i.Title, &i.Culprit, &i.Level, &i.Status,
		&i.FirstSeen, &i.LastSeen, &i.TimesSeen, &i.UsersAffected,
		&i.FirstRelease, &i.ResolvedInRelease, &i.AssigneeID, &i.SnoozeUntil, &i.Components,
	)
	if err != nil {
		if isNoRows(err) {
			return Issue{}, ErrNotFound
		}
		return Issue{}, fmt.Errorf("get issue: %w", err)
	}
	return i, nil
}

// SetIssueStatus resolves, ignores or reopens an issue.
//
// The release is recorded on resolve so that a later occurrence in a LATER
// release is a regression, while a straggler from an older release is not —
// events from the build you just fixed are expected to keep arriving for a
// while, and paging someone for them would make the feature worthless.
func (db *DB) SetIssueStatus(ctx context.Context, issueID uint64, userID *uint64, status, release string) (Issue, error) {
	const query = `
		UPDATE issues
		SET status = $2,
		    resolved_in_release = CASE WHEN $2 = 'resolved' THEN NULLIF($3, '') ELSE NULL END,
		    snooze_until = NULL
		WHERE id = $1
		RETURNING id`

	tag, err := db.Exec(ctx, query, issueID, status, release)
	if err != nil {
		return Issue{}, fmt.Errorf("set issue status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Issue{}, ErrNotFound
	}

	kind := status
	if status == "unresolved" {
		kind = "unresolved"
	}
	if err := db.recordActivity(ctx, issueID, userID, kind, map[string]string{"release": release}); err != nil {
		return Issue{}, err
	}
	return db.GetIssue(ctx, issueID)
}

// AssignIssue sets or clears the assignee.
func (db *DB) AssignIssue(ctx context.Context, issueID uint64, actorID *uint64, assignee *uint64) (Issue, error) {
	tag, err := db.Exec(ctx, `UPDATE issues SET assignee_id = $2 WHERE id = $1`, issueID, assignee)
	if err != nil {
		return Issue{}, fmt.Errorf("assign issue: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Issue{}, ErrNotFound
	}

	kind := "assigned"
	if assignee == nil {
		kind = "unassigned"
	}
	if err := db.recordActivity(ctx, issueID, actorID, kind, map[string]any{"assignee_id": assignee}); err != nil {
		return Issue{}, err
	}
	return db.GetIssue(ctx, issueID)
}

// Activity is one entry in an issue's audit trail.
type Activity struct {
	ID     uint64    `json:"id"`
	UserID *uint64   `json:"user_id,omitempty"`
	Kind   string    `json:"kind"`
	At     time.Time `json:"at"`
}

// IssueActivity returns the audit trail, newest first.
func (db *DB) IssueActivity(ctx context.Context, issueID uint64, limit int) ([]Activity, error) {
	const query = `
		SELECT id, user_id, kind, at
		FROM issue_activity WHERE issue_id = $1
		ORDER BY at DESC LIMIT $2`

	rows, err := db.Query(ctx, query, issueID, min(max(limit, 1), 100))
	if err != nil {
		return nil, fmt.Errorf("list issue activity: %w", err)
	}
	defer rows.Close()

	var out []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.UserID, &a.Kind, &a.At); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (db *DB) recordActivity(ctx context.Context, issueID uint64, userID *uint64, kind string, payload any) error {
	body, err := encodeJSON(payload)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx,
		`INSERT INTO issue_activity (issue_id, user_id, kind, payload) VALUES ($1, $2, $3, $4)`,
		issueID, userID, kind, body,
	)
	if err != nil {
		return fmt.Errorf("record issue activity: %w", err)
	}
	return nil
}
