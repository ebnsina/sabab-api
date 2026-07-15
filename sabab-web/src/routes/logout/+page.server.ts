import { redirect } from '@sveltejs/kit';
import { apiRaw } from '$lib/server/api';
import type { Actions } from './$types';

export const actions: Actions = {
	default: async ({ fetch, cookies, locals }) => {
		// Tell the Go API to delete the session row, so the token is dead
		// server-side and not merely forgotten by the browser.
		try {
			await apiRaw('/api/v1/auth/logout', {
				method: 'POST',
				session: locals.session,
				fetch
			});
		} catch {
			// Even if that call fails, clear the cookie — the row expires on its
			// own, and the user asked to be logged out.
		}
		cookies.delete('sabab_session', { path: '/' });
		throw redirect(303, '/login');
	}
};
