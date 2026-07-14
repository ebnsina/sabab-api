package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if err := VerifyPassword("correct horse battery staple", hash); err != nil {
		t.Errorf("the correct password was rejected: %v", err)
	}
	if err := VerifyPassword("wrong", hash); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("a wrong password must be ErrBadCredentials, got %v", err)
	}
}

// The salt must be random, or two users with the same password get the same
// hash — which tells an attacker who dumps the table exactly that.
func TestHashesAreSaltedUniquely(t *testing.T) {
	a, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}

	if a == b {
		t.Fatal("two hashes of the same password are identical — the salt is not random")
	}
	// Both must still verify.
	if err := VerifyPassword("same password", a); err != nil {
		t.Error(err)
	}
	if err := VerifyPassword("same password", b); err != nil {
		t.Error(err)
	}
}

// The parameters travel with the hash, so they can be raised later without
// invalidating every existing password.
func TestHashCarriesItsParameters(t *testing.T) {
	hash, err := HashPassword("a password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash is not in PHC format: %q", hash)
	}
	if !strings.Contains(hash, "m=65536,t=3,p=2") {
		t.Errorf("hash does not carry its cost parameters: %q", hash)
	}
}

func TestShortPasswordsAreRejected(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("want an error for a password under 8 characters")
	}
}

func TestGarbageHashDoesNotPanic(t *testing.T) {
	for _, hash := range []string{"", "not-a-hash", "$argon2id$broken"} {
		if err := VerifyPassword("anything", hash); !errors.Is(err, ErrBadCredentials) {
			t.Errorf("VerifyPassword(%q) = %v, want ErrBadCredentials", hash, err)
		}
	}
}

// --- sessions ---------------------------------------------------------------

type fakeSessions struct {
	stored map[string]uint64 // tokenHash -> userID
	user   User
}

func (f *fakeSessions) CreateSession(_ context.Context, tokenHash string, userID uint64, _ time.Time, _ string) error {
	if f.stored == nil {
		f.stored = map[string]uint64{}
	}
	f.stored[tokenHash] = userID
	return nil
}

func (f *fakeSessions) SessionUser(_ context.Context, tokenHash string) (User, error) {
	if _, ok := f.stored[tokenHash]; !ok {
		return User{}, errors.New("no such session")
	}
	return f.user, nil
}

func (f *fakeSessions) DeleteSession(_ context.Context, tokenHash string) error {
	delete(f.stored, tokenHash)
	return nil
}

// The plaintext token must exist ONLY in the cookie. A database dump must not
// hand an attacker working sessions.
func TestSessionTokenIsStoredOnlyAsAHash(t *testing.T) {
	store := &fakeSessions{user: User{ID: 7, Email: "a@example.com"}}
	sessions := NewSessions(store, true)

	cookie, err := sessions.Issue(t.Context(), User{ID: 7}, "test-agent")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for storedHash := range store.stored {
		if storedHash == cookie.Value {
			t.Fatal("the raw token was stored — a database dump would yield live sessions")
		}
	}
}

func TestSessionCookieIsHardened(t *testing.T) {
	sessions := NewSessions(&fakeSessions{}, true)

	cookie, err := sessions.Issue(t.Context(), User{ID: 1}, "")
	if err != nil {
		t.Fatal(err)
	}

	// HttpOnly: this tool runs alongside customer JS by definition; an XSS must
	// not be able to read the session.
	if !cookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if !cookie.Secure {
		t.Error("session cookie must be Secure in production")
	}
	// SameSite=None would let any site's form POST carry the session.
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
}

func TestAuthenticateRoundTrip(t *testing.T) {
	store := &fakeSessions{user: User{ID: 7, Email: "a@example.com"}}
	sessions := NewSessions(store, false)

	cookie, err := sessions.Issue(t.Context(), User{ID: 7}, "")
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)

	user, err := sessions.Authenticate(r)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if user.ID != 7 {
		t.Errorf("user = %+v", user)
	}

	// A request with no cookie is not authenticated.
	if _, err := sessions.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil)); !errors.Is(err, ErrNoSession) {
		t.Errorf("want ErrNoSession, got %v", err)
	}

	// A forged token must not authenticate.
	forged := httptest.NewRequest(http.MethodGet, "/", nil)
	forged.AddCookie(&http.Cookie{Name: SessionCookie, Value: "made-up"})
	if _, err := sessions.Authenticate(forged); !errors.Is(err, ErrNoSession) {
		t.Errorf("a forged token authenticated: %v", err)
	}
}

func TestRevokeClearsTheCookieAndTheRow(t *testing.T) {
	store := &fakeSessions{user: User{ID: 7}}
	sessions := NewSessions(store, false)

	cookie, err := sessions.Issue(t.Context(), User{ID: 7}, "")
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodPost, "/logout", nil)
	r.AddCookie(cookie)

	cleared := sessions.Revoke(t.Context(), r)
	if cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("logout must clear the cookie, got %+v", cleared)
	}
	if len(store.stored) != 0 {
		t.Error("logout must delete the session row, not just the cookie")
	}
}

func TestIngestKeysAreUnguessable(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		key, err := NewIngestKey()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(key, KeyPrefix) {
			t.Fatalf("key %q lacks the %q prefix that makes it recognisable to secret scanners", key, KeyPrefix)
		}
		if seen[key] {
			t.Fatal("NewIngestKey returned a duplicate — the random source is broken")
		}
		seen[key] = true
	}
}
