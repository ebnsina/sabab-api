<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import LevelBadge from '$lib/components/LevelBadge.svelte';
	import Sparkline from '$lib/components/Sparkline.svelte';
	import { relativeTime, compactNumber } from '$lib/time';
	import { Search, Users, Hash, CheckCircle2, Bell, EyeOff } from '@lucide/svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	// Local mirror of the search box so typing is smooth; navigation happens on
	// submit, which is what makes each filtered view a shareable URL. Re-synced
	// from the URL whenever a navigation lands (back button, cleared filter).
	let queryInput = $state('');
	$effect(() => {
		queryInput = data.q;
	});

	const statuses = [
		{ key: 'unresolved', label: 'Unresolved', icon: Bell },
		{ key: 'resolved', label: 'Resolved', icon: CheckCircle2 },
		{ key: 'ignored', label: 'Ignored', icon: EyeOff }
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

<div class="page">
	<header class="head">
		<h1>{data.project?.name ?? 'Issues'}</h1>

		<form class="search" onsubmit={submitSearch}>
			<Search size={15} color="var(--text-faint)" />
			<input
				class="search-input"
				bind:value={queryInput}
				placeholder="level:error release:web@2.4.1 !browser:Safari"
				spellcheck="false"
				autocapitalize="off"
			/>
		</form>
	</header>

	{#if data.queryError}
		<p class="query-error" role="alert">{data.queryError}</p>
	{/if}

	<div class="toolbar">
		<div class="segmented">
			{#each statuses as s (s.key)}
				<button
					class="seg"
					class:active={data.status === s.key}
					onclick={() => navigate({ status: s.key })}
				>
					<s.icon size={13} />
					{s.label}
				</button>
			{/each}
		</div>

		<div class="sort">
			<span class="faint">Sort</span>
			{#each sorts as s (s.key)}
				<button
					class="sort-btn"
					class:active={data.sort === s.key}
					onclick={() => navigate({ sort: s.key })}
				>
					{s.label}
				</button>
			{/each}
		</div>
	</div>

	<div class="issues card">
		<div class="list-head">
			<span class="col-issue">Issue</span>
			<span class="col-chart">Last 14d</span>
			<span class="col-num">Events</span>
			<span class="col-num">Users</span>
			<span class="col-age">Age</span>
		</div>

		{#each data.issues as issue (issue.id)}
			<a class="issue" href="/issues/{issue.id}">
				<div class="col-issue">
					<div class="issue-title-row">
						<LevelBadge level={issue.level} />
						<span class="issue-title">{issue.title}</span>
					</div>
					<div class="issue-meta">
						{#if issue.culprit}<span class="mono culprit">{issue.culprit}</span>{/if}
						{#if issue.first_release}
							<span class="faint"><Hash size={11} />{issue.first_release}</span>
						{/if}
					</div>
				</div>
				<div class="col-chart">
					<Sparkline data={issue.sparkline ?? []} />
				</div>
				<div class="col-num">{compactNumber(issue.times_seen)}</div>
				<div class="col-num">
					{#if issue.users_affected > 0}
						<span class="users"><Users size={12} />{compactNumber(issue.users_affected)}</span>
					{:else}
						<span class="faint">—</span>
					{/if}
				</div>
				<div class="col-age faint" title={issue.last_seen}>{relativeTime(issue.last_seen)}</div>
			</a>
		{:else}
			<div class="empty">
				<p class="muted">No {data.status} issues{data.q ? ' match this search' : ''}.</p>
			</div>
		{/each}
	</div>
</div>

<style>
	.page {
		padding: 24px 28px;
		max-width: 1100px;
	}
	.head {
		display: flex;
		align-items: center;
		gap: 20px;
		margin-bottom: 18px;
	}
	.head h1 {
		font-size: 20px;
		white-space: nowrap;
	}
	.search {
		flex: 1;
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 8px 12px;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
	}
	.search:focus-within {
		border-color: var(--accent-dim);
	}
	.search-input {
		flex: 1;
		background: none;
		border: none;
		outline: none;
		color: var(--text);
		font-family: var(--mono);
		font-size: 12.5px;
	}
	.search-input::placeholder {
		color: var(--text-faint);
	}
	.query-error {
		margin: 0 0 14px;
		padding: 8px 12px;
		border-radius: var(--radius-sm);
		background: color-mix(in srgb, var(--danger) 12%, transparent);
		border: 1px solid color-mix(in srgb, var(--danger) 28%, transparent);
		color: var(--danger);
		font-family: var(--mono);
		font-size: 12.5px;
	}
	.toolbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 14px;
	}
	.segmented {
		display: flex;
		gap: 2px;
		padding: 2px;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
	}
	.seg {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 6px 12px;
		background: transparent;
		border: none;
		border-radius: 4px;
		color: var(--text-dim);
		font-size: 12.5px;
		font-weight: 500;
	}
	.seg:hover {
		color: var(--text);
	}
	.seg.active {
		background: var(--surface-3);
		color: var(--text);
	}
	.sort {
		display: flex;
		align-items: center;
		gap: 4px;
		font-size: 12px;
	}
	.sort-btn {
		padding: 5px 9px;
		background: transparent;
		border: none;
		border-radius: 4px;
		color: var(--text-dim);
		font-size: 12.5px;
	}
	.sort-btn:hover {
		color: var(--text);
	}
	.sort-btn.active {
		color: var(--accent);
		font-weight: 600;
	}
	.issues {
		overflow: hidden;
	}
	.list-head,
	.issue {
		display: grid;
		grid-template-columns: 1fr 110px 70px 70px 54px;
		align-items: center;
		gap: 12px;
		padding: 11px 16px;
	}
	.list-head {
		font-size: 11px;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--text-faint);
		border-bottom: 1px solid var(--border);
	}
	.issue {
		border-bottom: 1px solid var(--border);
	}
	.issue:last-child {
		border-bottom: none;
	}
	.issue:hover {
		background: var(--surface-2);
	}
	.issue-title-row {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
	}
	.issue-title {
		font-weight: 500;
		font-size: 13.5px;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.issue-meta {
		display: flex;
		align-items: center;
		gap: 12px;
		margin-top: 3px;
		font-size: 12px;
	}
	.issue-meta span {
		display: inline-flex;
		align-items: center;
		gap: 3px;
	}
	.culprit {
		color: var(--text-dim);
		font-size: 11.5px;
	}
	.col-num {
		text-align: right;
		font-variant-numeric: tabular-nums;
		font-size: 13px;
	}
	.users {
		display: inline-flex;
		align-items: center;
		gap: 3px;
		color: var(--text-dim);
	}
	.col-age {
		text-align: right;
		font-size: 12px;
		font-variant-numeric: tabular-nums;
	}
	.empty {
		padding: 60px 24px;
		text-align: center;
	}
</style>
