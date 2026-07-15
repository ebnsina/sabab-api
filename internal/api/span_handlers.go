package api

import (
	"net/http"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/cursor"
	"github.com/ebnsina/sabab-api/internal/httpx"
	"github.com/ebnsina/sabab-api/internal/query"
	"github.com/google/uuid"
)

// handleTrace returns every span of a trace — the waterfall. The client
// reconstructs the tree from parent_span_id and lays out the timeline from the
// timestamps and durations.
func (a *API) handleTrace(w http.ResponseWriter, r *http.Request, user auth.User) {
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

	traceID, err := uuid.Parse(r.PathValue("trace_id"))
	if err != nil {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_trace", "That is not a valid trace id."))
		return
	}

	spans, err := a.ch.SpansForTrace(ctx, projectID, traceID)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	if len(spans) == 0 {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusNotFound, "no_trace",
			"No spans found for this trace within the retention window."))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"spans": spans})
}

// handleSearchSpans searches spans with the DSL — the entry to tracing, from
// which a user picks a trace to open in the waterfall.
func (a *API) handleSearchSpans(w http.ResponseWriter, r *http.Request, user auth.User) {
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
	parsed, err := query.Parse(r.URL.Query().Get("q"), query.Spans)
	if err != nil {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_query", err.Error()))
		return
	}
	sql, err := query.Compile(parsed, query.Spans, projectID, from, to)
	if err != nil {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_query", err.Error()))
		return
	}

	var cur timeUUIDCursor
	if err := decodeCursor(r, &cur); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	var before *time.Time
	if !cur.T.IsZero() {
		before = &cur.T
	}

	// Only segment (root) spans are listed: one row per trace, so the search
	// results are traces, not the thousands of child spans inside them.
	spans, hasMore, err := a.ch.SearchSegments(ctx, sql, intParam(r, "limit", 50), before, cur.ID)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	next := ""
	if hasMore && len(spans) > 0 {
		last := spans[len(spans)-1]
		next, _ = cursor.Encode(timeUUIDCursor{T: last.Timestamp, ID: last.TraceID})
	}
	httpx.WriteJSON(w, http.StatusOK, paginated("traces", spans, next))
}
