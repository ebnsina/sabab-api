import type { Exception, Frame, Mechanism } from "./types.js";

/**
 * Turn an Error into structured frames.
 *
 * The single most valuable field we send is `exception[].frames`, and it must be
 * STRUCTURED — never a pre-formatted stack string. Grouping, source maps and the
 * stack viewer all need real fields; a string turns each of them into a parsing
 * problem, and it is why errors arriving over OTLP will always group worse than
 * errors from our own SDK.
 */

/**
 * V8 (Chrome, Node, Edge):
 *   at renderCart (https://app.example.com/static/js/main.a3f9.js:1:48213)
 *   at https://app.example.com/static/js/main.a3f9.js:1:48213
 */
const V8_FRAME =
  /^\s*at (?:(.+?)\s+\()?(?:(.+?):(\d+):(\d+)|([^)]+))\)?\s*$/;

/**
 * SpiderMonkey (Firefox) and JavaScriptCore (Safari):
 *   renderCart@https://app.example.com/static/js/main.a3f9.js:1:48213
 *   @https://app.example.com/static/js/main.a3f9.js:1:48213
 */
const GECKO_FRAME = /^\s*(?:(.*?)@)?(.+?):(\d+):(\d+)\s*$/;

/** Paths that are not the customer's code. */
const VENDOR = [
  "node_modules/",
  "webpack-internal:",
  "webpack/bootstrap",
  "/deps/",
  ".pnpm/",
  "node:internal/",
];

/**
 * Frames from inside the SDK itself. They are always present at the top of a
 * captured stack and are pure noise to the user — worse, they would make every
 * captureException group together, since they share the top frames.
 */
const SDK_FRAME = /[\\/]@sabab[\\/]|sabab[.-](?:core|browser|node)/;

/** How deep a stack we keep. A runaway recursion produces thousands. */
const MAX_FRAMES = 50;

function isInApp(filename: string): boolean {
  if (!filename) return false;
  const lower = filename.toLowerCase();
  return !VENDOR.some((marker) => lower.includes(marker));
}

/** Parse one line into a frame, or null if it is not a frame at all. */
function parseLine(line: string): Frame | null {
  let match = V8_FRAME.exec(line);
  if (match) {
    const [, fn, file, lineno, colno, bare] = match;
    const filename = file ?? bare ?? "";
    return {
      function: fn || "<anonymous>",
      filename,
      lineno: lineno ? Number(lineno) : undefined,
      colno: colno ? Number(colno) : undefined,
      in_app: isInApp(filename),
    };
  }

  match = GECKO_FRAME.exec(line);
  if (match) {
    const [, fn, filename, lineno, colno] = match;
    return {
      function: fn || "<anonymous>",
      filename: filename ?? "",
      lineno: Number(lineno),
      colno: Number(colno),
      in_app: isInApp(filename ?? ""),
    };
  }

  return null;
}

/**
 * Parse an Error's stack into frames, ordered outermost-first — so the last
 * frame is where it threw, which is what the server expects.
 */
export function parseStack(stack: string | undefined): Frame[] {
  if (!stack) return [];

  const frames: Frame[] = [];
  for (const line of stack.split("\n")) {
    const frame = parseLine(line);
    if (!frame) continue;
    // Never show the user our own plumbing.
    if (SDK_FRAME.test(frame.filename ?? "")) continue;

    frames.push(frame);
    if (frames.length >= MAX_FRAMES) break;
  }

  // V8 reports innermost-first; the wire format wants outermost-first.
  return frames.reverse();
}

/**
 * Build the exception chain from an Error, following `cause`.
 *
 * A wrapped error carries the frames that actually explain the bug in its
 * cause, so flattening the chain to one entry throws away the answer.
 */
export function exceptionsFromError(
  error: Error,
  mechanism?: Mechanism,
): Exception[] {
  const chain: Exception[] = [];
  const seen = new Set<unknown>();

  let current: unknown = error;
  // Bounded: a cause cycle would otherwise loop forever inside an error handler,
  // which is the worst possible place to hang.
  while (current instanceof Error && !seen.has(current) && chain.length < 5) {
    seen.add(current);

    chain.push({
      type: current.name || "Error",
      value: current.message || "",
      // The mechanism belongs to the OUTERMOST error — it is how the error
      // reached us, not a property of its causes.
      mechanism: chain.length === 0 ? mechanism : undefined,
      frames: parseStack(current.stack),
    });

    current = (current as { cause?: unknown }).cause;
  }

  // Already "innermost last", which is what the wire format wants: walking
  // `cause` goes from the outer wrapper down to the root cause, and the root
  // cause is the innermost one. The server reads exception[len-1] as the error
  // that actually threw — reversing here would hand it the wrapper instead, and
  // every issue would be titled after the outermost rethrow rather than the bug.
  return chain;
}

/**
 * Coerce whatever was thrown into an exception chain.
 *
 * JavaScript lets you `throw` anything — a string, an object, undefined. An SDK
 * that only handles Error instances silently drops those, which is exactly when
 * the user most needs to know what happened.
 */
export function exceptionsFromUnknown(
  thrown: unknown,
  mechanism?: Mechanism,
): Exception[] {
  if (thrown instanceof Error) {
    return exceptionsFromError(thrown, mechanism);
  }

  if (typeof thrown === "string") {
    return [{ type: "Error", value: thrown, mechanism, frames: [] }];
  }

  // A DOM ErrorEvent, a rejected non-Error, a plain object...
  let value: string;
  try {
    value =
      thrown && typeof thrown === "object"
        ? JSON.stringify(thrown)
        : String(thrown);
  } catch {
    // A circular or exotic object. Say so rather than throwing inside the
    // error handler.
    value = "[unserializable]";
  }

  return [
    {
      type: "NonError",
      value: `Non-Error value thrown: ${value.slice(0, 500)}`,
      mechanism,
      frames: [],
    },
  ];
}
