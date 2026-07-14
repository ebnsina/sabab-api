import type { Dsn } from "./types.js";

/**
 * Parse the ingest URL:
 *
 *   https://pk_live_7f3a@ingest.sabab.dev/4
 *          └─ public key ┘ └── host ───┘ └ project
 *
 * One string carries the whole configuration an SDK needs. The public key is not
 * a secret — it ships in browser bundles by design, and is write-only,
 * rate-limited and revocable.
 */
export function parseDsn(dsn: string): Dsn {
  let url: URL;
  try {
    url = new URL(dsn);
  } catch {
    throw new Error(
      `Sabab: invalid DSN "${dsn}". Expected https://<key>@<host>/<project-id>`,
    );
  }

  const publicKey = url.username;
  const projectId = url.pathname.replace(/^\//, "");

  if (!publicKey) {
    throw new Error(`Sabab: DSN is missing the public key: "${dsn}"`);
  }
  if (!projectId) {
    throw new Error(`Sabab: DSN is missing the project id: "${dsn}"`);
  }

  return {
    publicKey,
    host: url.host,
    protocol: url.protocol.replace(":", ""),
    projectId,
    envelopeUrl: `${url.protocol}//${url.host}/ingest/v1/${projectId}/envelope`,
  };
}
