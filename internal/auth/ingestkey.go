// Package auth resolves credentials.
//
// Ingest keys are the public, write-only credentials that ship inside browser
// bundles. They are not secrets and must never be treated as one — but they are
// checked on every single request, which makes the *speed* of checking them a
// hot-path concern.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

// ErrInvalidKey means the key is unknown, revoked, or does not grant access to
// the project it was used against. The gateway maps all three to 401 and says
// nothing more: distinguishing "no such key" from "wrong project" would let an
// anonymous caller probe which project ids exist.
var ErrInvalidKey = errors.New("invalid ingest key")

// KeyPrefix is required on every public key, so a key pasted into the wrong
// field is recognisable on sight — including by secret scanners.
const KeyPrefix = "pk_"

// cacheTTL bounds how long a revoked key keeps working.
//
// This is the whole trade-off of the cache: without it every ingest request is
// a Postgres round trip, and the control plane becomes the bottleneck for the
// event plane. With it, a revoked key survives for up to this long. Thirty
// seconds is short enough to be an acceptable abuse window and long enough that
// a burst of traffic from one project costs one query, not thousands.
const cacheTTL = 30 * time.Second

// negativeCacheTTL bounds how long an unknown key is remembered as unknown.
// Shorter, because the common cause is a project that was created moments ago
// and a user watching an empty dashboard — but long enough that a flood of
// garbage keys cannot be turned into a flood of Postgres queries.
const negativeCacheTTL = 5 * time.Second

// projectLookup is the slice of the control plane this package needs. Depending
// on an interface rather than *postgres.DB keeps the gateway testable without a
// database.
type projectLookup interface {
	ProjectByIngestKey(ctx context.Context, publicKey string) (postgres.Project, error)
}

type cacheEntry struct {
	project postgres.Project
	found   bool
	expires time.Time
}

// IngestKeys resolves ingest keys to projects, with a short-lived cache.
type IngestKeys struct {
	store  projectLookup
	ttl    time.Duration
	negTTL time.Duration
	now    func() time.Time

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

// NewIngestKeys builds a resolver over the control plane.
func NewIngestKeys(store projectLookup) *IngestKeys {
	return &IngestKeys{
		store:  store,
		ttl:    cacheTTL,
		negTTL: negativeCacheTTL,
		now:    time.Now,
		cache:  make(map[string]cacheEntry),
	}
}

// Authenticate resolves publicKey and checks it grants access to projectID.
//
// projectID comes from the request path and the key comes from a header; both
// are attacker-controlled. The check that they agree is what makes the path
// component untrusted decoration rather than an authorization decision.
func (k *IngestKeys) Authenticate(ctx context.Context, publicKey string, projectID uint64) (postgres.Project, error) {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey == "" || !strings.HasPrefix(publicKey, KeyPrefix) {
		return postgres.Project{}, ErrInvalidKey
	}

	project, err := k.resolve(ctx, publicKey)
	if err != nil {
		return postgres.Project{}, err
	}
	// A valid key for project A must not write into project B.
	if project.ID != projectID {
		return postgres.Project{}, ErrInvalidKey
	}
	return project, nil
}

func (k *IngestKeys) resolve(ctx context.Context, publicKey string) (postgres.Project, error) {
	if entry, ok := k.lookupCache(publicKey); ok {
		if !entry.found {
			return postgres.Project{}, ErrInvalidKey
		}
		return entry.project, nil
	}

	project, err := k.store.ProjectByIngestKey(ctx, publicKey)
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		k.put(publicKey, cacheEntry{found: false, expires: k.now().Add(k.negTTL)})
		return postgres.Project{}, ErrInvalidKey
	case err != nil:
		// A database outage is ours, not the caller's: it must surface as a 5xx
		// so the SDK retries, never as a 401 that would make it discard the
		// events permanently.
		return postgres.Project{}, fmt.Errorf("resolve ingest key: %w", err)
	}

	k.put(publicKey, cacheEntry{project: project, found: true, expires: k.now().Add(k.ttl)})
	return project, nil
}

func (k *IngestKeys) lookupCache(publicKey string) (cacheEntry, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	entry, ok := k.cache[publicKey]
	if !ok || k.now().After(entry.expires) {
		return cacheEntry{}, false
	}
	return entry, true
}

func (k *IngestKeys) put(publicKey string, entry cacheEntry) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.cache[publicKey] = entry
}

// Invalidate drops a key from the cache, so revoking through the API takes
// effect at once on this instance rather than after the TTL.
func (k *IngestKeys) Invalidate(publicKey string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.cache, publicKey)
}
