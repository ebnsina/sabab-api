package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/envelope"
	"github.com/ebnsina/sabab-api/internal/health"
	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/queue"
	"github.com/ebnsina/sabab-api/internal/ratelimit"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

// --- fakes ------------------------------------------------------------------

type fakeProjects struct {
	keys map[string]postgres.Project
	err  error // simulates the control plane being down
}

func (f *fakeProjects) ProjectByIngestKey(_ context.Context, publicKey string) (postgres.Project, error) {
	if f.err != nil {
		return postgres.Project{}, f.err
	}
	p, ok := f.keys[publicKey]
	if !ok {
		// auth.ErrProjectNotFound, not a generic not-found: the two produce
		// different status codes (401 vs 503), and an unknown key answered with
		// 503 would make the SDK retry a request that can never succeed.
		return postgres.Project{}, auth.ErrProjectNotFound
	}
	return p, nil
}

type fakeProducer struct {
	published [][]byte
	err       error
}

func (f *fakeProducer) Publish(_ context.Context, bodies ...[]byte) error {
	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, bodies...)
	return nil
}
func (f *fakeProducer) Close() error { return nil }

type fakeLimiter struct {
	deny     bool
	err      error // simulates Redis being down
	lastCost int
}

func (f *fakeLimiter) AllowN(_ context.Context, _ uint64, cost int) (ratelimit.Decision, error) {
	f.lastCost = cost
	if f.err != nil {
		return ratelimit.Decision{}, f.err
	}
	if f.deny {
		return ratelimit.Decision{Allowed: false, Remaining: 0, RetryAfter: 1500 * time.Millisecond}, nil
	}
	return ratelimit.Decision{Allowed: true, Remaining: 42}, nil
}

func (f *fakeLimiter) Limit() ratelimit.Limit { return ratelimit.DefaultLimit }

// --- harness ----------------------------------------------------------------

type harness struct {
	handler  http.Handler
	producer *fakeProducer
	projects *fakeProjects
	limiter  *fakeLimiter
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	projects := &fakeProjects{keys: map[string]postgres.Project{
		"pk_live_valid": {ID: 4, OrgID: 1, Slug: "web", Name: "Web"},
	}}
	producer := &fakeProducer{}
	limiter := &fakeLimiter{}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := New(
		auth.NewIngestKeys(projects),
		limiter,
		producer,
		envelope.DefaultLimits(),
		health.New("gateway", "test"),
		log,
	)
	return &harness{handler: gw.Handler(), producer: producer, projects: projects, limiter: limiter}
}

func (h *harness) post(t *testing.T, projectID, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/ingest/v1/%s/envelope", projectID), strings.NewReader(body))
	if key != "" {
		req.Header.Set(KeyHeader, key)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func envelopeBody(items ...string) string {
	var b strings.Builder
	b.WriteString(`{"sent_at":"2026-07-14T10:00:00Z","sdk":{"name":"sabab.javascript.browser","version":"1.0.0"}}` + "\n")
	for _, payload := range items {
		fmt.Fprintf(&b, "{\"type\":\"error\",\"length\":%d}\n%s\n", len(payload), payload)
	}
	return b.String()
}

// --- tests ------------------------------------------------------------------

func TestAcceptsValidEnvelope(t *testing.T) {
	h := newHarness(t)

	rec := h.post(t, "4", "pk_live_valid", envelopeBody(`{"message":"boom"}`, `{"message":"bang"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var got acceptedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Accepted != 2 {
		t.Errorf("accepted = %d, want 2", got.Accepted)
	}
	if len(h.producer.published) != 2 {
		t.Fatalf("published %d jobs, want 2", len(h.producer.published))
	}

	// The queued job must carry the project resolved from the key, and our
	// clock — not anything the client claimed.
	job, err := ingest.Decode(h.producer.published[0])
	if err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if job.ProjectID != 4 {
		t.Errorf("job project = %d, want 4", job.ProjectID)
	}
	if job.ReceivedAt.IsZero() {
		t.Error("ReceivedAt must be stamped by the gateway")
	}
	if string(job.Payload) != `{"message":"boom"}` {
		t.Errorf("payload = %s", job.Payload)
	}
}

// The security property that matters most: a key is scoped to one project, and
// the project id in the path is untrusted decoration.
func TestKeyCannotWriteIntoAnotherProject(t *testing.T) {
	h := newHarness(t)

	rec := h.post(t, "9", "pk_live_valid", envelopeBody(`{"message":"boom"}`))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — a key for project 4 must not write into project 9", rec.Code)
	}
	if len(h.producer.published) != 0 {
		t.Error("nothing may be enqueued for a project the key does not own")
	}
}

func TestRejectsBadCredentials(t *testing.T) {
	tests := []struct {
		name      string
		projectID string
		key       string
	}{
		{name: "no key at all", projectID: "4", key: ""},
		{name: "unknown key", projectID: "4", key: "pk_live_nope"},
		{name: "key without the pk_ prefix", projectID: "4", key: "garbage"},
		// A non-numeric project must answer 401, not 400: a different status
		// would let an anonymous caller probe which project ids exist.
		{name: "non-numeric project", projectID: "abc", key: "pk_live_valid"},
		{name: "project zero", projectID: "0", key: "pk_live_valid"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.post(t, tc.projectID, tc.key, envelopeBody(`{"message":"boom"}`))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if len(h.producer.published) != 0 {
				t.Error("nothing may be enqueued for an unauthenticated request")
			}
		})
	}
}

// A control-plane outage is ours. It must produce a 5xx so the SDK retries —
// a 401 would make it discard the events permanently.
func TestControlPlaneOutageIsNotA401(t *testing.T) {
	h := newHarness(t)
	h.projects.err = fmt.Errorf("connection refused")

	rec := h.post(t, "4", "pk_live_valid", envelopeBody(`{"message":"boom"}`))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 so the SDK retries rather than dropping events", rec.Code)
	}
}

// If we cannot enqueue, we must not claim we accepted.
func TestQueueFailureIsNotReportedAsAccepted(t *testing.T) {
	h := newHarness(t)
	h.producer.err = fmt.Errorf("redis is down")

	rec := h.post(t, "4", "pk_live_valid", envelopeBody(`{"message":"boom"}`))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — a 200 here would silently lose the events", rec.Code)
	}
}

func TestRateLimitedRequestIs429WithRetryAfter(t *testing.T) {
	h := newHarness(t)
	h.limiter.deny = true

	rec := h.post(t, "4", "pk_live_valid", envelopeBody(`{"message":"boom"}`))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	// Without Retry-After the SDK has no idea how long to back off, and will
	// hammer us in a tight loop precisely when we are already overloaded.
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want %q (1.5s must round up, not down)", got, "2")
	}
	if len(h.producer.published) != 0 {
		t.Error("a rate-limited request must not enqueue anything")
	}
	assertErrorCode(t, rec.Body.Bytes(), "rate_limited")
}

// The limit is charged per item, not per request — otherwise it is bypassable
// by simply batching more events into one envelope.
func TestRateLimitIsChargedPerItem(t *testing.T) {
	h := newHarness(t)

	h.post(t, "4", "pk_live_valid", envelopeBody(`{"a":1}`, `{"b":2}`, `{"c":3}`))

	if h.limiter.lastCost != 3 {
		t.Errorf("cost = %d, want 3 — one request of 3 items costs 3 events of work", h.limiter.lastCost)
	}
}

// If our rate limiter is down, that is our problem. Rejecting the customer's
// events over it would inflict our outage on them; the queue's own bound is the
// backstop against real overload.
func TestRateLimiterOutageFailsOpen(t *testing.T) {
	h := newHarness(t)
	h.limiter.err = fmt.Errorf("redis is down")

	rec := h.post(t, "4", "pk_live_valid", envelopeBody(`{"message":"boom"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a limiter outage must not drop customer events", rec.Code)
	}
	if len(h.producer.published) != 1 {
		t.Error("the event should still have been enqueued")
	}
}

func TestMalformedEnvelopeIs400(t *testing.T) {
	h := newHarness(t)

	rec := h.post(t, "4", "pk_live_valid", "this is not an envelope\n")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	assertErrorCode(t, rec.Body.Bytes(), "malformed_envelope")
}

func TestOversizedEnvelopeIs413(t *testing.T) {
	h := newHarness(t)

	// One item declaring more than the per-item limit.
	body := `{"sdk":{}}` + "\n" +
		fmt.Sprintf(`{"type":"error","length":%d}`, envelope.DefaultMaxItemBytes+1) + "\n"

	rec := h.post(t, "4", "pk_live_valid", body)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	assertErrorCode(t, rec.Body.Bytes(), "payload_too_large")
}

// Every unrouted path must answer in our JSON shape, not Go's plain-text 404 —
// an SDK parses the error body to decide whether to retry.
func TestUnknownPathIsJSON404(t *testing.T) {
	h := newHarness(t)

	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	assertErrorCode(t, rec.Body.Bytes(), "not_found")
}

// A GET on the ingest path is a wrong method, not a missing endpoint. Saying
// 404 would send an SDK author debugging their URL instead of their verb.
func TestWrongMethodIs405(t *testing.T) {
	h := newHarness(t)

	req := httptest.NewRequest(http.MethodGet, "/ingest/v1/4/envelope", nil)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Errorf("Allow = %q, want POST", got)
	}
	assertErrorCode(t, rec.Body.Bytes(), "method_not_allowed")
}

func TestHealthEndpoints(t *testing.T) {
	h := newHarness(t)

	// Liveness must not depend on anything, so it stays 200 even with no deps
	// registered and nothing reachable.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", rec.Code)
	}
}

func assertErrorCode(t *testing.T, body []byte, want string) {
	t.Helper()
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, body)
	}
	if got.Error.Code != want {
		t.Errorf("error code = %q, want %q", got.Error.Code, want)
	}
}

var _ queue.Producer = (*fakeProducer)(nil)
