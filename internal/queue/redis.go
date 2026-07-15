package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ebnsina/sabab-api/internal/config"
	"github.com/redis/go-redis/v9"
)

// Stream and field names. bodyField is the single field each entry carries —
// Redis Streams entries are hashes, and we do not need more than one.
const (
	IngestStream = "sabab:ingest"
	bodyField    = "b"
)

// DefaultMaxLen bounds the stream. This is a deliberate, documented data-loss
// point: if the processor falls far enough behind, Redis trims the oldest
// entries and those events are gone.
//
// The alternative — an unbounded stream — trades a bounded, visible loss for an
// unbounded one: Redis runs out of memory and takes the gateway down with it,
// losing everything rather than the oldest slice. Backpressure has to have a
// defined behaviour at every hop, and this is the definition at this one.
// The processor's lag is what tells us we are near it.
const DefaultMaxLen int64 = 1_000_000

// Redis implements Producer and Consumer over Redis Streams.
type Redis struct {
	client *redis.Client
	stream string
	maxLen int64

	group    string
	consumer string
}

// NewRedis connects to Redis and verifies the connection.
func NewRedis(ctx context.Context, cfg config.Redis) (*Redis, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Redis{client: client, stream: IngestStream, maxLen: DefaultMaxLen}, nil
}

// OnStream returns a handle to a different stream sharing the same connection.
//
// The alerts stream is far lower-volume than ingest and has its own retention,
// so it is a separate stream — but it must not open a second Redis connection
// pool. One pool per process; this clones the handle, not the client.
func (q *Redis) OnStream(name string, maxLen int64) *Redis {
	clone := *q
	clone.stream = name
	clone.maxLen = maxLen
	// Reset any group binding: a stream handle starts as a producer, and a
	// consumer is derived from it with WithGroup.
	clone.group = ""
	clone.consumer = ""
	return &clone
}

// AlertStream is the low-volume stream carrying new-issue and regression
// signals from the processor to the alerter.
const AlertStream = "sabab:alerts"

// AlertStreamMaxLen bounds the alert stream. Much smaller than ingest: alert
// signals are rare, and if the alerter falls this far behind, dropping the
// oldest pending alert is the right failure — a week-old "new issue" notice is
// noise, not signal.
const AlertStreamMaxLen int64 = 100_000

// WithGroup returns a consumer bound to a consumer group, creating both the
// group and the stream if they do not exist.
//
// The group is what lets processors scale horizontally: Redis hands each entry
// to exactly one member, and remembers what each member has not yet acked.
func (q *Redis) WithGroup(ctx context.Context, group, consumer string) (*Redis, error) {
	// MkStream so the group can be created before anything has ever been
	// published — otherwise a processor that boots before the first event errors
	// out, which is the normal order of events on a fresh install.
	err := q.client.XGroupCreateMkStream(ctx, q.stream, group, "0").Err()
	if err != nil && !isBusyGroup(err) {
		return nil, fmt.Errorf("create consumer group %q: %w", group, err)
	}

	clone := *q
	clone.group = group
	clone.consumer = consumer
	return &clone, nil
}

// isBusyGroup reports whether err is Redis saying the group already exists,
// which is the expected case on every boot after the first.
func isBusyGroup(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "BUSYGROUP")
}

// Publish appends bodies to the stream in one pipeline round trip.
func (q *Redis) Publish(ctx context.Context, bodies ...[]byte) error {
	if len(bodies) == 0 {
		return nil
	}

	pipe := q.client.Pipeline()
	for _, body := range bodies {
		pipe.XAdd(ctx, &redis.XAddArgs{
			Stream: q.stream,
			// Approximate trimming: exact trimming makes XADD O(n) and would
			// put a latency spike on the gateway's hot path for no benefit.
			MaxLen: q.maxLen,
			Approx: true,
			Values: map[string]any{bodyField: body},
		})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("publish %d items: %w", len(bodies), err)
	}
	return nil
}

// Consume claims up to max new messages for this consumer.
func (q *Redis) Consume(ctx context.Context, max int, block time.Duration) ([]Message, error) {
	if err := q.requireGroup(); err != nil {
		return nil, err
	}

	// ">" means "entries never delivered to any consumer in this group".
	streams, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    q.group,
		Consumer: q.consumer,
		Streams:  []string{q.stream, ">"},
		Count:    int64(max),
		Block:    block,
	}).Result()
	if err != nil {
		// Nothing arrived within the block window. That is the idle path, not a
		// failure — returning an error here would spam the logs on a quiet
		// project.
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("read from group: %w", err)
	}

	var messages []Message
	for _, stream := range streams {
		for _, entry := range stream.Messages {
			messages = append(messages, toMessage(entry, 1))
		}
	}
	return messages, nil
}

// Reclaim takes over messages pending longer than minIdle.
func (q *Redis) Reclaim(ctx context.Context, minIdle time.Duration, max int) ([]Message, error) {
	if err := q.requireGroup(); err != nil {
		return nil, err
	}

	entries, _, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   q.stream,
		Group:    q.group,
		Consumer: q.consumer,
		MinIdle:  minIdle,
		Start:    "0-0",
		Count:    int64(max),
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("reclaim pending: %w", err)
	}

	messages := make([]Message, 0, len(entries))
	for _, entry := range entries {
		// Delivery count is not returned by XAUTOCLAIM; the caller treats a
		// reclaimed message as at least a second delivery, which is what the
		// poison-message check needs to know.
		messages = append(messages, toMessage(entry, 2))
	}
	return messages, nil
}

// Ack marks messages as processed.
func (q *Redis) Ack(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := q.requireGroup(); err != nil {
		return err
	}
	if err := q.client.XAck(ctx, q.stream, q.group, ids...).Err(); err != nil {
		return fmt.Errorf("ack %d messages: %w", len(ids), err)
	}
	return nil
}

// Lag reports how many entries the group has not yet delivered. This is the
// number that says whether we are approaching DefaultMaxLen — that is, whether
// we are about to start dropping events.
func (q *Redis) Lag(ctx context.Context) (int64, error) {
	if err := q.requireGroup(); err != nil {
		return 0, err
	}
	groups, err := q.client.XInfoGroups(ctx, q.stream).Result()
	if err != nil {
		return 0, fmt.Errorf("read group info: %w", err)
	}
	for _, g := range groups {
		if g.Name == q.group {
			return g.Lag, nil
		}
	}
	return 0, fmt.Errorf("consumer group %q not found", q.group)
}

// Ping satisfies health.Check.
func (q *Redis) Ping(ctx context.Context) error {
	if err := q.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis unreachable: %w", err)
	}
	return nil
}

func (q *Redis) Close() error { return q.client.Close() }

// Client exposes the underlying connection so the rate limiter can share it.
// One Redis connection pool per process, not one per feature.
func (q *Redis) Client() *redis.Client { return q.client }

func (q *Redis) requireGroup() error {
	if q.group == "" {
		return errors.New("queue: consumer used without a group; call WithGroup first")
	}
	return nil
}

func toMessage(entry redis.XMessage, deliveries int64) Message {
	var body []byte
	switch v := entry.Values[bodyField].(type) {
	case string:
		body = []byte(v)
	case []byte:
		body = v
	}
	return Message{ID: entry.ID, Body: body, Deliveries: deliveries}
}

var (
	_ Producer = (*Redis)(nil)
	_ Consumer = (*Redis)(nil)
)
