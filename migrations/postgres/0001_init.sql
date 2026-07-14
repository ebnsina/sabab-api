-- Control plane: everything mutable, transactional and low-volume.
-- Event bodies never land here; they live in ClickHouse.

-- Keeps updated_at honest without every writer having to remember it.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Slugs appear in URLs, so they are lowercase-kebab and stable.
CREATE DOMAIN slug AS text CHECK (VALUE ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(VALUE) BETWEEN 2 AND 64);

CREATE TABLE organizations (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug       slug        NOT NULL UNIQUE,
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER organizations_updated_at BEFORE UPDATE ON organizations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email         text        NOT NULL UNIQUE CHECK (email = lower(email)),
    -- NULL when the account authenticates purely through an OAuth provider.
    password_hash text,
    name          text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE org_members (
    org_id     bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text        NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX org_members_user_idx ON org_members (user_id);

CREATE TABLE projects (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id     bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    slug       slug        NOT NULL,
    name       text        NOT NULL,
    -- Free-form on purpose: "javascript-browser", "node", "go", ... The SDK
    -- reports it; we do not want a migration every time one is added.
    platform   text        NOT NULL DEFAULT 'other',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE TRIGGER projects_updated_at BEFORE UPDATE ON projects
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The public key ships inside browser bundles by design: it is write-only,
-- project-scoped, rate-limited and revocable. It is never a read credential.
CREATE TABLE ingest_keys (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id bigint      NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    public_key text        NOT NULL UNIQUE,
    label      text        NOT NULL DEFAULT 'default',
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);
-- The gateway looks a key up on every single request; keep only live keys hot.
CREATE INDEX ingest_keys_live_idx ON ingest_keys (public_key) WHERE revoked_at IS NULL;

CREATE TABLE releases (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id  bigint      NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    version     text        NOT NULL,          -- "web@2.4.1"
    ref         text,                          -- git sha / tag
    deployed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, version)
);

-- Source maps and other debug artifacts. The bytes live in S3; this is the index.
CREATE TABLE release_files (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    release_id   bigint      NOT NULL REFERENCES releases (id) ON DELETE CASCADE,
    url_pattern  text        NOT NULL,         -- "~/static/js/main.*.js.map"
    artifact_key text        NOT NULL,         -- S3 object key
    size_bytes   bigint      NOT NULL DEFAULT 0,
    checksum     text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (release_id, url_pattern)
);

-- The problem, as opposed to an occurrence of it. This is the only table the
-- dashboard mutates. group_hash is stored as 16-char hex rather than an integer
-- because it is an unsigned 64-bit xxhash and Postgres bigint is signed —
-- ClickHouse holds the same value as UInt64.
CREATE TABLE issues (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id          bigint      NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    group_hash          text        NOT NULL CHECK (group_hash ~ '^[0-9a-f]{16}$'),
    title               text        NOT NULL,  -- "TypeError: Cannot read properties of undefined"
    culprit             text        NOT NULL DEFAULT '',
    level               text        NOT NULL DEFAULT 'error'
                            CHECK (level IN ('debug', 'info', 'warning', 'error', 'fatal')),
    status              text        NOT NULL DEFAULT 'unresolved'
                            CHECK (status IN ('unresolved', 'resolved', 'ignored')),
    -- Why these events group together, kept so the UI can answer "why is this
    -- one issue?" and so merge/split can be offered later.
    group_components    jsonb       NOT NULL DEFAULT '[]'::jsonb,
    assignee_id         bigint      REFERENCES users (id) ON DELETE SET NULL,
    first_seen          timestamptz NOT NULL,
    last_seen           timestamptz NOT NULL,
    times_seen          bigint      NOT NULL DEFAULT 0,
    users_affected      bigint      NOT NULL DEFAULT 0,
    first_release       text,
    resolved_in_release text,
    snooze_until        timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, group_hash)
);
CREATE TRIGGER issues_updated_at BEFORE UPDATE ON issues
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The issue stream is "unresolved issues in this project, most recent first".
CREATE INDEX issues_stream_idx ON issues (project_id, status, last_seen DESC);
CREATE INDEX issues_frequency_idx ON issues (project_id, status, times_seen DESC);
CREATE INDEX issues_assignee_idx ON issues (assignee_id) WHERE assignee_id IS NOT NULL;

-- Comments and the audit trail, in one ordered stream per issue.
CREATE TABLE issue_activity (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    issue_id bigint      NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    -- NULL when Sabab itself acted (auto-resolve, regression detection).
    user_id  bigint      REFERENCES users (id) ON DELETE SET NULL,
    kind     text        NOT NULL CHECK (kind IN (
                    'created', 'comment', 'resolved', 'unresolved', 'ignored',
                    'assigned', 'unassigned', 'regressed', 'snoozed')),
    payload  jsonb       NOT NULL DEFAULT '{}'::jsonb,
    at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX issue_activity_issue_idx ON issue_activity (issue_id, at DESC);

CREATE TABLE alert_rules (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id       bigint      NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name             text        NOT NULL,
    kind             text        NOT NULL CHECK (kind IN ('new_issue', 'regression', 'frequency', 'metric')),
    conditions       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    channels         jsonb       NOT NULL DEFAULT '[]'::jsonb,   -- [{type:"slack",...}]
    throttle_seconds integer     NOT NULL DEFAULT 3600 CHECK (throttle_seconds >= 0),
    enabled          boolean     NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER alert_rules_updated_at BEFORE UPDATE ON alert_rules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE INDEX alert_rules_enabled_idx ON alert_rules (project_id) WHERE enabled;

CREATE TABLE alert_history (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    rule_id  bigint      NOT NULL REFERENCES alert_rules (id) ON DELETE CASCADE,
    issue_id bigint      REFERENCES issues (id) ON DELETE SET NULL,
    fired_at timestamptz NOT NULL DEFAULT now(),
    payload  jsonb       NOT NULL DEFAULT '{}'::jsonb
);
-- The alerter asks "did this rule already fire inside its throttle window?" on
-- every evaluation, so that lookup gets its own ordering.
CREATE INDEX alert_history_rule_idx ON alert_history (rule_id, fired_at DESC);

-- Read credentials for the API. Only the hash is stored; the token itself is
-- shown to the user exactly once.
CREATE TABLE api_tokens (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id       bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name         text        NOT NULL,
    token_hash   text        NOT NULL UNIQUE,
    scopes       text[]      NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz
);
CREATE INDEX api_tokens_org_idx ON api_tokens (org_id);
