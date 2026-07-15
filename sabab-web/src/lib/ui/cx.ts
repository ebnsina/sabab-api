/**
 * Join class names, dropping falsey ones. A dozen lines instead of pulling in
 * clsx — the platform of the language is enough for this.
 */
export function cx(...parts: (string | false | null | undefined)[]): string {
	return parts.filter(Boolean).join(' ');
}
