package alert

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ebnsina/sabab-api/internal/notify"
)

// MetricStore is the control-plane surface the metric evaluator needs beyond
// RuleStore: which projects have a metric rule to evaluate this pass.
type MetricStore interface {
	RuleStore
	ProjectIDsWithEnabledRules(ctx context.Context, kind string) ([]uint64, error)
}

// MetricReader is the event-plane half: reduce a metric to a single value over a
// window, so the evaluator can compare it to a threshold.
type MetricReader interface {
	AggregateMetric(ctx context.Context, projectID uint64, name, agg string, from, to time.Time, rollup string) (float64, bool, error)
}

// metricRollupCutover mirrors the dashboard: past this window width the query
// switches to hourly buckets so a long window stays cheap.
const metricRollupCutover = 48 * time.Hour

// EvaluateMetrics runs one pass of every metric-threshold rule.
//
// Like the frequency evaluator it is stateless and timer-driven: each pass asks
// "is the metric over threshold right now?", and the atomic throttle claim is
// what stops a still-breaching metric from re-paging every tick.
func (e *Engine) EvaluateMetrics(ctx context.Context, store MetricStore, reader MetricReader, now time.Time) error {
	projectIDs, err := store.ProjectIDsWithEnabledRules(ctx, "metric")
	if err != nil {
		return fmt.Errorf("list projects with metric rules: %w", err)
	}

	for _, projectID := range projectIDs {
		if err := e.evaluateMetricProject(ctx, store, reader, projectID, now); err != nil {
			// One project's failure must not silence the others.
			e.log.Error("metric evaluation failed for project",
				slog.Uint64("project_id", projectID), slog.Any("error", err))
		}
	}
	return nil
}

func (e *Engine) evaluateMetricProject(ctx context.Context, store MetricStore, reader MetricReader, projectID uint64, now time.Time) error {
	rules, err := store.EnabledRulesFor(ctx, projectID, "metric")
	if err != nil {
		return err
	}

	projectName := e.projectName(ctx, projectID)

	for _, rule := range rules {
		conds, err := parseConditions(rule.Conditions)
		if err != nil {
			e.log.Warn("skipping metric rule with bad conditions",
				slog.Uint64("rule_id", rule.ID), slog.Any("error", err))
			continue
		}

		window := time.Duration(conds.WindowMinutes) * time.Minute
		if conds.MetricName == "" || conds.Agg == "" || conds.Operator == "" || window <= 0 {
			e.log.Warn("metric rule missing name, agg, operator or window", slog.Uint64("rule_id", rule.ID))
			continue
		}

		rollup := "1m"
		if window > metricRollupCutover {
			rollup = "1h"
		}

		observed, found, err := reader.AggregateMetric(ctx, projectID, conds.MetricName, conds.Agg, now.Add(-window), now, rollup)
		if err != nil {
			return fmt.Errorf("read metric %q: %w", conds.MetricName, err)
		}
		// No data in the window is not a breach — a rule must not page because a
		// metric went quiet.
		if !found || !conds.breached(observed) {
			continue
		}

		windowLabel := fmt.Sprintf("%dm", conds.WindowMinutes)
		n := notify.Notification{
			Reason:      "Metric threshold",
			ProjectName: projectName,
			Title:       rule.Name,
			// e.g. "p95(api.latency) > 500 — 812 over 5m"
			Culprit: fmt.Sprintf("%s(%s) %s %s — %s over %s",
				conds.Agg, conds.MetricName, operatorSymbol(conds.Operator),
				trimFloat(conds.Value), trimFloat(observed), windowLabel),
			Window:  windowLabel,
			URL:     e.metricURL(projectID, conds.MetricName),
			FiredAt: now,
		}

		// One claim per rule: while the metric stays over threshold the throttle
		// window carries it, so it pages once, not every tick.
		e.fire(ctx, rule, fmt.Sprintf("metric:%d", rule.ID), n)
	}
	return nil
}

// metricURL deep-links to the metric's chart on the dashboard.
func (e *Engine) metricURL(projectID uint64, metricName string) string {
	if e.dashboardURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/projects/%d/metrics?name=%s",
		strings.TrimRight(e.dashboardURL, "/"), projectID, url.QueryEscape(metricName))
}

// trimFloat formats a float without trailing zeros — "500", not "500.000000".
func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
