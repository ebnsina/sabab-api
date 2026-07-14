package processor

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ebnsina/sabab-api/internal/event"
	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/google/uuid"
)

// maxClockSkew bounds how far a client's clock may disagree with ours before we
// stop believing it.
//
// Client clocks are wrong all the time — a laptop resuming from sleep, a phone
// with the wrong timezone, a VM whose clock drifted. An event timestamped in
// 2035 lands in a ClickHouse partition that will not expire for a decade; one
// timestamped in 1970 lands in a partition the TTL deletes immediately, so the
// event vanishes. Neither is acceptable, so we correct rather than trust.
const maxClockSkew = 24 * time.Hour

// normalize turns a queued job into the internal model.
//
// This is the only place a raw payload is decoded. Everything downstream —
// scrubbing, grouping, writing — works on event.Item and never sees the wire
// format, which is what will let the OTLP adapter (M8) reuse all of it.
func normalize(job ingest.Job) (event.Item, error) {
	item := event.Item{
		Kind: job.Type,
		Meta: event.Meta{
			ProjectID:  job.ProjectID,
			ReceivedAt: job.ReceivedAt,
			SDK:        job.SDK,
		},
	}

	switch job.Type {
	case event.KindError:
		var payload errorPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return event.Item{}, fmt.Errorf("decode error payload: %w", err)
		}
		item.Meta = mergeMeta(item.Meta, payload.meta(), job)
		item.Error = payload.error()

	default:
		// Logs, spans, metrics, sessions and client reports are modelled but not
		// yet ingested — they land in M2 through M4. Refusing them explicitly is
		// better than silently writing nothing and leaving someone to wonder
		// where their data went.
		return event.Item{}, fmt.Errorf("%w: %s", errUnsupportedKind, job.Type)
	}

	return item, nil
}

// errorPayload is the wire shape of an error event. It is a separate type from
// event.Error on purpose: the wire format is a contract with clients we do not
// control, and the internal model must be free to change without breaking it.
type errorPayload struct {
	EventID     string             `json:"event_id"`
	Timestamp   time.Time          `json:"timestamp"`
	Level       event.Level        `json:"level"`
	Platform    string             `json:"platform"`
	Release     string             `json:"release"`
	Environment string             `json:"environment"`
	Exception   []event.Exception  `json:"exception"`
	Message     string             `json:"message"`
	Breadcrumbs []event.Breadcrumb `json:"breadcrumbs"`
	Contexts    map[string]any     `json:"contexts"`
	User        event.User         `json:"user"`
	Tags        map[string]string  `json:"tags"`
	TraceID     string             `json:"trace_id"`
	SpanID      string             `json:"span_id"`
	Fingerprint []string           `json:"fingerprint"`
}

func (p errorPayload) meta() event.Meta {
	return event.Meta{
		EventID:     parseUUID(p.EventID),
		Timestamp:   p.Timestamp,
		Environment: p.Environment,
		Release:     p.Release,
		Platform:    p.Platform,
		TraceID:     parseUUID(p.TraceID),
		SpanID:      parseSpanID(p.SpanID),
		User:        p.User,
		Tags:        p.Tags,
	}
}

func (p errorPayload) error() *event.Error {
	level := p.Level
	if !level.Valid() {
		level = event.LevelError
	}
	return &event.Error{
		Level:       level,
		Exceptions:  p.Exception,
		Message:     p.Message,
		Breadcrumbs: p.Breadcrumbs,
		Contexts:    p.Contexts,
		Fingerprint: p.Fingerprint,
	}
}

// mergeMeta fills the gateway-established fields over the client-supplied ones.
// The client never gets to decide its own project, its received time, or its
// own IP.
func mergeMeta(base, payload event.Meta, job ingest.Job) event.Meta {
	merged := payload
	merged.ProjectID = base.ProjectID
	merged.ReceivedAt = base.ReceivedAt
	merged.SDK = base.SDK

	if merged.EventID == uuid.Nil {
		// An SDK that omits the id still gets one — the alternative is a row we
		// cannot address.
		merged.EventID = uuid.New()
	}
	merged.Timestamp = correctTimestamp(merged.Timestamp, job)

	// "{{auto}}" is the SDK asking us to fill in the address it cannot know.
	if merged.User.IP == autoIP || merged.User.IP == "" {
		merged.User.IP = job.ClientIP
	}
	if merged.Environment == "" {
		merged.Environment = "production"
	}
	return merged
}

// autoIP is the placeholder a browser sends, because it cannot know its own
// public address.
const autoIP = "{{auto}}"

// correctTimestamp keeps an event inside a believable window.
//
// When the client told us when it flushed (sent_at), we can measure its clock
// against ours and shift the event by that offset — which preserves the true
// *interval* between two events from the same device even if the device's clock
// is hours off. That interval is what a breadcrumb timeline depends on.
func correctTimestamp(ts time.Time, job ingest.Job) time.Time {
	if ts.IsZero() {
		return job.ReceivedAt
	}

	if !job.SentAt.IsZero() {
		skew := job.ReceivedAt.Sub(job.SentAt)
		if skew < -maxClockSkew || skew > maxClockSkew {
			// The clock is not merely off, it is nonsense. Do not try to
			// preserve intervals against a clock we have no faith in.
			return job.ReceivedAt
		}
		ts = ts.Add(skew)
	}

	// Whatever the correction produced, an event may not be in the future or
	// older than the retention window — either would land it in a partition that
	// makes it effectively unreachable.
	switch {
	case ts.After(job.ReceivedAt.Add(time.Minute)):
		return job.ReceivedAt
	case ts.Before(job.ReceivedAt.Add(-maxClockSkew)):
		return job.ReceivedAt
	}
	return ts
}

func parseUUID(s string) uuid.UUID {
	if s == "" {
		return uuid.Nil
	}
	parsed, err := uuid.Parse(s)
	if err != nil {
		// A malformed trace id must not lose us the whole event — it only
		// costs the correlation, which is better than dropping the error.
		return uuid.Nil
	}
	return parsed
}

// parseSpanID accepts the 16-hex-char form the W3C traceparent spec uses.
func parseSpanID(s string) uint64 {
	if s == "" {
		return 0
	}
	var id uint64
	for _, c := range s {
		var digit uint64
		switch {
		case c >= '0' && c <= '9':
			digit = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			digit = uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			digit = uint64(c-'A') + 10
		default:
			return 0
		}
		id = id<<4 | digit
	}
	return id
}
