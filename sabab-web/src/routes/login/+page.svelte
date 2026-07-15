<script lang="ts">
	import { enhance } from '$app/forms';
	import { fade } from 'svelte/transition';
	import { HugeiconsIcon, BrandIcon } from '$lib/icons';
	import { Button, Card, Input, Spinner } from '$lib/ui';
	import type { ActionData } from './$types';

	let { form }: { form: ActionData } = $props();
	let submitting = $state(false);
	let email = $state('');
	let password = $state('');

	$effect(() => {
		email = form?.email ?? '';
	});
</script>

<svelte:head><title>Sign in · Sabab</title></svelte:head>

<div class="grid min-h-screen place-items-center p-6">
	<Card class="w-full max-w-sm p-8 shadow-[0_4px_20px_rgba(0,0,0,0.4)]">
		<div class="flex items-center gap-2.5">
			<HugeiconsIcon icon={BrandIcon} size={22} color="var(--color-accent)" strokeWidth={2} />
			<span class="text-xl font-bold tracking-tight">sabab</span>
		</div>
		<p class="mt-1.5 mb-6 text-sm text-text-dim">See what broke, why, and for whom.</p>

		<form
			method="POST"
			class="flex flex-col gap-3.5"
			use:enhance={() => {
				submitting = true;
				return async ({ update }) => {
					await update();
					submitting = false;
				};
			}}
		>
			<label class="flex flex-col gap-1.5">
				<span class="text-xs font-medium text-text-dim">Email</span>
				<Input
					type="email"
					name="email"
					bind:value={email}
					placeholder="you@example.com"
					autocomplete="username"
					required
				/>
			</label>
			<label class="flex flex-col gap-1.5">
				<span class="text-xs font-medium text-text-dim">Password</span>
				<Input
					type="password"
					name="password"
					bind:value={password}
					autocomplete="current-password"
					required
				/>
			</label>

			{#if form?.error}
				<p
					transition:fade={{ duration: 150 }}
					class="rounded-xl border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger"
					role="alert"
				>
					{form.error}
				</p>
			{/if}

			<Button type="submit" variant="primary" disabled={submitting} class="mt-1 w-full py-2.5">
				{#if submitting}<Spinner />{/if}
				Sign in
			</Button>
		</form>
	</Card>
</div>
