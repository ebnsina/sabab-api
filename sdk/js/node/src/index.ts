import { Client, type Level, type SababOptions } from "@sabab/core";

/**
 * @sabab/node — errors from the server.
 *
 * The hard part on the server is not capturing the error, it is capturing it and
 * still letting the process die correctly. An uncaughtException means the
 * process is in an undefined state, and a well-behaved app exits. So we report,
 * flush with a hard deadline, and then get out of the way — we must never keep a
 * crashed process alive, and never delay its exit indefinitely waiting on our
 * own network call.
 */

const SDK = { name: "sabab.javascript.node", version: "1.0.0" };

/** How long we may hold up a crashing process to flush. */
const EXIT_FLUSH_MS = 2000;

let client: Client | undefined;

export function init(options: SababOptions): Client | undefined {
  if (client) return client;

  try {
    client = new Client(options, SDK, "node", send);

    installGlobalHandlers(client);

    client.setContext("runtime", { name: "node", version: process.version });
    return client;
  } catch (err) {
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

async function send(
  url: string,
  body: string,
  headers: Record<string, string>,
): Promise<boolean> {
  const response = await fetch(url, { method: "POST", body, headers });

  if (response.status === 429) {
    const retryAfter = Number(response.headers.get("Retry-After") ?? "60");
    client?.backOff(Number.isFinite(retryAfter) ? retryAfter : 60);
    return false;
  }
  return response.ok;
}

function installGlobalHandlers(c: Client): void {
  process.on("uncaughtException", (error: Error) => {
    c.captureException(error, {
      mechanism: { type: "uncaughtException", handled: false },
      level: "fatal",
    });

    void flushThenExit(c, error);
  });

  process.on("unhandledRejection", (reason: unknown) => {
    c.captureException(reason, {
      mechanism: { type: "unhandledRejection", handled: false },
    });
  });
}

/**
 * Report, then let the process die as it would have.
 *
 * Node's default behaviour for an uncaughtException is to print the error and
 * exit non-zero. By registering a handler we have SUPPRESSED that — so we must
 * reproduce it, or we have silently turned every crash into a zombie process
 * that a supervisor will never restart. That would be a far worse bug than the
 * one we were trying to report.
 */
async function flushThenExit(c: Client, error: Error): Promise<void> {
  const timeout = new Promise<void>((resolve) =>
    setTimeout(resolve, EXIT_FLUSH_MS).unref?.(),
  );

  try {
    await Promise.race([c.flush(), timeout]);
  } catch {
    /* nothing left to do */
  }

  // Print it the way Node would have, so logs and supervisors see what they
  // expect.
  console.error(error);
  process.exit(1);
}
