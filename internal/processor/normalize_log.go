package processor

import (
	"strings"
	"time"

	"github.com/ebnsina/sabab-api/internal/event"
)

// logPayload is the wire shape of a log item, kept separate from event.Log for
// the same reason errorPayload is separate from event.Error: the wire format is
// a contract with clients we do not control, and the internal model must be free
// to change without breaking it.
type logPayload struct {
	Timestamp   time.Time         `json:"timestamp"`
	Severity    string            `json:"severity"` // trace|debug|info|warn|error|fatal
	Service     string            `json:"service"`
	Body        string            `json:"body"`
	Template    string            `json:"template"`
	Environment string            `json:"environment"`
	Release     string            `json:"release"`
	TraceID     string            `json:"trace_id"`
	SpanID      string            `json:"span_id"`
	Attributes  map[string]string `json:"attributes"`
}

func (p logPayload) meta() event.Meta {
	return event.Meta{
		Timestamp:   p.Timestamp,
		Environment: p.Environment,
		Release:     p.Release,
		TraceID:     parseUUID(p.TraceID),
		SpanID:      parseSpanID(p.SpanID),
	}
}

func (p logPayload) log() *event.Log {
	severity := normalizeSeverity(p.Severity)
	return &event.Log{
		SeverityText:   severity,
		SeverityNumber: severityNumber(severity),
		Service:        p.Service,
		Body:           p.Body,
		Template:       p.Template,
		Attributes:     p.Attributes,
	}
}

// normalizeSeverity folds the many spellings clients use into the six the schema
// enum accepts. "warning"→"warn", "err"→"error", "critical"/"panic"→"fatal", so
// a console.warn and a pino "warn" and a "WARNING" all land in one bucket that
// "severity >= warn" can range over.
func normalizeSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return "trace"
	case "debug", "verbose":
		return "debug"
	case "warn", "warning":
		return "warn"
	case "error", "err":
		return "error"
	case "fatal", "critical", "crit", "panic", "emerg":
		return "fatal"
	default:
		// Unknown or empty defaults to info — the level a bare console.log emits,
		// and the safe assumption for anything we cannot classify.
		return "info"
	}
}

// severityNumber maps a level to the OpenTelemetry severity number the Enum8
// stores, so an OTLP adapter (M8) maps straight in and "severity >= warn"
// compares numbers.
func severityNumber(severity string) uint8 {
	switch severity {
	case "trace":
		return 1
	case "debug":
		return 5
	case "info":
		return 9
	case "warn":
		return 13
	case "error":
		return 17
	case "fatal":
		return 21
	default:
		return 9
	}
}
