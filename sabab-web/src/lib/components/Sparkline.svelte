<script lang="ts">
	/**
	 * The per-hour "events over time" chart in the issue list. It answers one
	 * question at a glance — is this spiking now, or trailing off?
	 *
	 * The series is normalised to a fixed number of buckets, right-aligned (recent
	 * on the right), so sparse data reads correctly: one active bucket shows as a
	 * single spike above a flat baseline, NOT a solid block filling the width. A
	 * faint baseline track keeps it legible even when a group has one data point.
	 */
	let {
		data = [],
		width = 96,
		height = 26,
		buckets = 24
	}: { data?: number[]; width?: number; height?: number; buckets?: number } = $props();

	// Right-align into a fixed bucket count: pad with leading zeros so a lone
	// recent spike sits at the right rather than stretching across everything.
	const series = $derived(
		data.length >= buckets
			? data.slice(-buckets)
			: [...Array(buckets - data.length).fill(0), ...data]
	);

	const max = $derived(Math.max(1, ...series));
	const slot = $derived(width / buckets);

	const bars = $derived(
		series.map((v, i) => {
			const h = v > 0 ? Math.max(2, (v / max) * height) : 0;
			return { x: i * slot, y: height - h, w: Math.max(1, slot - 1), h, empty: v === 0 };
		})
	);
</script>

{#if data.length > 0}
	<svg {width} {height} viewBox="0 0 {width} {height}" role="img" aria-label="Events over time">
		<!-- Baseline track, so the chart reads as a chart even with one bar. -->
		<line
			x1="0"
			y1={height - 0.5}
			x2={width}
			y2={height - 0.5}
			stroke="var(--color-border)"
			stroke-width="1"
		/>
		{#each bars as bar, i (i)}
			{#if !bar.empty}
				<rect x={bar.x} y={bar.y} width={bar.w} height={bar.h} rx="1" fill="var(--color-accent)" />
			{/if}
		{/each}
	</svg>
{:else}
	<span class="text-[11px] text-text-faint">—</span>
{/if}
