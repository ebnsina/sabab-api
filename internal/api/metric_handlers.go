package api

import (
	"net/http"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/httpx"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
)

// handleListMetrics returns the metric names a project has emitted — what the
// chart builder offers to chart.
func (a *API) handleListMetrics(w http.ResponseWriter, r *http.Request, user auth.User) {
	projectID, err := pathUint(r, "project_id")
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	if err := a.authorizeProject(r.Context(), user, projectID); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	names, err := a.ch.MetricNames(r.Context(), projectID)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"metrics": orEmpty(names)})
}

// handleQueryMetric returns a metric time series for the chart.
func (a *API) handleQueryMetric(w http.ResponseWriter, r *http.Request, user auth.User) {
	ctx := r.Context()

	projectID, err := pathUint(r, "project_id")
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	if err := a.authorizeProject(ctx, user, projectID); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_query", "A metric name is required."))
		return
	}

	from, to := timeRange(r)
	// Pick the rollup by window width: minute buckets keep a day's chart crisp,
	// hour buckets keep a month's chart from returning tens of thousands of rows.
	rollup := "1m"
	if to.Sub(from) > 48*time.Hour {
		rollup = "1h"
	}

	points, err := a.ch.QueryMetric(ctx, clickhouse.MetricQuery{
		ProjectID: projectID,
		Name:      name,
		Agg:       r.URL.Query().Get("agg"),
		GroupBy:   r.URL.Query().Get("group_by"),
		From:      from,
		To:        to,
		Rollup:    rollup,
	})
	if err != nil {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_query", err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"points": orEmpty(points), "rollup": rollup})
}
