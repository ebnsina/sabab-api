package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AlertRule is one configured rule.
type AlertRule struct {
	ID              uint64          `json:"id"`
	ProjectID       uint64          `json:"project_id"`
	Name            string          `json:"name"`
	Kind            string          `json:"kind"` // new_issue|regression|frequency|metric
	Conditions      json.RawMessage `json:"conditions"`
	Channels        json.RawMessage `json:"channels"`
	ThrottleSeconds int             `json:"throttle_seconds"`
	Enabled         bool            `json:"enabled"`
}

// EnabledRulesFor returns the enabled rules of a given kind for a project.
//
// Called on every alert signal, so it is a narrow, indexed lookup — the partial
// index `alert_rules_enabled_idx WHERE enabled` serves exactly this.
func (db *DB) EnabledRulesFor(ctx context.Context, projectID uint64, kind string) ([]AlertRule, error) {
	const query = `
		SELECT id, project_id, name, kind, conditions, channels, throttle_seconds, enabled
		FROM alert_rules
		WHERE project_id = $1 AND kind = $2 AND enabled = true`

	return db.scanRules(ctx, query, projectID, kind)
}

// EnabledRulesOfKind returns every enabled rule of a kind across all projects.
// The frequency evaluator uses it to sweep all frequency rules on its timer.
func (db *DB) EnabledRulesOfKind(ctx context.Context, kind string) ([]AlertRule, error) {
	const query = `
		SELECT id, project_id, name, kind, conditions, channels, throttle_seconds, enabled
		FROM alert_rules
		WHERE kind = $1 AND enabled = true`

	return db.scanRules(ctx, query, kind)
}

func (db *DB) scanRules(ctx context.Context, query string, args ...any) ([]AlertRule, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list alert rules: %w", err)
	}
	defer rows.Close()

	var rules []AlertRule
	for rows.Next() {
		var r AlertRule
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Name, &r.Kind,
			&r.Conditions, &r.Channels, &r.ThrottleSeconds, &r.Enabled); err != nil {
			return nil, fmt.Errorf("scan alert rule: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// ClaimAlert atomically records that a rule fired for a target, but only if it
// has not fired within its throttle window.
//
// This is the throttle, and it MUST be atomic: two alerter replicas processing
// the same regression at the same moment would otherwise both check "last fired"
// (see nothing), both decide to fire, and page the on-call engineer twice. The
// INSERT ... WHERE NOT EXISTS makes the check-and-record one statement, so
// exactly one replica wins.
//
// dedupeKey scopes the throttle: for a new-issue/regression alert it is the
// issue id, so two different issues under one rule each alert; for a frequency
// alert it is the rule alone, so the whole rule is throttled together.
// Returns true if the caller should send the notification.
func (db *DB) ClaimAlert(ctx context.Context, ruleID uint64, dedupeKey string, throttle time.Duration, payload any) (bool, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode alert payload: %w", err)
	}

	// The dedupe key rides in the payload so the WHERE clause can find the last
	// fire for this specific target.
	const query = `
		INSERT INTO alert_history (rule_id, issue_id, payload)
		SELECT $1, $2, $3
		WHERE NOT EXISTS (
			SELECT 1 FROM alert_history
			WHERE rule_id = $1
			  AND payload->>'dedupe_key' = $4
			  AND fired_at > now() - ($5::double precision * interval '1 second')
		)
		RETURNING id`

	var issueID any
	if id := issueIDFromKey(dedupeKey); id != 0 {
		issueID = id
	}

	var insertedID uint64
	err = db.QueryRow(ctx, query, ruleID, issueID, body, dedupeKey, throttle.Seconds()).Scan(&insertedID)
	if err != nil {
		if isNoRows(err) {
			// The WHERE NOT EXISTS blocked the insert: already fired inside the
			// throttle window. Not an error — the correct, quiet outcome.
			return false, nil
		}
		return false, fmt.Errorf("claim alert: %w", err)
	}
	return true, nil
}

// CreateAlertRule inserts a rule.
func (db *DB) CreateAlertRule(ctx context.Context, r AlertRule) (AlertRule, error) {
	const query = `
		INSERT INTO alert_rules (project_id, name, kind, conditions, channels, throttle_seconds, enabled)
		VALUES ($1, $2, $3, COALESCE($4, '{}'::jsonb), COALESCE($5, '[]'::jsonb), $6, $7)
		RETURNING id, project_id, name, kind, conditions, channels, throttle_seconds, enabled`

	var out AlertRule
	err := db.QueryRow(ctx, query,
		r.ProjectID, r.Name, r.Kind, nullJSON(r.Conditions), nullJSON(r.Channels),
		throttleOrDefault(r.ThrottleSeconds), r.Enabled,
	).Scan(&out.ID, &out.ProjectID, &out.Name, &out.Kind,
		&out.Conditions, &out.Channels, &out.ThrottleSeconds, &out.Enabled)
	if err != nil {
		return AlertRule{}, fmt.Errorf("create alert rule: %w", err)
	}
	return out, nil
}

// ListAlertRules returns every rule for a project, for the settings UI.
func (db *DB) ListAlertRules(ctx context.Context, projectID uint64) ([]AlertRule, error) {
	const query = `
		SELECT id, project_id, name, kind, conditions, channels, throttle_seconds, enabled
		FROM alert_rules WHERE project_id = $1 ORDER BY created_at`
	return db.scanRules(ctx, query, projectID)
}

// DeleteAlertRule removes a rule.
func (db *DB) DeleteAlertRule(ctx context.Context, ruleID, projectID uint64) error {
	tag, err := db.Exec(ctx,
		`DELETE FROM alert_rules WHERE id = $1 AND project_id = $2`, ruleID, projectID)
	if err != nil {
		return fmt.Errorf("delete alert rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAlertRuleEnabled toggles a rule without deleting it.
func (db *DB) SetAlertRuleEnabled(ctx context.Context, ruleID, projectID uint64, enabled bool) error {
	tag, err := db.Exec(ctx,
		`UPDATE alert_rules SET enabled = $3 WHERE id = $1 AND project_id = $2`,
		ruleID, projectID, enabled)
	if err != nil {
		return fmt.Errorf("update alert rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func throttleOrDefault(seconds int) int {
	if seconds <= 0 {
		return 3600
	}
	return seconds
}

func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

// issueIDFromKey extracts an issue id from a dedupe key of the form "issue:123",
// so the alert_history.issue_id foreign key is populated for issue-scoped
// alerts and left null for rule-scoped ones.
func issueIDFromKey(key string) uint64 {
	var id uint64
	if _, err := fmt.Sscanf(key, "issue:%d", &id); err != nil {
		return 0
	}
	return id
}
