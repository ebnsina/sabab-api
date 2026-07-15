<script lang="ts">
	import { cx } from './cx';

	/**
	 * A text input. Rounded-xl, with the app's one focus treatment. Bindable
	 * value so callers use it like a native input.
	 */
	let {
		value = $bindable(''),
		type = 'text',
		placeholder = '',
		class: klass = '',
		...rest
	}: {
		value?: string;
		type?: string;
		placeholder?: string;
		class?: string;
		[key: string]: unknown;
	} = $props();

	const cls = $derived(
		cx(
			'w-full rounded-xl border border-border bg-surface px-3.5 py-2.5 text-sm text-text',
			'placeholder:text-text-faint transition-[border-color,box-shadow] duration-150',
			'focus:outline-none focus:border-border-strong focus:ring-2 focus:ring-accent',
			'focus:ring-offset-2 focus:ring-offset-bg',
			klass
		)
	);
</script>

<input {type} {placeholder} bind:value class={cls} {...rest} />
