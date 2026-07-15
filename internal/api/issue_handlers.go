package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/cursor"
	"github.com/ebnsina/sabab-api/internal/grouping"
	"github.com/ebnsina/sabab-api/internal/httpx"
	"github.com/ebnsina/sabab-api/internal/query"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

// defaultWindow is how far back the issue stream looks when the caller does not
// say. Every event query must be time-ranged — the ClickHouse ORDER BY starts
// with (project_id, hour), so an unbounded query is a full scan.
const defaultWindow = 14 * 24 * time.Hour

// issueResponse is an issue plus its event-plane aggregates. The two halves come
// from two different databases and are stitched together here, which is the
// whole reason the split exists: mutable state in Postgres, counts in ClickHouse.
type issueResponse struct {
	postgres.Issue
	Sparkline []uint64 `json:"sparkline,omitempty"`
}

func (a *API) handleListIssues(w http.ResponseWriter, r *http.Request, user auth.User) {
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

	sort := r.URL.Query().Get("sort")
	filter := postgres.IssueFilter{
		ProjectID: projectID,
		Status:    r.URL.Query().Get("status"),
		Sort:      sort,
		Limit:     intParam(r, "limit", 50),
	}

	var ic issueCursor
	if err := decodeCursor(r, &ic); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	if len(ic.V) > 0 {
		val, err := parseIssueCursorVal(sort, ic.V)
		if err != nil {
			httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_cursor", "The pagination cursor is invalid."))
			return
		}
		filter.Cursor = &postgres.IssueCursor{Val: val, ID: ic.ID}
	}

	// The search DSL runs against ClickHouse, because the fields people search on
	// — browser, tag, user — only exist on events. The matching groups then filter
	// the Postgres issue list.
	if search := r.URL.Query().Get("q"); search != "" {
		hashes, err := a.searchGroups(r, projectID, search, from, to)
		if err != nil {
			httpx.WriteError(w, r, a.log, err)
			return
		}
		// A non-nil, possibly EMPTY slice: "the search matched nothing" must show
		// nothing, not everything.
		filter.GroupHashes = hashes
	}

	issues, hasMore, err := a.pg.ListIssues(ctx, filter)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	out, err := a.withStats(ctx, projectID, issues, from, to)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	next := ""
	if hasMore && len(issues) > 0 {
		last := issues[len(issues)-1]
		v, _ := json.Marshal(issueSortValue(sort, last))
		next, _ = cursor.Encode(issueCursor{V: v, ID: last.ID})
	}
	httpx.WriteJSON(w, http.StatusOK, paginated("issues", out, next))
}

// issueCursor is the wire form of the issue keyset cursor: the sort value as raw
// JSON (its type depends on the sort) plus the row id.
type issueCursor struct {
	V  json.RawMessage `json:"v"`
	ID uint64          `json:"id"`
}

// parseIssueCursorVal reads the cursor's sort value as the type the chosen sort
// column holds — a count for the count sorts, a time otherwise.
func parseIssueCursorVal(sort string, raw json.RawMessage) (any, error) {
	switch sort {
	case "times_seen", "frequency", "users":
		var n uint64
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		return n, nil
	default:
		var t time.Time
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, err
		}
		return t, nil
	}
}

// issueSortValue is the value of an issue's sort column — what the next cursor
// carries.
func issueSortValue(sort string, i postgres.Issue) any {
	switch sort {
	case "times_seen", "frequency":
		return i.TimesSeen
	case "users":
		return i.UsersAffected
	case "first_seen":
		return i.FirstSeen
	default:
		return i.LastSeen
	}
}

// searchGroups compiles the DSL and asks ClickHouse which groups match.
func (a *API) searchGroups(r *http.Request, projectID uint64, search string, from, to time.Time) ([]string, error) {
	parsed, err := query.Parse(search, query.Errors)
	if err != nil {
		// A bad query is the user's typo, and they need to be told what is wrong.
		// Returning an empty result set instead would leave them staring at "no
		// issues" with no idea why.
		return nil, httpx.NewError(http.StatusBadRequest, "bad_query", err.Error())
	}

	sql, err := query.Compile(parsed, query.Errors, projectID, from, to)
	if err != nil {
		return nil, httpx.NewError(http.StatusBadRequest, "bad_query", err.Error())
	}

	hashes, err := a.ch.MatchingGroups(r.Context(), sql, 1000)
	if err != nil {
		return nil, err
	}

	// Non-nil even when empty — see the caller.
	out := make([]string, 0, len(hashes))
	for _, h := range hashes {
		out = append(out, grouping.Hex(h))
	}
	return out, nil
}

// withStats attaches the sparkline from the materialized view.
func (a *API) withStats(ctx context.Context, projectID uint64, issues []postgres.Issue, from, to time.Time) ([]issueResponse, error) {
	out := make([]issueResponse, 0, len(issues))
	if len(issues) == 0 {
		return out, nil
	}

	hashes := make([]uint64, 0, len(issues))
	for _, issue := range issues {
		h, err := strconv.ParseUint(issue.GroupHash, 16, 64)
		if err != nil {
			continue
		}
		hashes = append(hashes, h)
	}

	stats, err := a.ch.StatsFor(ctx, projectID, hashes, from, to)
	if err != nil {
		// The counts are a nice-to-have; the issue list is not. A ClickHouse blip
		// must not blank the page the user came here for.
		a.log.Warn("issue stats unavailable", slog.Any("error", err))
		stats = nil
	}

	for _, issue := range issues {
		resp := issueResponse{Issue: issue}
		if h, err := strconv.ParseUint(issue.GroupHash, 16, 64); err == nil {
			if s, ok := stats[h]; ok {
				resp.Sparkline = s.Sparkline
			}
		}
		out = append(out, resp)
	}
	return out, nil
}

func (a *API) handleGetIssue(w http.ResponseWriter, r *http.Request, user auth.User) {
	issue, err := a.issueForUser(r, user)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	activity, err := a.pg.IssueActivity(r.Context(), issue.ID, 50)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"issue":    issue,
		"activity": activity,
	})
}

// handleLatestEvent returns the newest occurrence — the one the detail page
// opens on, because the most recent example of a bug is nearly always the one
// you want.
func (a *API) handleLatestEvent(w http.ResponseWriter, r *http.Request, user auth.User) {
	issue, err := a.issueForUser(r, user)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	hash, err := strconv.ParseUint(issue.GroupHash, 16, 64)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	event, err := a.ch.LatestEvent(r.Context(), issue.ProjectID, hash)
	if err != nil {
		if errors.Is(err, clickhouse.ErrNotFound) {
			// The issue exists but its events have aged out of the 90-day TTL.
			// That is a real state, and it needs an honest answer rather than a
			// blank page.
			httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusNotFound, "no_events",
				"This issue has no events within the retention window."))
			return
		}
		httpx.WriteError(w, r, a.log, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"event": event})
}

type statusRequest struct {
	Status  string `json:"status"`
	Release string `json:"release,omitempty"`
}

func (a *API) handleSetStatus(w http.ResponseWriter, r *http.Request, user auth.User) {
	issue, err := a.issueForUser(r, user)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	var req statusRequest
	if err := decode(r, &req); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	switch req.Status {
	case "resolved", "ignored", "unresolved":
	default:
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_status",
			"status must be resolved, ignored or unresolved."))
		return
	}

	updated, err := a.pg.SetIssueStatus(r.Context(), issue.ID, &user.ID, req.Status, req.Release)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"issue": updated})
}

type assignRequest struct {
	// AssigneeID is null to unassign — which is why it is a pointer: absent and
	// "clear it" are different requests.
	AssigneeID *uint64 `json:"assignee_id"`
}

func (a *API) handleAssign(w http.ResponseWriter, r *http.Request, user auth.User) {
	issue, err := a.issueForUser(r, user)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	var req assignRequest
	if err := decode(r, &req); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	// The assignee must themselves be able to see the project, or we would let a
	// user assign an issue to someone outside the org.
	if req.AssigneeID != nil {
		allowed, err := a.pg.CanAccessProject(r.Context(), *req.AssigneeID, issue.ProjectID)
		if err != nil {
			httpx.WriteError(w, r, a.log, err)
			return
		}
		if !allowed {
			httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_assignee",
				"That user does not have access to this project."))
			return
		}
	}

	updated, err := a.pg.AssignIssue(r.Context(), issue.ID, &user.ID, req.AssigneeID)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"issue": updated})
}

func (a *API) handleSearchEvents(w http.ResponseWriter, r *http.Request, user auth.User) {
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

	parsed, err := query.Parse(r.URL.Query().Get("q"), query.Errors)
	if err != nil {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_query", err.Error()))
		return
	}
	sql, err := query.Compile(parsed, query.Errors, projectID, from, to)
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

	events, hasMore, err := a.ch.SearchEvents(ctx, sql, intParam(r, "limit", 50), before, cur.ID)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	next := ""
	if hasMore && len(events) > 0 {
		last := events[len(events)-1]
		next, _ = cursor.Encode(timeUUIDCursor{T: last.Timestamp, ID: last.EventID})
	}
	httpx.WriteJSON(w, http.StatusOK, paginated("events", events, next))
}

func (a *API) handleListProjects(w http.ResponseWriter, r *http.Request, user auth.User) {
	projects, err := a.pg.ProjectsForUser(r.Context(), user.ID)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

// issueForUser loads an issue and checks the caller may see it. Every
// issue-scoped handler goes through here, so the tenancy check cannot be
// forgotten on one route.
func (a *API) issueForUser(r *http.Request, user auth.User) (postgres.Issue, error) {
	issueID, err := pathUint(r, "issue_id")
	if err != nil {
		return postgres.Issue{}, err
	}

	issue, err := a.pg.GetIssue(r.Context(), issueID)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return postgres.Issue{}, httpx.ErrNotFound
		}
		return postgres.Issue{}, err
	}
	if err := a.authorizeProject(r.Context(), user, issue.ProjectID); err != nil {
		return postgres.Issue{}, err
	}
	return issue, nil
}

// --- request helpers --------------------------------------------------------

func pathUint(r *http.Request, name string) (uint64, error) {
	raw := r.PathValue(name)
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, httpx.ErrNotFound
	}
	return id, nil
}

func intParam(r *http.Request, name string, fallback int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// timeRange reads ?from=&to=, defaulting to the last two weeks. It is never
// unbounded: an unbounded event query is a full table scan.
func timeRange(r *http.Request) (time.Time, time.Time) {
	now := time.Now().UTC()
	from, to := now.Add(-defaultWindow), now

	if raw := r.URL.Query().Get("from"); raw != "" {
		if ts, err := time.Parse(time.RFC3339, raw); err == nil {
			from = ts.UTC()
		}
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		if ts, err := time.Parse(time.RFC3339, raw); err == nil {
			to = ts.UTC()
		}
	}
	if !from.Before(to) {
		from, to = now.Add(-defaultWindow), now
	}
	return from, to
}
