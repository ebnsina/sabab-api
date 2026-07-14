package query

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Op is a comparison.
type Op string

const (
	OpEq       Op = "="
	OpNe       Op = "!="
	OpGt       Op = ">"
	OpGte      Op = ">="
	OpLt       Op = "<"
	OpLte      Op = "<="
	OpContains Op = "contains" // substring, for free text
)

// Condition is one parsed filter.
type Condition struct {
	Field   Field
	MapKey  string // set when Field.Kind == KindMap
	Op      Op
	Value   any // already coerced to the field's type; bound, never interpolated
	Negated bool
}

// Query is a parsed search.
type Query struct {
	Conditions []Condition
	// FreeText are bare words, matched against the schema's free-text columns.
	FreeText []string
}

// ParseError names the offending part of the query, so the UI can tell the user
// what to fix rather than showing an empty result set and letting them guess.
type ParseError struct {
	Term   string
	Reason string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s: %s", e.Term, e.Reason)
}

// Parse turns a query string into a Query against a schema.
//
// An empty query is valid and matches everything — the issue stream with no
// filters is the default view of the product.
func Parse(input string, schema Schema) (*Query, error) {
	q := &Query{}

	for _, tok := range lex(input) {
		if tok.kind == tokenTerm {
			if tok.value != "" {
				q.FreeText = append(q.FreeText, tok.value)
			}
			continue
		}

		field, mapKey, err := schema.Lookup(tok.field)
		if err != nil {
			return nil, &ParseError{Term: tok.field, Reason: err.Error()}
		}

		op, raw := splitOp(tok.value, tok.quoted)
		value, err := coerce(field.Kind, raw)
		if err != nil {
			return nil, &ParseError{Term: tok.field + ":" + tok.value, Reason: err.Error()}
		}

		// Ordering operators are meaningless on a string, and silently ignoring
		// them would produce results the user cannot explain.
		if op != OpEq && field.Kind == KindString && mapKey == "" {
			return nil, &ParseError{
				Term:   tok.field + ":" + tok.value,
				Reason: fmt.Sprintf("%q is a text field; %s is only valid on numbers, durations and times", tok.field, op),
			}
		}

		if tok.negated && op == OpEq {
			op = OpNe
		}

		q.Conditions = append(q.Conditions, Condition{
			Field:   field,
			MapKey:  mapKey,
			Op:      op,
			Value:   value,
			Negated: tok.negated,
		})
	}

	return q, nil
}

// splitOp pulls a leading comparison operator off a value: ">500ms" → (>, 500ms).
// A quoted value is literal, so ">=x" inside quotes stays a string.
func splitOp(value string, quoted bool) (Op, string) {
	if quoted {
		return OpEq, value
	}
	switch {
	case strings.HasPrefix(value, ">="):
		return OpGte, value[2:]
	case strings.HasPrefix(value, "<="):
		return OpLte, value[2:]
	case strings.HasPrefix(value, ">"):
		return OpGt, value[1:]
	case strings.HasPrefix(value, "<"):
		return OpLt, value[1:]
	default:
		return OpEq, value
	}
}

// coerce converts the raw text to the field's type. This is where a bad value is
// caught — "duration:>abc" must be a clear error, not a query that quietly
// matches nothing.
func coerce(kind Kind, raw string) (any, error) {
	raw = strings.TrimSpace(raw)

	switch kind {
	case KindNumber:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", raw)
		}
		return n, nil

	case KindDuration:
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not a duration (try 500ms, 1.5s, 2m)", raw)
		}
		return d.Nanoseconds(), nil

	case KindTime:
		return coerceTime(raw)

	default: // KindString, KindMap
		return raw, nil
	}
}

// coerceTime accepts an absolute timestamp or a relative age like "-24h", which
// is what people actually type.
func coerceTime(raw string) (any, error) {
	if strings.HasPrefix(raw, "-") {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not a relative time (try -24h)", raw)
		}
		return time.Now().UTC().Add(d), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts.UTC(), nil
		}
	}
	return nil, fmt.Errorf("%q is not a timestamp (try 2026-07-14 or -24h)", raw)
}
