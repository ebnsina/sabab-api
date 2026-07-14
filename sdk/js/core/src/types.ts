/**
 * The public shape of the Sabab SDK.
 *
 * These types mirror the wire format in docs/wire-format.md. They are declared
 * here, once, and re-exported by @sabab/browser and @sabab/node so the two
 * runtimes cannot drift apart.
 */

export type Level = "debug" | "info" | "warning" | "error" | "fatal";

/** A stack frame. Structured — never a pre-formatted string. */
export interface Frame {
  function?: string;
  module?: string;
  filename?: string;
  lineno?: number;
  colno?: number;
  /** The customer's own code, as opposed to a dependency. */
  in_app: boolean;
}

export interface Mechanism {
  /** "onerror", "onunhandledrejection", "instrument", "generic". */
  type?: string;
  /** false means a genuine crash, not a deliberate captureException. */
  handled?: boolean;
}

export interface Exception {
  type: string;
  value: string;
  mechanism?: Mechanism;
  frames?: Frame[];
}

export interface Breadcrumb {
  ts: string;
  type?: string;
  category?: string;
  level?: Level;
  message?: string;
  data?: Record<string, unknown>;
}

export interface User {
  id?: string;
  email?: string;
  /** "{{auto}}" asks the gateway to fill in the address we cannot know. */
  ip_address?: string;
}

export interface ErrorEvent {
  event_id: string;
  timestamp: string;
  level: Level;
  platform: string;
  release?: string;
  environment?: string;
  /** The cause chain, innermost last. */
  exception?: Exception[];
  message?: string;
  breadcrumbs?: Breadcrumb[];
  contexts?: Record<string, unknown>;
  user?: User;
  tags?: Record<string, string>;
  trace_id?: string;
  span_id?: string;
  /** ["{{default}}"] means "your algorithm, plus my extras". */
  fingerprint?: string[];
}

/** Why an event was thrown away locally. Reported back so our counts stay honest. */
export type DiscardReason =
  | "queue_overflow"
  | "ratelimit_backoff"
  | "before_send"
  | "network_error"
  | "sample_rate";

export interface SababOptions {
  /**
   * The whole configuration in one string:
   *   https://pk_live_7f3a@ingest.sabab.dev/4
   */
  dsn: string;

  /** "web@2.4.1". Without it we cannot symbolicate — stacks stay minified. */
  release?: string;
  environment?: string;

  /** Drop a fraction of events. 1.0 keeps everything. */
  sampleRate?: number;

  /** How many breadcrumbs to keep. Bounded, so we cannot grow without limit. */
  maxBreadcrumbs?: number;

  /**
   * The last word before an event leaves the process. Return null to drop it.
   * The place to strip anything we did not think of.
   */
  beforeSend?: (event: ErrorEvent) => ErrorEvent | null;

  /** Called on internal SDK errors. Off by default: we must be silent in production. */
  debug?: boolean;
}

/** A parsed DSN. */
export interface Dsn {
  publicKey: string;
  host: string;
  protocol: string;
  projectId: string;
  /** The full ingest endpoint. */
  envelopeUrl: string;
}
