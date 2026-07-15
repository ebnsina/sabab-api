-- Metrics: counters, gauges, distributions and sets the app emits.
--
-- The raw table is short-lived — the rollups are the product. Every query the
-- dashboard runs hits a pre-aggregated table, never the raw stream, so a chart
-- reads thousands of rows instead of billions (query-mv-incremental).

CREATE TABLE IF NOT EXISTS metrics_raw
(
	project_id UInt64                CODEC(Delta, ZSTD(1)),
	timestamp  DateTime64(3, 'UTC')  CODEC(Delta, ZSTD(1)),
	name       LowCardinality(String),
	-- schema-types-enum: a fixed, orderable set validated at insert.
	type       Enum8('counter' = 1, 'gauge' = 2, 'distribution' = 3, 'set' = 4),
	unit       LowCardinality(String),
	tags       Map(LowCardinality(String), String)  CODEC(ZSTD(1)),
	-- Gorilla is built for float metric time series — it XORs consecutive values,
	-- which are usually close, before ZSTD.
	value      Float64               CODEC(Gorilla, ZSTD(1))
)
ENGINE = MergeTree
PARTITION BY toDate(timestamp)
ORDER BY (project_id, name, timestamp)
-- Raw is a 7-day buffer; the rollups carry the history.
TTL toDateTime(timestamp) + INTERVAL 7 DAY DELETE;

-- Per-minute rollup: the resolution a live chart needs.
CREATE TABLE IF NOT EXISTS metrics_1m
(
	project_id UInt64          CODEC(Delta, ZSTD(1)),
	name       LowCardinality(String),
	type       Enum8('counter' = 1, 'gauge' = 2, 'distribution' = 3, 'set' = 4),
	unit       LowCardinality(String),
	tags       Map(LowCardinality(String), String),
	minute     DateTime('UTC') CODEC(Delta, ZSTD(1)),
	sum        AggregateFunction(sum, Float64),
	count      AggregateFunction(count),
	min        SimpleAggregateFunction(min, Float64),
	max        SimpleAggregateFunction(max, Float64),
	quantiles  AggregateFunction(quantiles(0.5, 0.75, 0.95, 0.99), Float64),
	unique     AggregateFunction(uniq, Float64)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(minute)
ORDER BY (project_id, name, type, unit, tags, minute)
TTL minute + INTERVAL 90 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS metrics_1m_mv TO metrics_1m AS
SELECT
	project_id,
	name,
	type,
	unit,
	tags,
	toStartOfMinute(timestamp)                    AS minute,
	sumState(value)                               AS sum,
	countState()                                  AS count,
	min(value)                                    AS min,
	max(value)                                    AS max,
	quantilesState(0.5, 0.75, 0.95, 0.99)(value)  AS quantiles,
	uniqState(value)                              AS unique
FROM metrics_raw
GROUP BY project_id, name, type, unit, tags, minute;

-- Per-hour rollup, kept far longer, for the zoomed-out view. It rolls up from
-- the 1m table rather than raw, so it costs almost nothing.
CREATE TABLE IF NOT EXISTS metrics_1h
(
	project_id UInt64          CODEC(Delta, ZSTD(1)),
	name       LowCardinality(String),
	type       Enum8('counter' = 1, 'gauge' = 2, 'distribution' = 3, 'set' = 4),
	unit       LowCardinality(String),
	tags       Map(LowCardinality(String), String),
	hour       DateTime('UTC') CODEC(Delta, ZSTD(1)),
	sum        AggregateFunction(sum, Float64),
	count      AggregateFunction(count),
	min        SimpleAggregateFunction(min, Float64),
	max        SimpleAggregateFunction(max, Float64),
	quantiles  AggregateFunction(quantiles(0.5, 0.75, 0.95, 0.99), Float64),
	unique     AggregateFunction(uniq, Float64)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (project_id, name, type, unit, tags, hour)
TTL hour + INTERVAL 400 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS metrics_1h_mv TO metrics_1h AS
SELECT
	project_id,
	name,
	type,
	unit,
	tags,
	toStartOfHour(minute) AS hour,
	sumMergeState(sum)              AS sum,
	countMergeState(count)          AS count,
	min(min)                        AS min,
	max(max)                        AS max,
	quantilesMergeState(0.5, 0.75, 0.95, 0.99)(quantiles) AS quantiles,
	uniqMergeState(unique)          AS unique
FROM metrics_1m
GROUP BY project_id, name, type, unit, tags, hour;
