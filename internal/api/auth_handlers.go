package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/httpx"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decode(r, &req); err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}

	user, hash, err := a.pg.UserByEmail(r.Context(), req.Email)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			// Same answer as a wrong password, deliberately: telling the caller
			// that no such account exists turns the login form into a way to
			// enumerate which of our users' emails are registered.
			//
			// (An attacker can still time it: a real account pays for an Argon2
			// verify and a missing one does not. Closing that gap means hashing
			// against a dummy on the miss path, which is worth doing before this
			// is exposed to the public internet.)
			httpx.WriteError(w, r, a.log, badCredentials())
			return
		}
		httpx.WriteError(w, r, a.log, err)
		return
	}

	if hash == "" || auth.VerifyPassword(req.Password, hash) != nil {
		httpx.WriteError(w, r, a.log, badCredentials())
		return
	}

	cookie, err := a.sessions.Issue(r.Context(), user, r.UserAgent())
	if err != nil {
		httpx.WriteError(w, r, a.log, err)
		return
	}
	http.SetCookie(w, cookie)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, a.sessions.Revoke(r.Context(), r))
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request, user auth.User) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": user})
}

func badCredentials() error {
	return httpx.NewError(http.StatusUnauthorized, "bad_credentials", "Invalid email or password.")
}

// decode reads a JSON body, rejecting unknown fields so a typo'd key is an
// error the caller can see rather than a setting that silently did nothing.
func decode(r *http.Request, into any) error {
	// Bound the body: an unauthenticated endpoint must not let a caller stream
	// us gigabytes.
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()

	if err := dec.Decode(into); err != nil {
		return httpx.Wrap(http.StatusBadRequest, "malformed_request",
			"The request body is not valid JSON.", err)
	}
	return nil
}
