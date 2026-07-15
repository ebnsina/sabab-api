<script lang="ts">
	import { fade } from 'svelte/transition';
	import { HugeiconsIcon, BrandIcon } from '$lib/icons';
	import { renderInline } from '$lib/changelog';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	// A small accent per section, so "Added" / "Changed" / "Fixed" read at a
	// glance without a legend.
	const sectionColor: Record<string, string> = {
		Added: 'var(--color-ok)',
		Changed: 'var(--color-info)',
		Fixed: 'var(--color-accent)',
		Removed: 'var(--color-danger)',
		Security: 'var(--color-danger)'
	};
</script>

<svelte:head>
	<title>Changelog · Sabab</title>
	<meta name="description" content="What's new in Sabab." />
</svelte:head>

<div class="mx-auto max-w-2xl px-6 py-16">
	<header class="mb-12" in:fade={{ duration: 200 }}>
		<a class="mb-6 inline-flex items-center gap-2 text-lg font-bold tracking-tight" href="/">
			<HugeiconsIcon icon={BrandIcon} size={20} color="var(--color-accent)" strokeWidth={2} />
			sabab
		</a>
		<h1 class="text-3xl font-bold tracking-tight">Changelog</h1>
		<p class="mt-2 text-text-dim">What's new, and what changed.</p>
	</header>

	<div class="flex flex-col gap-12">
		{#each data.releases as release (release.version)}
			<section>
				<!-- Version rail: a sticky-feeling label down the left, content on the
				     right, so the timeline reads top-to-bottom. -->
				<h2 class="mb-4 flex items-baseline gap-3">
					<span class="text-lg font-semibold tracking-tight">{release.version}</span>
				</h2>

				<div class="flex flex-col gap-5 border-l border-border pl-5">
					{#each release.sections as section (section.title)}
						<div>
							<h3
								class="mb-2 text-[11px] font-semibold uppercase tracking-wide"
								style:color={sectionColor[section.title] ?? 'var(--color-text-faint)'}
							>
								{section.title}
							</h3>
							<ul class="flex flex-col gap-2">
								{#each section.items as item (item)}
									<li class="flex gap-2.5 text-[14px] leading-relaxed text-text-dim">
										<span
											class="mt-2 h-1 w-1 shrink-0 rounded-full"
											style:background={sectionColor[section.title] ?? 'var(--color-text-faint)'}
										></span>
										<!-- eslint-disable-next-line svelte/no-at-html-tags -->
										<span class="[&_a]:text-info [&_a:hover]:underline [&_code]:font-mono [&_code]:text-[13px] [&_strong]:text-text [&_strong]:font-semibold">
											{@html renderInline(item)}
										</span>
									</li>
								{/each}
							</ul>
						</div>
					{/each}
				</div>
			</section>
		{/each}
	</div>
</div>
