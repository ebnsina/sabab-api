/**
 * The server-side proxy to the Go API.
 *
 * The browser never talks to the Go API directly. Every request goes through
 * SvelteKit's server, which forwards the session cookie. That keeps the session
 * cookie same-origin and HttpOnly — the dashboard's JavaScript never has to see
 * it, so an XSS in our own UI cannot exfiltrate it. This is an observability
 * tool: it runs next to customer code by definition, and its session is exactly
 * what an attacker would want.
 */
import { env } from '$env/dynamic/private';

/** Where the Go API lives. Configurable so dev and prod can differ. */
const API_URL = env.SABAB_API_URL ?? 'http://localhost:8091';

/** The session cookie name, kept in sync with the Go side (auth.SessionCookie). */
export const SESSION_COOKIE = 'sabab_session';

export interface ApiError {
	status: number;
	code: string;
	message: string;
}

/** Thrown by the api helpers so a load function can map it to the right page. */
export class ApiRequestError extends Error {
	constructor(readonly info: ApiError) {
		super(info.message);
		this.name = 'ApiRequestError';
	}
}

interface RequestOptions {
	method?: string;
	/** The session token, forwarded from the incoming request's cookie. */
	session?: string;
	body?: unknown;
	/** SvelteKit's fetch, so server-side requests are traced and deduped. */
	fetch: typeof globalThis.fetch;
}

/**
 * Call the Go API and return the parsed JSON.
 *
 * On a non-2xx it throws ApiRequestError carrying the structured error the Go
 * API returns — the same {code, message} shape every endpoint uses — so callers
 * can branch on the code rather than parse a message.
 */
export async function api<T>(path: string, opts: RequestOptions): Promise<T> {
	const headers: Record<string, string> = {};
	if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
	if (opts.session) headers['Cookie'] = `${SESSION_COOKIE}=${opts.session}`;

	let response: Response;
	try {
		response = await opts.fetch(`${API_URL}${path}`, {
			method: opts.method ?? 'GET',
			headers,
			body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined
		});
	} catch (cause) {
		// The Go API is unreachable. This is our outage, not the user's mistake,
		// and it must read as a 503 rather than a confusing blank page.
		throw new ApiRequestError({
			status: 503,
			code: 'api_unreachable',
			message: 'The Sabab API is not responding. Is it running?'
		});
	}

	if (response.status === 204) return undefined as T;

	const text = await response.text();
	const parsed = text ? safeJson(text) : null;

	if (!response.ok) {
		const err = (parsed as { error?: { code?: string; message?: string } })?.error;
		throw new ApiRequestError({
			status: response.status,
			code: err?.code ?? 'error',
			message: err?.message ?? `Request failed (${response.status}).`
		});
	}

	return parsed as T;
}

/**
 * Like api(), but returns the raw Response so a route can forward the Go API's
 * Set-Cookie header verbatim — which is how login hands the browser its session.
 */
export async function apiRaw(path: string, opts: RequestOptions): Promise<Response> {
	const headers: Record<string, string> = {};
	if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
	if (opts.session) headers['Cookie'] = `${SESSION_COOKIE}=${opts.session}`;

	return opts.fetch(`${API_URL}${path}`, {
		method: opts.method ?? 'GET',
		headers,
		body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined
	});
}

function safeJson(text: string): unknown {
	try {
		return JSON.parse(text);
	} catch {
		return null;
	}
}

// --- API response shapes ----------------------------------------------------
// These mirror the Go structs in internal/api and internal/store. Kept here so
// every route shares one definition rather than re-declaring the shape.

export interface User {
	id: number;
	email: string;
	name: string;
}

export interface Me {
	user: User;
}

export interface Project {
	id: number;
	org_id: number;
	slug: string;
	name: string;
	platform: string;
}

export interface Issue {
	id: number;
	project_id: number;
	group_hash: string;
	title: string;
	culprit: string;
	level: string;
	status: 'unresolved' | 'resolved' | 'ignored';
	first_seen: string;
	last_seen: string;
	times_seen: number;
	users_affected: number;
	first_release?: string;
	resolved_in_release?: string;
	assignee_id?: number | null;
	group_components?: string[];
	/** Per-hour counts for the little chart. Attached from ClickHouse. */
	sparkline?: number[];
}

export interface Frame {
	function?: string;
	module?: string;
	filename?: string;
	lineno?: number;
	colno?: number;
	in_app: boolean;
	pre_context?: string[];
	context_line?: string;
	post_context?: string[];
}

export interface ExceptionValue {
	type: string;
	value: string;
	frames?: Frame[];
}

export interface Breadcrumb {
	ts: string;
	category?: string;
	message?: string;
	level?: string;
}

export interface SabEvent {
	event_id: string;
	timestamp: string;
	level: string;
	environment: string;
	release: string;
	platform: string;
	exception_type: string;
	exception_value: string;
	culprit: string;
	trace_id: string;
	user_id?: string;
	user_email?: string;
	browser?: string;
	os?: string;
	tags?: Record<string, string>;
	/** Raw JSON strings from ClickHouse; parsed by the detail page. */
	stacktrace: string;
	breadcrumbs: string;
	contexts: string;
}

export interface Activity {
	id: number;
	user_id?: number | null;
	kind: string;
	at: string;
}

export interface LogEntry {
	timestamp: string;
	severity: string;
	service: string;
	body: string;
	template?: string;
	trace_id: string;
	environment?: string;
	release?: string;
	attributes?: Record<string, string>;
}
