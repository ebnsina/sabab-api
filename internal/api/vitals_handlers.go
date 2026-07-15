package api

import (
	"net/http"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/httpx"
)

// coreWebVitals are the metric names the browser SDK reports Web Vitals under,
// in the order a RUM view shows them (the three Core Web Vitals first).
var coreWebVitals = []string{"web.lcp", "web.inp", "web.cls", "web.fcp", "web.ttfb"}

// vitalResult is one vital's headline figure. p75 is the field the industry
// reports Web Vitals at — the experience at the 75th percentile, so a bad tail
// is not hidden by a good median.
type vitalResult struct {
	Metric string  `json:"metric"`
	P75    float64 `json:"p75"`
	Found  bool    `json:"found"`
}

// handleWebVitals returns the p75 of each Core Web Vital over the window. It
// reuses the metric rollups — Web Vitals are just distribution metrics — so
// there is no separate RUM store.
func (a *API) handleWebVitals(w http.ResponseWriter, r *http.Request, user auth.User) {
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

	from, to := timeRange(r)
	rollup := "1m"
	if to.Sub(from) > 48*time.Hour {
		rollup = "1h"
	}

	results := make([]vitalResult, 0, len(coreWebVitals))
	for _, name := range coreWebVitals {
		v, found, err := a.ch.AggregateMetric(ctx, projectID, name, "p75", from, to, rollup)
		if err != nil {
			httpx.WriteError(w, r, a.log, err)
			return
		}
		results = append(results, vitalResult{Metric: name, P75: v, Found: found})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"vitals": results})
}
