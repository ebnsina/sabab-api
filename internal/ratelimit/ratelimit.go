// Package ratelimit bounds what one project can cost us.
//
// The ingest key is public — it ships in browser bundles — so anyone who reads
// a customer's JS can post to their project. The rate limit is what keeps that
// from turning into an unbounded bill, or into one noisy project starving every
// other tenant's events out of the queue.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Decision is the outcome of one limit check.
type Decision struct {
	Allowed   bool
	Remaining int64
	// RetryAfter is how long the caller should wait. It goes straight into the
	// Retry-After header, because an SDK that backs off politely is worth far
	// more to us than one that hammers a 429 in a tight loop.
	RetryAfter time.Duration
}

// Limit is a token bucket: Burst tokens, refilled at Rate per second.
//
// A token bucket rather than a fixed window because a window lets a caller
// spend its whole quota in the last millisecond of one window and again in the
// first of the next — double the intended rate, exactly at the moment we are
// least able to absorb it.
type Limit struct {
	Rate  float64 // tokens per second
	Burst int64   // bucket capacity
}

// DefaultLimit is the per-project ceiling until per-project quotas land (M9).
// Generous on purpose: a limit that trips during a genuine incident — the very
// moment the customer needs us most — is worse than no limit at all.
var DefaultLimit = Limit{Rate: 100, Burst: 500}

// tokenBucket is evaluated inside Redis so that check-and-consume is atomic.
// Doing it in Go would mean read, decide, write — and two gateway instances
// interleaving those steps would both admit the request that took the last
// token.
//
// KEYS[1]   bucket key
// ARGV[1]   rate (tokens/sec)   ARGV[2] burst
// ARGV[3]   now (unix millis)   ARGV[4] cost
// Returns: {allowed, remaining, retry_after_ms}
var tokenBucket = redis.NewScript(`
local rate   = tonumber(ARGV[1])
local burst  = tonumber(ARGV[2])
local now    = tonumber(ARGV[3])
local cost   = tonumber(ARGV[4])

local state    = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens   = tonumber(state[1])
local last     = tonumber(state[2])

-- A bucket we have never seen starts full, so a brand-new project is not
-- throttled on its very first request.
if tokens == nil then
  tokens = burst
  last   = now
end

-- Refill for the time that has passed, capped at the burst size.
local elapsed = math.max(0, now - last) / 1000.0
tokens = math.min(burst, tokens + (elapsed * rate))

local allowed = 0
local retry   = 0
if tokens >= cost then
  allowed = 1
  tokens  = tokens - cost
else
  -- How long until enough tokens exist for this request.
  retry = math.ceil(((cost - tokens) / rate) * 1000)
end

redis.call('HSET', KEYS[1], 'tokens', tokens, 'ts', now)
-- Expire idle buckets so a flood of one-off keys cannot grow Redis without
-- bound. The TTL covers a full refill from empty, plus slack.
local ttl = math.ceil((burst / rate) * 2) + 10
redis.call('EXPIRE', KEYS[1], ttl)

return {allowed, math.floor(tokens), retry}
`)

// Limiter applies token buckets in Redis.
type Limiter struct {
	client *redis.Client
	limit  Limit
	now    func() time.Time
}

// New builds a limiter. A zero Limit uses DefaultLimit.
func New(client *redis.Client, limit Limit) *Limiter {
	if limit.Rate <= 0 || limit.Burst <= 0 {
		limit = DefaultLimit
	}
	return &Limiter{client: client, limit: limit, now: time.Now}
}

// AllowN consumes cost tokens from the project's bucket.
//
// cost is the number of envelope items, not the number of requests: one request
// carrying 500 errors costs us 500 events of work, and charging it as 1 would
// make the limit trivially bypassable by batching.
func (l *Limiter) AllowN(ctx context.Context, projectID uint64, cost int) (Decision, error) {
	if cost <= 0 {
		return Decision{Allowed: true, Remaining: l.limit.Burst}, nil
	}

	key := fmt.Sprintf("sabab:rl:project:%d", projectID)
	res, err := tokenBucket.Run(ctx, l.client, []string{key},
		l.limit.Rate, l.limit.Burst, l.now().UnixMilli(), cost,
	).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("rate limit check: %w", err)
	}
	if len(res) != 3 {
		return Decision{}, fmt.Errorf("rate limit check: want 3 values, got %d", len(res))
	}

	return Decision{
		Allowed:    res[0] == 1,
		Remaining:  res[1],
		RetryAfter: time.Duration(res[2]) * time.Millisecond,
	}, nil
}

// Limit reports the configured bucket, for the response headers that tell an
// SDK how much room it has left.
func (l *Limiter) Limit() Limit { return l.limit }
