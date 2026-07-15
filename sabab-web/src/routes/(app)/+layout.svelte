<script lang="ts">
	import { page } from '$app/state';
	import { HugeiconsIcon, BrandIcon, ProjectsIcon, LogoutIcon, ProjectDotIcon } from '$lib/icons';
	import type { LayoutData } from './$types';

	let { data, children }: { data: LayoutData; children: import('svelte').Snippet } =
		$props();

	// The project currently in the URL, so the sidebar can mark it active.
	const activeProjectId = $derived(page.params.projectId);
</script>

<div class="shell">
	<aside class="sidebar">
		<a class="brand" href="/">
			<HugeiconsIcon icon={BrandIcon} size={20} color="var(--accent)" strokeWidth={2} />
			<span>sabab</span>
		</a>

		<nav class="projects">
			<div class="nav-label">
				<HugeiconsIcon icon={ProjectsIcon} size={13} /> Projects
			</div>
			{#each data.projects as project (project.id)}
				<a
					class="project"
					class:active={String(project.id) === activeProjectId}
					href="/projects/{project.id}/issues"
				>
					<HugeiconsIcon icon={ProjectDotIcon} size={13} />
					<span class="project-name">{project.name}</span>
					<span class="project-platform faint">{project.platform}</span>
				</a>
			{:else}
				<p class="empty faint">No projects yet.</p>
			{/each}
		</nav>

		<div class="user">
			<div class="user-info">
				<div class="user-name">{data.user?.name || data.user?.email}</div>
				<div class="user-email faint">{data.user?.email}</div>
			</div>
			<form method="POST" action="/logout">
				<button class="btn-ghost logout" title="Sign out" aria-label="Sign out">
					<HugeiconsIcon icon={LogoutIcon} size={15} />
				</button>
			</form>
		</div>
	</aside>

	<main class="content">
		{@render children()}
	</main>
</div>

<style>
	.shell {
		display: grid;
		grid-template-columns: 232px 1fr;
		min-height: 100vh;
	}
	.sidebar {
		display: flex;
		flex-direction: column;
		background: var(--surface);
		border-right: 1px solid var(--border);
		padding: 16px 12px;
	}
	.brand {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 8px 16px;
		font-size: 17px;
		font-weight: 700;
		letter-spacing: -0.02em;
	}
	.nav-label {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 8px;
		font-size: 11px;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-faint);
	}
	.projects {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 1px;
	}
	.project {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 7px 8px;
		border-radius: var(--radius-sm);
		color: var(--text-dim);
		font-size: 13px;
	}
	.project:hover {
		background: var(--surface-2);
		color: var(--text);
	}
	.project.active {
		background: var(--surface-3);
		color: var(--text);
	}
	.project-name {
		flex: 1;
		font-weight: 500;
	}
	.project-platform {
		font-size: 11px;
	}
	.empty {
		padding: 8px;
		font-size: 12px;
	}
	.user {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-top: 12px;
		padding: 10px 8px 4px;
		border-top: 1px solid var(--border);
	}
	.user-info {
		flex: 1;
		min-width: 0;
	}
	.user-name {
		font-size: 13px;
		font-weight: 500;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.user-email {
		font-size: 11px;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.logout {
		display: grid;
		place-items: center;
		padding: 7px;
		border-radius: var(--radius-sm);
		border: 1px solid transparent;
		background: transparent;
		color: var(--text-dim);
	}
	.logout:hover {
		background: var(--surface-2);
		color: var(--danger);
	}
	.content {
		min-width: 0;
	}
</style>
