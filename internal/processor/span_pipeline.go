package processor

import (
	"context"
	"fmt"

	"github.com/ebnsina/sabab-api/internal/event"
	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
)

// ProcessSpan turns a span job into a row.
//
// Like logs, spans do not group or symbolicate. The valuable work is scrubbing
// (a db.statement or an http tag can carry PII) and carrying the trace context,
// so the waterfall can reassemble the request. The db statement is stored as
// sent — normalising it for N+1 detection is an aggregation concern, done at
// query time, not on the write path.
func (p *Pipeline) ProcessSpan(_ context.Context, job ingest.Job) (clickhouse.SpanRow, error) {
	item, err := normalize(job)
	if err != nil {
		return clickhouse.SpanRow{}, err
	}
	if item.Span == nil {
		return clickhouse.SpanRow{}, fmt.Errorf("%w: %s", errUnsupportedKind, item.Kind)
	}

	p.scrubSpan(&item)

	s := item.Span
	// A span that is its own segment (no parent) is where one service's slice of
	// the trace begins; the SDK marks it, and we trust that.
	return clickhouse.SpanRow{
		ProjectID:    item.Meta.ProjectID,
		TraceID:      item.Meta.TraceID,
		SpanID:       s.SpanID,
		ParentSpanID: s.ParentSpanID,
		SegmentID:    s.SegmentID,
		IsSegment:    s.IsSegment,
		Name:         s.Name,
		Op:           s.Op,
		Service:      s.Service,
		Timestamp:    item.Meta.Timestamp,
		ReceivedAt:   item.Meta.ReceivedAt,
		DurationNS:   uint64(s.Duration.Nanoseconds()),
		Status:       string(s.Status),
		Environment:  item.Meta.Environment,
		Release:      item.Meta.Release,
		HTTPMethod:   s.HTTPMethod,
		HTTPStatus:   s.HTTPStatus,
		HTTPRoute:    s.HTTPRoute,
		DBSystem:     s.DBSystem,
		DBStatement:  s.DBStatement,
		Tags:         nonNilMap(item.Meta.Tags),
		Measurements: s.Measurements,
		UserID:       item.Meta.User.ID,
	}, nil
}

// scrubSpan redacts the db statement and tags before the write.
func (p *Pipeline) scrubSpan(item *event.Item) {
	s := item.Span
	s.DBStatement = p.scrubber.String(s.DBStatement)
	item.Meta.Tags = p.scrubber.Map(item.Meta.Tags)
}
