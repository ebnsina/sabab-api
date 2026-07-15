<script lang="ts">
	import { enhance } from '$app/forms';
	import LevelBadge from '$lib/components/LevelBadge.svelte';
	import StackTrace from '$lib/components/StackTrace.svelte';
	import { fullTime, relativeTime, compactNumber } from '$lib/time';
	import type { ExceptionValue, Breadcrumb } from '$lib/server/api';
	import {
		CheckCircle2,
		EyeOff,
		RotateCcw,
		Users,
		Hash,
		Clock,
		Tag,
		Globe,
		Monitor,
		ArrowLeft
	} from '@lucide/svelte';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	// The stacktrace and breadcrumbs arrive as raw JSON strings from ClickHouse;
	// parse them once here, defensively — a malformed blob must not blank the page.
	const exceptions = $derived(parseJson<ExceptionValue[]>(data.event?.stacktrace) ?? []);
	const breadcrumbs = $derived(parseJson<Breadcrumb[]>(data.event?.breadcrumbs) ?? []);
	const tags = $derived(data.event?.tags ?? {});

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

<div class="detail">
	<a class="back" href="/projects/{data.issue.project_id}/issues">
		<ArrowLeft size={14} /> Issues
	</a>

	<header class="issue-header">
		<div class="title-block">
			<div class="title-row">
				<LevelBadge level={data.issue.level} />
				<h1>{data.issue.title}</h1>
			</div>
			{#if data.issue.culprit}
				<p class="culprit mono">{data.issue.culprit}</p>
			{/if}
		</div>

		<!-- The status actions. A resolved issue shows "reopen"; the rest show
		     resolve + ignore. The current status is never a dead-end. -->
		<div class="actions">
			{#if data.issue.status === 'resolved'}
				<form method="POST" action="?/status" use:enhance>
					<input type="hidden" name="status" value="unresolved" />
					<button class="btn"><RotateCcw size={14} /> Reopen</button>
				</form>
			{:else}
				<form method="POST" action="?/status" use:enhance>
					<input type="hidden" name="status" value="resolved" />
					<input type="hidden" name="release" value={data.event?.release ?? ''} />
					<button class="btn btn-primary"><CheckCircle2 size={14} /> Resolve</button>
				</form>
				<form method="POST" action="?/status" use:enhance>
					<input type="hidden" name="status" value="ignored" />
					<button class="btn"><EyeOff size={14} /> Ignore</button>
				</form>
			{/if}
		</div>
	</header>

	<div class="stat-row">
		<div class="stat">
			<span class="stat-label">Events</span>
			<span class="stat-value">{compactNumber(data.issue.times_seen)}</span>
		</div>
		<div class="stat">
			<span class="stat-label"><Users size={12} /> Users</span>
			<span class="stat-value">{compactNumber(data.issue.users_affected)}</span>
		</div>
		<div class="stat">
			<span class="stat-label"><Clock size={12} /> First seen</span>
			<span class="stat-value" title={fullTime(data.issue.first_seen)}>
				{relativeTime(data.issue.first_seen)} ago
			</span>
		</div>
		<div class="stat">
			<span class="stat-label"><Clock size={12} /> Last seen</span>
			<span class="stat-value" title={fullTime(data.issue.last_seen)}>
				{relativeTime(data.issue.last_seen)} ago
			</span>
		</div>
		{#if data.issue.first_release}
			<div class="stat">
				<span class="stat-label"><Hash size={12} /> Release</span>
				<span class="stat-value mono">{data.issue.first_release}</span>
			</div>
		{/if}
	</div>

	<div class="grid">
		<div class="main-col">
			<section class="panel">
				<h2 class="panel-title">Stack trace</h2>
				{#if exceptions.length > 0}
					<StackTrace {exceptions} />
				{:else if data.eventError}
					<p class="muted">{data.eventError}</p>
				{:else}
					<p class="muted">No stack trace on this event.</p>
				{/if}
			</section>

			{#if breadcrumbs.length > 0}
				<section class="panel">
					<h2 class="panel-title">Breadcrumbs</h2>
					<ol class="crumbs">
						{#each breadcrumbs as crumb, i (i)}
							<li class="crumb">
								<span class="crumb-cat faint">{crumb.category ?? 'default'}</span>
								<span class="crumb-msg">{crumb.message ?? ''}</span>
								<span class="crumb-time faint">{relativeTime(crumb.ts)}</span>
							</li>
						{/each}
					</ol>
				</section>
			{/if}
		</div>

		<aside class="side-col">
			{#if data.event}
				<section class="panel">
					<h2 class="panel-title">Event</h2>
					<dl class="kv">
						{#if data.event.environment}
							<dt>Environment</dt>
							<dd>{data.event.environment}</dd>
						{/if}
						{#if data.event.user_id || data.event.user_email}
							<dt><Users size={12} /> User</dt>
							<dd>{data.event.user_email || data.event.user_id}</dd>
						{/if}
						{#if data.event.browser}
							<dt><Monitor size={12} /> Browser</dt>
							<dd>{data.event.browser}</dd>
						{/if}
						{#if data.event.os}
							<dt><Monitor size={12} /> OS</dt>
							<dd>{data.event.os}</dd>
						{/if}
						{#if data.event.trace_id && !data.event.trace_id.startsWith('00000000')}
							<dt><Globe size={12} /> Trace</dt>
							<dd class="mono trace">{data.event.trace_id}</dd>
						{/if}
					</dl>
				</section>
			{/if}

			{#if Object.keys(tags).length > 0}
				<section class="panel">
					<h2 class="panel-title"><Tag size={13} /> Tags</h2>
					<div class="tags">
						{#each Object.entries(tags) as [key, value] (key)}
							<div class="tag">
								<span class="tag-key">{key}</span>
								<span class="tag-value mono">{value}</span>
							</div>
						{/each}
					</div>
				</section>
			{/if}
		</aside>
	</div>
</div>

<style>
	.detail {
		padding: 20px 28px 60px;
		max-width: 1100px;
	}
	.back {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		color: var(--text-dim);
		font-size: 13px;
		margin-bottom: 16px;
	}
	.back:hover {
		color: var(--text);
	}
	.issue-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 20px;
		margin-bottom: 20px;
	}
	.title-row {
		display: flex;
		align-items: center;
		gap: 10px;
	}
	.title-row h1 {
		font-size: 19px;
		line-height: 1.3;
	}
	.culprit {
		margin: 8px 0 0;
		color: var(--text-dim);
		font-size: 13px;
	}
	.actions {
		display: flex;
		gap: 8px;
		flex-shrink: 0;
	}
	.stat-row {
		display: flex;
		flex-wrap: wrap;
		gap: 28px;
		padding: 16px 0;
		margin-bottom: 20px;
		border-top: 1px solid var(--border);
		border-bottom: 1px solid var(--border);
	}
	.stat {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.stat-label {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		font-size: 11px;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--text-faint);
	}
	.stat-value {
		font-size: 15px;
		font-weight: 600;
		font-variant-numeric: tabular-nums;
	}
	.grid {
		display: grid;
		grid-template-columns: 1fr 280px;
		gap: 20px;
		align-items: start;
	}
	.panel {
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 18px;
		margin-bottom: 16px;
	}
	.panel-title {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 12px;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--text-faint);
		margin-bottom: 14px;
	}
	.crumbs {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
	}
	.crumb {
		display: grid;
		grid-template-columns: 90px 1fr auto;
		gap: 12px;
		align-items: baseline;
		padding: 7px 0;
		border-bottom: 1px solid var(--border);
		font-size: 12.5px;
	}
	.crumb:last-child {
		border-bottom: none;
	}
	.crumb-cat {
		font-size: 11px;
	}
	.crumb-time {
		font-size: 11px;
	}
	.kv {
		display: grid;
		grid-template-columns: auto 1fr;
		gap: 8px 12px;
		margin: 0;
		font-size: 12.5px;
	}
	.kv dt {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		color: var(--text-faint);
	}
	.kv dd {
		margin: 0;
		text-align: right;
		word-break: break-word;
	}
	.trace {
		font-size: 10.5px;
	}
	.tags {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}
	.tag {
		display: flex;
		justify-content: space-between;
		gap: 10px;
		font-size: 12.5px;
	}
	.tag-key {
		color: var(--text-faint);
	}
	@media (max-width: 860px) {
		.grid {
			grid-template-columns: 1fr;
		}
	}
</style>
