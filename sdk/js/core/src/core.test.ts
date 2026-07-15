import { describe, expect, it, vi } from "vitest";
import { Client } from "./client.js";
import { parseDsn } from "./dsn.js";
import { patchConsole } from "./logger.js";
import { exceptionsFromUnknown, parseStack } from "./stack.js";
import type { ErrorEvent, SababOptions } from "./types.js";

const DSN = "https://pk_live_abc@ingest.sabab.dev/4";

/** A client wired to a fake transport, so we can inspect what would be sent. */
function testClient(options: Partial<SababOptions> = {}) {
  const sent: string[] = [];
  const send = vi.fn(async (_url: string, body: string) => {
    sent.push(body);
    return true;
  });

  const client = new Client(
    { dsn: DSN, release: "web@2.4.1", ...options },
    { name: "sabab.javascript.test", version: "1.0.0" },
    "javascript",
    send,
  );
  return { client, sent, send };
}

/** Pull the error events out of a sent envelope. */
function eventsIn(envelope: string): ErrorEvent[] {
  const lines = envelope.trim().split("\n");
  const events: ErrorEvent[] = [];
  // line 0 is the envelope header; then header/payload pairs.
  for (let i = 1; i < lines.length; i += 2) {
    const header = JSON.parse(lines[i] as string);
    if (header.type === "error") {
      events.push(JSON.parse(lines[i + 1] as string));
    }
  }
  return events;
}

function itemsIn(envelope: string): { type: string; payload: unknown }[] {
  const lines = envelope.trim().split("\n");
  const items: { type: string; payload: unknown }[] = [];
  for (let i = 1; i < lines.length; i += 2) {
    const header = JSON.parse(lines[i] as string);
    items.push({ type: header.type, payload: JSON.parse(lines[i + 1] as string) });
  }
  return items;
}

describe("dsn", () => {
  it("parses the whole configuration out of one string", () => {
    const dsn = parseDsn(DSN);
    expect(dsn.publicKey).toBe("pk_live_abc");
    expect(dsn.projectId).toBe("4");
    expect(dsn.envelopeUrl).toBe("https://ingest.sabab.dev/ingest/v1/4/envelope");
  });

  it("rejects a DSN missing its key or project", () => {
    expect(() => parseDsn("https://ingest.sabab.dev/4")).toThrow(/public key/);
    expect(() => parseDsn("https://pk_live_abc@ingest.sabab.dev")).toThrow(/project id/);
    expect(() => parseDsn("not a url")).toThrow(/invalid DSN/);
  });
});

describe("stack parsing", () => {
  it("parses V8 frames into structured fields", () => {
    const stack = [
      "TypeError: Cannot read properties of undefined",
      "    at renderCart (https://app.example.com/static/js/main.a3f9.js:1:48213)",
      "    at handleClick (https://app.example.com/static/js/main.a3f9.js:1:12000)",
    ].join("\n");

    const frames = parseStack(stack);

    expect(frames).toHaveLength(2);
    // Outermost first: the LAST frame is where it threw.
    expect(frames.at(-1)?.function).toBe("renderCart");
    expect(frames.at(-1)?.lineno).toBe(1);
    expect(frames.at(-1)?.colno).toBe(48213);
    expect(frames.at(-1)?.in_app).toBe(true);
  });

  it("parses Firefox and Safari frames", () => {
    const stack = "renderCart@https://app.example.com/static/js/main.a3f9.js:1:48213";
    const frames = parseStack(stack);

    expect(frames).toHaveLength(1);
    expect(frames[0]?.function).toBe("renderCart");
    expect(frames[0]?.colno).toBe(48213);
  });

  it("marks dependency frames as not in_app", () => {
    const stack = [
      "    at handle (https://app.example.com/node_modules/react/index.js:1:100)",
      "    at renderCart (https://app.example.com/src/cart.ts:42:9)",
    ].join("\n");

    const frames = parseStack(stack);
    const vendor = frames.find((f) => f.filename?.includes("node_modules"));
    const app = frames.find((f) => f.filename?.includes("src/cart"));

    // Grouping depends on this: hashing framework frames would merge unrelated bugs.
    expect(vendor?.in_app).toBe(false);
    expect(app?.in_app).toBe(true);
  });

  it("follows the cause chain, innermost last", () => {
    const root = new Error("connection refused");
    const wrapper = new Error("could not load cart", { cause: root });

    const chain = exceptionsFromUnknown(wrapper);

    expect(chain).toHaveLength(2);
    expect(chain.at(-1)?.value).toBe("connection refused");
    expect(chain[0]?.value).toBe("could not load cart");
  });

  it("survives a cause cycle instead of looping forever", () => {
    const a = new Error("a");
    const b = new Error("b", { cause: a });
    (a as { cause?: unknown }).cause = b;

    // Hanging here would hang inside an error handler — the worst place for it.
    const chain = exceptionsFromUnknown(a);
    expect(chain.length).toBeGreaterThan(0);
    expect(chain.length).toBeLessThanOrEqual(5);
  });

  it("captures non-Error throws instead of dropping them", () => {
    // JavaScript lets you throw anything, and an SDK that only handles Error
    // silently loses exactly the cases the user most needs to see.
    expect(exceptionsFromUnknown("a string")[0]?.value).toBe("a string");
    expect(exceptionsFromUnknown({ code: 500 })[0]?.value).toContain("500");
    expect(exceptionsFromUnknown(undefined)[0]?.value).toContain("undefined");

    const circular: Record<string, unknown> = {};
    circular.self = circular;
    expect(exceptionsFromUnknown(circular)[0]?.value).toContain("unserializable");
  });
});

describe("client", () => {
  it("sends a well-formed envelope", async () => {
    const { client, sent } = testClient();

    client.captureException(new Error("boom"));
    await client.flush();

    expect(sent).toHaveLength(1);
    const [header] = sent[0]!.split("\n");
    expect(JSON.parse(header!).sdk.name).toBe("sabab.javascript.test");

    const events = eventsIn(sent[0]!);
    expect(events[0]?.exception?.[0]?.value).toBe("boom");
    expect(events[0]?.release).toBe("web@2.4.1");
    expect(events[0]?.event_id).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("declares item length in BYTES, not characters", async () => {
    const { client, sent } = testClient();

    // "é" is one character but two bytes. Getting this wrong corrupts every
    // envelope containing a non-ASCII message — i.e. most real ones.
    client.captureException(new Error("café ☕"));
    await client.flush();

    const lines = sent[0]!.trim().split("\n");
    const declared = JSON.parse(lines[1]!).length;
    const actual = new TextEncoder().encode(lines[2]!).length;
    expect(declared).toBe(actual);
  });

  it("attaches user, tags and breadcrumbs", async () => {
    const { client, sent } = testClient();

    client.setUser({ id: "u_91" });
    client.setTag("tenant", "acme");
    client.addBreadcrumb({ category: "navigation", message: "/ → /cart" });
    client.captureException(new Error("boom"));
    await client.flush();

    const event = eventsIn(sent[0]!)[0]!;
    expect(event.user?.id).toBe("u_91");
    // The browser cannot know its own IP, so it asks the gateway to fill it in.
    expect(event.user?.ip_address).toBe("{{auto}}");
    expect(event.tags?.tenant).toBe("acme");
    expect(event.breadcrumbs?.[0]?.message).toBe("/ → /cart");
  });

  it("bounds the breadcrumb buffer", async () => {
    const { client, sent } = testClient({ maxBreadcrumbs: 3 });

    for (let i = 0; i < 10; i++) client.addBreadcrumb({ message: `crumb ${i}` });
    client.captureException(new Error("boom"));
    await client.flush();

    const event = eventsIn(sent[0]!)[0]!;
    expect(event.breadcrumbs).toHaveLength(3);
    // The OLDEST are dropped; the most recent are the ones that explain the crash.
    expect(event.breadcrumbs?.at(-1)?.message).toBe("crumb 9");
  });

  it("reports what it dropped, so our counts stay honest", async () => {
    const { client, sent } = testClient({ beforeSend: () => null });

    client.captureException(new Error("dropped"));
    // beforeSend dropped it, so nothing is queued and nothing is sent yet.
    await client.flush();
    expect(sent).toHaveLength(0);

    // The NEXT real event carries the confession along with it.
    const { client: c2, sent: s2 } = testClient({
      beforeSend: (e) => (e.exception?.[0]?.value === "dropped" ? null : e),
    });
    c2.captureException(new Error("dropped"));
    c2.captureException(new Error("kept"));
    await c2.flush();

    const items = itemsIn(s2[0]!);
    const report = items.find((i) => i.type === "client_report");
    expect(report).toBeDefined();
    const payload = report!.payload as { discarded_events: { reason: string; quantity: number }[] };
    expect(payload.discarded_events[0]?.reason).toBe("before_send");
    expect(payload.discarded_events[0]?.quantity).toBe(1);
  });

  it("bounds the event queue rather than growing without limit", async () => {
    const { client, sent } = testClient();

    // An app throwing in a tight loop must not be able to make us the outage.
    for (let i = 0; i < 200; i++) client.captureException(new Error(`boom ${i}`));
    await client.flush();

    const items = itemsIn(sent[0]!);
    const errors = items.filter((i) => i.type === "error");
    expect(errors.length).toBeLessThanOrEqual(30);

    // ...and the overflow is confessed, not hidden.
    const report = items.find((i) => i.type === "client_report");
    const payload = report!.payload as { discarded_events: { reason: string }[] };
    expect(payload.discarded_events.some((d) => d.reason === "queue_overflow")).toBe(true);
  });

  // THE promise of the whole SDK: it must never break the host app.
  it("never throws, even when beforeSend does", async () => {
    const { client } = testClient({
      beforeSend: () => {
        throw new Error("the user's hook is broken");
      },
    });

    expect(() => client.captureException(new Error("boom"))).not.toThrow();
  });

  it("never throws when the transport fails", async () => {
    const client = new Client(
      { dsn: DSN },
      { name: "t", version: "1" },
      "javascript",
      async () => {
        throw new Error("network is down");
      },
    );

    client.captureException(new Error("boom"));
    // A failed flush resolves false. It does not reject, because a rejected
    // promise here would become an unhandled rejection in the host app — we
    // would be creating exactly the error we exist to report.
    await expect(client.flush()).resolves.toBe(false);
  });

  it("backs off when the server says 429", async () => {
    const { client, sent } = testClient();

    client.backOff(60);
    client.captureException(new Error("boom"));
    await client.flush();

    // Nothing is sent while backing off: hammering a 429 in a tight loop is how
    // an SDK turns a busy server into a dead one.
    expect(sent).toHaveLength(0);
  });
});

describe("logging", () => {
	it("captures a structured log with trace context", async () => {
		const { client, sent } = testClient();

		client.setTrace("6fa459ea-ee8a-3ca4-894e-db77e160355e");
		client.logger.info("checkout started", { cartSize: 3 });
		await client.flush();

		const items = itemsIn(sent[0]!);
		const log = items.find((i) => i.type === "log");
		expect(log).toBeDefined();
		const p = log!.payload as {
			severity: string;
			body: string;
			trace_id?: string;
			attributes?: Record<string, string>;
		};
		expect(p.severity).toBe("info");
		expect(p.body).toBe("checkout started");
		// The trace context must ride along, so "logs in this trace" works.
		expect(p.trace_id).toBe("6fa459ea-ee8a-3ca4-894e-db77e160355e");
		// Attribute values are coerced to strings, since the column is a string map.
		expect(p.attributes?.cartSize).toBe("3");
	});

	it("carries errors and logs in one envelope, so a crash and its logs correlate", async () => {
		const { client, sent } = testClient();

		client.logger.info("about to render");
		client.captureException(new Error("boom"));
		await client.flush();

		const items = itemsIn(sent[0]!);
		expect(items.some((i) => i.type === "log")).toBe(true);
		expect(items.some((i) => i.type === "error")).toBe(true);
	});

	it("console capture forwards to the sink without suppressing the console", async () => {
		const { client, sent } = testClient();
		const fakeConsole: Record<string, (...a: unknown[]) => void> = {};
		let originalCalled = false;
		fakeConsole.warn = () => {
			originalCalled = true;
		};

		const restore = patchConsole(fakeConsole, (r) => client.logger[r.severity](r.body));
		fakeConsole.warn("disk almost full");
		restore();

		// The app's own console must still run.
		expect(originalCalled).toBe(true);

		await client.flush();
		const log = itemsIn(sent[0]!).find((i) => i.type === "log");
		const p = log!.payload as { severity: string; body: string };
		expect(p.severity).toBe("warn");
		expect(p.body).toBe("disk almost full");
	});
});
