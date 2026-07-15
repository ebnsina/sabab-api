-- Logs: what the app printed, structured enough to group and search.
--
-- Same codec policy as errors (0001): Delta+ZSTD on sorted/monotonic columns,
-- ZSTD on high-cardinality text, none on random UUIDs, Enum8 for the closed
-- severity set. Annotated with the official ClickHouse best-practice rules.

CREATE TABLE IF NOT EXISTS logs
(
    -- schema-pk-cardinality-order: low-cardinality leading columns first.
    project_id      UInt64                CODEC(Delta, ZSTD(1)),

    -- schema-types-enum: a closed severity set fixed at schema time. Enum8 gives
    -- insert-time validation AND a numeric ordering, which is what turns
    -- "severity >= warn" into a cheap range scan instead of a set membership
    -- test. The numbers follow the OpenTelemetry severity scale so an OTLP
    -- adapter (M8) maps straight in.
    severity        Enum8('trace' = 1, 'debug' = 5, 'info' = 9, 'warn' = 13, 'error' = 17, 'fatal' = 21),

    -- DateTime64(9): nanosecond precision. Logs interleave far faster than
    -- errors, and millisecond ties would scramble the order of a burst — the one
    -- thing a log view must get right.
    timestamp       DateTime64(9, 'UTC')  CODEC(Delta, ZSTD(1)),
    received_at     DateTime64(3, 'UTC')  CODEC(Delta, ZSTD(1)),

    service         LowCardinality(String),
    environment     LowCardinality(String),
    release         LowCardinality(String),

    -- The interpolated line the app actually printed.
    body            String                CODEC(ZSTD(1)),
    -- The pre-interpolation form: "user {id} not found". Capturing it separately
    -- is what lets us group logs the way we group errors — "this same line fired
    -- 4M times" — which a plain text store cannot answer. LowCardinality because
    -- there are few distinct templates even across millions of lines.
    template        LowCardinality(String),

    -- The joins that make one product out of four datasets. A log carries the
    -- trace it was emitted inside, so "show me the logs around this error" is a
    -- lookup, not a guess.
    trace_id        UUID,
    span_id         UInt64,

    attributes      Map(LowCardinality(String), String)  CODEC(ZSTD(1)),

    -- schema-json-when-to-use / query-index-skipping-indices: full-text search
    -- over the body without a scan. Larger tokenbf than errors because log
    -- bodies are the primary thing people grep.
    INDEX idx_body       body           TYPE tokenbf_v1(32768, 3, 0) GRANULARITY 2,
    INDEX idx_trace_id   trace_id       TYPE bloom_filter(0.01)      GRANULARITY 4,
    INDEX idx_attr_values mapValues(attributes) TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = MergeTree
PARTITION BY toDate(timestamp)
-- schema-pk-prioritize-filters: logs are queried "this service, at this
-- severity, over this time range". service and severity lead so the common
-- "errors from the checkout service in the last hour" prunes hard; the coarse
-- hour bucket then bounds the time scan, and the full timestamp orders within.
ORDER BY (project_id, service, severity, toStartOfHour(timestamp), timestamp)
-- 30-day retention, aligned to daily partitions so expiry is a DROP PARTITION.
TTL toDateTime(timestamp) + INTERVAL 30 DAY DELETE;
