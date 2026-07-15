// Package alert evaluates rules and fires notifications.
//
// It has two entry points. New-issue and regression alerts are event-driven: the
// processor publishes a signal the moment it detects one, and the alerter reacts
// immediately — a "new issue" that arrives ten minutes late is barely an alert.
// Frequency alerts are evaluated on a timer, because "more than N in M minutes"
// is a question you can only answer by looking back over a window.
//
// One rule runs through both paths: throttling is enforced in the database, with
// an atomic claim, so two alerter replicas cannot page the same person twice for
// the same thing.
package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/notify"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

// RuleStore is the slice of the control plane the engine needs.
type RuleStore interface {
	EnabledRulesFor(ctx context.Context, projectID uint64, kind string) ([]postgres.AlertRule, error)
	ClaimAlert(ctx context.Context, ruleID uint64, dedupeKey string, throttle time.Duration, payload any) (bool, error)
	ProjectByID(ctx context.Context, projectID uint64) (postgres.Project, error)
}

// Engine matches signals to rules and dispatches notifications.
type Engine struct {
	rules      RuleStore
	dispatcher *notify.Dispatcher
	// dashboardURL builds the "view issue" link. Passed in, not read from a
	// global, so the engine is testable and self-host deployments get correct
	// links.
	dashboardURL string
	log          *slog.Logger
}

// NewEngine builds the engine.
func NewEngine(rules RuleStore, dispatcher *notify.Dispatcher, dashboardURL string, log *slog.Logger) *Engine {
	return &Engine{rules: rules, dispatcher: dispatcher, dashboardURL: dashboardURL, log: log}
}

// HandleSignal processes one new-issue or regression signal: find the matching
// rules, and for each that is not throttled, dispatch.
func (e *Engine) HandleSignal(ctx context.Context, sig ingest.AlertSignal) error {
	rules, err := e.rules.EnabledRulesFor(ctx, sig.ProjectID, string(sig.Kind))
	if err != nil {
		return fmt.Errorf("load rules: %w", err)
	}
	if len(rules) == 0 {
		return nil
	}

	projectName := e.projectName(ctx, sig.ProjectID)

	for _, rule := range rules {
		conds, err := parseConditions(rule.Conditions)
		if err != nil {
			e.log.Warn("skipping rule with bad conditions",
				slog.Uint64("rule_id", rule.ID), slog.Any("error", err))
			continue
		}
		// A rule can narrow to an environment, level or release, so a
		// staging-only rule does not page for a production blip and vice versa.
		if !conds.matchesSignal(sig) {
			continue
		}

		n := notify.Notification{
			Reason:      reasonFor(sig.Kind),
			ProjectName: projectName,
			Title:       sig.Title,
			Culprit:     sig.Culprit,
			Level:       sig.Level,
			Release:     sig.Release,
			Environment: sig.Environment,
			URL:         e.issueURL(sig.IssueID),
			FiredAt:     sig.At,
		}

		// Dedupe by issue: two different new issues under one rule each alert,
		// but the same issue does not alert twice inside the throttle window.
		e.fire(ctx, rule, fmt.Sprintf("issue:%d", sig.IssueID), n)
	}
	return nil
}

// fire claims the throttle slot and, if won, dispatches.
func (e *Engine) fire(ctx context.Context, rule postgres.AlertRule, dedupeKey string, n notify.Notification) {
	channels, err := parseChannels(rule.Channels)
	if err != nil {
		e.log.Warn("skipping rule with bad channels",
			slog.Uint64("rule_id", rule.ID), slog.Any("error", err))
		return
	}
	if len(channels) == 0 {
		return
	}

	throttle := time.Duration(rule.ThrottleSeconds) * time.Second

	// The claim is the throttle AND the dedupe, in one atomic step: if another
	// replica already fired this within the window, we get false and stay quiet.
	claimed, err := e.rules.ClaimAlert(ctx, rule.ID, dedupeKey, throttle, map[string]any{
		"dedupe_key": dedupeKey,
		"reason":     n.Reason,
		"title":      n.Title,
	})
	if err != nil {
		e.log.Error("claim alert", slog.Uint64("rule_id", rule.ID), slog.Any("error", err))
		return
	}
	if !claimed {
		e.log.Debug("alert throttled", slog.Uint64("rule_id", rule.ID), slog.String("dedupe", dedupeKey))
		return
	}

	results := e.dispatcher.Dispatch(ctx, channels, n)
	for _, r := range results {
		if r.Err != nil {
			e.log.Error("channel delivery failed",
				slog.Uint64("rule_id", rule.ID),
				slog.String("channel", r.Channel),
				slog.Any("error", r.Err))
		} else {
			e.log.Info("alert sent",
				slog.Uint64("rule_id", rule.ID),
				slog.String("channel", r.Channel),
				slog.String("reason", n.Reason))
		}
	}
}

func (e *Engine) projectName(ctx context.Context, projectID uint64) string {
	project, err := e.rules.ProjectByID(ctx, projectID)
	if err != nil {
		// A missing name is cosmetic; never drop an alert over it.
		return fmt.Sprintf("project %d", projectID)
	}
	return project.Name
}

func (e *Engine) issueURL(issueID uint64) string {
	if e.dashboardURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/issues/%d", strings.TrimRight(e.dashboardURL, "/"), issueID)
}

func reasonFor(kind ingest.AlertKind) string {
	switch kind {
	case ingest.AlertNewIssue:
		return "New issue"
	case ingest.AlertRegression:
		return "Regression"
	default:
		return "Alert"
	}
}

// conditions is the parsed form of a rule's `conditions` JSON.
type conditions struct {
	Environment string `json:"environment"`
	Level       string `json:"level"`
	Release     string `json:"release"`
	// Frequency fields.
	Threshold     uint64 `json:"threshold"`
	WindowMinutes int    `json:"window_minutes"`
}

func parseConditions(raw json.RawMessage) (conditions, error) {
	var c conditions
	if len(raw) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return conditions{}, fmt.Errorf("parse conditions: %w", err)
	}
	return c, nil
}

// matchesSignal reports whether a signal satisfies a rule's narrowing filters.
// An empty filter matches everything — a rule with no conditions fires on any
// signal of its kind.
func (c conditions) matchesSignal(sig ingest.AlertSignal) bool {
	if c.Environment != "" && !strings.EqualFold(c.Environment, sig.Environment) {
		return false
	}
	if c.Level != "" && !strings.EqualFold(c.Level, sig.Level) {
		return false
	}
	if c.Release != "" && c.Release != sig.Release {
		return false
	}
	return true
}

func parseChannels(raw json.RawMessage) ([]notify.ChannelConfig, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var channels []notify.ChannelConfig
	if err := json.Unmarshal(raw, &channels); err != nil {
		return nil, fmt.Errorf("parse channels: %w", err)
	}
	return channels, nil
}
