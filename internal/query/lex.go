// Package query compiles the user-facing search language into SQL.
//
//	level:error release:web@2.4.1 tenant:acme !browser:Safari duration:>500ms
//
// ONE language across all four signals. The same parser and the same compiler
// serve errors, logs, spans and metrics — only the field schema differs. That is
// the difference between one product and four tools that happen to share a
// login, and it is the thing that is miserable to retrofit, which is why it goes
// in during M1 rather than later.
//
// Values are NEVER interpolated into SQL. Every one becomes a bound parameter,
// and every column name is looked up in an allowlist. A search box that reaches
// the database is the most obvious injection surface in the product.
package query

import (
	"strings"
	"unicode"
)

// tokenKind distinguishes the pieces of a query string.
type tokenKind int

const (
	tokenTerm tokenKind = iota // a bare word: free-text search
	tokenPair                  // field:value
)

// token is one lexed unit.
type token struct {
	kind    tokenKind
	field   string
	value   string
	negated bool // "!field:value" or "-field:value"
	quoted  bool // the value was quoted, so it is literal
}

// lex splits a query string into tokens.
//
// It is hand-written rather than regex-driven because the tricky parts —
// quoted values containing spaces and colons, and a colon inside an unquoted
// value like release:web@2.4.1 — are exactly what a regex gets wrong.
func lex(input string) []token {
	var (
		tokens  []token
		current strings.Builder
		field   string
		negated bool
		inQuote bool
		quoted  bool
		hasPair bool
	)

	flush := func() {
		text := current.String()
		current.Reset()

		if text == "" && !quoted {
			field, negated, hasPair, quoted = "", false, false, false
			return
		}
		if hasPair {
			tokens = append(tokens, token{kind: tokenPair, field: field, value: text, negated: negated, quoted: quoted})
		} else {
			tokens = append(tokens, token{kind: tokenTerm, value: text, negated: negated, quoted: quoted})
		}
		field, negated, hasPair, quoted = "", false, false, false
	}

	for _, r := range input {
		switch {
		case r == '"':
			inQuote = !inQuote
			quoted = true

		case inQuote:
			// Inside quotes everything is literal, including spaces and colons.
			current.WriteRune(r)

		case unicode.IsSpace(r):
			flush()

		case r == ':' && !hasPair && current.Len() > 0:
			// The FIRST colon separates field from value. Any later colon is part
			// of the value — "release:web@2.4.1" and a timestamp both depend on
			// this.
			field = current.String()
			current.Reset()
			hasPair = true

		case (r == '!' || r == '-') && current.Len() == 0 && !hasPair:
			// Only leading, and only before a field: a "-" inside a value is a
			// hyphen, not a negation.
			negated = true

		default:
			current.WriteRune(r)
		}
	}
	flush()

	return tokens
}
