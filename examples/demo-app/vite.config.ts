import { defineConfig } from "vite";

// A distinct port so the demo runs alongside the dashboard dev server.
export default defineConfig({
	server: { port: 5180 },
});
