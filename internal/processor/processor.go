// Package processor drains the queue and writes events.
//
// Everything expensive in the platform happens here, which is exactly why it is
// behind the queue: the gateway stays fast, and this can be slow, retry, and
// fail without any customer's app noticing.
package processor

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ebnsina/sabab-api/internal/event"
	"github.com/ebnsina/sabab-api/internal/grouping"
	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/queue"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
)

// EventWriter is the event-plane half. One method per signal table; each new
// signal type adds a method and a batch in handle().
type EventWriter interface {
	InsertErrors(ctx context.Context, rows []clickhouse.ErrorRow) error
	InsertLogs(ctx context.Context, rows []clickhouse.LogRow) error
	InsertSpans(ctx context.Context, rows []clickhouse.SpanRow) error
}

// Options tune the worker loop.
type Options struct {
	// BatchSize is how many messages to claim per read.
	BatchSize int
	// FlushInterval bounds how long a row may wait in the buffer.
	//
	// This is the latency the user actually experiences: an error appears in the
	// dashboard roughly this long after it is thrown. It trades against
	// ClickHouse's strong preference for large batches, so it is a product
	// decision, not a technical one.
	FlushInterval time.Duration
	// MaxDeliveries is how many times a message may be redelivered before it is
	// parked. Without a cap, one payload that always panics is retried forever
	// and the pipeline never advances past it.
	MaxDeliveries int64
	// ReclaimInterval is how often to take over messages abandoned by a crashed
	// worker.
	ReclaimInterval time.Duration
	// ReclaimMinIdle is how long a message must be pending before another worker
	// may steal it. Longer than the slowest legitimate processing time, or two
	// workers will process the same event.
	ReclaimMinIdle time.Duration
}

// DefaultOptions are tuned for a self-hosted install: sub-second visibility,
// batches large enough that ClickHouse is not asked to merge thousands of tiny
// parts.
func DefaultOptions() Options {
	return Options{
		BatchSize:       500,
		FlushInterval:   time.Second,
		MaxDeliveries:   5,
		ReclaimInterval: 30 * time.Second,
		ReclaimMinIdle:  time.Minute,
	}
}

// AlertPublisher is where new-issue and regression signals go. An interface, and
// optional (may be nil): the processor's job is to write events, and it must
// keep doing that even if the alert path is unavailable.
type AlertPublisher interface {
	Publish(ctx context.Context, bodies ...[]byte) error
}

// Processor is one worker.
type Processor struct {
	queue    queue.Consumer
	pipeline *Pipeline
	events   EventWriter
	alerts   AlertPublisher
	opts     Options
	log      *slog.Logger
}

// New builds a Processor. alerts may be nil, in which case no alert signals are
// emitted — the event-writing path is unaffected.
func New(q queue.Consumer, pipeline *Pipeline, events EventWriter, alerts AlertPublisher, opts Options, log *slog.Logger) *Processor {
	if opts.BatchSize <= 0 {
		opts = DefaultOptions()
	}
	return &Processor{queue: q, pipeline: pipeline, events: events, alerts: alerts, opts: opts, log: log}
}

// Run drains the queue until ctx is cancelled.
func (p *Processor) Run(ctx context.Context) error {
	reclaim := time.NewTicker(p.opts.ReclaimInterval)
	defer reclaim.Stop()

	for {
		select {
		case <-ctx.Done():
			p.log.Info("processor stopping")
			return nil
		case <-reclaim.C:
			p.reclaim(ctx)
		default:
		}

		// Block briefly for new messages. The block is what keeps an idle
		// processor from spinning the CPU on an empty queue.
		messages, err := p.queue.Consume(ctx, p.opts.BatchSize, p.opts.FlushInterval)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// Redis is down. Do not spin: back off, and let readiness report it.
			p.log.Error("consume failed", slog.Any("error", err))
			sleep(ctx, time.Second)
			continue
		}
		if len(messages) == 0 {
			continue
		}
		p.handle(ctx, messages)
	}
}

// handle processes a batch. Each message is routed by signal type to its row
// batch, each batch is written to its own table, and a message is acked only
// after ITS table's write succeeds — so a failed error-write does not throw away
// logs that were written fine, and vice versa.
func (p *Processor) handle(ctx context.Context, messages []queue.Message) {
	var (
		errorRows []clickhouse.ErrorRow
		errorAck  []string // acked only if InsertErrors succeeds
		logRows   []clickhouse.LogRow
		logAck    []string // acked only if InsertLogs succeeds
		spanRows  []clickhouse.SpanRow
		spanAck   []string // acked only if InsertSpans succeeds
		// doneAck are messages finished regardless of any write — poison,
		// undecodable, or a signal we do not ingest yet. Always safe to ack.
		doneAck []string
		signals []ingest.AlertSignal
	)

	var newIssues, regressions int

	for _, msg := range messages {
		// A message that keeps coming back is poison: some payload we cannot
		// handle without dying. Park it and move on — one bad event must not
		// stall every other customer's pipeline.
		if msg.Deliveries > p.opts.MaxDeliveries {
			p.log.Error("parking poison message",
				slog.String("id", msg.ID),
				slog.Int64("deliveries", msg.Deliveries))
			doneAck = append(doneAck, msg.ID)
			continue
		}

		job, err := ingest.Decode(msg.Body)
		if err != nil {
			// Undecodable: it can never succeed, so retrying is pointless.
			p.log.Error("dropping undecodable job", slog.String("id", msg.ID), slog.Any("error", err))
			doneAck = append(doneAck, msg.ID)
			continue
		}

		switch job.Type {
		case event.KindError:
			processed, err := p.pipeline.Process(ctx, job)
			if err != nil {
				p.recordProcessFailure(msg, job, err, &doneAck)
				continue
			}
			errorRows = append(errorRows, processed.Row)
			errorAck = append(errorAck, msg.ID)

			// Collect alert signals but do NOT publish them yet: an alert must
			// only fire for an event we actually persisted.
			if processed.New {
				newIssues++
				signals = append(signals, alertFor(ingest.AlertNewIssue, job, processed))
			}
			if processed.Regressed {
				regressions++
				p.log.Info("issue regressed",
					slog.Uint64("issue_id", processed.IssueID),
					slog.Uint64("project_id", job.ProjectID))
				signals = append(signals, alertFor(ingest.AlertRegression, job, processed))
			}

		case event.KindLog:
			row, err := p.pipeline.ProcessLog(ctx, job)
			if err != nil {
				p.recordProcessFailure(msg, job, err, &doneAck)
				continue
			}
			logRows = append(logRows, row)
			logAck = append(logAck, msg.ID)

		case event.KindSpan:
			row, err := p.pipeline.ProcessSpan(ctx, job)
			if err != nil {
				p.recordProcessFailure(msg, job, err, &doneAck)
				continue
			}
			spanRows = append(spanRows, row)
			spanAck = append(spanAck, msg.ID)

		default:
			// A signal we model but do not ingest yet (spans, metrics, sessions).
			// Expected, not an error — ack and move on.
			p.log.Debug("skipping signal", slog.String("type", string(job.Type)))
			doneAck = append(doneAck, msg.ID)
		}
	}

	ackable := doneAck

	if len(errorRows) > 0 {
		if err := p.events.InsertErrors(ctx, errorRows); err != nil {
			// The write failed, so we must NOT ack these: they have to come back.
			// Acking would report success for events that are nowhere — the
			// silent data loss that destroys trust permanently.
			p.log.Error("error write failed, leaving batch unacked",
				slog.Int("rows", len(errorRows)), slog.Any("error", err))
		} else {
			ackable = append(ackable, errorAck...)
			// Only now that the events are durable, raise their alerts. Publishing
			// is best-effort: a missed notification beats a re-written event.
			p.publishAlerts(ctx, signals)
		}
	}

	if len(logRows) > 0 {
		if err := p.events.InsertLogs(ctx, logRows); err != nil {
			p.log.Error("log write failed, leaving batch unacked",
				slog.Int("rows", len(logRows)), slog.Any("error", err))
		} else {
			ackable = append(ackable, logAck...)
		}
	}

	if len(spanRows) > 0 {
		if err := p.events.InsertSpans(ctx, spanRows); err != nil {
			p.log.Error("span write failed, leaving batch unacked",
				slog.Int("rows", len(spanRows)), slog.Any("error", err))
		} else {
			ackable = append(ackable, spanAck...)
		}
	}

	if len(ackable) == 0 {
		return
	}
	if err := p.queue.Ack(ctx, ackable...); err != nil {
		// The rows are written but the ack failed. They will be redelivered and
		// written again — a duplicate row, not a lost event. Given the choice,
		// duplicate beats lost every time.
		p.log.Error("ack failed; events may be written twice",
			slog.Int("count", len(ackable)), slog.Any("error", err))
		return
	}

	p.log.Debug("batch processed",
		slog.Int("error_rows", len(errorRows)),
		slog.Int("log_rows", len(logRows)),
		slog.Int("new_issues", newIssues),
		slog.Int("regressions", regressions))
}

// recordProcessFailure classifies a pipeline error. An unsupported kind is an
// expected skip (ack it); anything else may be transient, so it is left unacked
// for redelivery and MaxDeliveries is the backstop.
func (p *Processor) recordProcessFailure(msg queue.Message, job ingest.Job, err error, doneAck *[]string) {
	if errors.Is(err, errUnsupportedKind) {
		p.log.Debug("skipping signal", slog.String("type", string(job.Type)))
		*doneAck = append(*doneAck, msg.ID)
		return
	}
	p.log.Error("processing failed",
		slog.String("id", msg.ID),
		slog.Uint64("project_id", job.ProjectID),
		slog.Any("error", err))
}

// alertFor builds an alert signal from a processed event, carrying everything
// the alerter needs so it never has to query back.
func alertFor(kind ingest.AlertKind, job ingest.Job, p Processed) ingest.AlertSignal {
	return ingest.AlertSignal{
		Kind:        kind,
		ProjectID:   job.ProjectID,
		IssueID:     p.IssueID,
		GroupHash:   grouping.Hex(p.Row.GroupHash),
		Title:       p.Title,
		Culprit:     p.Row.Culprit,
		Level:       p.Row.Level,
		Release:     p.Row.Release,
		Environment: p.Row.Environment,
		At:          p.Row.Timestamp,
	}
}

// publishAlerts enqueues the batch's signals. Best-effort by contract: the
// events are already durable, so a failure here costs a notification, never data.
func (p *Processor) publishAlerts(ctx context.Context, signals []ingest.AlertSignal) {
	if p.alerts == nil || len(signals) == 0 {
		return
	}

	bodies := make([][]byte, 0, len(signals))
	for _, sig := range signals {
		body, err := ingest.EncodeAlert(sig)
		if err != nil {
			p.log.Error("encode alert signal", slog.Any("error", err))
			continue
		}
		bodies = append(bodies, body)
	}

	if err := p.alerts.Publish(ctx, bodies...); err != nil {
		p.log.Error("publish alert signals; notification may be missed",
			slog.Int("count", len(bodies)), slog.Any("error", err))
	}
}

// reclaim takes over messages a crashed worker never acked. Without this, a
// worker that dies mid-batch leaves its in-flight events pending forever, and
// they are simply never processed.
func (p *Processor) reclaim(ctx context.Context) {
	messages, err := p.queue.Reclaim(ctx, p.opts.ReclaimMinIdle, p.opts.BatchSize)
	if err != nil {
		p.log.Error("reclaim failed", slog.Any("error", err))
		return
	}
	if len(messages) == 0 {
		return
	}
	p.log.Info("reclaimed abandoned messages", slog.Int("count", len(messages)))
	p.handle(ctx, messages)
}

// sleep returns early if ctx is cancelled, so shutdown is never delayed by a
// backoff.
func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
