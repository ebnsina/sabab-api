<script lang="ts">
	import { page } from '$app/state';
	import {
		HugeiconsIcon,
		BrandIcon,
		ProjectDotIcon,
		IssuesNavIcon,
		LogsNavIcon,
		SettingsIcon,
	} from '$lib/icons';
	import NavItem from '$lib/components/NavItem.svelte';
	import UserMenu from '$lib/components/UserMenu.svelte';
	import type { LayoutData } from './$types';

	let { data, children }: { data: LayoutData; children: import('svelte').Snippet } = $props();

	const activeProjectId = $derived(page.params.projectId);
	const activeProject = $derived(data.projects.find((p) => String(p.id) === activeProjectId));

	// Sections within a project. Data-driven, so adding Traces/Metrics later is
	// one line, not more markup.
	const sections = [
		{ key: 'issues', label: 'Issues', icon: IssuesNavIcon },
		{ key: 'logs', label: 'Logs', icon: LogsNavIcon }
	];
	const activeSection = $derived(page.url.pathname.split('/')[3] ?? '');

	const onSettings = $derived(page.url.pathname.startsWith('/settings'));

	// A breadcrumb crumb: text, optionally a link.
	type Crumb = { label: string; href?: string };

	// The header breadcrumb: where you are, top-to-bottom in the hierarchy. Each
	// route contributes what it knows — the issue-detail page exposes its issue
	// through page.data, so the crumb can name the project and issue.
	const crumbs = $derived.by((): Crumb[] => {
		if (onSettings) return [{ label: 'Settings' }];

		const issue = page.data.issue as { project_id: number; title: string } | undefined;
		if (issue) {
			const proj = data.projects.find((p) => p.id === issue.project_id);
			return [
				{ label: proj?.name ?? 'Project', href: `/projects/${issue.project_id}/issues` },
				{ label: 'Issues', href: `/projects/${issue.project_id}/issues` },
				{ label: truncate(issue.title, 48) }
			];
		}

		const out: Crumb[] = [];
		if (activeProject) {
			out.push({ label: activeProject.name, href: `/projects/${activeProject.id}/issues` });
		}
		const section = sections.find((s) => s.key === activeSection);
		if (section) out.push({ label: section.label });
		return out.length ? out : [{ label: 'Sabab' }];
	});

	function truncate(s: string, n: number): string {
		return s.length > n ? s.slice(0, n - 1) + '…' : s;
	}
</script>

<div class="grid min-h-screen grid-cols-[236px_1fr]">
	<!-- Sidebar ------------------------------------------------------------- -->
	<aside class="flex flex-col gap-1 border-r border-border bg-surface px-3 py-4">
		<a
			class="mb-3 flex items-center gap-2 px-2.5 text-[17px] font-bold tracking-tight"
			href="/"
		>
			<HugeiconsIcon icon={BrandIcon} size={20} color="var(--color-accent)" strokeWidth={2} />
			<span>sabab</span>
		</a>

		<nav class="flex flex-1 flex-col gap-4">
			<!-- Project group. -->
			<div class="flex flex-col gap-0.5">
				<div class="px-2.5 pb-1 text-[11px] font-medium uppercase tracking-wider text-text-faint">
					Projects
				</div>
				{#each data.projects as project (project.id)}
					{@const isActive = String(project.id) === activeProjectId}
					<NavItem
						href="/projects/{project.id}/issues"
						icon={ProjectDotIcon}
						label={project.name}
						active={isActive && !onSettings}
					/>
					{#if isActive && !onSettings}
						<!-- The active project's sections, nested under it: the whole block
						     is inset so the sub-items sit visibly to the right of the
						     project, reading as children without a connecting rail. -->
						<div class="mt-0.5 ml-4 flex flex-col gap-0.5">
							{#each sections as section (section.key)}
								<NavItem
									href="/projects/{project.id}/{section.key}"
									icon={section.icon}
									label={section.label}
									active={activeSection === section.key}
								/>
							{/each}
						</div>
					{/if}
				{:else}
					<p class="px-2.5 py-1 text-xs text-text-faint">No projects yet.</p>
				{/each}
			</div>
		</nav>

		<!-- Footer: just Settings. Profile and sign-out live inside it, so nothing
		     is repeated here or in the header. -->
		<div class="pt-2">
			<NavItem href="/settings" icon={SettingsIcon} label="Settings" active={onSettings} />
		</div>
	</aside>

	<!-- Content, with a proper header ------------------------------------- -->
	<div class="flex min-w-0 flex-col">
		<header class="flex h-14 shrink-0 items-center justify-between border-b border-border px-7">
			<nav class="flex items-center gap-2 text-sm" aria-label="Breadcrumb">
				{#each crumbs as crumb, i (i)}
					{#if i > 0}<span class="text-text-faint">/</span>{/if}
					{#if crumb.href && i < crumbs.length - 1}
						<a href={crumb.href} class="text-text-dim hover:text-text">{crumb.label}</a>
					{:else}
						<span class={i === crumbs.length - 1 ? 'font-semibold text-text' : 'text-text-dim'}>
							{crumb.label}
						</span>
					{/if}
				{/each}
			</nav>

			<UserMenu name={data.user?.name} email={data.user?.email} />
		</header>

		<main class="min-w-0 flex-1">
			{@render children()}
		</main>
	</div>
</div>
