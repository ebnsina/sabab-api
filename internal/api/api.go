// Package api serves the dashboard.
//
// Two rules run through every handler here:
//
//   - Every project-scoped request is checked against org membership. Without
//     that check, an authenticated user of one org reads another org's issues by
//     changing a number in the URL. It is the tenancy boundary, and it is not
//     optional on any route.
//   - Every user value that reaches the database is a bound parameter. The
//     search box is the most obvious injection surface in the product.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/health"
	"github.com/ebnsina/sabab-api/internal/httpx"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

// API holds the dashboard's dependencies.
type API struct {
	pg       *postgres.DB
	ch       *clickhouse.DB
	sessions *auth.Sessions
	health   *health.Checker
	log      *slog.Logger
	// devOrigin is the SvelteKit dev server, allowed through CORS with
	// credentials so the dashboard can be developed against a running API.
	devOrigin string
}

// New builds the API.
func New(pg *postgres.DB, ch *clickhouse.DB, sessions *auth.Sessions, checker *health.Checker, devOrigin string, log *slog.Logger) *API {
	return &API{pg: pg, ch: ch, sessions: sessions, health: checker, devOrigin: devOrigin, log: log}
}

// Handler returns the wired HTTP handler.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	a.health.Routes(mux)

	// Auth. Open by necessity — you cannot require a session to log in.
	mux.HandleFunc("POST /api/v1/auth/login", a.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", a.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/me", a.authenticated(a.handleMe))

	// Projects.
	mux.HandleFunc("GET /api/v1/projects", a.authenticated(a.handleListProjects))

	// Issues. The stream and its filters.
	mux.HandleFunc("GET /api/v1/projects/{project_id}/issues", a.authenticated(a.handleListIssues))
	mux.HandleFunc("GET /api/v1/projects/{project_id}/events", a.authenticated(a.handleSearchEvents))

	mux.HandleFunc("GET /api/v1/issues/{issue_id}", a.authenticated(a.handleGetIssue))
	mux.HandleFunc("GET /api/v1/issues/{issue_id}/latest-event", a.authenticated(a.handleLatestEvent))
	mux.HandleFunc("POST /api/v1/issues/{issue_id}/status", a.authenticated(a.handleSetStatus))
	mux.HandleFunc("POST /api/v1/issues/{issue_id}/assign", a.authenticated(a.handleAssign))

	// Logs.
	mux.HandleFunc("GET /api/v1/projects/{project_id}/logs", a.authenticated(a.handleSearchLogs))
	mux.HandleFunc("GET /api/v1/projects/{project_id}/logs/tail", a.authenticated(a.handleTailLogs))
	mux.HandleFunc("GET /api/v1/projects/{project_id}/traces/{trace_id}/logs", a.authenticated(a.handleTraceLogs))

	// Traces / spans.
	mux.HandleFunc("GET /api/v1/projects/{project_id}/spans", a.authenticated(a.handleSearchSpans))
	mux.HandleFunc("GET /api/v1/projects/{project_id}/traces/{trace_id}", a.authenticated(a.handleTrace))

	// Metrics.
	mux.HandleFunc("GET /api/v1/projects/{project_id}/metrics", a.authenticated(a.handleListMetrics))
	mux.HandleFunc("GET /api/v1/projects/{project_id}/metrics/query", a.authenticated(a.handleQueryMetric))

	// Performance (APM) — aggregation over spans.
	mux.HandleFunc("GET /api/v1/projects/{project_id}/performance/transactions", a.authenticated(a.handleTransactions))
	mux.HandleFunc("GET /api/v1/projects/{project_id}/performance/transactions/samples", a.authenticated(a.handleTransactionSamples))
	mux.HandleFunc("GET /api/v1/projects/{project_id}/performance/queries", a.authenticated(a.handleSlowQueries))
	mux.HandleFunc("GET /api/v1/projects/{project_id}/performance/n-plus-one", a.authenticated(a.handleNPlusOne))

	// Alert rules.
	mux.HandleFunc("GET /api/v1/projects/{project_id}/alert-rules", a.authenticated(a.handleListAlertRules))
	mux.HandleFunc("POST /api/v1/projects/{project_id}/alert-rules", a.authenticated(a.handleCreateAlertRule))
	mux.HandleFunc("DELETE /api/v1/projects/{project_id}/alert-rules/{rule_id}", a.authenticated(a.handleDeleteAlertRule))
	mux.HandleFunc("POST /api/v1/projects/{project_id}/alert-rules/{rule_id}/toggle", a.authenticated(a.handleToggleAlertRule))

	mux.HandleFunc("/", httpx.NotFound(a.log))

	return httpx.Chain(mux,
		httpx.Recover(a.log),
		httpx.LogRequests(a.log),
		a.cors(),
	)
}

// authenticated wraps a handler so it only runs for a logged-in user, and hands
// it the user rather than making every handler re-derive it.
func (a *API) authenticated(next func(http.ResponseWriter, *http.Request, auth.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := a.sessions.Authenticate(r)
		if err != nil {
			httpx.WriteError(w, r, a.log, httpx.ErrUnauthorized)
			return
		}
		next(w, r, user)
	}
}

// authorizeProject is the tenancy boundary. Every project-scoped handler calls
// it, and it answers 404 — not 403 — for a project the user cannot see, because
// 403 would confirm the project exists.
func (a *API) authorizeProject(ctx context.Context, user auth.User, projectID uint64) error {
	allowed, err := a.pg.CanAccessProject(ctx, user.ID, projectID)
	if err != nil {
		return httpx.Wrap(http.StatusInternalServerError, "internal_error",
			"Something went wrong on our end.", err)
	}
	if !allowed {
		return httpx.ErrNotFound
	}
	return nil
}

// cors allows the SvelteKit dev server through, with credentials.
//
// Exactly one origin, from config — never a wildcard. A wildcard with
// credentials is rejected by browsers anyway, and reflecting the caller's Origin
// header would let any website read a logged-in user's issues.
func (a *API) cors() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if a.devOrigin != "" && origin == a.devOrigin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SweepSessions deletes expired sessions on a timer.
func (a *API) SweepSessions(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := a.pg.SweepSessions(ctx)
			if err != nil {
				a.log.Error("session sweep failed", slog.Any("error", err))
				continue
			}
			if n > 0 {
				a.log.Debug("swept expired sessions", slog.Int64("count", n))
			}
		}
	}
}
