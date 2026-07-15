<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { fade, slide } from 'svelte/transition';
	import { clockTime } from '$lib/format';
	import { HugeiconsIcon, SearchIcon, PlayIcon, StopIcon } from '$lib/icons';
	import { Button, Card, cx } from '$lib/ui';
	import type { LogEntry } from '$lib/server/api';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	let queryInput = $state('');
	$effect(() => {
		queryInput = data.q;
	});

	// The loaded page, plus anything the live tail has appended. Separate state so
	// toggling the tail off does not lose the backlog.
	let live = $state(false);
	let tailed = $state<LogEntry[]>([]);
	// The expanded row — progressive disclosure keeps the stream scannable.
	let expanded = $state<number | null>(null);

	const rows = $derived(live ? [...data.logs, ...tailed] : data.logs);

	function submitSearch(e: SubmitEvent) {
		e.preventDefault();
		const params = new URLSearchParams(page.url.searchParams);
		if (queryInput) params.set('q', queryInput);
		else params.delete('q');
		goto(`?${params}`, { keepFocus: true });
	}

	// Live tail over the browser's native EventSource — no third-party client.
	// The $effect owns the connection: it opens when `live` turns on, and the
	// returned teardown closes it, so we can never leak a stream.
	$effect(() => {
		if (!live) return;

		const params = new URLSearchParams();
		if (data.q) params.set('q', data.q);
		const source = new EventSource(`/api/v1/projects/${page.params.projectId}/logs/tail?${params}`);
		source.onmessage = (ev) => {
			try {
				const entry = JSON.parse(ev.data) as LogEntry;
				// Cap what we hold: a busy tail must not grow unbounded and freeze
				// the tab — the point is to watch what's live.
				tailed = [...tailed, entry].slice(-500);
			} catch {
				/* a malformed frame must not break the tail */
			}
		};
		return () => source.close();
	});

	function toggleLive() {
		live = !live;
		if (!live) tailed = [];
	}
</script>

<svelte:head><title>Logs · {data.project?.name ?? 'Sabab'}</title></svelte:head>

<div class="max-w-[1100px] px-7 py-6">
	<header class="mb-4.5 flex items-center gap-3.5">
		<form
			class="flex flex-1 items-center gap-2 rounded-xl border border-border bg-surface px-3.5 py-2.5 transition-[border-color,box-shadow] duration-150 focus-within:border-border-strong focus-within:ring-2 focus-within:ring-accent focus-within:ring-offset-2 focus-within:ring-offset-bg"
			onsubmit={submitSearch}
		>
			<HugeiconsIcon icon={SearchIcon} size={15} color="var(--color-text-faint)" />
			<input
				class="flex-1 bg-transparent font-mono text-[12.5px] text-text outline-none placeholder:text-text-faint"
				bind:value={queryInput}
				placeholder="severity:>=warn service:checkout timeout"
				spellcheck="false"
				autocapitalize="off"
			/>
		</form>
		<Button onclick={toggleLive} class={live ? 'border-ok text-ok' : ''}>
			<HugeiconsIcon icon={live ? StopIcon : PlayIcon} size={14} />
			{live ? 'Stop' : 'Live tail'}
		</Button>
	</header>

	{#if data.queryError}
		<p
			transition:fade={{ duration: 150 }}
			class="mb-3.5 rounded-xl border border-danger/30 bg-danger/10 px-3 py-2 font-mono text-[12.5px] text-danger"
			role="alert"
		>
			{data.queryError}
		</p>
	{/if}

	<Card class="overflow-hidden">
		{#each rows as row, i (i)}
			<div
				style:--sev="var(--color-{row.severity}, var(--color-debug))"
				class="border-b border-border last:border-b-0"
			>
				<button
					class="grid w-full grid-cols-[8px_86px_46px_auto_1fr] items-center gap-3 px-3.5 py-1.5 text-left hover:bg-surface-2"
					onclick={() => (expanded = expanded === i ? null : i)}
				>
					<!-- A small severity dot: enough to scan the column by colour,
					     without a heavy full-height border fighting the card edge. -->
					<span class="h-1.5 w-1.5 rounded-full [background:var(--sev)]"></span>
					<time class="font-mono text-[11.5px] text-text-faint" title={row.timestamp}>
						{clockTime(row.timestamp)}
					</time>
					<span class="text-[10.5px] font-bold uppercase tracking-wide [color:var(--sev)]">
						{row.severity}
					</span>
					{#if row.service}
						<span class="truncate text-[11.5px] whitespace-nowrap text-text-dim">{row.service}</span>
					{:else}
						<span></span>
					{/if}
					<span class="truncate font-mono text-[12.5px]">{row.body}</span>
				</button>

				{#if expanded === i}
					<!-- Detail only exists once asked for, so the default view is one
					     glanceable line per log. -->
					<div transition:slide={{ duration: 150 }} class="flex flex-col gap-2 bg-surface-2 px-3.5 pt-2 pb-3 pl-6.5">
						{#if row.trace_id && !row.trace_id.startsWith('00000000')}
							<a
								class="w-fit font-mono text-[11.5px] text-info hover:underline"
								href="/projects/{page.params.projectId}/logs?q=trace:{row.trace_id}"
							>
								trace: {row.trace_id}
							</a>
						{/if}
						{#if row.attributes && Object.keys(row.attributes).length > 0}
							<dl class="grid grid-cols-[auto_1fr] gap-x-3.5 gap-y-0.5 text-xs">
								{#each Object.entries(row.attributes) as [k, v] (k)}
									<dt class="text-text-faint">{k}</dt>
									<dd class="m-0 font-mono">{v}</dd>
								{/each}
							</dl>
						{/if}
						{#if row.template && row.template !== row.body}
							<div class="font-mono text-[11.5px] text-text-faint" title="The pre-interpolation form">
								{row.template}
							</div>
						{/if}
					</div>
				{/if}
			</div>
		{:else}
			<div class="px-6 py-16 text-center text-text-dim">
				No logs{data.q ? ' match this search' : ' yet'}.
			</div>
		{/each}

		{#if live}
			<div class="flex items-center gap-2 px-3.5 py-2.5 text-xs text-text-dim">
				<span class="h-1.5 w-1.5 animate-pulse rounded-full bg-ok"></span> Listening for new logs…
			</div>
		{/if}
	</Card>
</div>
