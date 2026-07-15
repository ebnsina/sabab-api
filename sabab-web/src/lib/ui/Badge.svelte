<script lang="ts">
	import type { Snippet } from 'svelte';
	import { cx } from './cx';

	/**
	 * A small status pill. When `level` is set it colours itself from the
	 * severity palette (the single source of the level→colour mapping); otherwise
	 * it is a neutral chip.
	 */
	let {
		level = undefined,
		class: klass = '',
		children
	}: { level?: string; class?: string; children: Snippet } = $props();

	// Map a level to its palette colour via a CSS var, so components never hard-
	// code a hex. Unknown levels fall back to debug/grey.
	const color = $derived(level ? `var(--color-${level}, var(--color-debug))` : undefined);
</script>

<span
	class={cx(
		'inline-flex items-center rounded px-2 py-px text-[11px] font-semibold uppercase tracking-wide',
		!level && 'bg-surface-3 text-text-dim',
		klass
	)}
	style={color
		? `color:${color};background:color-mix(in srgb, ${color} 15%, transparent);border:1px solid color-mix(in srgb, ${color} 30%, transparent)`
		: ''}
>
	{@render children()}
</span>
