<script lang="ts">
	import type { ExceptionValue, Frame } from '$lib/server/api';
	import { ChevronRight, ChevronDown } from '@lucide/svelte';

	/**
	 * The stack-trace viewer. This is the payoff of the whole pipeline: the frame
	 * the user sees here is symbolicated back to original source, with the
	 * offending line shown in context. If this reads well, the product works.
	 */
	let { exceptions = [] }: { exceptions?: ExceptionValue[] } = $props();

	// The wire format is innermost-last (the last exception is the one that
	// threw). We show that one first, because it is the error the developer
	// actually needs — the wrappers are context below it.
	const ordered = $derived([...exceptions].reverse());

	// Non-app frames (framework, node_modules) are collapsed by default: they are
	// noise between the customer's own frames, and showing them all buries the
	// two lines that matter. Toggled per exception.
	let expanded = $state<Record<number, boolean>>({});

	function visibleFrames(frames: Frame[] | undefined, idx: number): Frame[] {
		if (!frames) return [];
		if (expanded[idx]) return [...frames].reverse();
		const inApp = frames.filter((f) => f.in_app);
		// If nothing is marked in-app (a stack we could not symbolicate), showing
		// nothing would be worse than showing everything.
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
	<div class="exception">
		<div class="exc-head">
			<span class="exc-type mono">{exc.type}</span>
			{#if exc.value}<span class="exc-value">{exc.value}</span>{/if}
		</div>

		{#if idx > 0}
			<div class="caused-by faint">caused the above</div>
		{/if}

		<div class="frames">
			{#each visibleFrames(exc.frames, idx) as frame, fi (fi)}
				<div class="frame" class:in-app={frame.in_app}>
					<div class="frame-head">
						<span class="fn mono">{frame.function || '<anonymous>'}</span>
						<span class="loc mono faint">{frameLocation(frame)}</span>
					</div>

					{#if frame.context_line}
						<!-- Source context: the actual code, not just a coordinate. This is
						     what makes it a stack-trace VIEWER rather than a stack string. -->
						<div class="context">
							{#each frame.pre_context ?? [] as line, i (i)}
								<div class="src-line pre"><code>{line || ' '}</code></div>
							{/each}
							<div class="src-line hit">
								<code>{frame.context_line}</code>
							</div>
							{#each frame.post_context ?? [] as line, i (i)}
								<div class="src-line post"><code>{line || ' '}</code></div>
							{/each}
						</div>
					{/if}
				</div>
			{/each}

			{#if hiddenCount(exc.frames, idx) > 0}
				<button class="toggle" onclick={() => (expanded[idx] = true)}>
					<ChevronRight size={13} />
					Show {hiddenCount(exc.frames, idx)} more frame{hiddenCount(exc.frames, idx) === 1
						? ''
						: 's'} from libraries
				</button>
			{:else if expanded[idx]}
				<button class="toggle" onclick={() => (expanded[idx] = false)}>
					<ChevronDown size={13} />
					Collapse library frames
				</button>
			{/if}
		</div>
	</div>
{/each}

<style>
	.exception {
		margin-bottom: 20px;
	}
	.exc-head {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 8px;
		margin-bottom: 12px;
	}
	.exc-type {
		font-size: 15px;
		font-weight: 600;
		color: var(--level-error);
	}
	.exc-value {
		color: var(--text-dim);
	}
	.caused-by {
		font-size: 11px;
		margin-bottom: 8px;
	}
	.frames {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.frame {
		border-radius: var(--radius-sm);
		border: 1px solid transparent;
		overflow: hidden;
	}
	.frame.in-app {
		border-color: var(--border);
		background: var(--surface);
	}
	.frame-head {
		display: flex;
		flex-wrap: wrap;
		gap: 10px;
		align-items: baseline;
		padding: 7px 12px;
	}
	.frame.in-app .frame-head {
		background: var(--surface-2);
	}
	.fn {
		font-weight: 600;
		font-size: 12.5px;
	}
	.loc {
		font-size: 12px;
	}
	.context {
		border-top: 1px solid var(--border);
		padding: 6px 0;
		overflow-x: auto;
	}
	.src-line {
		padding: 1px 12px;
		white-space: pre;
	}
	.src-line code {
		font-size: 12px;
		color: var(--text-faint);
	}
	.src-line.hit {
		background: color-mix(in srgb, var(--level-error) 12%, transparent);
		border-left: 2px solid var(--level-error);
	}
	.src-line.hit code {
		color: var(--text);
	}
	.toggle {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		margin-top: 4px;
		padding: 5px 8px;
		background: transparent;
		border: none;
		color: var(--text-dim);
		font-size: 12px;
	}
	.toggle:hover {
		color: var(--text);
	}
</style>
