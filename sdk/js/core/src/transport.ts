import type { DiscardReason, Dsn, ErrorEvent } from "./types.js";

/**
 * The transport: buffer events, encode envelopes, send them, and be honest
 * about anything dropped along the way.
 *
 * Two rules govern everything here.
 *
 * 1. NEVER break the host app. Every failure is swallowed. An observability tool
 *    that takes down production is worse than no observability tool.
 *
 * 2. NEVER silently lose an event. Anything we drop is counted and reported back
 *    in a client_report, so our numbers stay honest. Under-reporting without
 *    saying so destroys trust in the tool permanently — and unlike a crash, the
 *    user never finds out.
 */

/** SDK identity, sent on every envelope. */
export interface SdkInfo {
  name: string;
  version: string;
}

/**
 * The buffer is bounded. An app throwing in a tight loop must not be able to
 * make us consume its memory — we would then be the outage.
 */
const MAX_QUEUE = 30;

/** How long a flush may take before we give up on it. */
const TIMEOUT_MS = 5000;

type Item = { type: "error"; payload: ErrorEvent };

export class Transport {
  private queue: Item[] = [];
  private discarded = new Map<string, number>();
  private flushTimer: ReturnType<typeof setTimeout> | undefined;
  /** Set by a 429; we send nothing until it passes. */
  private rateLimitedUntil = 0;
  private sending = false;

  constructor(
    private readonly dsn: Dsn,
    private readonly sdk: SdkInfo,
    private readonly debug: boolean,
    /**
     * Injected so the browser can use sendBeacon on pagehide and Node can use
     * plain fetch. The core never reaches for a global that may not exist.
     */
    private readonly send: (url: string, body: string, headers: Record<string, string>) => Promise<boolean>,
  ) {}

  /** Queue an event. Returns false if it was dropped. */
  enqueue(event: ErrorEvent): boolean {
    const now = Date.now();
    if (now < this.rateLimitedUntil) {
      // The server told us to back off. Honour it — hammering a 429 in a tight
      // loop is how an SDK turns a busy server into a dead one.
      this.recordDiscard("ratelimit_backoff");
      return false;
    }

    if (this.queue.length >= MAX_QUEUE) {
      this.recordDiscard("queue_overflow");
      return false;
    }

    this.queue.push({ type: "error", payload: event });
    this.scheduleFlush();
    return true;
  }

  /** Count an event the caller dropped (sampling, beforeSend). */
  recordDiscard(reason: DiscardReason): void {
    this.discarded.set(reason, (this.discarded.get(reason) ?? 0) + 1);
  }

  /**
   * Batch briefly before sending: an app that throws three times in one tick
   * should cost one request, not three.
   */
  private scheduleFlush(): void {
    if (this.flushTimer !== undefined) return;
    this.flushTimer = setTimeout(() => {
      this.flushTimer = undefined;
      void this.flush();
    }, 100);
    // Do not hold a Node process open just to flush telemetry.
    (this.flushTimer as { unref?: () => void }).unref?.();
  }

  /** Send everything queued. Never throws. */
  async flush(): Promise<boolean> {
    if (this.sending || this.queue.length === 0) return true;

    this.sending = true;
    const items = this.queue;
    this.queue = [];

    try {
      const body = this.encode(items);
      const ok = await this.withTimeout(
        this.send(this.dsn.envelopeUrl, body, {
          "Content-Type": "application/x-sabab-envelope",
          "X-Sabab-Key": this.dsn.publicKey,
        }),
      );
      if (!ok) {
        // The events are gone. Say so, rather than pretending they arrived.
        for (let i = 0; i < items.length; i++) this.recordDiscard("network_error");
      }
      return ok;
    } catch (err) {
      this.log("flush failed", err);
      for (let i = 0; i < items.length; i++) this.recordDiscard("network_error");
      return false;
    } finally {
      this.sending = false;
    }
  }

  /** Tell the transport the server rate-limited us. */
  backOff(retryAfterSeconds: number): void {
    this.rateLimitedUntil = Date.now() + retryAfterSeconds * 1000;
  }

  /**
   * Encode the envelope: a header line, then item header / payload pairs.
   * The client_report rides along, so the drops are reported on the very next
   * request rather than being lost with the page.
   */
  private encode(items: Item[]): string {
    const header = JSON.stringify({
      sent_at: new Date().toISOString(),
      sdk: this.sdk,
    });

    const lines = [header];

    for (const item of items) {
      const payload = JSON.stringify(item.payload);
      lines.push(
        JSON.stringify({ type: item.type, length: byteLength(payload) }),
      );
      lines.push(payload);
    }

    const report = this.takeClientReport();
    if (report) {
      const payload = JSON.stringify(report);
      lines.push(
        JSON.stringify({ type: "client_report", length: byteLength(payload) }),
      );
      lines.push(payload);
    }

    return lines.join("\n") + "\n";
  }

  /** Drain the discard counters into a client_report. */
  private takeClientReport(): object | null {
    if (this.discarded.size === 0) return null;

    const discarded_events = [...this.discarded.entries()].map(
      ([reason, quantity]) => ({ reason, category: "error", quantity }),
    );
    this.discarded.clear();

    return { timestamp: new Date().toISOString(), discarded_events };
  }

  private async withTimeout(p: Promise<boolean>): Promise<boolean> {
    // A hard timeout, because a hanging request must not keep the page's
    // unload handler — or a Node process — waiting on us.
    return Promise.race([
      p,
      new Promise<boolean>((resolve) =>
        setTimeout(() => resolve(false), TIMEOUT_MS),
      ),
    ]);
  }

  private log(message: string, err?: unknown): void {
    if (!this.debug) return;
    // eslint-disable-next-line no-console
    console.warn(`[sabab] ${message}`, err);
  }
}

/**
 * The wire format counts BYTES, not UTF-16 code units.
 *
 * `"é".length` is 1 but it is 2 bytes on the wire. Using .length here would
 * declare the wrong item length and corrupt every envelope containing a
 * non-ASCII character — which is to say, most real error messages.
 */
const encoder = new TextEncoder();

function byteLength(s: string): number {
  return encoder.encode(s).length;
}
