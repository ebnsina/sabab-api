<script lang="ts">
	// Self-hosted variable fonts — bundled by Vite, no Google Fonts request, so
	// nothing about the dashboard's typography depends on a third-party CDN being
	// up (or watching). Imported before app.css so the @font-face rules exist
	// before the custom properties that reference them.
	import '@fontsource-variable/mona-sans';
	import '@fontsource-variable/geist-mono';
	import '$lib/styles/app.css';
	import favicon from '$lib/assets/favicon.svg';

	let { children } = $props();
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
</svelte:head>

{@render children()}
