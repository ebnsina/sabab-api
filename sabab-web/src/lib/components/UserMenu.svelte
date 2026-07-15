<script lang="ts">
	import { fade } from 'svelte/transition';
	import { HugeiconsIcon, SettingsIcon, LogoutIcon } from '$lib/icons';
	import { cx } from '$lib/ui';

	/**
	 * The account menu in the header: an avatar button that opens a small dropdown
	 * with the signed-in identity, Settings, and Sign out. Closes on outside click
	 * or Escape — handled with a couple of window listeners rather than a library.
	 */
	let { name, email }: { name?: string; email?: string } = $props();

	let open = $state(false);
	let root: HTMLElement;

	// Initials for the avatar: from the name if there is one, else the email.
	const initials = $derived.by(() => {
		const source = (name || email || '?').trim();
		const parts = source.split(/[\s@._-]+/).filter(Boolean);
		const letters = parts.length >= 2 ? parts[0][0] + parts[1][0] : source.slice(0, 2);
		return letters.toUpperCase();
	});

	function onWindowClick(e: MouseEvent) {
		if (open && root && !root.contains(e.target as Node)) open = false;
	}
	function onKey(e: KeyboardEvent) {
		if (e.key === 'Escape') open = false;
	}
</script>

<svelte:window onclick={onWindowClick} onkeydown={onKey} />

<div class="relative" bind:this={root}>
	<button
		class={cx(
			'grid h-8 w-8 place-items-center rounded-full bg-surface-3 text-[11px] font-semibold text-text-dim transition-colors hover:text-text',
			'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-bg',
			open && 'text-text ring-2 ring-accent ring-offset-2 ring-offset-bg'
		)}
		aria-haspopup="menu"
		aria-expanded={open}
		aria-label="Account menu"
		onclick={() => (open = !open)}
	>
		{initials}
	</button>

	{#if open}
		<div
			transition:fade={{ duration: 120 }}
			class="absolute right-0 z-20 mt-2 w-56 overflow-hidden rounded-xl border border-border bg-surface shadow-soft"
			role="menu"
		>
			<div class="border-b border-border px-3.5 py-3">
				{#if name}<div class="truncate text-[13px] font-medium">{name}</div>{/if}
				<div class="truncate text-[12px] text-text-dim">{email}</div>
			</div>
			<div class="p-1">
				<a
					href="/settings"
					class="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-text-dim hover:bg-surface-2 hover:text-text"
					role="menuitem"
					onclick={() => (open = false)}
				>
					<HugeiconsIcon icon={SettingsIcon} size={15} /> Settings
				</a>
				<form method="POST" action="/logout">
					<button
						class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] text-text-dim hover:bg-surface-2 hover:text-danger"
						role="menuitem"
					>
						<HugeiconsIcon icon={LogoutIcon} size={15} /> Sign out
					</button>
				</form>
			</div>
		</div>
	{/if}
</div>
