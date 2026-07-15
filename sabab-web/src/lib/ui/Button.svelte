<script lang="ts">
	import type { Snippet } from 'svelte';
	import { cx } from './cx';

	/**
	 * The one button. Pill-shaped, three variants, one focus treatment — so every
	 * button in the app is consistent by construction rather than by everyone
	 * remembering the same classes.
	 */
	type Variant = 'primary' | 'default' | 'ghost' | 'danger';
	type Size = 'sm' | 'md';

	let {
		variant = 'default',
		size = 'md',
		type = 'button',
		href = undefined,
		disabled = false,
		class: klass = '',
		children,
		...rest
	}: {
		variant?: Variant;
		size?: Size;
		type?: 'button' | 'submit';
		href?: string;
		disabled?: boolean;
		class?: string;
		children: Snippet;
		[key: string]: unknown;
	} = $props();

	const base =
		'inline-flex items-center justify-center gap-1.5 rounded-full font-medium ' +
		'transition-[background-color,border-color,box-shadow,color] duration-150 ' +
		'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent ' +
		'focus-visible:ring-offset-2 focus-visible:ring-offset-bg ' +
		'disabled:opacity-50 disabled:pointer-events-none border';

	const sizes: Record<Size, string> = {
		sm: 'px-3 py-1.5 text-xs',
		md: 'px-4 py-2 text-[13px]'
	};

	const variants: Record<Variant, string> = {
		primary: 'bg-accent border-accent text-on-accent hover:bg-accent-hover hover:border-accent-hover font-semibold',
		default: 'bg-surface-2 border-border-strong text-text hover:bg-surface-3 hover:border-text-faint',
		ghost: 'bg-transparent border-transparent text-text-dim hover:bg-surface-2 hover:text-text',
		danger: 'bg-transparent border-transparent text-danger hover:bg-danger/10'
	};

	const cls = $derived(cx(base, sizes[size], variants[variant], klass));
</script>

{#if href}
	<a {href} class={cls} {...rest}>{@render children()}</a>
{:else}
	<button {type} {disabled} class={cls} {...rest}>{@render children()}</button>
{/if}
