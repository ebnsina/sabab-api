<script lang="ts">
	import type { Snippet } from 'svelte';
	import { cx } from './cx';

	/** A surface panel: rounded-xl, subtle border. The default container. */
	let {
		class: klass = '',
		children,
		...rest
	}: { class?: string; children: Snippet; [key: string]: unknown } = $props();
</script>

<div class={cx('rounded-xl border border-border bg-surface shadow-soft', klass)} {...rest}>
	{@render children()}
</div>
