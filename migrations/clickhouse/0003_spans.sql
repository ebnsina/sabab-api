-- Spans: where a request spent its time, across services.
--
-- Same codec policy as errors and logs, annotated against the ClickHouse
-- best-practice rules. Two ORDER BYs are needed and both matter: the table is
-- sorted for aggregation (endpoint p95s, slow queries), and a projection is
-- sorted by trace so the waterfall view — "every span in this one trace" — is a
-- lookup rather than a scan.

CREATE TABLE IF NOT EXISTS spans
(
	project_id     UInt64                CODEC(Delta, ZSTD(1)),
	trace_id       UUID,                                          -- random: no codec
	span_id        UInt64,                                        -- random: no codec
	parent_span_id UInt64,
	-- The root span of one service's slice of the trace. is_segment marks it, so
	-- "which services did this request touch" is answerable without walking the
	-- parent chain.
	segment_id     UInt64,
	is_segment     Bool,

	-- name MUST be parameterized: "GET /users/:id", never "GET /users/8412". It
	-- is LowCardinality, so raw ids in here explode the dictionary and make every
	-- aggregation over it meaningless. Enforcing this is the single most important
	-- job of the SDK's auto-instrumentation.
	name           LowCardinality(String),
	-- The category: http.server | db.query | cache.get | ui.render. A closed-ish
	-- small set, so LowCardinality dictionary-encodes it cheaply.
	op             LowCardinality(String),
	service        LowCardinality(String),

	-- Microsecond precision: spans nest and overlap, and millisecond ties would
	-- scramble the waterfall order.
	timestamp      DateTime64(6, 'UTC')  CODEC(Delta, ZSTD(1)),
	received_at    DateTime64(3, 'UTC')  CODEC(Delta, ZSTD(1)),
	-- The number every APM aggregation is built on. T64 packs the high zero bits
	-- of typical sub-second durations before ZSTD, which Delta cannot (durations
	-- are not monotonic).
	duration_ns    UInt64                CODEC(T64, ZSTD(1)),

	-- schema-types-enum: a closed outcome set, validated at insert, and orderable.
	status         Enum8('ok' = 1, 'error' = 2, 'cancelled' = 3),

	environment    LowCardinality(String),
	release        LowCardinality(String),

	http_method    LowCardinality(String),
	http_status    UInt16                CODEC(T64, ZSTD(1)),
	http_route     String                CODEC(ZSTD(1)),

	db_system      LowCardinality(String),
	db_statement   String                CODEC(ZSTD(1)),

	tags           Map(LowCardinality(String), String)  CODEC(ZSTD(1)),
	-- The RUM Web Vitals ride here (lcp, inp, cls, fcp, ttfb) on the pageload
	-- span, so browser performance needs no separate ingest path.
	measurements   Map(LowCardinality(String), Float64) CODEC(ZSTD(1)),

	user_id        String                CODEC(ZSTD(1)),
	geo_country    LowCardinality(String),

	INDEX idx_trace_id  trace_id       TYPE bloom_filter(0.01) GRANULARITY 4,
	INDEX idx_http_route http_route    TYPE tokenbf_v1(8192, 3, 0) GRANULARITY 4
)
ENGINE = MergeTree
PARTITION BY toDate(timestamp)
-- schema-pk-prioritize-filters: APM queries are "this op, this endpoint, over
-- this time range" (endpoint p95s, slow db.query grouping). op and name lead so
-- those prune hard; the hour bucket bounds the time scan.
ORDER BY (project_id, toStartOfHour(timestamp), op, name, timestamp)
TTL toDateTime(timestamp) + INTERVAL 30 DAY DELETE;

-- The waterfall reads every span of one trace, which the aggregation ORDER BY
-- above cannot serve. A projection sorted by trace_id makes it a key lookup —
-- without it, opening one trace scans the partition.
ALTER TABLE spans ADD PROJECTION IF NOT EXISTS by_trace
(
	SELECT * ORDER BY (project_id, trace_id, timestamp)
);
