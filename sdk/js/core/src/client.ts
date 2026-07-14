import { parseDsn } from "./dsn.js";
import { exceptionsFromUnknown } from "./stack.js";
import { Transport, type SdkInfo } from "./transport.js";
import type {
  Breadcrumb,
  ErrorEvent,
  Level,
  Mechanism,
  SababOptions,
  User,
} from "./types.js";

/**
 * The client: scope (user, tags, breadcrumbs) plus capture.
 *
 * THE governing rule of this whole package: **the SDK must never break the host
 * app.** Every public method is wrapped, every failure is swallowed, every
 * buffer is bounded. An observability tool that takes down production is worse
 * than no observability tool — so an internal bug here must cost the user their
 * telemetry, never their application.
 */

const DEFAULT_MAX_BREADCRUMBS = 100;

export class Client {
  private readonly transport: Transport;
  private readonly options: SababOptions;
  private readonly platform: string;

  private user: User = {};
  private tags: Record<string, string> = {};
  private breadcrumbs: Breadcrumb[] = [];
  private contexts: Record<string, unknown> = {};

  constructor(
    options: SababOptions,
    sdk: SdkInfo,
    platform: string,
    send: (url: string, body: string, headers: Record<string, string>) => Promise<boolean>,
  ) {
    this.options = options;
    this.platform = platform;
    this.transport = new Transport(
      parseDsn(options.dsn),
      sdk,
      options.debug ?? false,
      send,
    );
  }

  /** Attach the user. It is what turns "500 errors" into "3 users affected". */
  setUser(user: User): void {
    this.guard(() => {
      // "{{auto}}" by default: the browser cannot know its own public IP, so it
      // asks the gateway to fill it in. The server may then truncate or drop it.
      this.user = { ip_address: "{{auto}}", ...user };
    });
  }

  setTag(key: string, value: string): void {
    this.guard(() => {
      this.tags[key] = String(value);
    });
  }

  setContext(key: string, value: unknown): void {
    this.guard(() => {
      this.contexts[key] = value;
    });
  }

  /** Record something the app did. Bounded — the oldest is dropped. */
  addBreadcrumb(crumb: Omit<Breadcrumb, "ts"> & { ts?: string }): void {
    this.guard(() => {
      const max = this.options.maxBreadcrumbs ?? DEFAULT_MAX_BREADCRUMBS;
      this.breadcrumbs.push({ ts: crumb.ts ?? new Date().toISOString(), ...crumb });
      if (this.breadcrumbs.length > max) {
        this.breadcrumbs.splice(0, this.breadcrumbs.length - max);
      }
    });
  }

  /** Capture anything that was thrown. */
  captureException(
    thrown: unknown,
    hint?: { mechanism?: Mechanism; level?: Level },
  ): void {
    this.guard(() => {
      const event = this.build({
        level: hint?.level ?? "error",
        exception: exceptionsFromUnknown(thrown, hint?.mechanism),
      });
      this.dispatch(event);
    });
  }

  /** Capture a message with no exception. */
  captureMessage(message: string, level: Level = "info"): void {
    this.guard(() => {
      this.dispatch(this.build({ level, message }));
    });
  }

  /** Send anything queued. Awaited on pagehide, and before a Node process exits. */
  async flush(): Promise<boolean> {
    try {
      return await this.transport.flush();
    } catch {
      return false;
    }
  }

  /** Tell the transport to back off, from a 429. */
  backOff(seconds: number): void {
    this.guard(() => this.transport.backOff(seconds));
  }

  private build(partial: Partial<ErrorEvent>): ErrorEvent {
    return {
      event_id: uuid(),
      timestamp: new Date().toISOString(),
      platform: this.platform,
      level: "error",
      release: this.options.release,
      environment: this.options.environment ?? "production",
      user: Object.keys(this.user).length > 0 ? this.user : undefined,
      tags: Object.keys(this.tags).length > 0 ? { ...this.tags } : undefined,
      contexts: Object.keys(this.contexts).length > 0 ? { ...this.contexts } : undefined,
      breadcrumbs: this.breadcrumbs.length > 0 ? [...this.breadcrumbs] : undefined,
      ...partial,
    };
  }

  private dispatch(event: ErrorEvent): void {
    const rate = this.options.sampleRate ?? 1;
    if (rate < 1 && Math.random() >= rate) {
      // Sampled out — but COUNTED, so the dashboard can still show a true rate
      // rather than the fraction that happened to be kept.
      this.transport.recordDiscard("sample_rate");
      return;
    }

    let final: ErrorEvent | null = event;
    if (this.options.beforeSend) {
      try {
        final = this.options.beforeSend(event);
      } catch {
        // A throwing beforeSend is the user's bug, but it must not become a
        // crash inside our error handler. Drop the event and say so.
        this.transport.recordDiscard("before_send");
        return;
      }
    }
    if (!final) {
      this.transport.recordDiscard("before_send");
      return;
    }

    this.transport.enqueue(final);
  }

  /**
   * Run fn, swallowing anything it throws.
   *
   * This is the promise the whole SDK is built on. It is applied at every public
   * entry point, so that a bug in our code — a bad regex, an exotic object we
   * fail to serialize — can never propagate into the host application.
   */
  private guard(fn: () => void): void {
    try {
      fn();
    } catch (err) {
      if (this.options.debug) {
        // eslint-disable-next-line no-console
        console.warn("[sabab] internal error", err);
      }
    }
  }
}

/** A UUID v4, without pulling in a dependency for it. */
function uuid(): string {
  const c: Crypto | undefined = globalThis.crypto;
  if (c?.randomUUID) return c.randomUUID();

  if (c?.getRandomValues) {
    const bytes = c.getRandomValues(new Uint8Array(16));
    // Stamp the version (4) and variant bits, as RFC 4122 requires.
    bytes.set([((bytes[6] ?? 0) & 0x0f) | 0x40], 6);
    bytes.set([((bytes[8] ?? 0) & 0x3f) | 0x80], 8);
    const hex = [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  }

  // Last resort. Weak randomness only risks an id collision, which the server
  // tolerates — it must not be a reason to fail to report the error.
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (ch) => {
    const r = (Math.random() * 16) | 0;
    const v = ch === "x" ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}
