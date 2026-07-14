// Package envelope parses the Sabab wire format.
//
// The format is newline-delimited: an envelope header, then repeating pairs of
// item header and item payload. See docs/wire-format.md.
//
//	{"sent_at":"…","sdk":{"name":"…","version":"…"}}
//	{"type":"error","length":842}
//	{ …842 bytes of error payload… }
//	{"type":"log","length":214}
//	{ …214 bytes of log payload… }
//
// This package is the outermost layer that touches attacker-controlled bytes:
// an ingest key is public by design, so anyone on the internet can post here.
// It therefore reads defensively — every limit is enforced *while* streaming,
// never after buffering, and a malformed envelope produces a typed error the
// gateway can turn into the right status code rather than a generic 500.
package envelope

import (
	"encoding/json"
	"time"

	"github.com/ebnsina/sabab-api/internal/event"
)

// Header is the first line of an envelope.
type Header struct {
	// SentAt is the client's clock when it flushed. Clock skew is real, so this
	// is used to correct event timestamps, never trusted as the truth.
	SentAt time.Time `json:"sent_at"`
	SDK    event.SDK `json:"sdk"`
}

// ItemHeader precedes each payload.
type ItemHeader struct {
	Type event.Kind `json:"type"`
	// Length is the payload size in bytes.
	//
	// It is what lets a v1 gateway accept an envelope from a v2 SDK that has
	// learned a new signal: we can skip exactly Length bytes of an item we do
	// not understand and keep the rest. Without it, one unknown type would
	// force us to reject the whole envelope — and an SDK upgrade would start
	// dropping the customer's errors.
	Length int `json:"length"`
}

// Item is one signal, still in its unparsed form. The processor decodes
// Payload according to Type; the gateway deliberately does not, because
// decoding is work it must not do on the hot path.
type Item struct {
	Type    event.Kind
	Payload []byte
}

// Envelope is a parsed request body.
type Envelope struct {
	Header Header
	Items  []Item

	// Skipped counts items whose type this build does not recognise. They are
	// dropped, not fatal — but the count is reported back to the SDK so our
	// numbers stay honest about what we threw away.
	Skipped int
}

// Limits bound what a single request may cost us. Zero means "use the default".
type Limits struct {
	// MaxCompressedBytes caps the request body as it arrives on the wire.
	MaxCompressedBytes int64
	// MaxDecompressedBytes caps the body after decompression. Enforced while
	// streaming: a 1 MiB body that inflates to 10 GiB is a zip bomb, and
	// noticing that after we have buffered it is too late.
	MaxDecompressedBytes int64
	// MaxItems caps how many items one envelope may carry.
	MaxItems int
	// MaxItemBytes caps a single payload.
	MaxItemBytes int
}

// Default limits, as published in docs/wire-format.md.
const (
	DefaultMaxCompressedBytes   int64 = 1 << 20  // 1 MiB
	DefaultMaxDecompressedBytes int64 = 20 << 20 // 20 MiB
	DefaultMaxItems             int   = 1000
	DefaultMaxItemBytes         int   = 1 << 20 // 1 MiB
)

// DefaultLimits returns the published limits.
func DefaultLimits() Limits {
	return Limits{
		MaxCompressedBytes:   DefaultMaxCompressedBytes,
		MaxDecompressedBytes: DefaultMaxDecompressedBytes,
		MaxItems:             DefaultMaxItems,
		MaxItemBytes:         DefaultMaxItemBytes,
	}
}

// withDefaults fills in any zero field, so a caller can override one limit
// without having to restate the others.
func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxCompressedBytes <= 0 {
		l.MaxCompressedBytes = d.MaxCompressedBytes
	}
	if l.MaxDecompressedBytes <= 0 {
		l.MaxDecompressedBytes = d.MaxDecompressedBytes
	}
	if l.MaxItems <= 0 {
		l.MaxItems = d.MaxItems
	}
	if l.MaxItemBytes <= 0 {
		l.MaxItemBytes = d.MaxItemBytes
	}
	return l
}

// MarshalItemHeader renders an item header line. Used by the SDKs' Go-side
// tests and by cmd/loadgen; keeping the writer next to the reader is what stops
// the two drifting apart.
func MarshalItemHeader(h ItemHeader) ([]byte, error) { return json.Marshal(h) }
