// Package cursor encodes an opaque keyset-pagination cursor.
//
// A cursor is just the sort key of the last row on a page, so the next page can
// ask for everything "after" it — WHERE (sort, tiebreaker) < (cursor). That is
// keyset pagination: unlike OFFSET, it does not get slower as the user pages
// deeper, because the database seeks straight to the key instead of counting
// past every skipped row.
//
// The value is base64url(JSON). It is opaque on purpose: callers must not parse
// it or build one by hand — its shape is the server's to change.
package cursor

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Encode turns a cursor value (any JSON-serialisable struct) into an opaque
// token. Returns "" for a nil value, so "no next page" is the empty string.
func Encode(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Decode parses a token into out. An empty token is not an error — it means the
// first page, and out is left untouched. A malformed token IS an error, so a
// tampered cursor is rejected rather than silently treated as page one.
func Decode(token string, out any) error {
	if token == "" {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return fmt.Errorf("decode cursor: %w", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("parse cursor: %w", err)
	}
	return nil
}
