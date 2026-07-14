package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// SessionCookie is the cookie the dashboard authenticates with.
const SessionCookie = "sabab_session"

// SessionTTL is how long a login lasts.
const SessionTTL = 14 * 24 * time.Hour

// ErrNoSession means the request carries no valid session.
var ErrNoSession = errors.New("not authenticated")

// SessionStore persists sessions. An interface so the API is testable without a
// database.
type SessionStore interface {
	CreateSession(ctx context.Context, tokenHash string, userID uint64, expires time.Time, userAgent string) error
	SessionUser(ctx context.Context, tokenHash string) (User, error)
	DeleteSession(ctx context.Context, tokenHash string) error
}

// User is the authenticated principal.
type User struct {
	ID    uint64 `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// Sessions issues and validates session tokens.
type Sessions struct {
	store SessionStore
	// secure marks the cookie Secure, so it is only ever sent over HTTPS. Off in
	// development, because localhost is not HTTPS and a Secure cookie there would
	// simply never be sent — making login appear broken.
	secure bool
}

// NewSessions builds a session manager.
func NewSessions(store SessionStore, secure bool) *Sessions {
	return &Sessions{store: store, secure: secure}
}

// Issue creates a session and returns the cookie to set.
//
// The token is random and stored only as a hash. The plaintext exists in exactly
// one place — the user's cookie — so a database dump yields no working sessions.
func (s *Sessions) Issue(ctx context.Context, user User, userAgent string) (*http.Cookie, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(SessionTTL)

	if err := s.store.CreateSession(ctx, hashToken(token), user.ID, expires, userAgent); err != nil {
		return nil, err
	}

	return &http.Cookie{
		Name:  SessionCookie,
		Value: token,
		Path:  "/",
		// HttpOnly: JavaScript must not be able to read it. This is an
		// observability tool — it runs alongside customer JS by definition, and
		// an XSS in the dashboard must not hand over the session.
		HttpOnly: true,
		Secure:   s.secure,
		// Lax, not None: the dashboard is same-site, and None would let any
		// site's form POST carry the session (CSRF).
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	}, nil
}

// Authenticate resolves the session on a request.
func (s *Sessions) Authenticate(r *http.Request) (User, error) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil || cookie.Value == "" {
		return User{}, ErrNoSession
	}

	user, err := s.store.SessionUser(r.Context(), hashToken(cookie.Value))
	if err != nil {
		return User{}, ErrNoSession
	}
	return user, nil
}

// Revoke deletes the session and returns a cookie that clears it.
func (s *Sessions) Revoke(ctx context.Context, r *http.Request) *http.Cookie {
	if cookie, err := r.Cookie(SessionCookie); err == nil && cookie.Value != "" {
		// A failure to delete is not worth failing the logout over — the cookie
		// is being cleared regardless, and the row expires on its own.
		_ = s.store.DeleteSession(ctx, hashToken(cookie.Value))
	}

	return &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// hashToken is what we store. SHA-256 is right here and Argon2 would be wrong:
// the token is 256 bits of true randomness, so there is no dictionary to attack
// and nothing for a slow hash to buy — while it would be paid on every request.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
