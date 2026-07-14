package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
)

// CreateUser inserts a user with an already-hashed password.
func (db *DB) CreateUser(ctx context.Context, email, passwordHash, name string) (auth.User, error) {
	const query = `
		INSERT INTO users (email, password_hash, name)
		VALUES ($1, $2, $3)
		RETURNING id, email, name`

	var u auth.User
	err := db.QueryRow(ctx, query, strings.ToLower(strings.TrimSpace(email)), passwordHash, name).
		Scan(&u.ID, &u.Email, &u.Name)
	if err != nil {
		return auth.User{}, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// UserByEmail returns a user and their password hash for login.
func (db *DB) UserByEmail(ctx context.Context, email string) (auth.User, string, error) {
	const query = `SELECT id, email, name, COALESCE(password_hash, '') FROM users WHERE email = $1`

	var (
		u    auth.User
		hash string
	)
	err := db.QueryRow(ctx, query, strings.ToLower(strings.TrimSpace(email))).
		Scan(&u.ID, &u.Email, &u.Name, &hash)
	if err != nil {
		if isNoRows(err) {
			return auth.User{}, "", ErrNotFound
		}
		return auth.User{}, "", fmt.Errorf("look up user: %w", err)
	}
	return u, hash, nil
}

// AddMember grants a user a role in an org.
func (db *DB) AddMember(ctx context.Context, orgID, userID uint64, role string) error {
	const query = `
		INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role`

	if _, err := db.Exec(ctx, query, orgID, userID, role); err != nil {
		return fmt.Errorf("add org member: %w", err)
	}
	return nil
}

// CanAccessProject reports whether a user is a member of the project's org.
//
// Every project-scoped API call goes through this. It is the tenancy boundary:
// without it, an authenticated user of one org could read another org's issues
// simply by changing the id in the URL.
func (db *DB) CanAccessProject(ctx context.Context, userID, projectID uint64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1
			FROM projects p
			JOIN org_members m ON m.org_id = p.org_id
			WHERE p.id = $1 AND m.user_id = $2
		)`

	var allowed bool
	if err := db.QueryRow(ctx, query, projectID, userID).Scan(&allowed); err != nil {
		return false, fmt.Errorf("check project access: %w", err)
	}
	return allowed, nil
}

// --- sessions ---------------------------------------------------------------

func (db *DB) CreateSession(ctx context.Context, tokenHash string, userID uint64, expires time.Time, userAgent string) error {
	const query = `
		INSERT INTO sessions (token_hash, user_id, expires_at, user_agent)
		VALUES ($1, $2, $3, $4)`

	if _, err := db.Exec(ctx, query, tokenHash, userID, expires, userAgent); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// SessionUser resolves a session token hash to its user.
//
// The expiry is checked in SQL rather than in Go: an expired row must not
// authenticate anyone even if a caller forgets to compare the timestamp.
func (db *DB) SessionUser(ctx context.Context, tokenHash string) (auth.User, error) {
	const query = `
		SELECT u.id, u.email, u.name
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`

	var u auth.User
	if err := db.QueryRow(ctx, query, tokenHash).Scan(&u.ID, &u.Email, &u.Name); err != nil {
		if isNoRows(err) {
			return auth.User{}, ErrNotFound
		}
		return auth.User{}, fmt.Errorf("look up session: %w", err)
	}
	return u, nil
}

func (db *DB) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// SweepSessions removes expired rows. Called on a timer by the API.
func (db *DB) SweepSessions(ctx context.Context) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	if err != nil {
		return 0, fmt.Errorf("sweep sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

var _ auth.SessionStore = (*DB)(nil)

// ErrDuplicate reports a unique-constraint violation, so signup can say "that
// email is taken" instead of "internal error".
var ErrDuplicate = errors.New("already exists")
