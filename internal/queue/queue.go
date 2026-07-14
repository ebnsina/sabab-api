// Package queue is the boundary between the gateway and the processor.
//
// The queue is not an optimisation, it is the reason the gateway can promise a
// fast response. Symbolication and grouping are slow and can fail; the gateway
// must acknowledge in single-digit milliseconds or we become the reason our
// customers' apps are slow. So the gateway does the cheap, safe work (auth,
// limits, enqueue) and everything expensive happens on the other side of this
// interface.
//
// Redis Streams backs it today because it is trivial to self-host. The
// interface exists so Kafka can replace it at scale without either side
// noticing — which is why nothing here leaks a Redis concept.
package queue

import (
	"context"
	"time"
)

// Message is one queued item, opaque to the queue itself.
type Message struct {
	// ID is assigned by the broker and is what Ack refers to.
	ID   string
	Body []byte

	// Deliveries is how many times this message has been handed to a consumer.
	// A message that keeps coming back is poison — it must eventually be parked
	// rather than retried forever, or one bad payload stalls the pipeline.
	Deliveries int64
}

// Producer is the gateway's half.
type Producer interface {
	// Publish enqueues bodies. It is all-or-nothing per call from the caller's
	// point of view: an error means the caller must not report success.
	Publish(ctx context.Context, bodies ...[]byte) error
	Close() error
}

// Consumer is the processor's half.
type Consumer interface {
	// Consume claims up to max new messages, blocking up to block for them.
	// It returns an empty slice — not an error — when nothing arrived in time.
	Consume(ctx context.Context, max int, block time.Duration) ([]Message, error)

	// Ack marks messages as done. Until a message is acked it stays pending and
	// will be reclaimed by another consumer, which is what makes a processor
	// crash lose nothing.
	Ack(ctx context.Context, ids ...string) error

	// Reclaim takes over messages that another consumer claimed and never
	// acked — because it crashed, or hung. Without this, a dead worker's
	// in-flight events would sit pending forever and simply never be processed.
	Reclaim(ctx context.Context, minIdle time.Duration, max int) ([]Message, error)

	Close() error
}
