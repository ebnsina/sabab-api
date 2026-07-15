package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/httpx"
)

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
