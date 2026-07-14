# The Sabab Envelope

The wire format between an SDK and the ingest gateway. It is ours, deliberately:
there is no compatibility layer with any other vendor's protocol, which means an
app can only send us data if we ship an SDK for it. That is a real cost, and we
pay it to keep the event model clean. The OTLP adapter (M8) is the escape hatch
for languages we have not written an SDK for.

## Why not one JSON object per request

An app that just crashed wants to flush an error, the three logs around it and
the finished trace it happened inside — in one round trip, from a page that may
be unloading. So the envelope is newline-delimited: a header line, then
alternating item-header / item-payload lines. A client can serialize items as it
buffers them and never has to hold the whole request in memory as one object.

## Request

```http
POST /ingest/v1/{project_id}/envelope
X-Sabab-Key: pk_live_7f3a…
Content-Type: application/x-sabab-envelope
Content-Encoding: gzip
```

Body:

```
{"sent_at":"2026-07-14T10:00:00Z","sdk":{"name":"sabab.javascript.browser","version":"1.0.0"}}
{"type":"error","length":842}
{ … error payload … }
{"type":"log","length":214}
{ … log payload … }
{"type":"span","length":1102}
{ … span payload … }
```

Line 1 is the **envelope header**. After it, items repeat: a header line naming
the item's `type` and byte `length`, then exactly that many bytes of payload.

`length` is what lets the gateway skip an item it does not understand — a v1
gateway can accept an envelope from a v2 SDK that has learned a new signal,
drop the unknown items and keep the rest. Without it, one unknown type would
force us to reject the whole envelope.

### Item types

`error` · `log` · `span` · `metric` · `session` · `client_report`

`client_report` is how the SDK tells us what it dropped locally — rate-limited,
buffer full, or discarded by a `beforeSend` hook. It is not optional politeness:
without it we report the counts we happened to receive and call them the truth.
Silently under-reporting is the one failure that destroys trust in an
observability tool permanently.

## Authentication

The ingest URL is the entire configuration an SDK needs, in one string:

```
https://pk_live_7f3a@ingest.sabab.dev/4
       └──── public key ────┘          └─ project id
```

The public key **is not a secret**. It ships in browser bundles by design. It is
scoped to one project, **write-only**, rate-limited and revocable. It is never
reused for the read API — that is what `api_tokens` are for. Any design that
requires the ingest key to be secret is wrong, because it cannot be.

## Limits

The gateway enforces these before doing any work, and answers `413` when they
are exceeded:

| Limit | Value |
|---|---|
| Compressed body | 1 MiB |
| Decompressed body | 20 MiB |
| Items per envelope | 1,000 |
| Single item payload | 1 MiB |

The decompressed cap is enforced while streaming, not after: a 1 MiB body that
inflates to 10 GiB is a zip bomb, and discovering that *after* buffering it is
too late.

## Responses

The gateway does as little as possible and answers fast. Symbolication, grouping
and writes all happen behind the queue — the gateway must never be the reason a
customer's app is slow.

| Status | Meaning |
|---|---|
| `200` | Accepted and enqueued. Body reports per-item counts, including any items dropped. |
| `400` | Malformed envelope. |
| `401` | Unknown or revoked ingest key. |
| `413` | A limit above was exceeded. |
| `429` | Rate limited. `Retry-After` is set; SDKs must back off and report the drop in a later `client_report`. |
| `5xx` | Ours. The SDK should retry with backoff. |

A `200` means *we have the bytes and will process them*, not *the event is
queryable*. Anything stronger would require doing the expensive work inline,
which is precisely what the queue exists to avoid.

## Error payload

The single most valuable field is `exception[].frames`. **Structured frames,
never a pre-formatted stack string** — grouping, source maps and the stack
viewer all depend on real fields. A string here turns each of them into a
parsing problem, and it is why errors arriving over OTLP will always group worse
than errors from our own SDK.

```jsonc
{
  "event_id": "…", "timestamp": "…", "level": "error",
  "platform": "javascript",
  "release": "web@2.4.1", "environment": "production",
  "exception": [{                       // array = chained causes, innermost last
    "type": "TypeError",
    "value": "Cannot read properties of undefined (reading 'id')",
    "mechanism": {"type": "onunhandledrejection", "handled": false},
    "frames": [{
      "function": "renderCart", "module": "app/cart",
      "filename": "/static/js/main.a3f9.js",
      "lineno": 1, "colno": 48213,
      "in_app": true,
      "pre_context": [], "context_line": "", "post_context": []
    }]
  }],
  "breadcrumbs": [{"ts": "…", "category": "navigation", "message": "/ → /cart"}],
  "contexts": {"browser": {}, "os": {}, "device": {}, "runtime": {}},
  "user": {"id": "u_91", "email": "…", "ip_address": "{{auto}}"},
  "tags": {"tenant": "acme", "feature_flag.new_cart": "true"},
  "trace_id": "…", "span_id": "…",      // the join to Tracing and Logs
  "fingerprint": ["{{default}}"]        // grouping override escape hatch
}
```

`ip_address: "{{auto}}"` asks the gateway to substitute the socket address,
because a browser cannot know its own public IP. The scrubber may then truncate
or drop it, per project policy.

## Rules that hold across every item

- **`trace_id`, `environment` and `release` on every signal.** These three are
  the joins that make one product out of four datasets. They live on the shared
  `event.Meta` in Go precisely so a new signal cannot forget them.
- **`project_id` is never read from the payload.** The gateway resolves it from
  the ingest key. A client must not be able to write into another project by
  claiming to.
- **Span `name` must be parameterized** — `GET /users/:id`, never
  `GET /users/8412`. It is a `LowCardinality` column; raw IDs make the
  dictionary explode and every aggregation over it meaningless.
