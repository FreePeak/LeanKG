// Versioned PostgreSQL migrations. Mirror of schema.go's sqlite layout minus
// FTS5 (PostgreSQL L2 uses ILIKE + pg_trgm). Each migration runs inside one
// transaction and is recorded in a schema_migrations table INSIDE the
// project's schema. Optional statements (extensions, trgm indexes) run
// outside the transaction with errors ignored — a failed statement inside a
// PostgreSQL transaction aborts it, so "best-effort" must stay outside.
package store

import "fmt"

// pgMigration is one versioned step of the PostgreSQL layout.
type pgMigration struct {
	version    int
	name       string
	bestEffort []string // run before ddl; errors ignored (optional extensions/indexes)
	ddl        string   // runs in one tx; empty = nothing versioned beyond the row
}

var pgMigrations = []pgMigration{
	{1, "index-layer", []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		`CREATE EXTENSION IF NOT EXISTS pg_trgm`,
	}, `
CREATE TABLE IF NOT EXISTS code_elements (
	id                BIGSERIAL PRIMARY KEY,
	qualified_name    TEXT NOT NULL UNIQUE,
	element_type      TEXT NOT NULL,
	name              TEXT NOT NULL,
	file_path         TEXT NOT NULL,
	line_start        INTEGER NOT NULL,
	line_end          INTEGER NOT NULL,
	language          TEXT NOT NULL,
	parent_qualified  TEXT,
	content           TEXT NOT NULL DEFAULT '',
	metadata          JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_code_elements_file ON code_elements (file_path);
CREATE INDEX IF NOT EXISTS idx_code_elements_name ON code_elements (lower(name));
CREATE INDEX IF NOT EXISTS idx_code_elements_qname ON code_elements (lower(qualified_name));

CREATE TABLE IF NOT EXISTS relationships (
	id                BIGSERIAL PRIMARY KEY,
	source_qualified  TEXT NOT NULL,
	target_qualified  TEXT NOT NULL,
	rel_type          TEXT NOT NULL,
	confidence        DOUBLE PRECISION NOT NULL DEFAULT 1.0,
	metadata          JSONB NOT NULL DEFAULT '{}',
	UNIQUE (source_qualified, target_qualified, rel_type)
);
CREATE INDEX IF NOT EXISTS idx_relationships_source ON relationships (source_qualified);
CREATE INDEX IF NOT EXISTS idx_relationships_target ON relationships (target_qualified);
CREATE INDEX IF NOT EXISTS idx_relationships_type ON relationships (rel_type);

-- 3-signal file change detection: size+mtime fast path, SHA-256 confirm.
CREATE TABLE IF NOT EXISTS code_files (
	path         TEXT PRIMARY KEY,
	size         BIGINT NOT NULL,
	mtime_ns     BIGINT NOT NULL,
	content_hash TEXT NOT NULL,
	indexed_at   TEXT NOT NULL
);
`},
	{2, "fts-ladder-l2", []string{
		// pg_trgm accelerates FindFuzzy's ILIKE scans; absent extension = plain seq scan.
		`CREATE INDEX IF NOT EXISTS idx_code_elements_name_trgm ON code_elements USING gin (name gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_code_elements_qname_trgm ON code_elements USING gin (qualified_name gin_trgm_ops)`,
	}, ``},
	{3, "embedding-layer", nil, `
-- Per-model stamp registry. The per-model embedding_state_<san> and
-- embedding_vectors_<san> tables are created dynamically by WriteStamp
-- (vector columns are fixed-dimension, so they follow the stamp).
CREATE TABLE IF NOT EXISTS emb_stamp (
	model_id   TEXT PRIMARY KEY,
	revision   TEXT NOT NULL,
	dimensions INTEGER NOT NULL,
	distance   TEXT NOT NULL,
	provider   TEXT NOT NULL
);
`},
	{4, "freshness-watermark", nil, `
-- DB-resident freshness: writers bump seq on every commit; readers compare
-- against the seq recorded in the index inventory. Replaces all per-process
-- TTL caches (Rust C4 TOCTOU fix).
CREATE TABLE IF NOT EXISTS write_watermark (
	id  INTEGER PRIMARY KEY CHECK (id = 1),
	seq BIGINT NOT NULL,
	at  BIGINT NOT NULL
);
INSERT INTO write_watermark (id, seq, at) VALUES (1, 0, 0) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS kv (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`},
	{5, "embed-pipeline-state", nil, `
-- Embed-pipeline runs: the serving binary reads this table to report queue
-- depth / last run / coverage — no in-process coupling with leankg-embed.
CREATE TABLE IF NOT EXISTS embed_runs (
	id           BIGSERIAL PRIMARY KEY,
	model_id     TEXT NOT NULL,
	mode         TEXT NOT NULL,
	started_at   TEXT NOT NULL,
	finished_at  TEXT,
	dirty        INTEGER NOT NULL DEFAULT 0,
	embedded     INTEGER NOT NULL DEFAULT 0,
	skipped      INTEGER NOT NULL DEFAULT 0,
	failed       INTEGER NOT NULL DEFAULT 0,
	truncations  INTEGER NOT NULL DEFAULT 0,
	orphans      INTEGER NOT NULL DEFAULT 0,
	status       TEXT NOT NULL DEFAULT 'running'
);
CREATE INDEX IF NOT EXISTS idx_embed_runs_model ON embed_runs (model_id, id);
`},
	{6, "audit-ledger", nil, `
-- Hash-chained audit ledger (Rust audit/mod.rs parity): Hash =
-- sha256(prev_hash|at|actor|action|target|details_json); VerifyAuditChain
-- recomputes the chain to detect any tampering.
CREATE TABLE IF NOT EXISTS audit_ledger (
	seq        BIGINT PRIMARY KEY,
	at         BIGINT NOT NULL,
	actor      TEXT NOT NULL,
	action     TEXT NOT NULL,
	target     TEXT NOT NULL DEFAULT '',
	details    JSONB NOT NULL DEFAULT '{}',
	prev_hash  TEXT NOT NULL DEFAULT '',
	hash       TEXT NOT NULL
);
`},
	{7, "auth-tokens", nil, `
-- DB-backed bearer tokens (Rust auth/tokens.rs + 004_auth.sql access_tokens
-- parity): only the SHA-256 hex of each secret is ever stored.
CREATE TABLE IF NOT EXISTS tokens (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    secret     TEXT NOT NULL UNIQUE,
    role       TEXT NOT NULL,
    created_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tokens_created ON tokens (created_at);
`},
	{8, "auth-token-lifecycle", nil, `
-- Additive lifecycle columns for the tokens table (Rust auth/tokens.rs
-- AccessToken parity); ADD COLUMN IF NOT EXISTS so the step is safe to
-- re-enter and existing 007 schemas migrate in place with rows intact.
ALTER TABLE tokens ADD COLUMN IF NOT EXISTS expires_at   BIGINT;
ALTER TABLE tokens ADD COLUMN IF NOT EXISTS revoked_at   BIGINT;
ALTER TABLE tokens ADD COLUMN IF NOT EXISTS last_used_at BIGINT;
ALTER TABLE tokens ADD COLUMN IF NOT EXISTS scopes       TEXT NOT NULL DEFAULT '';
ALTER TABLE tokens ADD COLUMN IF NOT EXISTS account_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE tokens ADD COLUMN IF NOT EXISTS org_id       TEXT NOT NULL DEFAULT '';
`},
	{9, "enterprise-auth", nil, `
-- Enterprise auth (Rust src/auth/accounts.rs + 004_auth.sql parity):
-- accounts, orgs, org memberships, team members and resource ownership.
-- Access tokens live in the tokens table (migrations 007/008).
CREATE TABLE IF NOT EXISTS accounts (
	id            TEXT PRIMARY KEY,
	email         TEXT NOT NULL UNIQUE,
	name          TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	status        TEXT NOT NULL DEFAULT 'active',
	created_at    BIGINT NOT NULL,
	updated_at    BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_accounts_status ON accounts (status);

CREATE TABLE IF NOT EXISTS orgs (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	owner_account_id TEXT NOT NULL,
	created_at       BIGINT NOT NULL,
	updated_at       BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_orgs_owner ON orgs (owner_account_id);

-- UNIQUE (org_id, account_id) so re-adds upsert instead of duplicating.
CREATE TABLE IF NOT EXISTS org_memberships (
	org_id     TEXT NOT NULL,
	account_id TEXT NOT NULL,
	role       TEXT NOT NULL,
	joined_at  BIGINT NOT NULL,
	PRIMARY KEY (org_id, account_id)
);
CREATE INDEX IF NOT EXISTS idx_org_memberships_account ON org_memberships (account_id);

CREATE TABLE IF NOT EXISTS team_members (
	team_id    TEXT NOT NULL,
	account_id TEXT NOT NULL,
	role       TEXT NOT NULL,
	joined_at  BIGINT NOT NULL,
	PRIMARY KEY (team_id, account_id)
);
CREATE INDEX IF NOT EXISTS idx_team_members_account ON team_members (account_id);

-- Ownership index for permission checks: which account owns a resource.
-- The PK makes a re-claim an update; IsResourceOwner reads it either way.
CREATE TABLE IF NOT EXISTS resource_ownership (
	resource_type    TEXT NOT NULL,
	resource_id      TEXT NOT NULL,
	owner_account_id TEXT NOT NULL,
	org_id           TEXT,
	created_at       BIGINT NOT NULL,
	PRIMARY KEY (resource_type, resource_id, owner_account_id)
);
CREATE INDEX IF NOT EXISTS idx_resource_ownership_owner ON resource_ownership (owner_account_id);
CREATE INDEX IF NOT EXISTS idx_resource_ownership_resource ON resource_ownership (resource_type, resource_id);
`},
}

// Migrate applies all pending migrations (RW only). Migrations are applied in
// order and recorded in schema_migrations; each runs inside one transaction.
func (s *PGStore) Migrate() error {
	if s.mode == RO {
		return fmt.Errorf("store: migrate requires RW mode")
	}
	if _, err := s.pool.Exec(pgCtx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("store: ensure schema_migrations: %w", err)
	}
	for _, m := range pgMigrations {
		var done int
		if err := s.pool.QueryRow(pgCtx, `SELECT COUNT(*) FROM schema_migrations WHERE version = $1`, m.version).Scan(&done); err != nil {
			return err
		}
		if done == 1 {
			continue
		}
		for _, stmt := range m.bestEffort {
			_, _ = s.pool.Exec(pgCtx, stmt) // optional: extension may be unavailable; must not fail the migration
		}
		if m.ddl != "" {
			tx, err := s.pool.Begin(pgCtx)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(pgCtx, m.ddl); err != nil {
				_ = tx.Rollback(pgCtx)
				return fmt.Errorf("store: migration %03d %s: %w", m.version, m.name, err)
			}
			if _, err := tx.Exec(pgCtx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, `+pgNow+`)`, m.version, m.name); err != nil {
				_ = tx.Rollback(pgCtx)
				return fmt.Errorf("store: record migration %03d: %w", m.version, err)
			}
			if err := tx.Commit(pgCtx); err != nil {
				return fmt.Errorf("store: record migration %03d: %w", m.version, err)
			}
		} else if _, err := s.pool.Exec(pgCtx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, `+pgNow+`)`, m.version, m.name); err != nil {
			return fmt.Errorf("store: record migration %03d: %w", m.version, err)
		}
	}
	return nil
}
