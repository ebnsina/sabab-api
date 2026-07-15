<script lang="ts">
	import { page } from '$app/state';
	import { HugeiconsIcon, AlertIcon, HomeIcon } from '$lib/icons';
	import { Button } from '$lib/ui';

	// The real status and message from whatever error() was thrown — a 404 from a
	// missing issue, a 503 from an API outage. The difference tells the user
	// whether to retry or check the URL, so we show it rather than a generic line.
	const status = $derived(page.status);
	const message = $derived(page.error?.message ?? 'Something went wrong.');
</script>

<svelte:head><title>{status} · Sabab</title></svelte:head>

<div class="flex flex-col items-center gap-3 px-6 py-36 text-center">
	<HugeiconsIcon icon={AlertIcon} size={36} color="var(--color-warning)" strokeWidth={1.8} />
	<div class="text-[40px] font-bold tracking-tight">{status}</div>
	<p class="mb-2 max-w-md text-text-dim">{message}</p>
	<Button href="/"><HugeiconsIcon icon={HomeIcon} size={15} /> Back to issues</Button>
</div>
