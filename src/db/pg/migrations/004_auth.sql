-- OAuth2-style access-token auth for protected DB resources.
-- accounts / orgs / memberships / teams / access_tokens / resource_ownership.
-- Follows existing conventions: TEXT ids, BIGINT epoch timestamps, JSONB lists,
-- CREATE TABLE IF NOT EXISTS, <table>_<col>_index indexes.
-- `teams` / `team_invites` already exist (001_schema) and are reused as-is.

CREATE TABLE IF NOT EXISTS accounts (
    id            TEXT NOT NULL,
    email         TEXT NOT NULL,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'active',
    created_at    BIGINT NOT NULL,
    updated_at    BIGINT NOT NULL
);

-- PK on id so the translator's `:put ... ON CONFLICT (id)` upserts (E42P10
-- otherwise: "no unique constraint matching the ON CONFLICT specification").
ALTER TABLE accounts ADD CONSTRAINT accounts_pkey PRIMARY KEY (id);
CREATE UNIQUE INDEX IF NOT EXISTS accounts_email_uniq ON accounts (email);
CREATE INDEX IF NOT EXISTS accounts_status_index ON accounts (status);

CREATE TABLE IF NOT EXISTS orgs (
    id               TEXT NOT NULL,
    name             TEXT NOT NULL,
    owner_account_id TEXT NOT NULL,
    created_at       BIGINT NOT NULL,
    updated_at       BIGINT NOT NULL
);

ALTER TABLE orgs ADD CONSTRAINT orgs_pkey PRIMARY KEY (id);
CREATE INDEX IF NOT EXISTS orgs_owner_index ON orgs (owner_account_id);

-- org_memberships — UNIQUE (org_id, account_id) so re-adds upsert.
CREATE TABLE IF NOT EXISTS org_memberships (
    org_id     TEXT NOT NULL,
    account_id TEXT NOT NULL,
    role       TEXT NOT NULL,
    joined_at  BIGINT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS org_memberships_pair_uniq
    ON org_memberships (org_id, account_id);
CREATE INDEX IF NOT EXISTS org_memberships_account_index ON org_memberships (account_id);

-- team_members — UNIQUE (team_id, account_id). teams lives in 001_schema.
CREATE TABLE IF NOT EXISTS team_members (
    team_id    TEXT NOT NULL,
    account_id TEXT NOT NULL,
    role       TEXT NOT NULL,
    joined_at  BIGINT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS team_members_pair_uniq
    ON team_members (team_id, account_id);
CREATE INDEX IF NOT EXISTS team_members_account_index ON team_members (account_id);

-- access_tokens — server-generated opaque tokens, SHA-256 hash stored.
-- id PK drives the translator's ON CONFLICT upsert; token_hash UNIQUE too.
CREATE TABLE IF NOT EXISTS access_tokens (
    id           TEXT NOT NULL,
    account_id   TEXT NOT NULL,
    org_id       TEXT,
    token_hash   TEXT NOT NULL,
    name         TEXT NOT NULL,
    role         TEXT NOT NULL,
    scopes       JSONB NOT NULL DEFAULT '[]'::jsonb,
    expires_at   BIGINT,
    created_at   BIGINT NOT NULL,
    revoked_at   BIGINT,
    last_used_at BIGINT
);

ALTER TABLE access_tokens ADD CONSTRAINT access_tokens_pkey PRIMARY KEY (id);
CREATE UNIQUE INDEX IF NOT EXISTS access_tokens_hash_uniq ON access_tokens (token_hash);
CREATE INDEX IF NOT EXISTS access_tokens_account_index ON access_tokens (account_id);

-- resource_ownership — "owner the resources": maps a protected resource to its
-- owning account + org. Resource rows live in their natural tables; this is the
-- ownership index for permission checks.
CREATE TABLE IF NOT EXISTS resource_ownership (
    resource_type    TEXT NOT NULL,
    resource_id      TEXT NOT NULL,
    owner_account_id TEXT NOT NULL,
    org_id           TEXT,
    created_at       BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS resource_ownership_owner_index
    ON resource_ownership (owner_account_id);
CREATE INDEX IF NOT EXISTS resource_ownership_resource_index
    ON resource_ownership (resource_type, resource_id);
