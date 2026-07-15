import { redirect } from '@sveltejs/kit';
import { api, type Project } from '$lib/server/api';
import type { PageServerLoad } from './$types';

// The root has no page of its own — it forwards to the first project's issue
// stream, which is the thing a user actually came to see.
export const load: PageServerLoad = async ({ locals, fetch }) => {
	const { projects } = await api<{ projects: Project[] }>('/api/v1/projects', {
		session: locals.session,
		fetch
	});
	if (projects.length > 0) {
		throw redirect(303, `/projects/${projects[0].id}/issues`);
	}
	return { empty: true };
};
