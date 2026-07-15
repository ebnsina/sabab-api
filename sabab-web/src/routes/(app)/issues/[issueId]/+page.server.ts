import { error, fail } from '@sveltejs/kit';
import {
	api,
	ApiRequestError,
	type Activity,
	type Issue,
	type SabEvent
} from '$lib/server/api';
import type { Actions, PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ params, locals, fetch }) => {
	const issueId = params.issueId;

	try {
		const { issue, activity } = await api<{ issue: Issue; activity: Activity[] }>(
			`/api/v1/issues/${issueId}`,
			{ session: locals.session, fetch }
		);

		// The latest event is fetched separately: it can legitimately be absent
		// (the issue outlived its events' 90-day TTL), and that must not fail the
		// whole page.
		let event: SabEvent | null = null;
		let eventError: string | null = null;
		try {
			const res = await api<{ event: SabEvent }>(`/api/v1/issues/${issueId}/latest-event`, {
				session: locals.session,
				fetch
			});
			event = res.event;
		} catch (err) {
			eventError =
				err instanceof ApiRequestError
					? err.info.message
					: 'Could not load the latest event.';
		}

		return { issue, activity, event, eventError };
	} catch (err) {
		if (err instanceof ApiRequestError) {
			throw error(err.info.status, err.info.message);
		}
		throw err;
	}
};

export const actions: Actions = {
	// Resolve / ignore / reopen. One action, because they are the same operation
	// with a different target status.
	status: async ({ request, params, locals, fetch }) => {
		const form = await request.formData();
		const status = String(form.get('status') ?? '');
		const release = String(form.get('release') ?? '');

		try {
			await api(`/api/v1/issues/${params.issueId}/status`, {
				method: 'POST',
				body: { status, release },
				session: locals.session,
				fetch
			});
			return { ok: true };
		} catch (err) {
			if (err instanceof ApiRequestError) {
				return fail(err.info.status, { error: err.info.message });
			}
			throw err;
		}
	},

	assign: async ({ request, params, locals, fetch }) => {
		const form = await request.formData();
		const raw = form.get('assignee_id');
		// Empty string means "unassign", which the API expects as null.
		const assignee_id = raw ? Number(raw) : null;

		try {
			await api(`/api/v1/issues/${params.issueId}/assign`, {
				method: 'POST',
				body: { assignee_id },
				session: locals.session,
				fetch
			});
			return { ok: true };
		} catch (err) {
			if (err instanceof ApiRequestError) {
				return fail(err.info.status, { error: err.info.message });
			}
			throw err;
		}
	}
};
