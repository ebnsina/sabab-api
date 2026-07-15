<script lang="ts">
	import { HugeiconsIcon, SpinnerIcon } from '$lib/icons';
	let { size = 15 }: { size?: number } = $props();
</script>

<span class="inline-flex animate-spin"><HugeiconsIcon icon={SpinnerIcon} {size} /></span>
