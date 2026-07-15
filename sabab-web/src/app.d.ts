import type { User } from '$lib/server/api';

declare global {
	namespace App {
		interface Locals {
			/** The logged-in user, or null. Set by hooks.server.ts on every request. */
			user: User | null;
			/** The raw session token, forwarded to the Go API by load functions. */
			session: string | undefined;
		}
		interface PageData {
			user?: User | null;
		}
	}
}

export {};
