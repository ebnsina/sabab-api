/**
 * Theme: follows the system setting by default, with a manual override that
 * persists. Three states — 'system', 'light', 'dark' — because "match my OS" is
 * a real preference distinct from a fixed choice.
 *
 * The actual palette lives in CSS (app.css, keyed on prefers-color-scheme and
 * [data-theme]); this only decides which data-theme attribute to stamp on
 * <html>. 'system' clears the attribute so the CSS media query takes over.
 */
import { browser } from '$app/environment';

export type ThemeChoice = 'system' | 'light' | 'dark';

const KEY = 'sabab-theme';

/** The persisted choice, or 'system' when none. */
export function storedChoice(): ThemeChoice {
	if (!browser) return 'system';
	const v = localStorage.getItem(KEY);
	return v === 'light' || v === 'dark' ? v : 'system';
}

/** Apply a choice: persist it and stamp (or clear) data-theme on <html>. */
export function applyChoice(choice: ThemeChoice): void {
	if (!browser) return;
	if (choice === 'system') {
		localStorage.removeItem(KEY);
		document.documentElement.removeAttribute('data-theme');
	} else {
		localStorage.setItem(KEY, choice);
		document.documentElement.setAttribute('data-theme', choice);
	}
}

/** The theme actually showing right now, after resolving 'system'. */
export function resolvedTheme(choice: ThemeChoice): 'light' | 'dark' {
	if (choice !== 'system') return choice;
	if (!browser) return 'light';
	return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}
