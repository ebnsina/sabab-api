package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sampleNotification() Notification {
	return Notification{
		Reason:      "New issue",
		ProjectName: "Web",
		Title:       "TypeError: Cannot read properties of undefined",
		Culprit:     "renderCart(app/cart)",
		Level:       "error",
		Release:     "web@2.4.1",
		Environment: "production",
		URL:         "https://sabab.example/issues/7",
		FiredAt:     time.Unix(1_700_000_000, 0).UTC(),
	}
}

// captureServer returns a test server that records the one request it receives.
func captureServer(t *testing.T, status int) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.body = body
		captured.header = r.Header.Clone()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

type capturedRequest struct {
	body   []byte
	header http.Header
}

func TestSlackPayload(t *testing.T) {
	srv, captured := captureServer(t, http.StatusOK)

	err := SlackSender{}.Send(context.Background(), ChannelConfig{
		Settings: map[string]string{"webhook_url": srv.URL},
	}, sampleNotification())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(captured.body, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	// The fallback text must stand alone for mobile push, without the blocks.
	text, _ := payload["text"].(string)
	if !strings.Contains(text, "New issue") || !strings.Contains(text, "Web") {
		t.Errorf("fallback text = %q", text)
	}
	if _, ok := payload["blocks"]; !ok {
		t.Error("payload should carry Block Kit blocks")
	}
}

// A non-2xx from the endpoint must surface as an error, so the alerter can log
// that the alert did not actually arrive.
func TestSlackReportsFailure(t *testing.T) {
	srv, _ := captureServer(t, http.StatusInternalServerError)

	err := SlackSender{}.Send(context.Background(), ChannelConfig{
		Settings: map[string]string{"webhook_url": srv.URL},
	}, sampleNotification())
	if err == nil {
		t.Fatal("a 500 from Slack must be reported as an error")
	}
}

func TestSlackRequiresWebhookURL(t *testing.T) {
	err := SlackSender{}.Send(context.Background(), ChannelConfig{}, sampleNotification())
	if err == nil {
		t.Fatal("want an error when webhook_url is missing")
	}
}

func TestDiscordPayload(t *testing.T) {
	srv, captured := captureServer(t, http.StatusNoContent)

	err := DiscordSender{}.Send(context.Background(), ChannelConfig{
		Settings: map[string]string{"webhook_url": srv.URL},
	}, sampleNotification())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var payload struct {
		Content string           `json:"content"`
		Embeds  []map[string]any `json:"embeds"`
	}
	if err := json.Unmarshal(captured.body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Embeds) != 1 {
		t.Fatalf("want 1 embed, got %d", len(payload.Embeds))
	}
	// The embed colour must match the error level, so an alert reads the same as
	// the dashboard.
	if payload.Embeds[0]["color"] != float64(0xf0663f) {
		t.Errorf("embed colour = %v, want the error colour", payload.Embeds[0]["color"])
	}
}

// The webhook body must be signed when a secret is set, so a receiver can reject
// anything not from us.
func TestWebhookSigning(t *testing.T) {
	srv, captured := captureServer(t, http.StatusOK)
	const secret = "shh"

	err := WebhookSender{}.Send(context.Background(), ChannelConfig{
		Settings: map[string]string{"url": srv.URL, "secret": secret},
	}, sampleNotification())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	sig := captured.header.Get("X-Sabab-Signature")
	if sig == "" {
		t.Fatal("a signed webhook must carry X-Sabab-Signature")
	}

	// Recompute the HMAC over the exact body and confirm it matches.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(captured.body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if sig != want {
		t.Errorf("signature = %q, want %q", sig, want)
	}
}

// Without a secret, no signature header — but the request still goes out.
func TestWebhookUnsignedStillSends(t *testing.T) {
	srv, captured := captureServer(t, http.StatusOK)

	err := WebhookSender{}.Send(context.Background(), ChannelConfig{
		Settings: map[string]string{"url": srv.URL},
	}, sampleNotification())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if captured.header.Get("X-Sabab-Signature") != "" {
		t.Error("no signature should be sent without a secret")
	}
	if len(captured.body) == 0 {
		t.Error("the request body should still be present")
	}
}

// Dispatch isolates channels: one failing endpoint must not stop the others,
// and every result is reported.
func TestDispatchIsolatesChannels(t *testing.T) {
	good, _ := captureServer(t, http.StatusOK)
	bad, _ := captureServer(t, http.StatusInternalServerError)

	d := NewDispatcher(SlackSender{}, WebhookSender{})
	results := d.Dispatch(context.Background(), []ChannelConfig{
		{Type: "webhook", Settings: map[string]string{"url": bad.URL}},
		{Type: "slack", Settings: map[string]string{"webhook_url": good.URL}},
	}, sampleNotification())

	if len(results) != 2 {
		t.Fatalf("want a result per channel, got %d", len(results))
	}
	var slackOK, webhookFailed bool
	for _, r := range results {
		if r.Channel == "slack" && r.Err == nil {
			slackOK = true
		}
		if r.Channel == "webhook" && r.Err != nil {
			webhookFailed = true
		}
	}
	if !slackOK {
		t.Error("slack should have succeeded despite the webhook failing")
	}
	if !webhookFailed {
		t.Error("the failing webhook should be reported")
	}
}

// An unknown channel type is reported, not silently skipped — a rule naming a
// channel we cannot send is a misconfiguration the operator needs to see.
func TestDispatchReportsUnknownChannel(t *testing.T) {
	d := NewDispatcher(SlackSender{})
	results := d.Dispatch(context.Background(), []ChannelConfig{{Type: "carrier_pigeon"}}, sampleNotification())

	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("an unknown channel must produce a reported error, got %+v", results)
	}
}
