<script lang="ts">
	import type { IconSvgElement } from '@hugeicons/svelte';
	import { HugeiconsIcon } from '$lib/icons';
	import { cx } from '$lib/ui';

	/**
	 * A sidebar navigation item. Active state is a clean, quiet surface fill with
	 * a touch of weight — no accent bar, no colour wash. The current location
	 * reads clearly without shouting, which is what keeps a dense sidebar calm.
	 */
	let {
		href,
		icon,
		label,
		active = false,
		indent = false
	}: {
		href: string;
		icon: IconSvgElement;
		label: string;
		active?: boolean;
		indent?: boolean;
	} = $props();
</script>

<a
	{href}
	class={cx(
		'flex items-center gap-2.5 rounded-lg py-2 pr-2.5 text-[13px] transition-colors',
		indent ? 'pl-8' : 'pl-2.5',
		active
			? 'bg-surface-3 font-medium text-text'
			: 'text-text-dim hover:bg-surface-2 hover:text-text'
	)}
	aria-current={active ? 'page' : undefined}
>
	<HugeiconsIcon {icon} size={16} color="currentColor" />
	<span class="flex-1 truncate">{label}</span>
</a>
