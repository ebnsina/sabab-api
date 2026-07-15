package processor

import (
	"time"

	"github.com/ebnsina/sabab-api/internal/event"
)

// spanPayload is the wire shape of a span item.
type spanPayload struct {
	EventID      string `json:"event_id"`
	TraceID      string `json:"trace_id"`
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id"`
	SegmentID    string `json:"segment_id"`
	IsSegment    bool   `json:"is_segment"`

	Name    string `json:"name"`
	Op      string `json:"op"`
	Service string `json:"service"`

	StartTime  time.Time `json:"start_time"`
	DurationMS float64   `json:"duration_ms"`
	Status     string    `json:"status"`

	Environment string `json:"environment"`
	Release     string `json:"release"`

	HTTPMethod string `json:"http_method"`
	HTTPStatus uint16 `json:"http_status"`
	HTTPRoute  string `json:"http_route"`

	DBSystem    string `json:"db_system"`
	DBStatement string `json:"db_statement"`

	Tags         map[string]string  `json:"tags"`
	Measurements map[string]float64 `json:"measurements"`

	User event.User `json:"user"`
}

func (p spanPayload) meta() event.Meta {
	return event.Meta{
		EventID:     parseUUID(p.EventID),
		Timestamp:   p.StartTime,
		Environment: p.Environment,
		Release:     p.Release,
		TraceID:     parseUUID(p.TraceID),
		SpanID:      parseSpanID(p.SpanID),
		User:        p.User,
	}
}

func (p spanPayload) span() *event.Span {
	status := event.SpanStatus(p.Status)
	switch status {
	case event.SpanStatusOK, event.SpanStatusError, event.SpanStatusCancelled:
	default:
		status = event.SpanStatusOK
	}

	return &event.Span{
		SpanID:       parseSpanID(p.SpanID),
		ParentSpanID: parseSpanID(p.ParentSpanID),
		SegmentID:    parseSpanID(p.SegmentID),
		IsSegment:    p.IsSegment,
		Name:         p.Name,
		Op:           p.Op,
		Service:      p.Service,
		StartTime:    p.StartTime,
		// The wire carries milliseconds (what a browser Performance API reports);
		// we store nanoseconds, so convert once here.
		Duration:     time.Duration(p.DurationMS * float64(time.Millisecond)),
		Status:       status,
		HTTPMethod:   p.HTTPMethod,
		HTTPStatus:   p.HTTPStatus,
		HTTPRoute:    p.HTTPRoute,
		DBSystem:     p.DBSystem,
		DBStatement:  p.DBStatement,
		Measurements: p.Measurements,
	}
}
