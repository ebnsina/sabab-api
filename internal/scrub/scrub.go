// Package scrub removes sensitive data before anything is persisted.
//
// It runs in the processor, on the path to the first write. That placement is
// the whole design: a redaction feature bolted on later is worthless, because by
// then the secrets are already on disk, already in backups, and already in
// whatever the customer's compliance team is about to ask us about.
//
// The policy is default-deny by key name. We do not attempt to be clever about
// what "looks like" a secret in an arbitrary value — that direction produces
// false negatives, and a false negative here is a leaked credential.
package scrub

import (
	"regexp"
	"strings"
)

// Redacted replaces any value we remove. It is deliberately visible rather than
// silent: a user seeing [redacted] knows the field existed and that we dropped
// it on purpose. Deleting the key outright would leave them wondering whether
// their SDK is broken.
const Redacted = "[redacted]"

// deniedKeys are the field names whose *values* never survive, wherever they
// appear — in tags, in contexts, in breadcrumb data, in HTTP headers.
//
// Matched as a substring of the lowercased key, so "password", "user_password"
// and "passwordConfirm" are all caught. Broad on purpose: the cost of redacting
// one harmless field is a mildly less useful debug view. The cost of missing one
// is a credential in our database.
var deniedKeys = []string{
	"password", "passwd", "secret", "token", "auth", "authorization",
	"credential", "api_key", "apikey", "access_key", "private_key",
	"session", "cookie", "csrf", "xsrf",
	"credit_card", "creditcard", "card_number", "cardnumber", "cvv", "cvc",
	"ssn", "social_security", "pin",
}

// Card numbers and JWTs are matched by *value* as well, because they show up
// pasted into free-text messages and breadcrumbs where no key name protects
// them — "payment failed for card 4111 1111 1111 1111".
var (
	// 13–19 digits, optionally separated by spaces or dashes.
	cardNumber = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)
	// header.payload.signature — three base64url segments.
	jwt = regexp.MustCompile(`\beyJ[\w-]*\.[\w-]+\.[\w-]+\b`)
	// "Bearer <anything>" / "Basic <anything>" in a free-text value.
	authScheme = regexp.MustCompile(`(?i)\b(bearer|basic|token)\s+[\w\-._~+/]+=*`)
)

// Config is the per-project scrubbing policy.
//
// Per-project because one customer's harmless field is another's regulated one:
// a health app may need "diagnosis" gone, while a shop needs it kept.
type Config struct {
	// DenyKeys are additional key names to redact, on top of the defaults.
	DenyKeys []string
	// AllowKeys are key names to keep even though a default rule would redact
	// them. The escape hatch for a field like "token_count" that is a metric,
	// not a credential.
	AllowKeys []string
	// TruncateIP drops the last octet of an IPv4 address (and the low 80 bits
	// of an IPv6 one), which is enough for country-level geo and not enough to
	// identify a person.
	TruncateIP bool
	// DropIP removes the address entirely. Stronger than TruncateIP, and what a
	// GDPR-conscious deployment will want.
	DropIP bool
}

// Scrubber applies a Config.
type Scrubber struct {
	denied  []string
	allowed map[string]bool
	cfg     Config
}

// New builds a Scrubber from a policy.
func New(cfg Config) *Scrubber {
	denied := make([]string, 0, len(deniedKeys)+len(cfg.DenyKeys))
	denied = append(denied, deniedKeys...)
	for _, k := range cfg.DenyKeys {
		if k = normalizeKey(k); k != "" {
			denied = append(denied, k)
		}
	}

	allowed := make(map[string]bool, len(cfg.AllowKeys))
	for _, k := range cfg.AllowKeys {
		if k = normalizeKey(k); k != "" {
			allowed[k] = true
		}
	}

	return &Scrubber{denied: denied, allowed: allowed, cfg: cfg}
}

// Default returns the policy used when a project has configured none.
func Default() *Scrubber { return New(Config{}) }

// normalizeKey folds the separators apart so one rule covers every spelling a
// field arrives in. HTTP headers use hyphens ("X-Api-Key"), JSON bodies use
// underscores ("api_key") or camelCase ("apiKey") — and a deny list that only
// knows one of those spellings leaks the other two.
var keySeparators = strings.NewReplacer("-", "_", " ", "_", ".", "_")

func normalizeKey(key string) string {
	return keySeparators.Replace(strings.ToLower(strings.TrimSpace(key)))
}

// IsDenied reports whether a key's value must be redacted.
func (s *Scrubber) IsDenied(key string) bool {
	k := normalizeKey(key)
	if s.allowed[k] {
		return false
	}
	for _, denied := range s.denied {
		if strings.Contains(k, denied) {
			return true
		}
	}
	return false
}

// String redacts secrets that appear inside a free-text value — a message, a
// breadcrumb, an exception value — where there is no key name to judge by.
func (s *Scrubber) String(value string) string {
	if value == "" {
		return value
	}
	value = jwt.ReplaceAllString(value, Redacted)
	value = authScheme.ReplaceAllString(value, Redacted)
	value = cardNumber.ReplaceAllStringFunc(value, func(match string) string {
		// Only redact if it passes Luhn. Without this check, any long run of
		// digits — an order id, a timestamp in millis, a phone number — would be
		// destroyed, and the error message with it.
		if luhn(match) {
			return Redacted
		}
		return match
	})
	return value
}

// Map redacts a flat string map in place and returns it. Tags are the common case.
func (s *Scrubber) Map(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	for k, v := range m {
		if s.IsDenied(k) {
			m[k] = Redacted
			continue
		}
		m[k] = s.String(v)
	}
	return m
}

// Any recursively redacts an arbitrary decoded-JSON value: contexts, breadcrumb
// data, anything the SDK attached that we do not have a schema for.
//
// Depth is bounded. A deeply nested payload is either a mistake or an attempt to
// make us recurse until the stack gives out; either way the answer is the same.
func (s *Scrubber) Any(value any) any { return s.anyDepth(value, 0) }

const maxDepth = 20

func (s *Scrubber) anyDepth(value any, depth int) any {
	if depth > maxDepth {
		return Redacted
	}

	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if s.IsDenied(key) {
				v[key] = Redacted
				continue
			}
			v[key] = s.anyDepth(child, depth+1)
		}
		return v
	case []any:
		for i, child := range v {
			v[i] = s.anyDepth(child, depth+1)
		}
		return v
	case string:
		return s.String(v)
	default:
		// Numbers, booleans, nulls: nothing to redact.
		return v
	}
}

// luhn reports whether digits pass the Luhn checksum, which is what separates a
// real card number from any other long run of digits.
func luhn(s string) bool {
	var (
		sum    int
		digits int
		double bool
	)
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c == ' ' || c == '-' {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
		n := int(c - '0')
		if double {
			if n *= 2; n > 9 {
				n -= 9
			}
		}
		sum += n
		digits++
		double = !double
	}
	if digits < 13 || digits > 19 {
		return false
	}
	return sum%10 == 0
}
