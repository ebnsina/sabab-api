import {
  Client,
  patchConsole,
  type Level,
  type MetricOptions,
  type SababOptions,
} from "@sabab/core";

/**
 * @sabab/browser — errors from the browser.
 *
 * Everything here hooks a global that the host application also relies on:
 * window.onerror, unhandledrejection, fetch. Each hook is wrapped so that a bug
 * in ours can never break theirs, and every original is called through. An
 * observability tool that breaks the page it is watching is worse than none.
 */

const SDK = { name: "sabab.javascript.browser", version: "1.0.0" };

let client: Client | undefined;

/** Start the SDK. Safe to call twice; the second call is ignored. */
export function init(options: SababOptions): Client | undefined {
  if (client) return client;

  try {
    const c = new Client(options, SDK, "javascript", send);
    client = c;

    installGlobalHandlers(c);
    installBreadcrumbs(c);
    installFlushOnHide(c);

    // Console capture is opt-in: it changes what the app's telemetry contains,
    // so we do not turn it on behind the user's back.
    if (options.captureConsole) {
      patchConsole(
        console as unknown as Record<string, (...args: unknown[]) => void>,
        (record) => c.logger[record.severity](record.body),
      );
    }

    c.setContext("browser", {
      name: browserName(),
      user_agent: navigator.userAgent,
    });
    return c;
  } catch (err) {
    // Failing to initialise must not take the page down with it.
    if (options.debug) console.warn("[sabab] init failed", err);
    return undefined;
  }
}

export function captureException(thrown: unknown): void {
  client?.captureException(thrown);
}

export function captureMessage(message: string, level: Level = "info"): void {
  client?.captureMessage(message, level);
}

export function setUser(user: { id?: string; email?: string }): void {
  client?.setUser(user);
}

export function setTag(key: string, value: string): void {
  client?.setTag(key, value);
}

export function addBreadcrumb(crumb: {
  category?: string;
  message?: string;
  level?: Level;
  data?: Record<string, unknown>;
}): void {
  client?.addBreadcrumb(crumb);
}

export function flush(): Promise<boolean> {
  return client?.flush() ?? Promise.resolve(true);
}

/**
 * The structured logger: Sabab.log.info("checkout started", { cartSize: 3 }).
 *
 * Safe to call before init — it no-ops until the SDK is running, so a log
 * statement never throws just because init has not run yet.
 */
export const log = {
  trace: (m: string, a?: Record<string, unknown>) => client?.logger.trace(m, a),
  debug: (m: string, a?: Record<string, unknown>) => client?.logger.debug(m, a),
  info: (m: string, a?: Record<string, unknown>) => client?.logger.info(m, a),
  warn: (m: string, a?: Record<string, unknown>) => client?.logger.warn(m, a),
  error: (m: string, a?: Record<string, unknown>) => client?.logger.error(m, a),
  fatal: (m: string, a?: Record<string, unknown>) => client?.logger.fatal(m, a),
};

/**
 * Metrics: Sabab.metrics.increment("checkout.completed", 1, { tags: { plan } }).
 *
 * Safe to call before init — it no-ops until the SDK is running, so a counter
 * never throws just because init has not run yet.
 */
export const metrics = {
  increment: (name: string, value?: number, opts?: MetricOptions) =>
    client?.metrics.increment(name, value, opts),
  gauge: (name: string, value: number, opts?: MetricOptions) =>
    client?.metrics.gauge(name, value, opts),
  distribution: (name: string, value: number, opts?: MetricOptions) =>
    client?.metrics.distribution(name, value, opts),
  timing: (name: string, milliseconds: number, opts?: MetricOptions) =>
    client?.metrics.timing(name, milliseconds, opts),
  set: (name: string, value: number, opts?: MetricOptions) =>
    client?.metrics.set(name, value, opts),
  startTimer: (name: string, opts?: MetricOptions) =>
    client?.metrics.startTimer(name, opts) ?? (() => {}),
};

/**
 * The transport. fetch with keepalive, so a request survives the page starting
 * to unload — which is exactly when a crash tends to be reported.
 */
async function send(
  url: string,
  body: string,
  headers: Record<string, string>,
): Promise<boolean> {
  const response = await fetch(url, {
    method: "POST",
    body,
    headers,
    keepalive: true,
    // Never send cookies to the ingest endpoint. It is a public, write-only
    // key; attaching credentials would be pointless and a CSRF surface.
    credentials: "omit",
    mode: "cors",
  });

  if (response.status === 429) {
    const retryAfter = Number(response.headers.get("Retry-After") ?? "60");
    client?.backOff(Number.isFinite(retryAfter) ? retryAfter : 60);
    return false;
  }
  return response.ok;
}

function installGlobalHandlers(c: Client): void {
  // An uncaught error. `error` is the real Error when the browser has one;
  // otherwise we fall back to the message, which is all a cross-origin script
  // gives us.
  window.addEventListener("error", (event: ErrorEvent) => {
    c.captureException(event.error ?? event.message, {
      mechanism: { type: "onerror", handled: false },
    });
  });

  // The one people forget. An unhandled rejection is a crash in everything but
  // name, and it is the most common way a modern app breaks.
  window.addEventListener("unhandledrejection", (event: PromiseRejectionEvent) => {
    c.captureException(event.reason, {
      mechanism: { type: "onunhandledrejection", handled: false },
    });
  });
}

/**
 * Breadcrumbs: what the app was doing before it broke.
 *
 * These patch globals the app owns. Each patch calls the original and swallows
 * its own errors, so the app cannot be broken by our instrumentation.
 */
function installBreadcrumbs(c: Client): void {
  // Clicks.
  document.addEventListener(
    "click",
    (event) => {
      try {
        const target = event.target as Element | null;
        if (!target?.tagName) return;
        c.addBreadcrumb({
          category: "ui.click",
          message: describeElement(target),
        });
      } catch {
        /* a breadcrumb is never worth an exception */
      }
    },
    { capture: true, passive: true },
  );

  // Navigation, including SPA route changes, which are invisible otherwise.
  const patchHistory = (method: "pushState" | "replaceState") => {
    const original = history[method];
    history[method] = function (this: History, ...args: Parameters<History["pushState"]>) {
      try {
        c.addBreadcrumb({
          category: "navigation",
          message: `${location.pathname} → ${String(args[2] ?? "")}`,
        });
      } catch {
        /* never break navigation */
      }
      return original.apply(this, args);
    };
  };
  patchHistory("pushState");
  patchHistory("replaceState");

  // fetch.
  const originalFetch = window.fetch;
  window.fetch = async function (...args: Parameters<typeof fetch>) {
    const started = Date.now();
    try {
      const response = await originalFetch.apply(this, args);
      try {
        c.addBreadcrumb({
          category: "fetch",
          level: response.ok ? "info" : "warning",
          message: `${requestMethod(args)} ${requestUrl(args)} → ${response.status}`,
          data: { duration_ms: Date.now() - started },
        });
      } catch {
        /* ignore */
      }
      return response;
    } catch (err) {
      try {
        c.addBreadcrumb({
          category: "fetch",
          level: "error",
          message: `${requestMethod(args)} ${requestUrl(args)} → failed`,
        });
      } catch {
        /* ignore */
      }
      // The app's error is the app's. Re-throw it untouched.
      throw err;
    }
  };
}

/**
 * Flush on pagehide, not on unload: unload is unreliable on mobile, where a
 * backgrounded tab may simply be killed. pagehide is the last event we are
 * guaranteed, and it is when a crash report has to make it out or be lost.
 */
function installFlushOnHide(c: Client): void {
  addEventListener("pagehide", () => void c.flush());
  addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") void c.flush();
  });
}

function describeElement(el: Element): string {
  const id = el.id ? `#${el.id}` : "";
  const cls =
    typeof el.className === "string" && el.className
      ? `.${el.className.trim().split(/\s+/).slice(0, 2).join(".")}`
      : "";
  const text = el.textContent?.trim().slice(0, 40) ?? "";
  return `${el.tagName.toLowerCase()}${id}${cls}${text ? ` "${text}"` : ""}`;
}

function requestUrl(args: Parameters<typeof fetch>): string {
  const [input] = args;
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.toString();
  return input.url;
}

function requestMethod(args: Parameters<typeof fetch>): string {
  const [input, init] = args;
  if (init?.method) return init.method.toUpperCase();
  if (input instanceof Request) return input.method.toUpperCase();
  return "GET";
}

function browserName(): string {
  const ua = navigator.userAgent;
  // Order matters: Edge claims to be Chrome, Chrome claims to be Safari.
  if (ua.includes("Edg/")) return "Edge";
  if (ua.includes("OPR/")) return "Opera";
  if (ua.includes("Firefox/")) return "Firefox";
  if (ua.includes("Chrome/")) return "Chrome";
  if (ua.includes("Safari/")) return "Safari";
  return "";
}
