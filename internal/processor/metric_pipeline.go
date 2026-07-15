package processor

import (
	"context"
	"fmt"

	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
)

// ProcessMetric turns a metric job into a raw row.
//
// Metrics need no symbolication or grouping. Tags are scrubbed (a tag value can
// carry PII), and the row goes to metrics_raw, where the rollup MVs aggregate it
// at insert time. Nothing reads metrics_raw directly.
func (p *Pipeline) ProcessMetric(_ context.Context, job ingest.Job) (clickhouse.MetricRow, error) {
	item, err := normalize(job)
	if err != nil {
		return clickhouse.MetricRow{}, err
	}
	if item.Metric == nil {
		return clickhouse.MetricRow{}, fmt.Errorf("%w: %s", errUnsupportedKind, item.Kind)
	}

	item.Meta.Tags = p.scrubber.Map(item.Meta.Tags)

	m := item.Metric
	return clickhouse.MetricRow{
		ProjectID: item.Meta.ProjectID,
		Timestamp: item.Meta.Timestamp,
		Name:      m.Name,
		Type:      string(m.Type),
		Unit:      m.Unit,
		Tags:      nonNilMap(item.Meta.Tags),
		Value:     m.Value,
	}, nil
}
