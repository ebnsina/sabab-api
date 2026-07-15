<script lang="ts">
	import { fade } from 'svelte/transition';
	import { HugeiconsIcon, ProfileIcon, AppearanceIcon, LogoutIcon } from '$lib/icons';
	import { Button, Card } from '$lib/ui';
	import ThemeToggle from '$lib/components/ThemeToggle.svelte';
	import type { PageData } from './$types';

	// Layout data (the signed-in user) is merged into the page's data prop.
	let { data }: { data: PageData } = $props();
</script>

<svelte:head><title>Settings · Sabab</title></svelte:head>

<div class="mx-auto max-w-2xl px-7 py-8" in:fade={{ duration: 150 }}>
	<h1 class="mb-6 text-xl font-semibold tracking-tight">Settings</h1>

	<div class="flex flex-col gap-5">
		<!-- Profile ------------------------------------------------------------ -->
		<Card class="p-5">
			<div class="mb-4 flex items-center gap-2 text-xs uppercase tracking-wide text-text-faint">
				<HugeiconsIcon icon={ProfileIcon} size={14} /> Profile
			</div>
			<div class="flex items-center gap-4">
				<span
					class="grid h-12 w-12 shrink-0 place-items-center rounded-full bg-surface-3 text-text-dim"
				>
					<HugeiconsIcon icon={ProfileIcon} size={24} />
				</span>
				<div class="min-w-0">
					<div class="truncate font-medium">{data.user?.name || '—'}</div>
					<div class="truncate text-sm text-text-dim">{data.user?.email}</div>
				</div>
			</div>
		</Card>

		<!-- Appearance --------------------------------------------------------- -->
		<Card class="p-5">
			<div class="mb-4 flex items-center gap-2 text-xs uppercase tracking-wide text-text-faint">
				<HugeiconsIcon icon={AppearanceIcon} size={14} /> Appearance
			</div>
			<div class="flex items-center justify-between gap-4">
				<div>
					<div class="text-sm font-medium">Theme</div>
					<div class="text-[13px] text-text-dim">Follows your system setting by default.</div>
				</div>
				<ThemeToggle />
			</div>
		</Card>

		<!-- Account ------------------------------------------------------------ -->
		<Card class="p-5">
			<div class="mb-4 text-xs uppercase tracking-wide text-text-faint">Account</div>
			<div class="flex items-center justify-between gap-4">
				<div class="text-[13px] text-text-dim">Sign out of this device.</div>
				<form method="POST" action="/logout">
					<Button type="submit" variant="danger">
						<HugeiconsIcon icon={LogoutIcon} size={14} /> Sign out
					</Button>
				</form>
			</div>
		</Card>
	</div>
</div>
