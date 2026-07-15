import { error } from '@sveltejs/kit';
import { api, ApiRequestError, type LogEntry, type Project } from '$lib/server/api';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ params, url, locals, fetch }) => {
	const projectId = params.projectId;
	const q = url.searchParams.get('q') ?? '';

	const qs = new URLSearchParams({ limit: '100' });
	if (q) qs.set('q', q);

	try {
		const { logs } = await api<{ logs: LogEntry[] }>(
			`/api/v1/projects/${projectId}/logs?${qs}`,
			{ session: locals.session, fetch }
		);
		const { projects } = await api<{ projects: Project[] }>('/api/v1/projects', {
			session: locals.session,
			fetch
		});
		const project = projects.find((p) => String(p.id) === projectId);

		return { logs, project, q, queryError: null };
	} catch (err) {
		if (err instanceof ApiRequestError) {
			// A bad query is the user's typo — keep the page usable and show it
			// inline rather than throwing to a full error page.
			if (err.info.code === 'bad_query') {
				return { logs: [] as LogEntry[], project: undefined, q, queryError: err.info.message };
			}
			throw error(err.info.status, err.info.message);
		}
		throw err;
	}
};
