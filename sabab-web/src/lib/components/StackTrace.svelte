<script lang="ts">
	import type { ExceptionValue, Frame } from '$lib/server/api';
	import { HugeiconsIcon, ChevronRightIcon, ChevronDownIcon } from '$lib/icons';
	import { cx } from '$lib/ui';

	/**
	 * The stack-trace viewer. The payoff of the whole pipeline: the frame shown
	 * here is symbolicated back to original source, with the offending line in
	 * context. If this reads well, the product works.
	 */
	let { exceptions = [] }: { exceptions?: ExceptionValue[] } = $props();

	// Wire format is innermost-last; show the one that threw first, wrappers below.
	const ordered = $derived([...exceptions].reverse());

	// Non-app frames collapse by default — they are noise between the customer's
	// own frames, and showing them all buries the lines that matter.
	let expanded = $state<Record<number, boolean>>({});

	function visibleFrames(frames: Frame[] | undefined, idx: number): Frame[] {
		if (!frames) return [];
		if (expanded[idx]) return [...frames].reverse();
		const inApp = frames.filter((f) => f.in_app);
		return (inApp.length > 0 ? inApp : frames).slice().reverse();
	}

	function hiddenCount(frames: Frame[] | undefined, idx: number): number {
		if (!frames || expanded[idx]) return 0;
		const inApp = frames.filter((f) => f.in_app).length;
		return inApp > 0 ? frames.length - inApp : 0;
	}

	function frameLocation(f: Frame): string {
		const file = f.filename ?? '<unknown>';
		return f.lineno ? `${file}:${f.lineno}` : file;
	}
</script>

{#each ordered as exc, idx (idx)}
	<div class="mb-5">
		<div class="mb-3 flex flex-wrap items-baseline gap-2">
			<span class="font-mono text-[15px] font-semibold text-error">{exc.type}</span>
			{#if exc.value}<span class="text-text-dim">{exc.value}</span>{/if}
		</div>

		{#if idx > 0}
			<div class="mb-2 text-[11px] text-text-faint">caused the above</div>
		{/if}

		<div class="flex flex-col gap-0.5">
			{#each visibleFrames(exc.frames, idx) as frame, fi (fi)}
				<div
					class={cx(
						'overflow-hidden rounded-lg border',
						frame.in_app ? 'border-border bg-surface' : 'border-transparent'
					)}
				>
					<div
						class={cx(
							'flex flex-wrap items-baseline gap-2.5 px-3 py-1.5',
							frame.in_app && 'bg-surface-2'
						)}
					>
						<span class="font-mono text-[12.5px] font-semibold">
							{frame.function || '<anonymous>'}
						</span>
						<span class="font-mono text-xs text-text-faint">{frameLocation(frame)}</span>
					</div>

					{#if frame.context_line}
						<!-- Source context: the actual code, not just a coordinate. -->
						<div class="overflow-x-auto border-t border-border py-1.5">
							{#each frame.pre_context ?? [] as line, i (i)}
								<div class="px-3 py-px whitespace-pre">
									<code class="text-xs text-text-faint">{line || ' '}</code>
								</div>
							{/each}
							<div class="border-l-2 border-error bg-error/10 px-3 py-px whitespace-pre">
								<code class="text-xs text-text">{frame.context_line}</code>
							</div>
							{#each frame.post_context ?? [] as line, i (i)}
								<div class="px-3 py-px whitespace-pre">
									<code class="text-xs text-text-faint">{line || ' '}</code>
								</div>
							{/each}
						</div>
					{/if}
				</div>
			{/each}

			{#if hiddenCount(exc.frames, idx) > 0}
				<button
					class="mt-1 inline-flex w-fit items-center gap-1 px-2 py-1 text-xs text-text-dim hover:text-text"
					onclick={() => (expanded[idx] = true)}
				>
					<HugeiconsIcon icon={ChevronRightIcon} size={13} />
					Show {hiddenCount(exc.frames, idx)} more frame{hiddenCount(exc.frames, idx) === 1
						? ''
						: 's'} from libraries
				</button>
			{:else if expanded[idx]}
				<button
					class="mt-1 inline-flex w-fit items-center gap-1 px-2 py-1 text-xs text-text-dim hover:text-text"
					onclick={() => (expanded[idx] = false)}
				>
					<HugeiconsIcon icon={ChevronDownIcon} size={13} />
					Collapse library frames
				</button>
			{/if}
		</div>
	</div>
{/each}
