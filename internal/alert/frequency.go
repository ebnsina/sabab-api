package alert

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ebnsina/sabab-api/internal/grouping"
	"github.com/ebnsina/sabab-api/internal/notify"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

// FrequencyStore is the extra surface the frequency evaluator needs beyond
// RuleStore.
type FrequencyStore interface {
	RuleStore
	ProjectIDsWithEnabledRules(ctx context.Context, kind string) ([]uint64, error)
	IssuesByGroupHashes(ctx context.Context, projectID uint64, hashes []string) (map[string]postgres.Issue, error)
}

// EventCounter is the event-plane half: how many times each group fired in a
// window.
type EventCounter interface {
	FrequentGroups(ctx context.Context, projectID uint64, since time.Time, threshold uint64) ([]FrequentGroup, error)
}

// FrequentGroup mirrors the ClickHouse result so this package does not import
// the store's type into its interface.
type FrequentGroup struct {
	GroupHash uint64
	Count     uint64
}

// EvaluateFrequency runs one pass of every frequency rule.
//
// Called on a timer. It is deliberately simple and stateless: each pass asks
// "which groups have exceeded their threshold in the window right now?", and the
// throttle (claimed atomically, same as event-driven alerts) is what stops it
// re-firing every tick while a spike is ongoing. Without the throttle a rule
// with a 5-minute window evaluated every minute would fire five times for one
// spike.
func (e *Engine) EvaluateFrequency(ctx context.Context, store FrequencyStore, counter EventCounter, now time.Time) error {
	projectIDs, err := store.ProjectIDsWithEnabledRules(ctx, "frequency")
	if err != nil {
		return fmt.Errorf("list projects with frequency rules: %w", err)
	}

	for _, projectID := range projectIDs {
		if err := e.evaluateProject(ctx, store, counter, projectID, now); err != nil {
			// One project's failure must not stop the others: a bad rule or a
			// transient query error should not silence every other project's
			// alerting.
			e.log.Error("frequency evaluation failed for project",
				slog.Uint64("project_id", projectID), slog.Any("error", err))
		}
	}
	return nil
}

func (e *Engine) evaluateProject(ctx context.Context, store FrequencyStore, counter EventCounter, projectID uint64, now time.Time) error {
	rules, err := store.EnabledRulesFor(ctx, projectID, "frequency")
	if err != nil {
		return err
	}

	projectName := e.projectName(ctx, projectID)

	for _, rule := range rules {
		conds, err := parseConditions(rule.Conditions)
		if err != nil {
			e.log.Warn("skipping frequency rule with bad conditions",
				slog.Uint64("rule_id", rule.ID), slog.Any("error", err))
			continue
		}

		threshold := conds.Threshold
		window := time.Duration(conds.WindowMinutes) * time.Minute
		if threshold == 0 || window <= 0 {
			// A frequency rule with no threshold or window is meaningless and
			// would either never fire or fire constantly; skip it loudly.
			e.log.Warn("frequency rule missing threshold or window", slog.Uint64("rule_id", rule.ID))
			continue
		}

		groups, err := counter.FrequentGroups(ctx, projectID, now.Add(-window), threshold)
		if err != nil {
			return fmt.Errorf("count frequent groups: %w", err)
		}
		if len(groups) == 0 {
			continue
		}

		// Resolve the spiking hashes to issues so the alert can name them.
		hashes := make([]string, 0, len(groups))
		countByHash := make(map[string]uint64, len(groups))
		for _, g := range groups {
			hex := grouping.Hex(g.GroupHash)
			hashes = append(hashes, hex)
			countByHash[hex] = g.Count
		}
		issues, err := store.IssuesByGroupHashes(ctx, projectID, hashes)
		if err != nil {
			return fmt.Errorf("resolve spiking issues: %w", err)
		}

		windowLabel := fmt.Sprintf("%dm", conds.WindowMinutes)
		for hex, issue := range issues {
			// A level filter narrows a frequency rule to, say, only fatal spikes.
			if conds.Level != "" && !strings.EqualFold(conds.Level, issue.Level) {
				continue
			}

			n := notify.Notification{
				Reason:      "High frequency",
				ProjectName: projectName,
				Title:       issue.Title,
				Culprit:     issue.Culprit,
				Level:       issue.Level,
				Environment: conds.Environment,
				Count:       countByHash[hex],
				Window:      windowLabel,
				URL:         e.issueURL(issue.ID),
				FiredAt:     now,
			}
			// Dedupe per issue AND per rule, so the same spike does not re-fire
			// every tick — the throttle window carries it.
			e.fire(ctx, rule, fmt.Sprintf("freq:%d:issue:%d", rule.ID, issue.ID), n)
		}
	}
	return nil
}
