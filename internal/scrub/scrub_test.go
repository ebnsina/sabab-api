package scrub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeniedKeysAreRedacted(t *testing.T) {
	s := Default()

	denied := []string{
		"password", "user_password", "passwordConfirm", "PASSWORD",
		"secret", "client_secret",
		"token", "access_token", "refresh_token",
		"authorization", "Authorization",
		"api_key", "apiKey", "x-api-key",
		"cookie", "Set-Cookie",
		"session_id", "csrf_token",
		"credit_card", "cardNumber", "cvv",
		"ssn",
	}
	for _, key := range denied {
		if !s.IsDenied(key) {
			t.Errorf("%q must be redacted", key)
		}
	}

	// ...and ordinary fields must survive, or the debug view is useless.
	kept := []string{"user_id", "email", "release", "tenant", "url", "method", "status_code"}
	for _, key := range kept {
		if s.IsDenied(key) {
			t.Errorf("%q must NOT be redacted — it is what makes the event useful", key)
		}
	}
}

func TestAllowKeysOverrideTheDefaults(t *testing.T) {
	// "token_count" is a metric, not a credential — but it contains "token".
	s := New(Config{AllowKeys: []string{"token_count"}})

	if s.IsDenied("token_count") {
		t.Error("an explicitly allowed key must survive the default deny rule")
	}
	if !s.IsDenied("access_token") {
		t.Error("allowing one key must not disable the rule for others")
	}
}

func TestMapRedaction(t *testing.T) {
	s := Default()

	got := s.Map(map[string]string{
		"tenant":        "acme",
		"authorization": "Bearer abc123",
		"password":      "hunter2",
	})

	if got["tenant"] != "acme" {
		t.Errorf("tenant was altered: %q", got["tenant"])
	}
	if got["authorization"] != Redacted {
		t.Errorf("authorization = %q, want %q", got["authorization"], Redacted)
	}
	if got["password"] != Redacted {
		t.Errorf("password = %q, want %q", got["password"], Redacted)
	}
}

// Secrets pasted into free text have no key name to protect them.
func TestSecretsInsideFreeTextAreRedacted(t *testing.T) {
	s := Default()

	tests := []struct {
		name string
		in   string
		gone string // must not appear in the output
	}{
		{
			name: "jwt in a message",
			in:   "auth failed with eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc123def",
			gone: "eyJhbGciOiJIUzI1NiJ9",
		},
		{
			name: "bearer token in a breadcrumb",
			in:   "GET /api/me with Bearer sk_live_abc123def456",
			gone: "sk_live_abc123def456",
		},
		{
			name: "credit card in an error message",
			in:   "payment declined for card 4111 1111 1111 1111",
			gone: "4111 1111 1111 1111",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := s.String(tc.in)
			if strings.Contains(got, tc.gone) {
				t.Errorf("secret survived scrubbing: %q", got)
			}
			if !strings.Contains(got, Redacted) {
				t.Errorf("nothing was redacted: %q", got)
			}
		})
	}
}

// A long run of digits that is NOT a card number must survive. Destroying every
// order id and timestamp would gut the error messages we exist to show.
func TestNonCardDigitsSurvive(t *testing.T) {
	s := Default()

	tests := []string{
		"order 1234567890123456789 not found", // 19 digits, fails Luhn
		"timestamp 1752480000000 is in the future",
		"user 8412 not found",
	}
	for _, in := range tests {
		if got := s.String(in); got != in {
			t.Errorf("Scrub(%q) = %q — a non-card number was destroyed", in, got)
		}
	}
}

// Contexts and breadcrumb data are arbitrary nested JSON.
func TestNestedStructuresAreScrubbed(t *testing.T) {
	s := Default()

	var payload map[string]any
	raw := `{
		"request": {
			"url": "https://example.com/checkout",
			"headers": {"Authorization": "Bearer abc123", "Accept": "application/json"},
			"cookies": {"session": "xyz"}
		},
		"breadcrumbs": [
			{"message": "clicked pay", "data": {"password": "hunter2"}}
		]
	}`
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}

	out, err := json.Marshal(s.Any(payload))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	for _, secret := range []string{"abc123", "hunter2", `"xyz"`} {
		if strings.Contains(got, secret) {
			t.Errorf("secret %q survived in nested data: %s", secret, got)
		}
	}
	// The useful parts must remain.
	for _, kept := range []string{"https://example.com/checkout", "application/json", "clicked pay"} {
		if !strings.Contains(got, kept) {
			t.Errorf("scrubbing destroyed useful context %q: %s", kept, got)
		}
	}
}

// A pathologically nested payload must not recurse until the stack gives out.
func TestDeepNestingIsBounded(t *testing.T) {
	s := Default()

	deep := any("bottom")
	for range 200 {
		deep = map[string]any{"next": deep}
	}

	// The assertion is simply that this returns rather than crashing the
	// processor — which, for an untrusted payload, is the whole point.
	_ = s.Any(deep)
}

func TestIPPolicy(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		in   string
		want string
	}{
		{name: "kept by default", cfg: Config{}, in: "203.0.113.42", want: "203.0.113.42"},
		{name: "dropped", cfg: Config{DropIP: true}, in: "203.0.113.42", want: ""},
		{
			// /24 keeps country-level geo and gives up the individual.
			name: "ipv4 truncated to /24",
			cfg:  Config{TruncateIP: true},
			in:   "203.0.113.42",
			want: "203.0.113.0",
		},
		{
			name: "ipv6 truncated to /48",
			cfg:  Config{TruncateIP: true},
			in:   "2001:db8:1234:5678::1",
			want: "2001:db8:1234::",
		},
		{
			name: "unparseable address is dropped, not stored",
			cfg:  Config{TruncateIP: true},
			in:   "not-an-ip",
			want: "",
		},
		{name: "empty stays empty", cfg: Config{}, in: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := New(tc.cfg).IP(tc.in); got != tc.want {
				t.Errorf("IP(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
