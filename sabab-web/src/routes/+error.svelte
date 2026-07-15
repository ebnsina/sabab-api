<script lang="ts">
	import { page } from '$app/state';
	import { AlertTriangle, Home } from '@lucide/svelte';

	// The message and status come from whatever `error()` was thrown — a 404 from
	// a missing issue, a 503 from an API outage. We show the real one rather than
	// a generic "something went wrong", because the difference tells the user
	// whether to retry or to check the URL.
	const status = $derived(page.status);
	const message = $derived(page.error?.message ?? 'Something went wrong.');
</script>

<svelte:head><title>{status} · Sabab</title></svelte:head>

<div class="error-page">
	<AlertTriangle size={36} color="var(--level-warning)" />
	<div class="code">{status}</div>
	<p class="message">{message}</p>
	<a class="btn" href="/"><Home size={15} /> Back to issues</a>
</div>

<style>
	.error-page {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 12px;
		padding: 140px 24px;
		text-align: center;
	}
	.code {
		font-size: 40px;
		font-weight: 700;
		letter-spacing: -0.02em;
	}
	.message {
		color: var(--text-dim);
		margin: 0 0 8px;
		max-width: 420px;
	}
</style>
