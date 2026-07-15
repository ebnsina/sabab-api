<script lang="ts">
	import { HugeiconsIcon, EmptyIcon } from '$lib/icons';
</script>

<div class="empty-state">
	<HugeiconsIcon icon={EmptyIcon} size={40} color="var(--text-faint)" strokeWidth={1.5} />
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
