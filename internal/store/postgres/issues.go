package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// IssueUpsert is one occurrence, folded into the issue it belongs to.
type IssueUpsert struct {
	ProjectID  uint64
	GroupHash  string // 16-char hex
	Title      string // "TypeError: Cannot read properties of undefined"
	Culprit    string
	Level      string
	Components []string // why these events group together
	Seen       time.Time
	Release    string
}

// UpsertResult reports what the upsert actually did.
type UpsertResult struct {
	IssueID uint64
	// New is true when this is the first time we have seen this group.
	New bool
	// Regressed is true when a resolved issue started happening again.
	//
	// This is the single most valuable alert an error tracker can send: it means
	// someone believed they fixed a bug, shipped, and were wrong. Detecting it
	// requires knowing the *previous* status, which is why the upsert returns it
	// rather than the caller reading the row back afterwards and racing.
	Regressed bool
	Status    string
}

// UpsertIssue folds an occurrence into its issue, creating it if it is new.
//
// The whole thing is one statement so that concurrent processors handling two
// events of the same issue cannot race: without ON CONFLICT, two workers would
// both see "no row", both insert, and one would fail on the unique constraint —
// losing an event we already told the SDK we had accepted.
//
// times_seen is incremented here rather than counted from ClickHouse because the
// issue stream sorts by it, and a sort key cannot be a cross-database query.
func (db *DB) UpsertIssue(ctx context.Context, in IssueUpsert) (UpsertResult, error) {
	components, err := json.Marshal(in.Components)
	if err != nil {
		return UpsertResult{}, fmt.Errorf("encode group components: %w", err)
	}

	// xmax = 0 identifies a row this statement INSERTed rather than UPDATEd —
	// the standard way to tell the two apart in one round trip.
	//
	// RETURNING can only see the row as it now stands, so it cannot tell us the
	// status the issue had *before* this occurrence. That prior status is the
	// entire regression signal, so it is read first, in the same transaction.
	const query = `
		INSERT INTO issues (
			project_id, group_hash, title, culprit, level, group_components,
			first_seen, last_seen, times_seen, first_release
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7, 1, NULLIF($8, ''))
		ON CONFLICT (project_id, group_hash) DO UPDATE SET
			last_seen  = GREATEST(issues.last_seen, EXCLUDED.last_seen),
			times_seen = issues.times_seen + 1,
			-- A resolved issue happening again is a regression: reopen it.
			-- An ignored issue stays ignored — the user asked for silence, and
			-- overriding that would teach them the button does not work.
			status = CASE WHEN issues.status = 'resolved' THEN 'unresolved' ELSE issues.status END,
			resolved_in_release = CASE WHEN issues.status = 'resolved' THEN NULL ELSE issues.resolved_in_release END,
			-- Keep the newest title and culprit: a symbolicated one is better
			-- than the minified one we may have stored first.
			title   = EXCLUDED.title,
			culprit = EXCLUDED.culprit
		RETURNING id, (xmax = 0) AS is_new, status`

	tx, err := db.Begin(ctx)
	if err != nil {
		return UpsertResult{}, fmt.Errorf("begin issue upsert: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// FOR UPDATE so that two processors handling two occurrences of the same
	// resolved issue serialise here — otherwise both would read "resolved" and
	// both would report a regression, and the user gets paged twice for one.
	var priorStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM issues WHERE project_id = $1 AND group_hash = $2 FOR UPDATE`,
		in.ProjectID, in.GroupHash,
	).Scan(&priorStatus)
	if err != nil && !isNoRows(err) {
		return UpsertResult{}, fmt.Errorf("read prior issue status: %w", err)
	}

	var result UpsertResult
	err = tx.QueryRow(ctx, query,
		in.ProjectID, in.GroupHash, in.Title, in.Culprit, in.Level, components,
		in.Seen, in.Release,
	).Scan(&result.IssueID, &result.New, &result.Status)
	if err != nil {
		return UpsertResult{}, fmt.Errorf("upsert issue: %w", err)
	}

	result.Regressed = priorStatus == "resolved"

	if err := tx.Commit(ctx); err != nil {
		return UpsertResult{}, fmt.Errorf("commit issue upsert: %w", err)
	}
	return result, nil
}

// RecordActivity appends to an issue's audit trail. user_id is nil when Sabab
// itself acted — a regression is detected by the processor, not by a person.
func (db *DB) RecordActivity(ctx context.Context, issueID uint64, kind string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode activity payload: %w", err)
	}
	_, err = db.Exec(ctx,
		`INSERT INTO issue_activity (issue_id, user_id, kind, payload) VALUES ($1, NULL, $2, $3)`,
		issueID, kind, body,
	)
	if err != nil {
		return fmt.Errorf("record issue activity: %w", err)
	}
	return nil
}
