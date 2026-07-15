<script lang="ts">
	import { enhance } from '$app/forms';
	import { fade } from 'svelte/transition';
	import LevelBadge from '$lib/components/LevelBadge.svelte';
	import StackTrace from '$lib/components/StackTrace.svelte';
	import { fullTime, relativeTime, compactNumber } from '$lib/format';
	import { Button, Card } from '$lib/ui';
	import type { ExceptionValue, Breadcrumb } from '$lib/server/api';
	import {
		HugeiconsIcon,
		ResolveIcon,
		IgnoreIcon,
		ReopenIcon,
		UsersIcon,
		ReleaseIcon,
		ClockIcon,
		TagIcon,
		TraceIcon,
		DeviceIcon,
		LogsNavIcon
	} from '$lib/icons';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	// The stacktrace and breadcrumbs arrive as raw JSON strings from ClickHouse;
	// parse them once, defensively — a malformed blob must not blank the page.
	const exceptions = $derived(parseJson<ExceptionValue[]>(data.event?.stacktrace) ?? []);
	const breadcrumbs = $derived(parseJson<Breadcrumb[]>(data.event?.breadcrumbs) ?? []);
	const tags = $derived(data.event?.tags ?? {});

	const traceId = $derived(data.event?.trace_id);
	const hasTrace = $derived(!!traceId && !traceId.startsWith('00000000'));

	function parseJson<T>(raw: string | undefined): T | null {
		if (!raw) return null;
		try {
			return JSON.parse(raw) as T;
		} catch {
			return null;
		}
	}
</script>

<svelte:head><title>{data.issue.title} · Sabab</title></svelte:head>

<div class="max-w-[1100px] px-7 pt-6 pb-16" in:fade={{ duration: 150 }}>
	<Card class="mb-5 p-5">
		<header class="flex items-start justify-between gap-5">
		<div>
			<div class="flex items-center gap-2.5">
				<LevelBadge level={data.issue.level} />
				<h1 class="text-[19px] font-semibold leading-tight">{data.issue.title}</h1>
			</div>
			{#if data.issue.culprit}
				<p class="mt-2 font-mono text-[13px] text-text-dim">{data.issue.culprit}</p>
			{/if}
		</div>

		<!-- Status actions. Resolved shows "reopen"; the rest show resolve + ignore.
		     The current status is never a dead-end. -->
		<div class="flex shrink-0 gap-2">
			{#if data.issue.status === 'resolved'}
				<form method="POST" action="?/status" use:enhance>
					<input type="hidden" name="status" value="unresolved" />
					<Button type="submit"><HugeiconsIcon icon={ReopenIcon} size={14} /> Reopen</Button>
				</form>
			{:else}
				<form method="POST" action="?/status" use:enhance>
					<input type="hidden" name="status" value="resolved" />
					<input type="hidden" name="release" value={data.event?.release ?? ''} />
					<Button type="submit" variant="primary">
						<HugeiconsIcon icon={ResolveIcon} size={14} /> Resolve
					</Button>
				</form>
				<form method="POST" action="?/status" use:enhance>
					<input type="hidden" name="status" value="ignored" />
					<Button type="submit"><HugeiconsIcon icon={IgnoreIcon} size={14} /> Ignore</Button>
				</form>
			{/if}
		</div>
		</header>

		<div class="mt-4 flex flex-wrap gap-7 border-t border-border pt-4">
		<div class="flex flex-col gap-1">
			<span class="text-[11px] uppercase tracking-wide text-text-faint">Events</span>
			<span class="text-[15px] font-semibold tabular-nums">{compactNumber(data.issue.times_seen)}</span>
		</div>
		<div class="flex flex-col gap-1">
			<span class="flex items-center gap-1 text-[11px] uppercase tracking-wide text-text-faint">
				<HugeiconsIcon icon={UsersIcon} size={12} /> Users
			</span>
			<span class="text-[15px] font-semibold tabular-nums">
				{compactNumber(data.issue.users_affected)}
			</span>
		</div>
		<div class="flex flex-col gap-1">
			<span class="flex items-center gap-1 text-[11px] uppercase tracking-wide text-text-faint">
				<HugeiconsIcon icon={ClockIcon} size={12} /> First seen
			</span>
			<span class="text-[15px] font-semibold" title={fullTime(data.issue.first_seen)}>
				{relativeTime(data.issue.first_seen)}
			</span>
		</div>
		<div class="flex flex-col gap-1">
			<span class="flex items-center gap-1 text-[11px] uppercase tracking-wide text-text-faint">
				<HugeiconsIcon icon={ClockIcon} size={12} /> Last seen
			</span>
			<span class="text-[15px] font-semibold" title={fullTime(data.issue.last_seen)}>
				{relativeTime(data.issue.last_seen)}
			</span>
		</div>
		{#if data.issue.first_release}
			<div class="flex flex-col gap-1">
				<span class="flex items-center gap-1 text-[11px] uppercase tracking-wide text-text-faint">
					<HugeiconsIcon icon={ReleaseIcon} size={12} /> Release
				</span>
				<span class="font-mono text-[15px] font-semibold">{data.issue.first_release}</span>
			</div>
		{/if}
		</div>
	</Card>

	<div class="grid grid-cols-1 items-start gap-5 md:grid-cols-[1fr_280px]">
		<div>
			<Card class="mb-4 p-4.5">
				<h2 class="mb-3.5 text-xs uppercase tracking-wide text-text-faint">Stack trace</h2>
				{#if exceptions.length > 0}
					<StackTrace {exceptions} />
				{:else if data.eventError}
					<p class="text-text-dim">{data.eventError}</p>
				{:else}
					<p class="text-text-dim">No stack trace on this event.</p>
				{/if}
			</Card>

			{#if breadcrumbs.length > 0}
				<Card class="mb-4 p-4.5">
					<h2 class="mb-3.5 text-xs uppercase tracking-wide text-text-faint">Breadcrumbs</h2>
					<ol class="flex flex-col">
						{#each breadcrumbs as crumb, i (i)}
							<li
								class="grid grid-cols-[90px_1fr_auto] items-baseline gap-3 border-b border-border py-1.5 text-[12.5px] last:border-b-0"
							>
								<span class="text-[11px] text-text-faint">{crumb.category ?? 'default'}</span>
								<span>{crumb.message ?? ''}</span>
								<span class="text-[11px] text-text-faint">{relativeTime(crumb.ts)}</span>
							</li>
						{/each}
					</ol>
				</Card>
			{/if}
		</div>

		<aside>
			{#if data.event}
				<Card class="mb-4 p-4.5">
					<h2 class="mb-3.5 text-xs uppercase tracking-wide text-text-faint">Event</h2>
					<dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-2 text-[12.5px]">
						{#if data.event.environment}
							<dt class="text-text-faint">Environment</dt>
							<dd class="m-0 text-right">{data.event.environment}</dd>
						{/if}
						{#if data.event.user_id || data.event.user_email}
							<dt class="flex items-center gap-1 text-text-faint">
								<HugeiconsIcon icon={UsersIcon} size={12} /> User
							</dt>
							<dd class="m-0 text-right break-words">
								{data.event.user_email || data.event.user_id}
							</dd>
						{/if}
						{#if data.event.browser}
							<dt class="flex items-center gap-1 text-text-faint">
								<HugeiconsIcon icon={DeviceIcon} size={12} /> Browser
							</dt>
							<dd class="m-0 text-right">{data.event.browser}</dd>
						{/if}
						{#if data.event.os}
							<dt class="flex items-center gap-1 text-text-faint">
								<HugeiconsIcon icon={DeviceIcon} size={12} /> OS
							</dt>
							<dd class="m-0 text-right">{data.event.os}</dd>
						{/if}
						{#if hasTrace}
							<dt class="flex items-center gap-1 text-text-faint">
								<HugeiconsIcon icon={TraceIcon} size={12} /> Trace
							</dt>
							<dd class="m-0 text-right font-mono text-[10.5px]">{traceId}</dd>
						{/if}
					</dl>

					{#if hasTrace}
						<!-- Surfaced correlation: jump to the logs emitted inside
						     this error's trace. -->
						<a
							class="mt-3.5 flex items-center gap-1.5 rounded-xl border border-border px-3 py-2 text-[12.5px] text-text-dim transition-colors hover:border-border-strong hover:text-text"
							href="/projects/{data.issue.project_id}/logs?q=trace:{traceId}"
						>
							<HugeiconsIcon icon={LogsNavIcon} size={13} /> View logs in this trace
						</a>
					{/if}
				</Card>
			{/if}

			{#if Object.keys(tags).length > 0}
				<Card class="mb-4 p-4.5">
					<h2 class="mb-3.5 flex items-center gap-1.5 text-xs uppercase tracking-wide text-text-faint">
						<HugeiconsIcon icon={TagIcon} size={13} /> Tags
					</h2>
					<div class="flex flex-col gap-1.5">
						{#each Object.entries(tags) as [key, value] (key)}
							<div class="flex justify-between gap-2.5 text-[12.5px]">
								<span class="text-text-faint">{key}</span>
								<span class="font-mono">{value}</span>
							</div>
						{/each}
					</div>
				</Card>
			{/if}
		</aside>
	</div>
</div>
