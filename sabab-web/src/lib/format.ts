/**
 * Formatting helpers, built on the browser's Intl API — no third-party library
 * for what the platform already does well and locale-correctly.
 */

const relative = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto', style: 'narrow' });

// Thresholds in seconds, largest unit first, so we pick the coarsest unit that
// still reads as a whole number ("3h" not "180m").
const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
	['year', 31_536_000],
	['month', 2_592_000],
	['week', 604_800],
	['day', 86_400],
	['hour', 3600],
	['minute', 60],
	['second', 1]
];

/** "3 min ago", "2 hr ago" — a compact age for an ops view. */
export function relativeTime(iso: string): string {
	const then = new Date(iso).getTime();
	if (Number.isNaN(then)) return '';

	const deltaSeconds = Math.round((then - Date.now()) / 1000);
	const abs = Math.abs(deltaSeconds);
	if (abs < 5) return 'just now';

	for (const [unit, secondsPerUnit] of UNITS) {
		if (abs >= secondsPerUnit) {
			return relative.format(Math.round(deltaSeconds / secondsPerUnit), unit);
		}
	}
	return 'just now';
}

const fullFormatter = new Intl.DateTimeFormat(undefined, {
	year: 'numeric',
	month: 'short',
	day: 'numeric',
	hour: '2-digit',
	minute: '2-digit',
	second: '2-digit'
});

/** A full, unambiguous timestamp for tooltips and detail headers. */
export function fullTime(iso: string): string {
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? iso : fullFormatter.format(d);
}

// A time-of-day formatter with milliseconds, for the log stream where sub-second
// order matters. `fractionalSecondDigits` is the Intl way to get millis.
const clockFormatter = new Intl.DateTimeFormat(undefined, {
	hour: '2-digit',
	minute: '2-digit',
	second: '2-digit',
	fractionalSecondDigits: 3,
	hour12: false
});

/** "14:03:22.481" — for a log line, where the day is context and the ms matter. */
export function clockTime(iso: string): string {
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? iso : clockFormatter.format(d);
}

const compactFormatter = new Intl.NumberFormat(undefined, {
	notation: 'compact',
	maximumFractionDigits: 1
});

/** "12.4K", "1.2M" — big counts made scannable, via Intl compact notation. */
export function compactNumber(n: number): string {
	return compactFormatter.format(n);
}

const plainFormatter = new Intl.NumberFormat(undefined);

/** "12,400" — a full number with locale-correct grouping. */
export function plainNumber(n: number): string {
	return plainFormatter.format(n);
}
