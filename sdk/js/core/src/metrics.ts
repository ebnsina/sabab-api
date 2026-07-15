/**
 * The metric capture surface.
 *
 * A metric call ships ONE raw observation. The server keeps rollups (per-minute
 * and per-hour aggregates), so the SDK does not pre-sum or pre-bucket — it sends
 * the point and lets the rollups do the maths. That keeps the client cheap and
 * the aggregation correct even across many processes reporting the same name.
 *
 * Like everything else in the SDK, a failure to record a metric is swallowed: a
 * counter that fails to increment must never take down the request it was
 * measuring.
 */

/** How a metric value is aggregated, matching the server's metric types. */
export type MetricType = "counter" | "gauge" | "distribution" | "set";

/** One captured observation, before the client stamps environment/release on. */
export interface MetricRecord {
	timestamp: string;
	name: string;
	type: MetricType;
	unit: string;
	value: number;
	tags?: Record<string, string>;
}

/** How a captured metric leaves the surface. */
export type MetricSink = (record: MetricRecord) => void;

/** Per-call options shared by every metric method. */
export interface MetricOptions {
	/** A unit for the axis — "millisecond", "byte", "request". Free-form. */
	unit?: string;
	/** Tags split the series on the dashboard: one line per distinct value. Keep
	 *  the VALUES low-cardinality (a route name, not a user id) — high-cardinality
	 *  tags explode the rollup's key space. */
	tags?: Record<string, string>;
}

/**
 * Metrics is what the app calls: counters, gauges, distributions, sets.
 */
export class Metrics {
	constructor(private readonly sink: MetricSink) {}

	/** Add to a counter — requests served, jobs run, errors handled. Defaults to
	 *  +1, the common case. Counters answer "how many, how fast". */
	increment(name: string, value = 1, opts?: MetricOptions): void {
		this.emit("counter", name, value, opts);
	}

	/** Record a point-in-time value that goes up and down — queue depth, memory,
	 *  connections. The rollup keeps min/max/avg of what was reported. */
	gauge(name: string, value: number, opts?: MetricOptions): void {
		this.emit("gauge", name, value, opts);
	}

	/** Record a value whose SHAPE matters — the rollup keeps percentiles, so you
	 *  can chart p50/p95/p99, not just the mean. Use for latencies and sizes. */
	distribution(name: string, value: number, opts?: MetricOptions): void {
		this.emit("distribution", name, value, opts);
	}

	/** A distribution in milliseconds — the unit is filled in for you. */
	timing(name: string, milliseconds: number, opts?: MetricOptions): void {
		this.emit("distribution", name, milliseconds, {
			...opts,
			unit: opts?.unit ?? "millisecond",
		});
	}

	/** Count DISTINCT values — unique users, unique IPs. The rollup keeps an
	 *  approximate distinct count, so members are cheap to report repeatedly. */
	set(name: string, value: number, opts?: MetricOptions): void {
		this.emit("set", name, value, opts);
	}

	/**
	 * Start a timer and return a stop function that records the elapsed time as a
	 * timing distribution. Idiomatic for "how long did this block take":
	 *
	 *   const done = sabab.metrics.startTimer("db.query", { tags: { table } });
	 *   await query();
	 *   done();
	 */
	startTimer(name: string, opts?: MetricOptions): () => void {
		const start = now();
		let stopped = false;
		return () => {
			if (stopped) return; // a double-call must not record a bogus second sample
			stopped = true;
			this.timing(name, Math.max(0, now() - start), opts);
		};
	}

	private emit(
		type: MetricType,
		name: string,
		value: number,
		opts?: MetricOptions,
	): void {
		try {
			// A non-finite value would poison the rollup's sum/quantiles for the
			// whole minute — drop it rather than ship NaN/Infinity.
			if (!Number.isFinite(value)) return;
			this.sink({
				timestamp: new Date().toISOString(),
				name,
				type,
				unit: opts?.unit ?? "",
				value,
				tags: stringifyTags(opts?.tags),
			});
		} catch {
			// Recording a metric is never worth an exception in the app.
		}
	}
}

/** A monotonic-ish millisecond clock, preferring performance.now when present. */
function now(): number {
	const p = (globalThis as { performance?: { now?: () => number } }).performance;
	return typeof p?.now === "function" ? p.now() : Date.now();
}

/** Coerce tag values to strings, since tags are a string map on the wire. */
function stringifyTags(
	tags: Record<string, string> | undefined,
): Record<string, string> | undefined {
	if (!tags) return undefined;
	const out: Record<string, string> = {};
	for (const [key, value] of Object.entries(tags)) {
		out[key] = typeof value === "string" ? value : String(value);
	}
	return out;
}
