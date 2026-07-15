package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/httpx"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

// validAlertKinds are the rule kinds the UI may create.
var validAlertKinds = map[string]bool{
	"new_issue":  true,
	"regression": true,
	"frequency":  true,
	"metric":     true,
}

// validMetricAggs are the aggregations a metric rule may threshold on — the same
// fixed set the rollup reader serves.
var validMetricAggs = map[string]bool{
	"sum": true, "count": true, "avg": true, "min": true, "max": true,
	"uniq": true, "p50": true, "p75": true, "p95": true, "p99": true,
}

var validMetricOperators = map[string]bool{"gt": true, "gte": true, "lt": true, "lte": true}

// metricConditions is the subset of a rule's conditions a metric rule requires.
type metricConditions struct {
	MetricName    string  `json:"metric_name"`
	Agg           string  `json:"agg"`
	Operator      string  `json:"operator"`
	Value         float64 `json:"value"`
	WindowMinutes int     `json:"window_minutes"`
}

// validateMetricRule checks a metric rule's conditions at create time, so a rule
// that could never evaluate is rejected up front rather than skipped silently by
// the alerter every tick.
func validateMetricRule(raw json.RawMessage) error {
	var c metricConditions
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			return httpx.NewError(http.StatusBadRequest, "bad_rule", "conditions is not valid JSON.")
		}
	}
	switch {
	case c.MetricName == "":
		return httpx.NewError(http.StatusBadRequest, "bad_rule", "A metric rule needs a metric name.")
	case !validMetricAggs[c.Agg]:
		return httpx.NewError(http.StatusBadRequest, "bad_rule", "agg must be one of sum, count, avg, min, max, uniq, p50, p75, p95, p99.")
	case !validMetricOperators[c.Operator]:
		return httpx.NewError(http.StatusBadRequest, "bad_rule", "operator must be gt, gte, lt or lte.")
	case c.WindowMinutes <= 0:
		return httpx.NewError(http.StatusBadRequest, "bad_rule", "window_minutes must be greater than zero.")
	}
	return nil
}

func (a *API) handleListAlertRules(w http.ResponseWriter, r *http.Request, user auth.User) {
	projectID, err := pathUint(r, "project_id")
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	if err := a.authorizeProject(r.Context(), user, projectID); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	rules, err := a.pg.ListAlertRules(r.Context(), projectID)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

type createRuleRequest struct {
	Name            string          `json:"name"`
	Kind            string          `json:"kind"`
	Conditions      json.RawMessage `json:"conditions"`
	Channels        json.RawMessage `json:"channels"`
	ThrottleSeconds int             `json:"throttle_seconds"`
	Enabled         *bool           `json:"enabled"`
}

func (a *API) handleCreateAlertRule(w http.ResponseWriter, r *http.Request, user auth.User) {
	projectID, err := pathUint(r, "project_id")
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	if err := a.authorizeProject(r.Context(), user, projectID); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	var req createRuleRequest
	if err := decode(r, &req); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	if req.Name == "" {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_rule", "A rule needs a name."))
		return
	}
	if !validAlertKinds[req.Kind] {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_rule",
			"kind must be new_issue, regression, frequency or metric."))
		return
	}
	if req.Kind == "metric" {
		if err := validateMetricRule(req.Conditions); err != nil {
			httpx.WriteError(w, r, a.log, err)
			return
		}
	}
	// A rule with no channels can never notify anyone — reject it rather than
	// let someone believe they are covered when they are not.
	if len(req.Channels) == 0 || string(req.Channels) == "[]" {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_rule",
			"A rule needs at least one channel."))
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	rule, err := a.pg.CreateAlertRule(r.Context(), postgres.AlertRule{
		ProjectID:       projectID,
		Name:            req.Name,
		Kind:            req.Kind,
		Conditions:      req.Conditions,
		Channels:        req.Channels,
		ThrottleSeconds: req.ThrottleSeconds,
		Enabled:         enabled,
	})
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"rule": rule})
}

func (a *API) handleDeleteAlertRule(w http.ResponseWriter, r *http.Request, user auth.User) {
	projectID, ruleID, ok := a.ruleParams(w, r, user)
	if !ok {
		return
	}

	if err := a.pg.DeleteAlertRule(r.Context(), ruleID, projectID); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			httpx.WriteError(w, r, a.log, httpx.ErrNotFound)
			return
		}
		httpx.WriteError(w, r, a.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type toggleRuleRequest struct {
	Enabled bool `json:"enabled"`
}

func (a *API) handleToggleAlertRule(w http.ResponseWriter, r *http.Request, user auth.User) {
	projectID, ruleID, ok := a.ruleParams(w, r, user)
	if !ok {
		return
	}

	var req toggleRuleRequest
	if err := decode(r, &req); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	if err := a.pg.SetAlertRuleEnabled(r.Context(), ruleID, projectID, req.Enabled); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			httpx.WriteError(w, r, a.log, httpx.ErrNotFound)
			return
		}
		httpx.WriteError(w, r, a.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": req.Enabled})
}

// ruleParams resolves and authorises a project+rule path pair, so the two
// rule-scoped handlers do not repeat the checks.
func (a *API) ruleParams(w http.ResponseWriter, r *http.Request, user auth.User) (projectID, ruleID uint64, ok bool) {
	projectID, err := pathUint(r, "project_id")
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return 0, 0, false
	}
	if err := a.authorizeProject(r.Context(), user, projectID); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return 0, 0, false
	}
	ruleID, err = pathUint(r, "rule_id")
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return 0, 0, false
	}
	return projectID, ruleID, true
}
