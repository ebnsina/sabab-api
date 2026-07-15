import { fail, redirect } from '@sveltejs/kit';
import { apiRaw, ApiRequestError } from '$lib/server/api';
import type { Actions } from './$types';

export const actions: Actions = {
	default: async ({ request, fetch, cookies, url }) => {
		const form = await request.formData();
		const email = String(form.get('email') ?? '');
		const password = String(form.get('password') ?? '');

		if (!email || !password) {
			// Return the email so the field is not cleared on a failed submit —
			// re-typing it is a small thing that feels broken.
			return fail(400, { email, error: 'Enter your email and password.' });
		}

		let response: Response;
		try {
			response = await apiRaw('/api/v1/auth/login', {
				method: 'POST',
				body: { email, password },
				fetch
			});
		} catch {
			return fail(503, { email, error: 'The Sabab API is not responding.' });
		}

		if (!response.ok) {
			// The Go API already gives the same answer for unknown-email and
			// wrong-password, so this message cannot leak which accounts exist.
			return fail(response.status, { email, error: 'Invalid email or password.' });
		}

		// Forward the Set-Cookie the Go API issued, so the browser gets the exact
		// HttpOnly session cookie the API minted rather than one we re-wrap.
		const setCookie = response.headers.get('set-cookie');
		if (setCookie) {
			const value = parseSessionValue(setCookie);
			if (value) {
				cookies.set('sabab_session', value, {
					path: '/',
					httpOnly: true,
					sameSite: 'lax',
					secure: url.protocol === 'https:',
					maxAge: 60 * 60 * 24 * 14
				});
			}
		}

		// Back to where they were headed before the login redirect sent them here.
		const next = url.searchParams.get('next');
		throw redirect(303, safeNext(next));
	}
};

/** Pull the session token out of the Go API's Set-Cookie header. */
function parseSessionValue(setCookie: string): string | null {
	const match = /sabab_session=([^;]+)/.exec(setCookie);
	return match ? match[1] : null;
}

/** Only allow same-site relative redirects — never an attacker-supplied URL. */
function safeNext(next: string | null): string {
	if (next && next.startsWith('/') && !next.startsWith('//')) return next;
	return '/';
}
