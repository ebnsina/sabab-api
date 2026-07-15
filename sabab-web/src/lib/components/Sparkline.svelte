<script lang="ts">
	/**
	 * The little per-hour chart in the issue list. It answers one question at a
	 * glance — is this spiking right now, or trailing off? — which is usually all
	 * you want to know from a list of a hundred issues.
	 */
	let {
		data = [],
		width = 96,
		height = 26
	}: { data?: number[]; width?: number; height?: number } = $props();

	const max = $derived(Math.max(1, ...data));

	// Bars, not a line: with sparse hourly buckets a line implies a continuity
	// the data does not have, and a single tall bar is what "spiking" looks like.
	const bars = $derived(
		data.map((v, i) => {
			const barW = width / Math.max(data.length, 1);
			const h = Math.max(1, (v / max) * height);
			return { x: i * barW, y: height - h, w: Math.max(1, barW - 1), h };
		})
	);
</script>

{#if data.length > 0}
	<svg {width} {height} viewBox="0 0 {width} {height}" role="img" aria-label="Events over time">
		{#each bars as bar (bar.x)}
			<rect x={bar.x} y={bar.y} width={bar.w} height={bar.h} rx="1" fill="var(--accent-dim)" />
		{/each}
	</svg>
{:else}
	<span class="faint" style="font-size:11px">—</span>
{/if}
