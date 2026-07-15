<script lang="ts">
	import { HugeiconsIcon, LightIcon, DarkIcon, SystemThemeIcon } from '$lib/icons';
	import { applyChoice, storedChoice, type ThemeChoice } from '$lib/theme';
	import { cx } from '$lib/ui';

	/**
	 * A three-way theme switch: system / light / dark. A segmented control rather
	 * than a single toggle, because "follow my OS" is a distinct choice from a
	 * fixed one and the user should be able to pick it explicitly.
	 */
	let choice = $state<ThemeChoice>('system');

	// Read the persisted choice on mount (client only — localStorage does not
	// exist during SSR).
	$effect(() => {
		choice = storedChoice();
	});

	const options: { key: ThemeChoice; label: string; icon: typeof LightIcon }[] = [
		{ key: 'system', label: 'System', icon: SystemThemeIcon },
		{ key: 'light', label: 'Light', icon: LightIcon },
		{ key: 'dark', label: 'Dark', icon: DarkIcon }
	];

	function pick(next: ThemeChoice) {
		choice = next;
		applyChoice(next);
	}
</script>

<div class="flex gap-0.5 rounded-full border border-border bg-surface p-0.5" role="group" aria-label="Theme">
	{#each options as opt (opt.key)}
		<button
			class={cx(
				'grid place-items-center rounded-full p-1.5 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-surface',
				choice === opt.key ? 'bg-surface-3 text-text' : 'text-text-faint hover:text-text'
			)}
			title={opt.label}
			aria-label={opt.label}
			aria-pressed={choice === opt.key}
			onclick={() => pick(opt.key)}
		>
			<HugeiconsIcon icon={opt.icon} size={14} />
		</button>
	{/each}
</div>
