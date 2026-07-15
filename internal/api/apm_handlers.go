package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/httpx"
)

// defaultApdexT is the Apdex threshold when the caller does not set one. 500ms is
// the conventional "satisfied" bar for a web request.
const defaultApdexT = 500 * time.Millisecond

// handleTransactions lists endpoints with latency, throughput, failure rate and
// Apdex — the performance overview.
func (a *API) handleTransactions(w http.ResponseWriter, r *http.Request, user auth.User) {
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
	apdexT := defaultApdexT
	if raw := r.URL.Query().Get("apdex_t_ms"); raw != "" {
		if ms, perr := strconv.Atoi(raw); perr == nil && ms > 0 {
			apdexT = time.Duration(ms) * time.Millisecond
		}
	}

	txns, err := a.ch.Transactions(ctx, projectID, from, to, apdexT, r.URL.Query().Get("sort"), 200)
	if err != nil {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_query", err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"transactions": txns,
		"apdex_t_ms":   apdexT.Milliseconds(),
	})
}

// handleTransactionSamples returns the slowest sample traces for one endpoint.
func (a *API) handleTransactionSamples(w http.ResponseWriter, r *http.Request, user auth.User) {
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
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_query", "An endpoint name is required."))
		return
	}

	from, to := timeRange(r)
	samples, err := a.ch.TransactionSamples(ctx, projectID, name, from, to, 20)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"samples": samples})
}

// handleSlowQueries ranks database statements by the total time spent in them.
func (a *API) handleSlowQueries(w http.ResponseWriter, r *http.Request, user auth.User) {
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
	queries, err := a.ch.SlowQueries(ctx, projectID, from, to, 50)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"queries": queries})
}
