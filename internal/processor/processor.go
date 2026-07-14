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

	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/queue"
	"github.com/ebnsina/sabab-api/internal/store/clickhouse"
)

// EventWriter is the event-plane half.
type EventWriter interface {
	InsertErrors(ctx context.Context, rows []clickhouse.ErrorRow) error
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

// Processor is one worker.
type Processor struct {
	queue    queue.Consumer
	pipeline *Pipeline
	events   EventWriter
	opts     Options
	log      *slog.Logger
}

// New builds a Processor.
func New(q queue.Consumer, pipeline *Pipeline, events EventWriter, opts Options, log *slog.Logger) *Processor {
	if opts.BatchSize <= 0 {
		opts = DefaultOptions()
	}
	return &Processor{queue: q, pipeline: pipeline, events: events, opts: opts, log: log}
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

// handle processes a batch: every message is turned into a row, the rows are
// written in one ClickHouse batch, and only then is anything acked.
func (p *Processor) handle(ctx context.Context, messages []queue.Message) {
	rows := make([]clickhouse.ErrorRow, 0, len(messages))
	// ackable are messages that are done with — successfully processed, or
	// permanently undeliverable. Both must be acked, or they come back forever.
	ackable := make([]string, 0, len(messages))

	var newIssues, regressions int

	for _, msg := range messages {
		// A message that keeps coming back is poison: some payload we cannot
		// handle without dying. Park it and move on — one bad event must not
		// stall every other customer's pipeline.
		if msg.Deliveries > p.opts.MaxDeliveries {
			p.log.Error("parking poison message",
				slog.String("id", msg.ID),
				slog.Int64("deliveries", msg.Deliveries))
			ackable = append(ackable, msg.ID)
			continue
		}

		job, err := ingest.Decode(msg.Body)
		if err != nil {
			// Undecodable: it can never succeed, so retrying is pointless.
			p.log.Error("dropping undecodable job", slog.String("id", msg.ID), slog.Any("error", err))
			ackable = append(ackable, msg.ID)
			continue
		}

		processed, err := p.pipeline.Process(ctx, job)
		switch {
		case errors.Is(err, errUnsupportedKind):
			// A signal we model but do not ingest yet. Expected, not an error.
			p.log.Debug("skipping signal", slog.String("type", string(job.Type)))
			ackable = append(ackable, msg.ID)
			continue
		case err != nil:
			// Could be transient (Postgres blipped) — leave it unacked so it is
			// redelivered, and let MaxDeliveries stop it if it is not.
			p.log.Error("processing failed",
				slog.String("id", msg.ID),
				slog.Uint64("project_id", job.ProjectID),
				slog.Any("error", err))
			continue
		}

		rows = append(rows, processed.Row)
		ackable = append(ackable, msg.ID)

		if processed.New {
			newIssues++
		}
		if processed.Regressed {
			regressions++
			p.log.Info("issue regressed",
				slog.Uint64("issue_id", processed.IssueID),
				slog.Uint64("project_id", job.ProjectID))
		}
	}

	if len(rows) > 0 {
		if err := p.events.InsertErrors(ctx, rows); err != nil {
			// The write failed, so we must NOT ack: the messages have to come
			// back. Acking here would report success for events that are
			// nowhere — the silent data loss that destroys trust permanently.
			p.log.Error("write failed, leaving batch unacked for redelivery",
				slog.Int("rows", len(rows)), slog.Any("error", err))
			return
		}
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
		slog.Int("rows", len(rows)),
		slog.Int("new_issues", newIssues),
		slog.Int("regressions", regressions))
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
