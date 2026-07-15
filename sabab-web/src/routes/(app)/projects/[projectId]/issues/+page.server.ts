import { error } from '@sveltejs/kit';
import { api, ApiRequestError, type Issue, type Project } from '$lib/server/api';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ params, url, locals, fetch }) => {
	const projectId = params.projectId;

	// The stream is driven entirely by the URL, so a filtered view is a shareable
	// link — copy it to a colleague and they see exactly what you see.
	const status = url.searchParams.get('status') ?? 'unresolved';
	const sort = url.searchParams.get('sort') ?? 'last_seen';
	const q = url.searchParams.get('q') ?? '';

	const qs = new URLSearchParams({ status, sort });
	if (q) qs.set('q', q);

	try {
		const { issues } = await api<{ issues: Issue[] }>(
			`/api/v1/projects/${projectId}/issues?${qs}`,
			{ session: locals.session, fetch }
		);

		// The project name for the header. The layout already loaded the list, but
		// a load function should not reach into the layout's data, so this is a
		// cheap dedicated fetch.
		const { projects } = await api<{ projects: Project[] }>('/api/v1/projects', {
			session: locals.session,
			fetch
		});
		const project = projects.find((p) => String(p.id) === projectId);

		return { issues, project, status, sort, q, queryError: null };
	} catch (err) {
		if (err instanceof ApiRequestError) {
			// A bad search query is the user's typo, not a page failure. Return it
			// so the search box can show the message inline and keep the rest of
			// the page usable, rather than throwing to a full error page.
			if (err.info.code === 'bad_query') {
				return {
					issues: [] as Issue[],
					project: undefined,
					status,
					sort,
					q,
					queryError: err.info.message
				};
			}
			throw error(err.info.status, err.info.message);
		}
		throw err;
	}
};
