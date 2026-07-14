// Package gateway is the ingest edge.
//
// It does exactly four things — authenticate the key, enforce the limits, rate
// limit the project, enqueue the items — and then answers. Everything else
// (parsing payloads, symbolicating, grouping, writing) happens behind the
// queue, because the gateway's p99 is a number our customers feel in their own
// apps. An observability tool that slows down production is worse than none.
package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/ebnsina/sabab-api/internal/auth"
	"github.com/ebnsina/sabab-api/internal/envelope"
	"github.com/ebnsina/sabab-api/internal/health"
	"github.com/ebnsina/sabab-api/internal/httpx"
	"github.com/ebnsina/sabab-api/internal/ingest"
	"github.com/ebnsina/sabab-api/internal/queue"
	"github.com/ebnsina/sabab-api/internal/ratelimit"
)

// KeyHeader carries the public ingest key.
const KeyHeader = "X-Sabab-Key"

// Limiter is the slice of rate limiting the gateway needs. An interface rather
// than the concrete type so the gateway's behaviour under a *failing* limiter —
// the case that decides whether our outage becomes the customer's — is testable
// without a live Redis.
type Limiter interface {
	AllowN(ctx context.Context, projectID uint64, cost int) (ratelimit.Decision, error)
	Limit() ratelimit.Limit
}

// Gateway handles ingest requests.
type Gateway struct {
	keys    *auth.IngestKeys
	limiter Limiter
	queue   queue.Producer
	limits  envelope.Limits
	health  *health.Checker
	log     *slog.Logger
}

// New builds a Gateway.
func New(
	keys *auth.IngestKeys,
	limiter Limiter,
	producer queue.Producer,
	limits envelope.Limits,
	checker *health.Checker,
	log *slog.Logger,
) *Gateway {
	return &Gateway{
		keys:    keys,
		limiter: limiter,
		queue:   producer,
		limits:  limits,
		health:  checker,
		log:     log,
	}
}

// Handler returns the fully wired HTTP handler.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	g.health.Routes(mux)

	mux.HandleFunc("POST /ingest/v1/{project_id}/envelope", g.handleEnvelope)
	// The same path without a method catches every other verb and answers 405.
	// Without this, the catch-all below would swallow a GET and call it a 404,
	// telling an SDK author with the wrong method that the endpoint does not
	// exist — which sends them debugging the wrong thing entirely.
	mux.HandleFunc("/ingest/v1/{project_id}/envelope", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", http.MethodPost)
		httpx.WriteError(w, r, g.log, httpx.ErrMethodNotAllowed)
	})

	// Anything unrouted answers in our JSON error shape rather than Go's
	// default plain-text 404.
	mux.HandleFunc("/", httpx.NotFound(g.log))

	return httpx.Chain(mux,
		httpx.Recover(g.log),
		httpx.LogRequests(g.log),
	)
}

// acceptedResponse tells the SDK exactly what we took and what we did not.
//
// Reporting `skipped` matters: an SDK that sent 10 items and hears "accepted"
// would otherwise believe all 10 are queryable. Being honest about what we
// dropped is the difference between a tool people trust and one they stop
// believing.
type acceptedResponse struct {
	Accepted int `json:"accepted"`
	Skipped  int `json:"skipped,omitempty"`
}

func (g *Gateway) handleEnvelope(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	projectID, err := strconv.ParseUint(r.PathValue("project_id"), 10, 64)
	if err != nil || projectID == 0 {
		// Deliberately 401, not 400: a bad project id and a bad key are the
		// same answer, so this endpoint cannot be used to enumerate projects.
		httpx.WriteError(w, r, g.log, httpx.ErrUnauthorized)
		return
	}

	project, err := g.keys.Authenticate(ctx, r.Header.Get(KeyHeader), projectID)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidKey) {
			httpx.WriteError(w, r, g.log, httpx.ErrUnauthorized)
			return
		}
		// The control plane is down. This is ours, so answer 5xx and let the
		// SDK retry — a 401 here would make it discard the events for good.
		httpx.WriteError(w, r, g.log,
			httpx.Wrap(http.StatusServiceUnavailable, "unavailable",
				"Unable to verify credentials right now. Retry shortly.", err))
		return
	}

	env, err := g.parse(r)
	if err != nil {
		httpx.WriteError(w, r, g.log, err)
		return
	}
	if len(env.Items) == 0 {
		httpx.WriteJSON(w, http.StatusOK, acceptedResponse{Accepted: 0, Skipped: env.Skipped})
		return
	}

	// Charged per item, not per request: one request carrying 500 errors costs
	// us 500 events of work, and charging it as one would make the limit
	// bypassable by batching.
	decision, err := g.limiter.AllowN(ctx, project.ID, len(env.Items))
	if err != nil {
		// Redis is down. Fail open — dropping a customer's events because our
		// rate limiter is unavailable inflicts our outage on them. The queue's
		// own bound is the backstop against real overload.
		g.log.Error("rate limiter unavailable, allowing request",
			slog.Uint64("project_id", project.ID), slog.Any("error", err))
	} else {
		g.setRateLimitHeaders(w, decision)
		if !decision.Allowed {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(decision.RetryAfter)))
			httpx.WriteError(w, r, g.log,
				httpx.NewError(http.StatusTooManyRequests, "rate_limited",
					"Too many events. Back off and retry after the interval in Retry-After."))
			return
		}
	}

	received := time.Now().UTC()
	clientIP := clientIP(r)

	bodies := make([][]byte, 0, len(env.Items))
	for _, item := range env.Items {
		body, err := ingest.Encode(ingest.Job{
			ProjectID:  project.ID,
			Type:       item.Type,
			Payload:    item.Payload,
			ReceivedAt: received,
			SentAt:     env.Header.SentAt,
			SDK:        env.Header.SDK,
			ClientIP:   clientIP,
		})
		if err != nil {
			httpx.WriteError(w, r, g.log, httpx.Wrap(http.StatusInternalServerError,
				"internal_error", "Something went wrong on our end.", err))
			return
		}
		bodies = append(bodies, body)
	}

	if err := g.queue.Publish(ctx, bodies...); err != nil {
		// We could not enqueue, so we must not claim we accepted. A 503 tells
		// the SDK to retry; a 200 here would silently lose the events.
		httpx.WriteError(w, r, g.log,
			httpx.Wrap(http.StatusServiceUnavailable, "unavailable",
				"Unable to accept events right now. Retry shortly.", err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, acceptedResponse{
		Accepted: len(env.Items),
		Skipped:  env.Skipped,
	})
}

// parse reads the body under the configured limits and turns any failure into
// the right status code — 413 for oversized, 400 for malformed.
func (g *Gateway) parse(r *http.Request) (*envelope.Envelope, error) {
	// Cap the compressed body at the socket, before we read a single byte of it.
	body := http.MaxBytesReader(nil, r.Body, g.limits.MaxCompressedBytes)

	decompressed, err := envelope.Decompress(body, r.Header.Get("Content-Encoding"))
	if err != nil {
		return nil, toHTTPError(err)
	}
	defer decompressed.Close()

	env, err := envelope.Parse(decompressed, g.limits)
	if err != nil {
		return nil, toHTTPError(err)
	}
	return env, nil
}

// toHTTPError maps a parse failure to a status code.
func toHTTPError(err error) error {
	var maxBytes *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytes):
		return httpx.NewError(http.StatusRequestEntityTooLarge, "payload_too_large",
			"The request body exceeds the maximum size.")
	case errors.Is(err, envelope.ErrTooLarge):
		return httpx.NewError(http.StatusRequestEntityTooLarge, "payload_too_large", err.Error())
	case errors.Is(err, envelope.ErrMalformed):
		return httpx.NewError(http.StatusBadRequest, "malformed_envelope", err.Error())
	default:
		// An unexpected read failure (a client that hung up mid-body) is not
		// something we can describe, and not something to page anyone about.
		return httpx.Wrap(http.StatusBadRequest, "malformed_envelope",
			"The request body could not be read.", err)
	}
}

func (g *Gateway) setRateLimitHeaders(w http.ResponseWriter, d ratelimit.Decision) {
	limit := g.limiter.Limit()
	w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(limit.Burst, 10))
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(max(d.Remaining, 0), 10))
}

// retryAfterSeconds rounds up: Retry-After is in whole seconds, and rounding a
// 1.2s wait down to 1s would invite the SDK back before a token exists.
func retryAfterSeconds(d time.Duration) int {
	seconds := int(d.Seconds())
	if d > time.Duration(seconds)*time.Second {
		seconds++
	}
	return max(seconds, 1)
}

// clientIP is the socket address. It resolves the SDK's "{{auto}}" placeholder,
// which a browser has to send because it cannot know its own public IP.
//
// X-Forwarded-For is deliberately NOT trusted here: it is trivially spoofable
// by the caller, and honouring it would let anyone forge the geo and IP of
// every event they send. A deployment behind a proxy should have the proxy set
// the socket address, or this must become an explicit, configured trust list.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
