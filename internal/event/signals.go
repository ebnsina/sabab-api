package event

import (
	"time"

	"github.com/google/uuid"
)

// Log is one line the app printed. Ingested from M2; modelled now so that
// TraceID is on it from the first day rather than being retrofitted.
type Log struct {
	SeverityText   string `json:"severity_text"`   // trace|debug|info|warn|error|fatal
	SeverityNumber uint8  `json:"severity_number"` // numeric, so ">= warn" is a range scan
	Service        string `json:"service,omitempty"`

	Body string `json:"body"` // "user 8412 not found"
	// Template is the pre-interpolation form: "user {id} not found". Capturing
	// it separately is what lets us group logs the way we group errors — "this
	// same line fired 4M times" — which a plain text store cannot answer.
	Template string `json:"template,omitempty"`

	Attributes map[string]string `json:"attributes,omitempty"`
}

// SpanStatus is the outcome of a span.
type SpanStatus string

const (
	SpanStatusOK        SpanStatus = "ok"
	SpanStatusError     SpanStatus = "error"
	SpanStatusCancelled SpanStatus = "cancelled"
)

// Span is one unit of work inside a trace.
type Span struct {
	SpanID       uint64 `json:"span_id"`
	ParentSpanID uint64 `json:"parent_span_id,omitempty"`
	// SegmentID is the root span of one service's slice of the trace.
	SegmentID uint64 `json:"segment_id,omitempty"`
	IsSegment bool   `json:"is_segment,omitempty"`

	// Name MUST be parameterized — "GET /users/:id", never "GET /users/8412".
	// It is a LowCardinality column: raw IDs in here make the dictionary
	// explode and every aggregation over it meaningless. Enforcing this is the
	// single most important job of auto-instrumentation.
	Name string `json:"name"`
	// Op is the category: http.server, db.query, cache.get, ui.render.
	Op      string `json:"op"`
	Service string `json:"service,omitempty"`

	StartTime time.Time     `json:"start_time"`
	Duration  time.Duration `json:"duration"`
	Status    SpanStatus    `json:"status"`

	HTTPMethod string `json:"http_method,omitempty"`
	HTTPStatus uint16 `json:"http_status,omitempty"`
	HTTPRoute  string `json:"http_route,omitempty"`

	DBSystem    string `json:"db_system,omitempty"`
	DBStatement string `json:"db_statement,omitempty"`

	// Measurements carries the RUM Web Vitals (lcp, inp, cls, fcp, ttfb) on the
	// pageload span. Modelling them as a map rather than columns means M6 needs
	// no new ingest path at all.
	Measurements map[string]float64 `json:"measurements,omitempty"`
}

// MetricType is how a metric value is aggregated.
type MetricType string

const (
	MetricCounter      MetricType = "counter"
	MetricGauge        MetricType = "gauge"
	MetricDistribution MetricType = "distribution"
	MetricSet          MetricType = "set"
)

// Valid reports whether t is a known metric type.
func (t MetricType) Valid() bool {
	switch t {
	case MetricCounter, MetricGauge, MetricDistribution, MetricSet:
		return true
	}
	return false
}

// Metric is a single counter/gauge/distribution/set observation.
type Metric struct {
	Name  string     `json:"name"`
	Type  MetricType `json:"type"`
	Unit  string     `json:"unit,omitempty"` // "millisecond", "byte", "none"
	Value float64    `json:"value"`
}

// Session tracks whether a user's session was crash-free, which is the number
// that answers "is this release safe to roll out further?".
type Session struct {
	SessionID uuid.UUID     `json:"session_id"`
	Status    string        `json:"status"` // ok | errored | crashed | exited
	Duration  time.Duration `json:"duration,omitempty"`
	Errors    uint32        `json:"errors,omitempty"`
}

// ClientReport is the SDK telling us what it threw away locally — because it
// was rate-limited, its buffer was full, or a beforeSend hook dropped it.
//
// Without this we would report the counts we happened to receive and call them
// the truth. Silently under-reporting is the one thing that destroys trust in
// an observability tool permanently, so the SDK is required to confess.
type ClientReport struct {
	Timestamp       time.Time         `json:"timestamp"`
	DiscardedEvents []DiscardedEvents `json:"discarded_events"`
}

// DiscardedEvents is a count of items dropped for one reason, for one signal.
type DiscardedEvents struct {
	Reason   string `json:"reason"` // "queue_overflow", "ratelimit_backoff", "before_send"
	Category Kind   `json:"category"`
	Quantity uint64 `json:"quantity"`
}
