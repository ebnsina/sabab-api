<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { fade } from 'svelte/transition';
	import LevelBadge from '$lib/components/LevelBadge.svelte';
	import Sparkline from '$lib/components/Sparkline.svelte';
	import { relativeTime, compactNumber } from '$lib/format';
	import { Card, cx } from '$lib/ui';
	import {
		HugeiconsIcon,
		SearchIcon,
		UsersIcon,
		ReleaseIcon,
		ResolveIcon,
		UnresolvedIcon,
		IgnoreIcon
	} from '$lib/icons';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	let queryInput = $state('');
	$effect(() => {
		queryInput = data.q;
	});

	const statuses = [
		{ key: 'unresolved', label: 'Unresolved', icon: UnresolvedIcon },
		{ key: 'resolved', label: 'Resolved', icon: ResolveIcon },
		{ key: 'ignored', label: 'Ignored', icon: IgnoreIcon }
	];
	const sorts = [
		{ key: 'last_seen', label: 'Last seen' },
		{ key: 'first_seen', label: 'First seen' },
		{ key: 'times_seen', label: 'Events' },
		{ key: 'users', label: 'Users' }
	];

	function navigate(patch: Record<string, string>) {
		const params = new URLSearchParams(page.url.searchParams);
		for (const [k, v] of Object.entries(patch)) {
			if (v) params.set(k, v);
			else params.delete(k);
		}
		goto(`?${params}`, { keepFocus: true });
	}

	function submitSearch(e: SubmitEvent) {
		e.preventDefault();
		navigate({ q: queryInput });
	}
</script>

<svelte:head>
	<title>{data.project?.name ?? 'Issues'} · Sabab</title>
</svelte:head>

<div class="max-w-[1100px] px-7 py-6">
	<header class="mb-4.5">
		<form
			class="flex flex-1 items-center gap-2 rounded-xl border border-border bg-surface px-3.5 py-2.5 transition-[border-color,box-shadow] duration-150 focus-within:border-border-strong focus-within:ring-2 focus-within:ring-accent focus-within:ring-offset-2 focus-within:ring-offset-bg"
			onsubmit={submitSearch}
		>
			<HugeiconsIcon icon={SearchIcon} size={15} color="var(--color-text-faint)" />
			<input
				class="flex-1 bg-transparent font-mono text-[12.5px] text-text outline-none placeholder:text-text-faint"
				bind:value={queryInput}
				placeholder="level:error release:web@2.4.1 !browser:Safari"
				spellcheck="false"
				autocapitalize="off"
			/>
		</form>
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

	<div class="mb-3.5 flex items-center justify-between">
		<div class="flex gap-0.5 rounded-full border border-border bg-surface p-0.5">
			{#each statuses as s (s.key)}
				<button
					class={cx(
						'flex items-center gap-1.5 rounded-full px-3 py-1.5 text-[12.5px] font-medium transition-colors',
						data.status === s.key ? 'bg-surface-3 text-text' : 'text-text-dim hover:text-text'
					)}
					onclick={() => navigate({ status: s.key })}
				>
					<HugeiconsIcon icon={s.icon} size={13} />
					{s.label}
				</button>
			{/each}
		</div>

		<div class="flex items-center gap-1 text-xs">
			<span class="text-text-faint">Sort</span>
			{#each sorts as s (s.key)}
				<button
					class={cx(
						'rounded-full px-2.5 py-1.5 text-[12.5px]',
						data.sort === s.key ? 'font-semibold text-accent' : 'text-text-dim hover:text-text'
					)}
					onclick={() => navigate({ sort: s.key })}
				>
					{s.label}
				</button>
			{/each}
		</div>
	</div>

	<Card class="overflow-hidden">
		<div
			class="grid grid-cols-[1fr_110px_70px_70px_54px] items-center gap-3 border-b border-border px-4 py-2.5 text-[11px] uppercase tracking-wide text-text-faint"
		>
			<span>Issue</span>
			<span>Last 14d</span>
			<span class="text-right">Events</span>
			<span class="text-right">Users</span>
			<span class="text-right">Age</span>
		</div>

		{#each data.issues as issue (issue.id)}
			<a
				class="grid grid-cols-[1fr_110px_70px_70px_54px] items-center gap-3 border-b border-border px-4 py-2.5 last:border-b-0 hover:bg-surface-2"
				href="/issues/{issue.id}"
			>
				<div class="min-w-0">
					<div class="flex items-center gap-2">
						<LevelBadge level={issue.level} />
						<span class="truncate text-[13.5px] font-medium">{issue.title}</span>
					</div>
					<div class="mt-1 flex items-center gap-3 text-xs">
						{#if issue.culprit}
							<span class="font-mono text-[11.5px] text-text-dim">{issue.culprit}</span>
						{/if}
						{#if issue.first_release}
							<span class="inline-flex items-center gap-1 text-text-faint">
								<HugeiconsIcon icon={ReleaseIcon} size={11} />{issue.first_release}
							</span>
						{/if}
					</div>
				</div>
				<Sparkline data={issue.sparkline ?? []} />
				<div class="text-right text-[13px] tabular-nums">{compactNumber(issue.times_seen)}</div>
				<div class="text-right text-[13px] tabular-nums">
					{#if issue.users_affected > 0}
						<span class="inline-flex items-center gap-1 text-text-dim">
							<HugeiconsIcon icon={UsersIcon} size={12} />{compactNumber(issue.users_affected)}
						</span>
					{:else}
						<span class="text-text-faint">—</span>
					{/if}
				</div>
				<div class="text-right text-xs tabular-nums text-text-faint" title={issue.last_seen}>
					{relativeTime(issue.last_seen)}
				</div>
			</a>
		{:else}
			<div class="px-6 py-16 text-center text-text-dim">
				No {data.status} issues{data.q ? ' match this search' : ''}.
			</div>
		{/each}
	</Card>
</div>
