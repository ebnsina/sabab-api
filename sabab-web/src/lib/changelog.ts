/**
 * A focused parser for OUR changelog format — not a general markdown engine.
 *
 * We own CHANGELOG.md's grammar (## version, ### section, "- " bullets, with
 * **bold**, `code` and [links] inline), so a ~40-line parser scoped to that is
 * the right call over pulling in a markdown dependency for one page.
 */

export interface ChangelogSection {
	title: string; // "Added", "Changed", "Fixed"
	items: string[]; // raw markdown of each bullet
}

export interface ChangelogRelease {
	version: string; // "Unreleased", "0.1.0 — Foundations…"
	sections: ChangelogSection[];
}

export function parseChangelog(markdown: string): ChangelogRelease[] {
	const releases: ChangelogRelease[] = [];
	let release: ChangelogRelease | null = null;
	let section: ChangelogSection | null = null;

	for (const raw of markdown.split('\n')) {
		const line = raw.trimEnd();

		if (line.startsWith('## ')) {
			// A new release. Strip the surrounding [] that Keep a Changelog uses.
			release = { version: cleanVersion(line.slice(3)), sections: [] };
			releases.push(release);
			section = null;
		} else if (line.startsWith('### ') && release) {
			section = { title: line.slice(4).trim(), items: [] };
			release.sections.push(section);
		} else if (line.startsWith('- ') && section) {
			section.items.push(line.slice(2).trim());
		} else if (line.startsWith('  ') && section && section.items.length > 0) {
			// A wrapped continuation of the previous bullet.
			section.items[section.items.length - 1] += ' ' + line.trim();
		}
	}

	return releases;
}

function cleanVersion(v: string): string {
	return v.replace(/^\[/, '').replace(/\]/, '').trim();
}

/**
 * Render the small inline subset — **bold**, `code`, [text](url) — to safe HTML.
 * Everything is HTML-escaped first, so no markdown construct can inject markup;
 * only our three patterns are then re-introduced as tags.
 */
export function renderInline(text: string): string {
	const escaped = text
		.replaceAll('&', '&amp;')
		.replaceAll('<', '&lt;')
		.replaceAll('>', '&gt;');

	return escaped
		.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
		.replace(/`(.+?)`/g, '<code>$1</code>')
		.replace(
			/\[(.+?)\]\((https?:\/\/[^\s)]+)\)/g,
			'<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>'
		);
}
