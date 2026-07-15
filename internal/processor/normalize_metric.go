package processor

import (
	"time"

	"github.com/ebnsina/sabab-api/internal/event"
)

// metricPayload is the wire shape of a metric item.
type metricPayload struct {
	Timestamp   time.Time         `json:"timestamp"`
	Name        string            `json:"name"`
	Type        string            `json:"type"` // counter | gauge | distribution | set
	Unit        string            `json:"unit"`
	Value       float64           `json:"value"`
	Environment string            `json:"environment"`
	Release     string            `json:"release"`
	Tags        map[string]string `json:"tags"`
}

func (p metricPayload) meta() event.Meta {
	return event.Meta{
		Timestamp:   p.Timestamp,
		Environment: p.Environment,
		Release:     p.Release,
		Tags:        p.Tags,
	}
}

func (p metricPayload) metric() *event.Metric {
	t := event.MetricType(p.Type)
	if !t.Valid() {
		t = event.MetricCounter
	}
	return &event.Metric{
		Name:  p.Name,
		Type:  t,
		Unit:  p.Unit,
		Value: p.Value,
	}
}
