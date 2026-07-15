package api

import (
	"net/http"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/httpx"
)

// environmentsWindow is how far back we look for distinct environments. A month
// is long enough that a staging environment which went quiet for the weekend
// still appears in the switcher, without scanning the whole retention.
const environmentsWindow = 30 * 24 * time.Hour

// handleEnvironments lists the environments a project has sent data to recently,
// for the environment filter. It never fails the page: an error or an empty
// result both return an empty list, because "no environments to pick from" is a
// valid state, not an error worth blocking on.
func (a *API) handleEnvironments(w http.ResponseWriter, r *http.Request, user auth.User) {
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

	since := time.Now().UTC().Add(-environmentsWindow)
	envs, err := a.ch.Environments(ctx, projectID, since)
	if err != nil {
		a.log.Warn("list environments failed", "project_id", projectID, "error", err)
		envs = nil
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"environments": orEmpty(envs)})
}
