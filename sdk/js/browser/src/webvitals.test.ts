import { describe, expect, it } from "vitest";
import { installWebVitals } from "./webvitals.js";

type ObserverCb = (list: { getEntries: () => unknown[] }) => void;

/** A minimal browser environment: fake PerformanceObserver, performance,
 *  document, location and event listeners, plus a spy client. */
function setup() {
	const observers: { type: string; cb: ObserverCb }[] = [];
	const listeners: Record<string, (() => void)[]> = {};
	const sent: { name: string; value: number; opts: { unit?: string; tags?: Record<string, string> } }[] = [];

	const g = globalThis as unknown as Record<string, unknown>;
	g.PerformanceObserver = class {
		cb: ObserverCb;
		constructor(cb: ObserverCb) {
			this.cb = cb;
		}
		observe(opts: { type: string }) {
			observers.push({ type: opts.type, cb: this.cb });
		}
		disconnect() {}
	};
	g.performance = {
		getEntriesByType: (t: string) => (t === "navigation" ? [{ responseStart: 120 }] : []),
	};
	g.location = { pathname: "/checkout" };
	// visibilitychange listeners register on document; pagehide on window. Both
	// funnel into the same map so a single fire() drives either.
	const register = (ev: string, cb: () => void) => {
		(listeners[ev] ||= []).push(cb);
	};
	g.document = { visibilityState: "visible", addEventListener: register };
	g.addEventListener = register;

	const client = {
		metrics: {
			distribution: (name: string, value: number, opts: { unit?: string; tags?: Record<string, string> }) =>
				sent.push({ name, value, opts }),
		},
		flush: () => Promise.resolve(true),
		// eslint-disable-next-line @typescript-eslint/no-explicit-any
	} as any;

	return {
		client,
		sent,
		emit: (type: string, entries: unknown[]) => {
			for (const o of observers) if (o.type === type) o.cb({ getEntries: () => entries });
		},
		fire: (ev: string) => {
			for (const cb of listeners[ev] || []) cb();
		},
		hide: () => {
			(g.document as { visibilityState: string }).visibilityState = "hidden";
		},
	};
}

describe("web vitals", () => {
	it("reports each vital, correctly reduced, once the page is hidden", () => {
		const { client, sent, emit, fire, hide } = setup();
		installWebVitals(client);

		emit("largest-contentful-paint", [{ startTime: 1800 }, { startTime: 2400 }]); // keep the last
		emit("paint", [{ name: "first-contentful-paint", startTime: 1200 }]);
		// One real shift and one that followed input — only the first counts.
		emit("layout-shift", [
			{ value: 0.05, hadRecentInput: false },
			{ value: 0.9, hadRecentInput: true },
		]);
		emit("event", [{ duration: 90 }, { duration: 220 }]); // INP is the slowest

		hide();
		fire("visibilitychange");

		const by = (n: string) => sent.find((s) => s.name === n);
		expect(by("web.lcp")?.value).toBe(2400);
		expect(by("web.fcp")?.value).toBe(1200);
		expect(by("web.ttfb")?.value).toBe(120);
		expect(by("web.inp")?.value).toBe(220);
		expect(by("web.cls")?.value).toBe(0.05);
		// Every vital is tagged with the page it was measured on.
		expect(by("web.lcp")?.opts.tags?.page).toBe("/checkout");
		expect(by("web.lcp")?.opts.unit).toBe("millisecond");
	});

	it("reports exactly once, even if hide and pagehide both fire", () => {
		const { client, sent, emit, fire, hide } = setup();
		installWebVitals(client);
		emit("largest-contentful-paint", [{ startTime: 1000 }]);
		hide();
		fire("visibilitychange");
		fire("pagehide");
		expect(sent.filter((s) => s.name === "web.lcp")).toHaveLength(1);
	});

	it("does not report a vital that was never measured", () => {
		const { client, sent, fire, hide } = setup();
		installWebVitals(client); // no LCP/FCP/INP entries emitted
		hide();
		fire("visibilitychange");
		// TTFB (from navigation) and CLS (reported at 0) are present; the rest are not.
		expect(sent.find((s) => s.name === "web.lcp")).toBeUndefined();
		expect(sent.find((s) => s.name === "web.ttfb")?.value).toBe(120);
		expect(sent.find((s) => s.name === "web.cls")?.value).toBe(0);
	});
});
