// Package event defines the normalized internal model — the boundary of the
// system.
//
// Everything converts *to* these types: our own SDKs via the envelope, and one
// day OTLP via the inbound adapter. Nothing downstream of the processor knows
// what the wire format looked like. When a new ingest path is added, it is
// judged by one question: can it produce these structs?
//
// Three fields appear on every signal — TraceID, Environment and Release. They
// are the joins that make one product out of four datasets, which is why they
// live on the shared Meta rather than on each signal separately: it must not be
// possible to add a signal and forget them.
package event

import (
	"time"

	"github.com/google/uuid"
)

// Kind identifies which signal an item carries. It matches the `type` field of
// an envelope item header.
type Kind string

const (
	KindError        Kind = "error"
	KindLog          Kind = "log"
	KindSpan         Kind = "span"
	KindMetric       Kind = "metric"
	KindSession      Kind = "session"
	KindClientReport Kind = "client_report"
)

// Valid reports whether k is a signal we know how to ingest.
func (k Kind) Valid() bool {
	switch k {
	case KindError, KindLog, KindSpan, KindMetric, KindSession, KindClientReport:
		return true
	}
	return false
}

// Level is the severity of an error event. The values match the `level` Enum8
// in ClickHouse and the CHECK constraint on issues.level — all three must be
// changed together.
type Level string

const (
	LevelDebug   Level = "debug"
	LevelInfo    Level = "info"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
	LevelFatal   Level = "fatal"
)

// Valid reports whether l is a known severity.
func (l Level) Valid() bool {
	switch l {
	case LevelDebug, LevelInfo, LevelWarning, LevelError, LevelFatal:
		return true
	}
	return false
}

// SDK identifies the client that sent an item. Recorded so we can tell a bug in
// our own SDK apart from a bug in the customer's app.
type SDK struct {
	Name    string `json:"name"`    // "sabab.javascript.browser"
	Version string `json:"version"` // "1.0.0"
}

// User is who the event happened to. Populating this is what turns
// "500 errors" into "3 users affected", which is the number that decides
// whether anyone gets paged.
type User struct {
	ID    string `json:"id,omitempty"`
	Email string `json:"email,omitempty"`
	// IP is the caller's address. The SDK may send the literal "{{auto}}", in
	// which case the gateway substitutes the socket address. The scrubber may
	// then truncate or drop it entirely, per project policy.
	IP string `json:"ip_address,omitempty"`
}

// Meta is the set of fields carried by every signal.
type Meta struct {
	// ProjectID is resolved by the gateway from the ingest key. It is never
	// read from the payload — a client must not be able to write into another
	// project by lying about it.
	ProjectID uint64 `json:"-"`

	EventID    uuid.UUID `json:"event_id"`
	Timestamp  time.Time `json:"timestamp"`   // when it happened, per the client
	ReceivedAt time.Time `json:"received_at"` // when we ingested it; set by the gateway

	Environment string `json:"environment,omitempty"` // "production"
	Release     string `json:"release,omitempty"`     // "web@2.4.1"
	Platform    string `json:"platform,omitempty"`    // "javascript"

	// The join keys. TraceID is zero when the SDK could not associate the item
	// with a trace; SpanID is zero when it is not inside a span.
	TraceID uuid.UUID `json:"trace_id,omitempty"`
	SpanID  uint64    `json:"span_id,omitempty"`

	SDK  SDK               `json:"sdk"`
	User User              `json:"user,omitzero"`
	Tags map[string]string `json:"tags,omitempty"`
}

// Item is one normalized signal. Exactly one of the pointers is non-nil, and it
// is the one Kind names.
//
// A tagged union rather than an `any` payload: the processor switches on Kind
// and the compiler then guarantees it handled the right shape.
type Item struct {
	Kind Kind
	Meta Meta

	Error        *Error
	Log          *Log
	Span         *Span
	Metric       *Metric
	Session      *Session
	ClientReport *ClientReport
}
