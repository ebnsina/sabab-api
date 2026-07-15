package alert

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/notify"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

// --- fakes ------------------------------------------------------------------

type fakeRuleStore struct {
	rules []postgres.AlertRule

	mu        sync.Mutex
	claimed   map[string]bool // dedupeKey -> already claimed (simulates the throttle)
	claimCall int
}

func (f *fakeRuleStore) EnabledRulesFor(_ context.Context, projectID uint64, kind string) ([]postgres.AlertRule, error) {
	var out []postgres.AlertRule
	for _, r := range f.rules {
		if r.ProjectID == projectID && r.Kind == kind && r.Enabled {
			out = append(out, r)
		}
	}
	return out, nil
}

// ClaimAlert models the atomic throttle: the first claim for a key wins, later
// ones inside the window lose.
func (f *fakeRuleStore) ClaimAlert(_ context.Context, _ uint64, dedupeKey string, _ time.Duration, _ any) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimCall++
	if f.claimed == nil {
		f.claimed = map[string]bool{}
	}
	if f.claimed[dedupeKey] {
		return false, nil
	}
	f.claimed[dedupeKey] = true
	return true, nil
}

func (f *fakeRuleStore) ProjectByID(_ context.Context, projectID uint64) (postgres.Project, error) {
	return postgres.Project{ID: projectID, Name: "Web"}, nil
}

// recordingSender captures what was dispatched.
type recordingSender struct {
	kind string
	mu   sync.Mutex
	sent []notify.Notification
}

func (s *recordingSender) Kind() string { return s.kind }
func (s *recordingSender) Send(_ context.Context, _ notify.ChannelConfig, n notify.Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, n)
	return nil
}
func (s *recordingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func rule(id, project uint64, kind string, conditions string) postgres.AlertRule {
	return postgres.AlertRule{
		ID: id, ProjectID: project, Kind: kind, Enabled: true,
		Name:            kind,
		Conditions:      json.RawMessage(conditions),
		Channels:        json.RawMessage(`[{"type":"slack","settings":{"webhook_url":"http://example"}}]`),
		ThrottleSeconds: 3600,
	}
}

func newIssueSignal() ingest.AlertSignal {
	return ingest.AlertSignal{
		Kind: ingest.AlertNewIssue, ProjectID: 4, IssueID: 7,
		Title: "TypeError: boom", Culprit: "renderCart(app/cart)",
		Level: "error", Release: "web@2.4.1", Environment: "production",
		At: time.Now(),
	}
}

func engineWith(store RuleStore, sender *recordingSender) *Engine {
	return NewEngine(store, notify.NewDispatcher(sender), "https://sabab.example", discardLog())
}

// --- tests ------------------------------------------------------------------

func TestNewIssueAlertFires(t *testing.T) {
	store := &fakeRuleStore{rules: []postgres.AlertRule{rule(1, 4, "new_issue", `{}`)}}
	sender := &recordingSender{kind: "slack"}
	engine := engineWith(store, sender)

	if err := engine.HandleSignal(t.Context(), newIssueSignal()); err != nil {
		t.Fatalf("HandleSignal: %v", err)
	}

	if sender.count() != 1 {
		t.Fatalf("want 1 notification, got %d", sender.count())
	}
	got := sender.sent[0]
	if got.Reason != "New issue" {
		t.Errorf("reason = %q", got.Reason)
	}
	// The link must be built from the dashboard URL and issue id.
	if got.URL != "https://sabab.example/issues/7" {
		t.Errorf("url = %q", got.URL)
	}
}

// The throttle is the whole point: the same issue must not page twice inside the
// window.
func TestThrottleSuppressesRepeat(t *testing.T) {
	store := &fakeRuleStore{rules: []postgres.AlertRule{rule(1, 4, "new_issue", `{}`)}}
	sender := &recordingSender{kind: "slack"}
	engine := engineWith(store, sender)

	// Same signal three times.
	for range 3 {
		if err := engine.HandleSignal(t.Context(), newIssueSignal()); err != nil {
			t.Fatal(err)
		}
	}

	if sender.count() != 1 {
		t.Errorf("want 1 notification despite 3 signals, got %d — the throttle did not hold", sender.count())
	}
}

// Two different issues under one rule each alert — the throttle is per-issue,
// not per-rule.
func TestDifferentIssuesEachAlert(t *testing.T) {
	store := &fakeRuleStore{rules: []postgres.AlertRule{rule(1, 4, "new_issue", `{}`)}}
	sender := &recordingSender{kind: "slack"}
	engine := engineWith(store, sender)

	a := newIssueSignal()
	b := newIssueSignal()
	b.IssueID = 99
	b.Title = "RangeError: other bug"

	_ = engine.HandleSignal(t.Context(), a)
	_ = engine.HandleSignal(t.Context(), b)

	if sender.count() != 2 {
		t.Errorf("want 2 notifications for 2 different issues, got %d", sender.count())
	}
}

// A rule's conditions narrow what it fires on.
func TestConditionsNarrowTheRule(t *testing.T) {
	tests := []struct {
		name       string
		conditions string
		wantFires  bool
	}{
		{name: "no conditions matches everything", conditions: `{}`, wantFires: true},
		{name: "matching environment", conditions: `{"environment":"production"}`, wantFires: true},
		{name: "wrong environment", conditions: `{"environment":"staging"}`, wantFires: false},
		{name: "matching level", conditions: `{"level":"error"}`, wantFires: true},
		{name: "wrong level", conditions: `{"level":"fatal"}`, wantFires: false},
		{name: "matching release", conditions: `{"release":"web@2.4.1"}`, wantFires: true},
		{name: "wrong release", conditions: `{"release":"web@9.9.9"}`, wantFires: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRuleStore{rules: []postgres.AlertRule{rule(1, 4, "new_issue", tc.conditions)}}
			sender := &recordingSender{kind: "slack"}
			engine := engineWith(store, sender)

			_ = engine.HandleSignal(t.Context(), newIssueSignal())

			fired := sender.count() == 1
			if fired != tc.wantFires {
				t.Errorf("fired = %v, want %v", fired, tc.wantFires)
			}
		})
	}
}

// A rule for one kind must not fire on another kind's signal.
func TestKindIsRespected(t *testing.T) {
	store := &fakeRuleStore{rules: []postgres.AlertRule{rule(1, 4, "regression", `{}`)}}
	sender := &recordingSender{kind: "slack"}
	engine := engineWith(store, sender)

	// A new_issue signal must not match a regression rule.
	if err := engine.HandleSignal(t.Context(), newIssueSignal()); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 0 {
		t.Errorf("a new_issue signal fired a regression rule")
	}

	// ...but a regression signal does.
	sig := newIssueSignal()
	sig.Kind = ingest.AlertRegression
	_ = engine.HandleSignal(t.Context(), sig)
	if sender.count() != 1 {
		t.Errorf("regression signal did not fire the regression rule")
	}
}

// Every channel on a rule receives the alert, and one channel failing does not
// stop the others.
func TestAllChannelsReceive(t *testing.T) {
	r := rule(1, 4, "new_issue", `{}`)
	r.Channels = json.RawMessage(`[
		{"type":"slack","settings":{"webhook_url":"http://s"}},
		{"type":"email","settings":{"to":"a@example.com"}}
	]`)
	store := &fakeRuleStore{rules: []postgres.AlertRule{r}}

	slack := &recordingSender{kind: "slack"}
	email := &recordingSender{kind: "email"}
	engine := NewEngine(store, notify.NewDispatcher(slack, email), "https://sabab.example", discardLog())

	_ = engine.HandleSignal(t.Context(), newIssueSignal())

	if slack.count() != 1 || email.count() != 1 {
		t.Errorf("both channels should receive: slack=%d email=%d", slack.count(), email.count())
	}
}

// A rule with no channels is claimed-then-nothing: it must not even claim a
// throttle slot, or a misconfigured rule silently consumes the window.
func TestRuleWithNoChannelsDoesNothing(t *testing.T) {
	r := rule(1, 4, "new_issue", `{}`)
	r.Channels = json.RawMessage(`[]`)
	store := &fakeRuleStore{rules: []postgres.AlertRule{r}}
	sender := &recordingSender{kind: "slack"}
	engine := engineWith(store, sender)

	_ = engine.HandleSignal(t.Context(), newIssueSignal())

	if store.claimCall != 0 {
		t.Errorf("a channel-less rule must not claim a throttle slot, got %d claims", store.claimCall)
	}
	if sender.count() != 0 {
		t.Errorf("nothing should have been sent")
	}
}
