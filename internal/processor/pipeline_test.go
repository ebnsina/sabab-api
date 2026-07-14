package processor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ebnsina/sabab-api/internal/event"
	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/scrub"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

type fakeIssues struct {
	last   postgres.IssueUpsert
	result postgres.UpsertResult
	err    error
}

func (f *fakeIssues) UpsertIssue(_ context.Context, in postgres.IssueUpsert) (postgres.UpsertResult, error) {
	f.last = in
	if f.err != nil {
		return postgres.UpsertResult{}, f.err
	}
	return f.result, nil
}

func jobWith(t *testing.T, payload string) ingest.Job {
	t.Helper()
	return ingest.Job{
		ProjectID:  4,
		Type:       event.KindError,
		Payload:    json.RawMessage(payload),
		ReceivedAt: time.Date(2026, 7, 14, 12, 0, 5, 0, time.UTC),
		SentAt:     time.Date(2026, 7, 14, 12, 0, 4, 0, time.UTC),
		SDK:        event.SDK{Name: "sabab.javascript.browser", Version: "1.0.0"},
		ClientIP:   "203.0.113.42",
	}
}

func newPipeline(issues IssueStore) *Pipeline {
	return NewPipeline(scrub.Default(), nil, issues)
}

func TestProcessProducesRowAndIssue(t *testing.T) {
	issues := &fakeIssues{result: postgres.UpsertResult{IssueID: 7, New: true}}
	p := newPipeline(issues)

	job := jobWith(t, `{
		"timestamp": "2026-07-14T12:00:00Z",
		"level": "error",
		"release": "web@2.4.1",
		"environment": "production",
		"platform": "javascript",
		"exception": [{
			"type": "TypeError",
			"value": "Cannot read properties of undefined (reading 'id')",
			"frames": [{"function": "renderCart", "module": "app/cart", "lineno": 42, "in_app": true}]
		}],
		"user": {"id": "u_91", "email": "a@example.com", "ip_address": "{{auto}}"}
	}`)

	got, err := p.Process(t.Context(), job)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if !got.New {
		t.Error("New should be true — the alerter fires on it")
	}
	if got.Row.ProjectID != 4 {
		t.Errorf("project = %d, want 4", got.Row.ProjectID)
	}
	if got.Row.ExceptionType != "TypeError" {
		t.Errorf("exception_type = %q", got.Row.ExceptionType)
	}
	// Culprit is derived here, not sent by the SDK.
	if got.Row.Culprit != "renderCart(app/cart)" {
		t.Errorf("culprit = %q, want renderCart(app/cart)", got.Row.Culprit)
	}
	if issues.last.Title != "TypeError: Cannot read properties of undefined (reading 'id')" {
		t.Errorf("title = %q", issues.last.Title)
	}
	// The components must be stored so the UI can answer "why are these grouped?".
	if len(issues.last.Components) == 0 {
		t.Error("no grouping components were recorded")
	}
	// "{{auto}}" must be resolved to the address the gateway saw.
	if got.Row.UserIP.String() != "203.0.113.42" {
		t.Errorf("user_ip = %q, want the socket address", got.Row.UserIP)
	}
}

// Scrubbing must happen before the row is built — the whole point is that the
// secret never reaches a writer.
func TestProcessScrubsBeforeWriting(t *testing.T) {
	issues := &fakeIssues{}
	p := newPipeline(issues)

	job := jobWith(t, `{
		"timestamp": "2026-07-14T12:00:00Z",
		"level": "error",
		"exception": [{"type": "Error", "value": "auth failed with Bearer sk_live_secret123"}],
		"tags": {"tenant": "acme", "api_key": "sk_live_abcdef"},
		"contexts": {"request": {"headers": {"Authorization": "Bearer sk_live_xyz"}}},
		"breadcrumbs": [{"ts": "2026-07-14T11:59:00Z", "message": "login", "data": {"password": "hunter2"}}]
	}`)

	got, err := p.Process(t.Context(), job)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	// Nothing secret may appear anywhere in what we are about to persist.
	haystack := strings.Join([]string{
		got.Row.ExceptionValue, got.Row.Contexts, got.Row.Breadcrumbs,
		got.Row.Tags["api_key"], got.Row.Tags["tenant"],
	}, " ")

	for _, secret := range []string{"sk_live_secret123", "sk_live_abcdef", "sk_live_xyz", "hunter2"} {
		if strings.Contains(haystack, secret) {
			t.Errorf("secret %q reached the writer: %s", secret, haystack)
		}
	}
	if got.Row.Tags["tenant"] != "acme" {
		t.Errorf("scrubbing destroyed a useful tag: %q", got.Row.Tags["tenant"])
	}
}

// A client clock hours in the future must not park the event in a partition
// that will not expire for years.
func TestFutureTimestampIsClamped(t *testing.T) {
	issues := &fakeIssues{}
	p := newPipeline(issues)

	job := jobWith(t, `{"timestamp": "2035-01-01T00:00:00Z", "level": "error",
		"exception": [{"type": "Error", "value": "boom"}]}`)

	got, err := p.Process(t.Context(), job)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.Row.Timestamp.After(job.ReceivedAt.Add(time.Minute)) {
		t.Errorf("timestamp %v was not clamped to our clock", got.Row.Timestamp)
	}
}

// A client clock that is merely a little off should have its interval preserved
// via the sent_at/received_at offset, not be flattened to our clock.
func TestModestClockSkewIsCorrectedNotDiscarded(t *testing.T) {
	issues := &fakeIssues{}
	p := newPipeline(issues)

	// Client says it happened at 12:00:00 and flushed at 12:00:04. We received
	// it at 12:00:05, so its clock is 1s behind: the event was really at
	// 12:00:01.
	job := jobWith(t, `{"timestamp": "2026-07-14T12:00:00Z", "level": "error",
		"exception": [{"type": "Error", "value": "boom"}]}`)

	got, err := p.Process(t.Context(), job)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want := time.Date(2026, 7, 14, 12, 0, 1, 0, time.UTC)
	if !got.Row.Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want %v (skew-corrected)", got.Row.Timestamp, want)
	}
}

func TestMissingTimestampFallsBackToReceivedAt(t *testing.T) {
	issues := &fakeIssues{}
	p := newPipeline(issues)

	job := jobWith(t, `{"level": "error", "exception": [{"type": "Error", "value": "boom"}]}`)

	got, err := p.Process(t.Context(), job)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !got.Row.Timestamp.Equal(job.ReceivedAt) {
		t.Errorf("timestamp = %v, want ReceivedAt %v", got.Row.Timestamp, job.ReceivedAt)
	}
}

// Signals we model but do not ingest yet must be a clean, identifiable skip —
// not an error that gets retried forever.
func TestUnsupportedSignalIsSkippedNotRetried(t *testing.T) {
	p := newPipeline(&fakeIssues{})

	job := jobWith(t, `{"body": "hello"}`)
	job.Type = event.KindLog

	_, err := p.Process(t.Context(), job)
	if !errors.Is(err, errUnsupportedKind) {
		t.Fatalf("want errUnsupportedKind so the worker acks and moves on, got %v", err)
	}
}

// An event id is required to address a row; the SDK omitting one must not cost
// us the event.
func TestMissingEventIDIsGenerated(t *testing.T) {
	p := newPipeline(&fakeIssues{})

	job := jobWith(t, `{"level": "error", "exception": [{"type": "Error", "value": "boom"}]}`)

	got, err := p.Process(t.Context(), job)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.Row.EventID.String() == "00000000-0000-0000-0000-000000000000" {
		t.Error("no event id was generated")
	}
}

func TestMalformedPayloadIsAnError(t *testing.T) {
	p := newPipeline(&fakeIssues{})

	if _, err := p.Process(t.Context(), jobWith(t, `{not json`)); err == nil {
		t.Fatal("want an error for a payload that cannot be decoded")
	}
}
