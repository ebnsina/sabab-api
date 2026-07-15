<script lang="ts">
	import { Inbox } from '@lucide/svelte';
</script>

<div class="empty-state">
	<Inbox size={40} color="var(--text-faint)" />
	<h2>No projects yet</h2>
	<p class="muted">Create a project and send your first event to get started.</p>
</div>

<style>
	.empty-state {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 10px;
		padding: 120px 24px;
		text-align: center;
	}
</style>
