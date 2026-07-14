-- Event plane: immutable, high-volume, only ever read in aggregate.
--
-- Every statement here must be idempotent. ClickHouse has no transactional DDL,
-- so a file that fails halfway has to converge when it is re-run.
--
-- This schema was checked against the official ClickHouse best-practice rules;
-- the load-bearing decisions are annotated with the rule they follow, and the
-- one place we knowingly diverge says why.

CREATE TABLE IF NOT EXISTS errors
(
    -- schema-pk-cardinality-order: leading key columns are the low-cardinality
    -- ones, so the sparse index can skip whole granules.
    project_id      UInt64                CODEC(Delta, ZSTD(1)),
    group_hash      UInt64                CODEC(Delta, ZSTD(1)),  -- fingerprint; joins to issues.group_hash
    event_id        UUID,                                         -- random: incompressible, so no codec
    timestamp       DateTime64(3, 'UTC')  CODEC(Delta, ZSTD(1)),  -- when it happened, per the client
    received_at     DateTime64(3, 'UTC')  CODEC(Delta, ZSTD(1)),  -- when we ingested it

    -- schema-types-enum: a closed set fixed at schema time, so Enum8 buys
    -- insert-time validation (no more 'warn' vs 'warning' drift) and natural
    -- ordering, which is what makes `WHERE level >= 'error'` a range scan.
    level           Enum8('debug' = 1, 'info' = 2, 'warning' = 3, 'error' = 4, 'fatal' = 5),

    -- schema-types-lowcardinality: all comfortably under 10k distinct values.
    environment     LowCardinality(String),
    release         LowCardinality(String),
    platform        LowCardinality(String),
    exception_type  LowCardinality(String),                       -- "TypeError", "IOError"

    -- ...and these are deliberately NOT LowCardinality: an error message and a
    -- culprit are effectively unbounded, and dictionary-encoding a
    -- high-cardinality column is worse than not encoding it at all.
    exception_value String                CODEC(ZSTD(1)),
    culprit         String                CODEC(ZSTD(1)),         -- "renderCart(app/cart)"

    -- The joins that make one product out of four datasets.
    trace_id        UUID,
    span_id         UInt64,

    -- schema-types-avoid-nullable: no Nullable anywhere in this table. Absent
    -- means empty string / zero, which costs nothing extra to store.
    user_id         String                CODEC(ZSTD(1)),
    user_email      String                CODEC(ZSTD(1)),
    user_ip         IPv6                  CODEC(ZSTD(1)),         -- IPv4 mapped in; may be truncated by the scrubber
    geo_country     LowCardinality(String),

    browser         LowCardinality(String),
    os              LowCardinality(String),
    sdk_name        LowCardinality(String),
    sdk_version     LowCardinality(String),

    tags            Map(LowCardinality(String), String)  CODEC(ZSTD(1)),

    -- Rendered on the detail page, never filtered on. Trade CPU for disk.
    stacktrace      String                CODEC(ZSTD(3)),         -- symbolicated frames
    breadcrumbs     String                CODEC(ZSTD(3)),
    contexts        String                CODEC(ZSTD(3)),

    -- query-index-skipping-indices: these four are real filters that the
    -- ORDER BY cannot serve. Each is high-cardinality overall but clustered
    -- within granules (one trace's errors land together in time), which is
    -- exactly the case skip indices are for.
    INDEX idx_exception_value exception_value TYPE tokenbf_v1(8192, 3, 0) GRANULARITY 4,
    INDEX idx_tag_values      mapValues(tags)  TYPE bloom_filter(0.01)    GRANULARITY 4,
    INDEX idx_trace_id        trace_id         TYPE bloom_filter(0.01)    GRANULARITY 4,
    INDEX idx_user_id         user_id          TYPE bloom_filter(0.01)    GRANULARITY 4
)
ENGINE = MergeTree
-- KNOWING DIVERGENCE from schema-partition-low-cardinality, which prefers
-- monthly partitions. Daily is correct here *because* of the 90-day TTL:
--   * Bounded at ~90 live partitions — inside the rule's 100–1,000 budget, and
--     it cannot grow, which is the failure the rule actually guards against.
--   * schema-partition-lifecycle: aligning the partition with the TTL turns
--     expiry into an instant DROP PARTITION. Monthly partitions would force
--     row-level TTL merges, rewriting parts to evict 90-day-old rows.
-- If the retention window ever grows past ~2 years, revisit this.
PARTITION BY toDate(timestamp)
-- schema-pk-prioritize-filters: every query in the product is project-scoped
-- and time-ranged, and the issue-detail page reads exactly one group_hash.
-- This is the most consequential line in the schema; get it wrong and every
-- page becomes a full scan.
ORDER BY (project_id, toStartOfHour(timestamp), group_hash, timestamp)
TTL toDateTime(timestamp) + INTERVAL 90 DAY DELETE;

-- query-mv-incremental: the issue stream needs counts and sparklines per issue.
-- Aggregating at insert time means the stream reads thousands of rows instead
-- of scanning billions of raw events on every dashboard load.
CREATE TABLE IF NOT EXISTS issue_stats_1h
(
    project_id  UInt64          CODEC(Delta, ZSTD(1)),
    group_hash  UInt64          CODEC(Delta, ZSTD(1)),
    hour        DateTime('UTC') CODEC(Delta, ZSTD(1)),
    times_seen  AggregateFunction(count),
    users_state AggregateFunction(uniq, String),
    first_seen  SimpleAggregateFunction(min, DateTime64(3, 'UTC')),
    last_seen   SimpleAggregateFunction(max, DateTime64(3, 'UTC'))
)
ENGINE = AggregatingMergeTree
-- Monthly here, not daily: this table is orders of magnitude smaller than
-- `errors`, so daily partitions would only produce many tiny parts.
PARTITION BY toYYYYMM(hour)
ORDER BY (project_id, group_hash, hour)
TTL hour + INTERVAL 90 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS issue_stats_1h_mv TO issue_stats_1h AS
SELECT
    project_id,
    group_hash,
    toStartOfHour(timestamp) AS hour,
    countState()             AS times_seen,
    uniqState(user_id)       AS users_state,
    min(timestamp)           AS first_seen,
    max(timestamp)           AS last_seen
FROM errors
GROUP BY project_id, group_hash, hour;
