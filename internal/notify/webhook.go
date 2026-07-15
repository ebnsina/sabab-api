package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// httpClient is shared by the webhook-based senders. The timeout is a backstop;
// Dispatch already bounds each call with a context deadline.
var httpClient = &http.Client{}

// postJSON sends a JSON body and treats any non-2xx as a failure. It drains and
// closes the body so the connection can be reused — a leaked body is a slow
// resource leak that only shows up under sustained alert volume.
func postJSON(ctx context.Context, url string, body any, header map[string]string) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// --- Slack ------------------------------------------------------------------

// SlackSender posts to a Slack incoming webhook.
type SlackSender struct{}

func (SlackSender) Kind() string { return "slack" }

func (SlackSender) Send(ctx context.Context, cfg ChannelConfig, n Notification) error {
	url := cfg.Settings["webhook_url"]
	if url == "" {
		return fmt.Errorf("slack channel is missing webhook_url")
	}

	// Block Kit: a header line, the issue title, and a context row with the
	// release and environment. Enough to triage from a phone without opening the
	// dashboard, with a button for when you do.
	blocks := []map[string]any{
		{
			"type": "section",
			"text": map[string]string{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*%s* in *%s*\n%s", n.Reason, n.ProjectName, slackEscape(n.Title)),
			},
		},
	}

	context := metaLine(n)
	if context != "" {
		blocks = append(blocks, map[string]any{
			"type":     "context",
			"elements": []map[string]string{{"type": "mrkdwn", "text": context}},
		})
	}

	if n.URL != "" {
		blocks = append(blocks, map[string]any{
			"type": "actions",
			"elements": []map[string]any{{
				"type": "button",
				"text": map[string]string{"type": "plain_text", "text": "View issue"},
				"url":  n.URL,
			}},
		})
	}

	return postJSON(ctx, url, map[string]any{
		// text is the notification/fallback line shown in the sidebar and on
		// mobile push, so it must stand alone without the blocks.
		"text":   fmt.Sprintf("%s in %s: %s", n.Reason, n.ProjectName, n.Title),
		"blocks": blocks,
	}, nil)
}

// slackEscape neutralises the three characters Slack treats as markup, so an
// error message containing "<", ">" or "&" renders as written.
func slackEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// --- Discord ----------------------------------------------------------------

// DiscordSender posts to a Discord webhook.
type DiscordSender struct{}

func (DiscordSender) Kind() string { return "discord" }

func (DiscordSender) Send(ctx context.Context, cfg ChannelConfig, n Notification) error {
	url := cfg.Settings["webhook_url"]
	if url == "" {
		return fmt.Errorf("discord channel is missing webhook_url")
	}

	fields := []map[string]any{}
	if n.Release != "" {
		fields = append(fields, map[string]any{"name": "Release", "value": n.Release, "inline": true})
	}
	if n.Environment != "" {
		fields = append(fields, map[string]any{"name": "Environment", "value": n.Environment, "inline": true})
	}
	if n.Count > 0 {
		fields = append(fields, map[string]any{
			"name": "Frequency", "value": fmt.Sprintf("%d in %s", n.Count, n.Window), "inline": true,
		})
	}

	embed := map[string]any{
		"title":       truncate(n.Title, 240),
		"description": n.Culprit,
		"color":       levelColor(n.Level),
		"fields":      fields,
	}
	if n.URL != "" {
		embed["url"] = n.URL
	}

	return postJSON(ctx, url, map[string]any{
		"content": fmt.Sprintf("**%s** in **%s**", n.Reason, n.ProjectName),
		"embeds":  []map[string]any{embed},
	}, nil)
}

// --- Generic webhook --------------------------------------------------------

// WebhookSender posts our own structured payload, optionally HMAC-signed so the
// receiver can verify it came from us.
type WebhookSender struct{}

func (WebhookSender) Kind() string { return "webhook" }

func (WebhookSender) Send(ctx context.Context, cfg ChannelConfig, n Notification) error {
	url := cfg.Settings["url"]
	if url == "" {
		return fmt.Errorf("webhook channel is missing url")
	}

	payload := map[string]any{
		"reason":      n.Reason,
		"project":     n.ProjectName,
		"title":       n.Title,
		"culprit":     n.Culprit,
		"level":       n.Level,
		"release":     n.Release,
		"environment": n.Environment,
		"url":         n.URL,
		"fired_at":    n.FiredAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if n.Count > 0 {
		payload["count"] = n.Count
		payload["window"] = n.Window
	}

	header := map[string]string{}
	// A shared secret signs the body, so a receiver exposed to the internet can
	// reject anything not from us instead of trusting an unauthenticated POST.
	if secret := cfg.Settings["secret"]; secret != "" {
		body, _ := json.Marshal(payload)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		header["X-Sabab-Signature"] = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	return postJSON(ctx, url, payload, header)
}

// metaLine renders the release/environment/frequency context shared by the
// chat channels.
func metaLine(n Notification) string {
	var parts []string
	if n.Level != "" {
		parts = append(parts, "level: "+n.Level)
	}
	if n.Release != "" {
		parts = append(parts, "release: "+n.Release)
	}
	if n.Environment != "" {
		parts = append(parts, "env: "+n.Environment)
	}
	if n.Count > 0 {
		parts = append(parts, fmt.Sprintf("%d events in %s", n.Count, n.Window))
	}
	return strings.Join(parts, "  ·  ")
}

// levelColor maps a severity to the sidebar colour Discord embeds use. The
// values match the dashboard's --level-* palette so an alert and the UI agree.
func levelColor(level string) int {
	switch level {
	case "fatal":
		return 0xf04a6e
	case "error":
		return 0xf0663f
	case "warning":
		return 0xf5a623
	case "info":
		return 0x4a9df0
	default:
		return 0x6b7686
	}
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit-1] + "…"
}
