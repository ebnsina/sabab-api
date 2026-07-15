package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/cursor"
	"github.com/ebnsina/sabab-api/internal/httpx"
	"github.com/ebnsina/sabab-api/internal/query"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
	"github.com/google/uuid"
)

// handleSearchLogs runs the log-search DSL and returns a page of lines.
func (a *API) handleSearchLogs(w http.ResponseWriter, r *http.Request, user auth.User) {
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
	sql, err := a.compileLogQuery(r, projectID, from, to)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	var cur timeCursor
	if err := decodeCursor(r, &cur); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	var before *time.Time
	if !cur.T.IsZero() {
		before = &cur.T
	}

	logs, hasMore, err := a.ch.SearchLogs(ctx, sql, intParam(r, "limit", 100), before)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	next := ""
	if hasMore && len(logs) > 0 {
		next, _ = cursor.Encode(timeCursor{T: logs[len(logs)-1].Timestamp})
	}
	httpx.WriteJSON(w, http.StatusOK, paginated("logs", logs, next))
}

// handleTraceLogs returns the logs emitted inside a trace — the jump from an
// error to the logs around it.
func (a *API) handleTraceLogs(w http.ResponseWriter, r *http.Request, user auth.User) {
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

	logs, err := a.ch.LogsForTrace(ctx, projectID, traceID, intParam(r, "limit", 200))
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"logs": logs})
}

// handleTailLogs streams new log lines over Server-Sent Events.
//
// SSE, not WebSocket: the tail is one-directional (server → browser), and SSE is
// plain HTTP that reconnects on its own and needs no protocol upgrade — the
// right tool for "append lines as they arrive". The handler polls ClickHouse on
// a short interval rather than holding a live subscription, because ClickHouse
// has no LISTEN/NOTIFY; the poll is cheap since each round only asks for rows
// newer than the last one seen.
func (a *API) handleTailLogs(w http.ResponseWriter, r *http.Request, user auth.User) {
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusInternalServerError,
			"streaming_unsupported", "Streaming is not available."))
		return
	}

	// The tail filters over a wide-open recent window; the after-cursor does the
	// real narrowing. A bad query here is the user's, so surface it before we
	// switch the response into an event stream.
	now := time.Now().UTC()
	sql, err := a.compileLogQuery(r, projectID, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Defeat proxy buffering, which would otherwise hold events until the
	// connection closed and make the "live" tail arrive all at once.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Start from now: a tail shows what happens next, not the backlog (the search
	// view is for the backlog).
	cursor := now
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// An immediate heartbeat so the client knows the stream is open.
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			// The browser navigated away or closed the tab. Not an error.
			return
		case <-ticker.C:
			logs, err := a.ch.TailLogs(ctx, sql, cursor, 200)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				a.log.Error("tail query failed", slog.Any("error", err))
				// Keep the stream open through a transient blip rather than
				// dropping the user's tail on one failed poll.
				continue
			}
			for _, entry := range logs {
				if entry.Timestamp.After(cursor) {
					cursor = entry.Timestamp
				}
				if err := writeSSE(w, entry); err != nil {
					return
				}
			}
			if len(logs) > 0 {
				flusher.Flush()
			} else {
				// A comment line keeps the connection alive through idle periods
				// so proxies do not time it out.
				fmt.Fprintf(w, ": keep-alive\n\n")
				flusher.Flush()
			}
		}
	}
}

// compileLogQuery parses the ?q= DSL against the logs schema and compiles it,
// mapping a query error to a 400 the client can show inline.
func (a *API) compileLogQuery(r *http.Request, projectID uint64, from, to time.Time) (query.SQL, error) {
	parsed, err := query.Parse(r.URL.Query().Get("q"), query.Logs)
	if err != nil {
		return query.SQL{}, httpx.NewError(http.StatusBadRequest, "bad_query", err.Error())
	}
	sql, err := query.Compile(parsed, query.Logs, projectID, from, to)
	if err != nil {
		return query.SQL{}, httpx.NewError(http.StatusBadRequest, "bad_query", err.Error())
	}
	return sql, nil
}

// writeSSE emits one log entry as a JSON `data:` event.
func writeSSE(w http.ResponseWriter, entry clickhouse.LogEntry) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", body)
	return err
}
