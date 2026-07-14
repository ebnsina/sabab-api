package envelope

import (
	"errors"
	"fmt"
)

// Sentinel errors. The gateway maps these to status codes, so a malformed
// envelope produces a 400 and an oversized one a 413 — never a 500. A 500 would
// tell the SDK to retry, and it would then retry a body that can never succeed,
// forever.
var (
	// ErrMalformed means the bytes are not a valid envelope.
	ErrMalformed = errors.New("malformed envelope")
	// ErrTooLarge means a published limit was exceeded.
	ErrTooLarge = errors.New("envelope too large")
)

// ParseError describes where parsing failed, in terms the sender can act on.
type ParseError struct {
	// Kind is ErrMalformed or ErrTooLarge.
	Kind error
	// Item is the zero-based index of the offending item, or -1 for the header.
	Item   int
	Reason string
}

func (e *ParseError) Error() string {
	where := "envelope header"
	if e.Item >= 0 {
		where = fmt.Sprintf("item %d", e.Item)
	}
	return fmt.Sprintf("%s: %s: %s", e.Kind, where, e.Reason)
}

// Unwrap lets errors.Is(err, ErrTooLarge) work, which is how the gateway picks
// the status code.
func (e *ParseError) Unwrap() error { return e.Kind }

func malformed(item int, format string, args ...any) *ParseError {
	return &ParseError{Kind: ErrMalformed, Item: item, Reason: fmt.Sprintf(format, args...)}
}

func tooLarge(item int, format string, args ...any) *ParseError {
	return &ParseError{Kind: ErrTooLarge, Item: item, Reason: fmt.Sprintf(format, args...)}
}
