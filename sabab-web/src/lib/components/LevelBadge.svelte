<script lang="ts">
	/** Colour-codes a severity so it reads at a glance, no legend needed. */
	let { level }: { level: string } = $props();

	// The colour comes from the CSS custom property, which is the single source
	// of truth for the level→colour mapping (see app.css).
	const color = $derived(`var(--level-${level}, var(--level-debug))`);
</script>

<span class="badge" style:--c={color}>{level}</span>

<style>
	.badge {
		display: inline-flex;
		align-items: center;
		padding: 1px 8px;
		border-radius: 4px;
		font-size: 11px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.03em;
		color: var(--c);
		background: color-mix(in srgb, var(--c) 15%, transparent);
		border: 1px solid color-mix(in srgb, var(--c) 30%, transparent);
	}
</style>
