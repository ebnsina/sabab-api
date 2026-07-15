package processor

import (
	"context"
	"fmt"

	"github.com/ebnsina/sabab-api/internal/event"
	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
)

// ProcessLog turns a log job into a row.
//
// Logs are simpler than errors: no symbolication, no grouping, no issue upsert.
// The valuable steps that remain are scrubbing (a log line is a prime place for
// a token to leak into) and carrying the trace_id through, so "show me the logs
// around this error" works. The template is preserved as sent — grouping logs by
// template is an M2+ dashboard concern, not something the write path decides.
func (p *Pipeline) ProcessLog(_ context.Context, job ingest.Job) (clickhouse.LogRow, error) {
	item, err := normalize(job)
	if err != nil {
		return clickhouse.LogRow{}, err
	}
	if item.Log == nil {
		return clickhouse.LogRow{}, fmt.Errorf("%w: %s", errUnsupportedKind, item.Kind)
	}

	p.scrubLog(&item)

	log := item.Log
	return clickhouse.LogRow{
		ProjectID:   item.Meta.ProjectID,
		Severity:    log.SeverityText,
		Timestamp:   item.Meta.Timestamp,
		ReceivedAt:  item.Meta.ReceivedAt,
		Service:     log.Service,
		Environment: item.Meta.Environment,
		Release:     item.Meta.Release,
		Body:        log.Body,
		Template:    log.Template,
		TraceID:     item.Meta.TraceID,
		SpanID:      item.Meta.SpanID,
		Attributes:  nonNilMap(log.Attributes),
	}, nil
}

// scrubLog redacts the body, the template and the attributes before the write.
// A log line is exactly where an "Authorization: Bearer …" or a card number ends
// up pasted, so scrubbing here is not optional.
func (p *Pipeline) scrubLog(item *event.Item) {
	log := item.Log
	log.Body = p.scrubber.String(log.Body)
	log.Template = p.scrubber.String(log.Template)
	log.Attributes = p.scrubber.Map(log.Attributes)
}

// nonNilMap ensures the Map column gets an empty map rather than nil, since it
// is not Nullable (schema-types-avoid-nullable).
func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
