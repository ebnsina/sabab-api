<script lang="ts">
	import { Badge } from '$lib/ui';

	/** A severity chip. Thin wrapper over the shared Badge, kept for call sites
	 *  that read better as `<LevelBadge level={...} />`. */
	let { level }: { level: string } = $props();
</script>

<Badge {level}>{level}</Badge>
