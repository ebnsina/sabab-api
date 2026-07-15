import type { Level } from "./types.js";

/**
 * The log capture surface.
 *
 * Two ways in, and both matter. Explicit `logger.info(...)` calls are the
 * high-value structured path — a message plus attributes, with the trace context
 * attached automatically. Console patching is the zero-effort path: it captures
 * what an app already prints, so a user gets logs without changing any code.
 *
 * Everything here obeys the same rule as the rest of the SDK: it must never
 * break the host app, so a failure to ship a log is swallowed and the original
 * console still runs.
 */

/** The severities the server accepts, matching the logs table enum. */
export type LogSeverity = "trace" | "debug" | "info" | "warn" | "error" | "fatal";

/** One captured log, before the transport stamps trace context onto it. */
export interface LogRecord {
	timestamp: string;
	severity: LogSeverity;
	body: string;
	/** The pre-interpolation form, when we can recover it — "user {id} not found". */
	template?: string;
	attributes?: Record<string, string>;
}

/** How a captured log leaves the logger. */
export type LogSink = (record: LogRecord) => void;

/**
 * Logger is what the app calls, and what console patching feeds into.
 */
export class Logger {
	constructor(private readonly sink: LogSink) {}

	trace(message: string, attributes?: Record<string, unknown>): void {
		this.emit("trace", message, attributes);
	}
	debug(message: string, attributes?: Record<string, unknown>): void {
		this.emit("debug", message, attributes);
	}
	info(message: string, attributes?: Record<string, unknown>): void {
		this.emit("info", message, attributes);
	}
	warn(message: string, attributes?: Record<string, unknown>): void {
		this.emit("warn", message, attributes);
	}
	error(message: string, attributes?: Record<string, unknown>): void {
		this.emit("error", message, attributes);
	}
	fatal(message: string, attributes?: Record<string, unknown>): void {
		this.emit("fatal", message, attributes);
	}

	private emit(
		severity: LogSeverity,
		message: string,
		attributes?: Record<string, unknown>,
	): void {
		try {
			this.sink({
				timestamp: new Date().toISOString(),
				severity,
				body: message,
				attributes: stringifyAttributes(attributes),
			});
		} catch {
			// A failure to record a log is never worth an exception in the app.
		}
	}
}

/** Console methods we intercept, mapped to our severities. */
const CONSOLE_LEVELS: Record<string, LogSeverity> = {
	debug: "debug",
	info: "info",
	log: "info",
	warn: "warn",
	error: "error",
};

/**
 * Patch the global console so existing print statements become logs.
 *
 * Each patched method calls the ORIGINAL first, then forwards a copy to the
 * sink. The app's console output is never suppressed or altered — we are a
 * silent observer, and a bug in our forwarding must not swallow a line the
 * developer expected to see in their terminal.
 *
 * Returns a function that restores the original console, so the capture can be
 * cleanly torn down (and so tests do not leak a patched console).
 */
export function patchConsole(
	console: Record<string, (...args: unknown[]) => void>,
	sink: LogSink,
): () => void {
	const originals: Record<string, (...args: unknown[]) => void> = {};

	for (const [method, severity] of Object.entries(CONSOLE_LEVELS)) {
		const original = console[method];
		if (typeof original !== "function") continue;
		originals[method] = original;

		console[method] = (...args: unknown[]) => {
			// The app's own output, untouched and first.
			original.apply(console, args);

			try {
				sink({
					timestamp: new Date().toISOString(),
					severity,
					body: formatConsoleArgs(args),
				});
			} catch {
				/* never let capture break a console call */
			}
		};
	}

	return () => {
		for (const [method, original] of Object.entries(originals)) {
			console[method] = original;
		}
	};
}

/** Map a numeric or textual level to one of our severities. */
export function toSeverity(level: Level | string): LogSeverity {
	switch (level) {
		case "debug":
		case "trace":
		case "info":
		case "warn":
		case "error":
		case "fatal":
			return level;
		case "warning":
			return "warn";
		default:
			return "info";
	}
}

/**
 * Render console arguments the way the console would, so the stored body matches
 * what the developer saw. Objects are JSON where possible, because "[object
 * Object]" in a log is useless.
 */
function formatConsoleArgs(args: unknown[]): string {
	return args
		.map((arg) => {
			if (typeof arg === "string") return arg;
			if (arg instanceof Error) return `${arg.name}: ${arg.message}`;
			try {
				return JSON.stringify(arg);
			} catch {
				return String(arg);
			}
		})
		.join(" ")
		.slice(0, 8192); // bound a single line so one huge object cannot bloat the envelope
}

/** Coerce attribute values to strings, since the attributes column is a string map. */
function stringifyAttributes(
	attributes: Record<string, unknown> | undefined,
): Record<string, string> | undefined {
	if (!attributes) return undefined;

	const out: Record<string, string> = {};
	for (const [key, value] of Object.entries(attributes)) {
		out[key] =
			typeof value === "string" ? value : safeStringify(value);
	}
	return out;
}

function safeStringify(value: unknown): string {
	try {
		return typeof value === "object" ? JSON.stringify(value) : String(value);
	} catch {
		return String(value);
	}
}
