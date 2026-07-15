// Read the repo's CHANGELOG.md at build time (Vite ?raw) and parse it, so the
// public page always reflects the committed changelog — one source of truth, no
// duplicated content to keep in sync.
import changelogMarkdown from '../../../../CHANGELOG.md?raw';
import { parseChangelog } from '$lib/changelog';
import type { PageServerLoad } from './$types';

// Public page: no session required (see PUBLIC_PATHS in hooks.server.ts).
export const load: PageServerLoad = async () => {
	return { releases: parseChangelog(changelogMarkdown) };
};
