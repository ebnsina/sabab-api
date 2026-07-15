package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/httpx"
)

type createProjectRequest struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

// handleCreateProject creates a project in the user's org, mints its first
// ingest key, and returns it with a ready-to-paste DSN — so a new project is one
// request away from sending data.
func (a *API) handleCreateProject(w http.ResponseWriter, r *http.Request, user auth.User) {
	ctx := r.Context()

	var req createProjectRequest
	if err := decode(r, &req); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusBadRequest, "bad_project", "A project needs a name."))
		return
	}
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		platform = "javascript"
	}

	orgID, err := a.pg.OrgForUser(ctx, user.ID)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	project, err := a.pg.CreateProject(ctx, orgID, makeSlug(name), name, platform)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	key, err := auth.NewIngestKey()
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	if err := a.pg.CreateIngestKey(ctx, project.ID, key, "default"); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"project": project,
		"dsn":     a.buildDSN(key, project.ID),
	})
}

// makeSlug turns a project name into a valid slug (^[a-z0-9]+(-[a-z0-9]+)*$),
// with a short random suffix so it is unique within the org without a lookup.
func makeSlug(name string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		case !prevHyphen && b.Len() > 0:
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if len(base) < 2 {
		base = "project"
	}
	if len(base) > 55 {
		base = strings.Trim(base[:55], "-")
	}

	suffix := make([]byte, 2)
	_, _ = rand.Read(suffix)
	return base + "-" + hex.EncodeToString(suffix)
}

// keyResponse is one ingest key with the ready-to-paste DSN built from it.
type keyResponse struct {
	PublicKey string    `json:"public_key"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
	DSN       string    `json:"dsn"`
}

// handleProjectKeys returns a project's live ingest keys, each with its DSN — the
// one string a user pastes into their SDK to start sending data.
func (a *API) handleProjectKeys(w http.ResponseWriter, r *http.Request, user auth.User) {
	projectID, err := pathUint(r, "project_id")
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	if err := a.authorizeProject(r.Context(), user, projectID); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	keys, err := a.pg.IngestKeysForProject(r.Context(), projectID)
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	out := make([]keyResponse, 0, len(keys))
	for _, k := range keys {
		out = append(out, keyResponse{
			PublicKey: k.PublicKey,
			Label:     k.Label,
			CreatedAt: k.CreatedAt,
			DSN:       a.buildDSN(k.PublicKey, projectID),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"keys": out})
}

// buildDSN turns the ingest URL and a public key into the DSN the SDK parses:
//
//	http://pk_live_…@host:port/<projectId>
//
// The key rides in the userinfo, matching parseDsn in the SDK.
func (a *API) buildDSN(publicKey string, projectID uint64) string {
	// Split scheme from host so the key can be injected as userinfo.
	scheme, host := "http", strings.TrimRight(a.ingestURL, "/")
	if i := strings.Index(host, "://"); i >= 0 {
		scheme, host = host[:i], host[i+3:]
	}
	return scheme + "://" + publicKey + "@" + host + "/" + strconv.FormatUint(projectID, 10)
}
