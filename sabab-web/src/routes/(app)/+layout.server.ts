import { api, type Project } from '$lib/server/api';
import type { LayoutServerLoad } from './$types';

/**
 * Loads the shell: the user (already resolved by the hook) and the projects
 * they can see. Runs for every page under (app), so the sidebar is always
 * populated without each page re-fetching it.
 */
export const load: LayoutServerLoad = async ({ locals, fetch }) => {
	// The hook guarantees a user here — an unauthenticated request was already
	// redirected to /login before this load ran.
	const { projects } = await api<{ projects: Project[] }>('/api/v1/projects', {
		session: locals.session,
		fetch
	});

	return { user: locals.user, projects };
};
