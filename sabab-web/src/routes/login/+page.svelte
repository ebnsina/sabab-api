<script lang="ts">
	import { enhance } from '$app/forms';
	import { HugeiconsIcon, BrandIcon, SpinnerIcon } from '$lib/icons';
	import type { ActionData } from './$types';

	let { form }: { form: ActionData } = $props();
	let submitting = $state(false);
</script>

<svelte:head><title>Sign in · Sabab</title></svelte:head>

<div class="wrap">
	<div class="card login">
		<div class="brand">
			<HugeiconsIcon icon={BrandIcon} size={22} color="var(--accent)" strokeWidth={2} />
			<span class="wordmark">sabab</span>
		</div>
		<p class="muted tagline">See what broke, why, and for whom.</p>

		<form
			method="POST"
			use:enhance={() => {
				submitting = true;
				return async ({ update }) => {
					await update();
					submitting = false;
				};
			}}
		>
			<label>
				<span>Email</span>
				<input
					class="input"
					type="email"
					name="email"
					value={form?.email ?? ''}
					placeholder="you@example.com"
					autocomplete="username"
					required
				/>
			</label>
			<label>
				<span>Password</span>
				<input
					class="input"
					type="password"
					name="password"
					autocomplete="current-password"
					required
				/>
			</label>

			{#if form?.error}
				<p class="error" role="alert">{form.error}</p>
			{/if}

			<button class="btn btn-primary submit" type="submit" disabled={submitting}>
				{#if submitting}
					<span class="spin"><HugeiconsIcon icon={SpinnerIcon} size={15} /></span>
				{/if}
				Sign in
			</button>
		</form>
	</div>
</div>

<style>
	.wrap {
		min-height: 100vh;
		display: grid;
		place-items: center;
		padding: 24px;
	}
	.login {
		width: 100%;
		max-width: 360px;
		padding: 32px;
		box-shadow: var(--shadow);
	}
	.brand {
		display: flex;
		align-items: center;
		gap: 9px;
	}
	.wordmark {
		font-size: 20px;
		font-weight: 700;
		letter-spacing: -0.02em;
	}
	.tagline {
		margin: 6px 0 24px;
		font-size: 13px;
	}
	form {
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 5px;
	}
	label span {
		font-size: 12px;
		color: var(--text-dim);
		font-weight: 500;
	}
	.error {
		margin: 0;
		padding: 8px 11px;
		border-radius: var(--radius-sm);
		background: color-mix(in srgb, var(--danger) 12%, transparent);
		border: 1px solid color-mix(in srgb, var(--danger) 30%, transparent);
		color: var(--danger);
		font-size: 13px;
	}
	.submit {
		justify-content: center;
		margin-top: 4px;
		padding: 9px;
	}
	.spin {
		display: inline-flex;
		animation: spin 0.7s linear infinite;
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
