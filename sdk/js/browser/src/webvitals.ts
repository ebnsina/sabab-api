import type { Client } from "@sabab/core";

/**
 * Web Vitals collection — the real experience a user had, not a synthetic run.
 *
 * Each vital is gathered with a PerformanceObserver and reported as a metric, so
 * it flows through the same rollups as everything else: the dashboard reads p75
 * per page without any new ingest path.
 *
 * The vitals that accumulate — LCP, CLS, INP — are only final once the page is
 * hidden, so everything is reported on the first `visibilitychange` to hidden
 * (or `pagehide`), exactly once. Like the rest of the SDK, every failure is
 * swallowed: a browser that lacks an entry type simply skips that one vital.
 */
export function installWebVitals(c: Client): void {
	if (typeof PerformanceObserver === "undefined" || typeof performance === "undefined") return;

	const tags = { page: location.pathname };

	let lcp = 0;
	let fcp = 0;
	let cls = 0;
	let inp = 0;
	let reported = false;

	// LCP: the largest element painted; keep the latest candidate.
	observe("largest-contentful-paint", (entries) => {
		const last = entries[entries.length - 1];
		if (last) lcp = last.startTime;
	});

	// FCP: first content on screen, from the paint timeline.
	observe("paint", (entries) => {
		for (const e of entries) if (e.name === "first-contentful-paint") fcp = e.startTime;
	});

	// CLS: sum of unexpected layout shifts (those not following user input).
	observe("layout-shift", (entries) => {
		for (const e of entries as LayoutShift[]) if (!e.hadRecentInput) cls += e.value;
	});

	// INP (approximated as the slowest interaction): how long the UI took to
	// respond to input. Only durations past a threshold are worth recording.
	observe(
		"event",
		(entries) => {
			for (const e of entries) if (e.duration > inp) inp = e.duration;
		},
		{ durationThreshold: 40 },
	);

	// TTFB: available immediately from navigation timing.
	const nav = performance.getEntriesByType("navigation")[0] as PerformanceNavigationTiming | undefined;
	const ttfb = nav ? nav.responseStart : 0;

	const report = () => {
		if (reported) return;
		reported = true;
		const ms = { unit: "millisecond", tags };
		if (lcp > 0) c.metrics.distribution("web.lcp", Math.round(lcp), ms);
		if (fcp > 0) c.metrics.distribution("web.fcp", Math.round(fcp), ms);
		if (ttfb > 0) c.metrics.distribution("web.ttfb", Math.round(ttfb), ms);
		if (inp > 0) c.metrics.distribution("web.inp", Math.round(inp), ms);
		// CLS is a unitless score; report it even at 0, since "no shift" is the
		// good result and a chart of zeros is meaningful.
		c.metrics.distribution("web.cls", Math.round(cls * 1000) / 1000, { tags });
		void c.flush();
	};

	// These metrics stop changing when the page is backgrounded — that is when to
	// send them. Both events are used because no single one fires reliably across
	// every browser and platform.
	addEventListener("visibilitychange", () => {
		if (document.visibilityState === "hidden") report();
	});
	addEventListener("pagehide", report);
}

/** Wrap a PerformanceObserver so an unsupported entry type is a no-op, never a
 *  thrown error. `buffered` replays entries that occurred before we observed. */
function observe(
	type: string,
	cb: (entries: PerformanceEntry[]) => void,
	extra?: Record<string, unknown>,
): void {
	try {
		const po = new PerformanceObserver((list) => cb(list.getEntries()));
		po.observe({ type, buffered: true, ...extra });
	} catch {
		/* entry type not supported here — skip this vital */
	}
}

/** The layout-shift entry shape, which TypeScript's DOM lib does not include. */
interface LayoutShift extends PerformanceEntry {
	value: number;
	hadRecentInput: boolean;
}
