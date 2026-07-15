/**
 * Runs on every request. It resolves the current user once and hangs it on
 * event.locals, so every load function can read `locals.user` instead of each
 * re-deriving it from the cookie.
 */
import { redirect, type Handle } from '@sveltejs/kit';
import { api, ApiRequestError, SESSION_COOKIE, type Me } from '$lib/server/api';

/** Routes reachable without a session. Everything else requires login. */
const PUBLIC_PATHS = ['/login', '/changelog'];

/** Public routes a logged-in user should still be allowed to view (rather than
 *  be bounced to the app). Login is not one of these — a signed-in user has no
 *  reason to see it. */
const SHARED_PATHS = ['/changelog'];

export const handle: Handle = async ({ event, resolve }) => {
	const session = event.cookies.get(SESSION_COOKIE);
	event.locals.session = session;
	event.locals.user = null;

	if (session) {
		try {
			const me = await api<Me>('/api/v1/auth/me', { session, fetch: event.fetch });
			event.locals.user = me.user;
		} catch (err) {
			// An expired or invalid session is not an error to show — it just means
			// "logged out". A real API outage (503) is different: surfacing it as
			// "logged out" would send the user to a login page that also cannot
			// work, so we leave the session in place and let the page report it.
			if (err instanceof ApiRequestError && err.info.status !== 401) {
				// fall through: the page's own load will surface the outage
			} else {
				event.cookies.delete(SESSION_COOKIE, { path: '/' });
			}
		}
	}

	const isPublic = PUBLIC_PATHS.some((p) => event.url.pathname.startsWith(p));

	// Gate at the edge, not in each page: a route that forgets its own guard is
	// a data leak, so the default is "authentication required".
	if (!event.locals.user && !isPublic) {
		// Remember where they were headed, so login can send them back.
		const returnTo = event.url.pathname + event.url.search;
		throw redirect(303, `/login?next=${encodeURIComponent(returnTo)}`);
	}

	// A logged-in user hitting a login-only public page (i.e. /login) goes
	// straight to the app — but shared public pages like /changelog stay
	// reachable while signed in.
	const isShared = SHARED_PATHS.some((p) => event.url.pathname.startsWith(p));
	if (event.locals.user && isPublic && !isShared) {
		throw redirect(303, '/');
	}

	return resolve(event);
};
