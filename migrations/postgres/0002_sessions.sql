-- Dashboard sessions.
--
-- Only the hash of a session token is stored. A leaked database dump must not
-- hand the attacker working sessions for every logged-in user — the same reason
-- we never store a password, only its hash.
CREATE TABLE sessions (
    -- The SHA-256 of the token. The token itself exists only in the user's
    -- cookie and is never written down anywhere on our side.
    token_hash text        PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    -- For the "active sessions" list, so a user can see and revoke a login they
    -- do not recognise.
    user_agent text        NOT NULL DEFAULT '',
    ip         inet
);

CREATE INDEX sessions_user_idx ON sessions (user_id);
-- Expired rows are swept on a schedule; this makes that cheap.
CREATE INDEX sessions_expiry_idx ON sessions (expires_at);
