package store

type migration struct {
	version int
	name    string
	ddl     string
}

// migrations define the Go engine's fresh SQLite layout. Unlike the Rust
// engine (CozoDB Datalog), every table is plain typed SQL with real keys:
// code_elements is keyed on qualified_name and relationships carry a real
// uniqueness constraint (deliberate behavior change documented in the
// rewrite analysis §9: kills the delete-then-put duplicate dance).
var migrations = []migration{
	{1, "index-layer", `
CREATE TABLE IF NOT EXISTS code_elements (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	qualified_name    TEXT NOT NULL UNIQUE,
	element_type      TEXT NOT NULL,
	name              TEXT NOT NULL,
	file_path         TEXT NOT NULL,
	line_start        INTEGER NOT NULL,
	line_end          INTEGER NOT NULL,
	language          TEXT NOT NULL,
	parent_qualified  TEXT,
	content           TEXT NOT NULL DEFAULT '',
	metadata          TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_code_elements_file ON code_elements (file_path);
CREATE INDEX IF NOT EXISTS idx_code_elements_name ON code_elements (name COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_code_elements_qname ON code_elements (qualified_name COLLATE NOCASE);

CREATE TABLE IF NOT EXISTS relationships (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	source_qualified  TEXT NOT NULL,
	target_qualified  TEXT NOT NULL,
	rel_type          TEXT NOT NULL,
	confidence        REAL NOT NULL DEFAULT 1.0,
	metadata          TEXT NOT NULL DEFAULT '{}',
	UNIQUE (source_qualified, target_qualified, rel_type)
);
CREATE INDEX IF NOT EXISTS idx_relationships_source ON relationships (source_qualified);
CREATE INDEX IF NOT EXISTS idx_relationships_target ON relationships (target_qualified);
CREATE INDEX IF NOT EXISTS idx_relationships_type ON relationships (rel_type);

-- 3-signal file change detection: size+mtime fast path, SHA-256 confirm.
CREATE TABLE IF NOT EXISTS code_files (
	path         TEXT PRIMARY KEY,
	size         INTEGER NOT NULL,
	mtime_ns     INTEGER NOT NULL,
	content_hash TEXT NOT NULL,
	indexed_at   TEXT NOT NULL
);
`},
	{2, "fts-ladder-l2", `
CREATE VIRTUAL TABLE IF NOT EXISTS elements_fts USING fts5(
	name, qualified_name, content
);
`},
	{3, "embedding-layer", `
CREATE TABLE IF NOT EXISTS emb_stamp (
	model_id   TEXT PRIMARY KEY,
	revision   TEXT NOT NULL,
	dimensions INTEGER NOT NULL,
	distance   TEXT NOT NULL,
	provider   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS embedding_state (
	model_id       TEXT NOT NULL,
	qualified_name TEXT NOT NULL,

	content_hash   TEXT NOT NULL,
	state          TEXT NOT NULL,
	embedded_at    TEXT,
	PRIMARY KEY (model_id, qualified_name)
);

CREATE TABLE IF NOT EXISTS embedding_vectors (
	model_id       TEXT NOT NULL,
	qualified_name TEXT NOT NULL,
	vec            BLOB NOT NULL,
	PRIMARY KEY (model_id, qualified_name)
);
`},
	{4, "freshness-watermark", `
-- DB-resident freshness: writers bump seq on every commit; readers compare
-- against the seq recorded in the index inventory. Replaces all per-process
-- TTL caches (Rust C4 TOCTOU fix).
CREATE TABLE IF NOT EXISTS write_watermark (
	id  INTEGER PRIMARY KEY CHECK (id = 1),
	seq INTEGER NOT NULL,
	at  INTEGER NOT NULL
);
INSERT OR IGNORE INTO write_watermark (id, seq, at) VALUES (1, 0, 0);

CREATE TABLE IF NOT EXISTS kv (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`},
	{5, "embed-pipeline-state", `
-- Embed-pipeline runs: the serving binary reads this table to report queue
-- depth / last run / coverage — no in-process coupling with leankg-embed.
CREATE TABLE IF NOT EXISTS embed_runs (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
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
}

func init() {
	migrations = append(migrations, migration{6, "audit-ledger", `
-- Hash-chained audit ledger (Rust audit/mod.rs parity): Hash =
-- sha256(prev_hash|at|actor|action|target|details_json); VerifyAuditChain
-- recomputes the chain to detect any tampering.
CREATE TABLE IF NOT EXISTS audit_ledger (
	seq        INTEGER PRIMARY KEY,
	at         INTEGER NOT NULL,
	actor      TEXT NOT NULL,
	action     TEXT NOT NULL,
	target     TEXT NOT NULL DEFAULT '',
	details    TEXT NOT NULL DEFAULT '{}',
	prev_hash  TEXT NOT NULL DEFAULT '',
	hash       TEXT NOT NULL
);
`})
	migrations = append(migrations, migration{7, "auth-tokens", `
-- DB-backed bearer tokens (Rust auth/tokens.rs + 004_auth.sql access_tokens
-- parity): only the SHA-256 hex of each secret is ever stored.
CREATE TABLE IF NOT EXISTS tokens (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    secret     TEXT NOT NULL UNIQUE,
    role       TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tokens_created ON tokens (created_at);
`})
	migrations = append(migrations, migration{8, "auth-token-lifecycle", `
-- Additive lifecycle columns for the tokens table (Rust auth/tokens.rs
-- AccessToken parity): expiry, soft revocation, last-use tracking, scopes,
-- and the Rust ownership labels. ALTER-only so databases created by
-- migration 007 (or its Rust-era layout) migrate in place; fresh databases
-- get the same result because 007 still runs first.
ALTER TABLE tokens ADD COLUMN expires_at   INTEGER;
ALTER TABLE tokens ADD COLUMN revoked_at   INTEGER;
ALTER TABLE tokens ADD COLUMN last_used_at INTEGER;
ALTER TABLE tokens ADD COLUMN scopes       TEXT NOT NULL DEFAULT '';
ALTER TABLE tokens ADD COLUMN account_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE tokens ADD COLUMN org_id       TEXT NOT NULL DEFAULT '';
`})
	migrations = append(migrations, migration{9, "enterprise-auth", `
-- Enterprise auth (Rust src/auth/accounts.rs + 004_auth.sql parity):
-- accounts, orgs, org memberships, team members and resource ownership.
-- Access tokens live in the tokens table (migrations 007/008).
CREATE TABLE IF NOT EXISTS accounts (
	id            TEXT PRIMARY KEY,
	email         TEXT NOT NULL UNIQUE,
	name          TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	status        TEXT NOT NULL DEFAULT 'active',
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_accounts_status ON accounts (status);

CREATE TABLE IF NOT EXISTS orgs (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	owner_account_id TEXT NOT NULL,
	created_at       INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_orgs_owner ON orgs (owner_account_id);

-- UNIQUE (org_id, account_id) so re-adds upsert instead of duplicating.
CREATE TABLE IF NOT EXISTS org_memberships (
	org_id     TEXT NOT NULL,
	account_id TEXT NOT NULL,
	role       TEXT NOT NULL,
	joined_at  INTEGER NOT NULL,
	PRIMARY KEY (org_id, account_id)
);
CREATE INDEX IF NOT EXISTS idx_org_memberships_account ON org_memberships (account_id);

CREATE TABLE IF NOT EXISTS team_members (
	team_id    TEXT NOT NULL,
	account_id TEXT NOT NULL,
	role       TEXT NOT NULL,
	joined_at  INTEGER NOT NULL,
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
	created_at       INTEGER NOT NULL,
	PRIMARY KEY (resource_type, resource_id, owner_account_id)
);
CREATE INDEX IF NOT EXISTS idx_resource_ownership_owner ON resource_ownership (owner_account_id);
CREATE INDEX IF NOT EXISTS idx_resource_ownership_resource ON resource_ownership (resource_type, resource_id);
`})
	migrations = append(migrations, migration{10, "org-knowledge", `
-- Org/ops knowledge surfaces (Rust src/db/mod.rs parity): incidents,
-- knowledge_entries (team notes / annotations), service_metadata (team,
-- on-call, repo_url, language for the service-context read) and
-- env_snapshots.
--
-- CEILING: Go's code_elements has no env column — it is keyed on
-- qualified_name alone (schema.go migration 001), because the Go indexer
-- indexes one environment at a time. The Rust env-scoped code_elements rows
-- that find_env_conflicts compares are therefore re-expressed as
-- env_snapshots: (env, qualified_name) -> captured element metadata. That is
-- the whole input that detection needs (per-env presence + metadata compare);
-- nothing else in the Go engine reads element envs today.
-- JSON array/object values (affected_services, tags, metadata) are TEXT here,
-- exactly as the Rust Cozo layout stored them.
CREATE TABLE IF NOT EXISTS incidents (
	id                TEXT PRIMARY KEY,
	env               TEXT NOT NULL DEFAULT 'local',
	title             TEXT NOT NULL,
	severity          TEXT NOT NULL,
	occurred_at       INTEGER NOT NULL,
	resolved_at       INTEGER,
	root_cause        TEXT NOT NULL,
	resolution        TEXT NOT NULL,
	affected_services TEXT NOT NULL DEFAULT '[]',
	trigger_pattern   TEXT,
	prevention        TEXT,
	tags              TEXT NOT NULL DEFAULT '[]',
	author            TEXT NOT NULL,
	linked_ticket     TEXT
);
CREATE INDEX IF NOT EXISTS idx_incidents_env ON incidents (env);
CREATE INDEX IF NOT EXISTS idx_incidents_occurred ON incidents (occurred_at);

CREATE TABLE IF NOT EXISTS knowledge_entries (
	id                TEXT PRIMARY KEY,
	knowledge_type    TEXT NOT NULL DEFAULT 'general',
	title             TEXT NOT NULL,
	content           TEXT NOT NULL,
	element_qualified TEXT,
	user_story_id     TEXT,
	feature_id        TEXT,
	tags              TEXT NOT NULL DEFAULT '',
	environment       TEXT NOT NULL DEFAULT 'local',
	branch            TEXT,
	author            TEXT NOT NULL,
	created_at        INTEGER NOT NULL,
	updated_at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_knowledge_element ON knowledge_entries (element_qualified);
CREATE INDEX IF NOT EXISTS idx_knowledge_feature ON knowledge_entries (feature_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_type ON knowledge_entries (knowledge_type);
CREATE INDEX IF NOT EXISTS idx_knowledge_env ON knowledge_entries (environment, updated_at);

CREATE TABLE IF NOT EXISTS service_metadata (
	service_name    TEXT NOT NULL,
	env             TEXT NOT NULL DEFAULT 'local',
	team            TEXT,
	on_call         TEXT,
	repo_url        TEXT,
	language        TEXT,
	health_endpoint TEXT,
	slo_p99_ms      INTEGER,
	incident_count  INTEGER NOT NULL DEFAULT 0,
	last_incident   INTEGER,
	tags            TEXT NOT NULL DEFAULT '',
	version         TEXT,
	deploy_envs     TEXT NOT NULL DEFAULT '',
	created_at      INTEGER NOT NULL,
	updated_at      INTEGER NOT NULL,
	PRIMARY KEY (service_name, env)
);

CREATE TABLE IF NOT EXISTS env_snapshots (
	env            TEXT NOT NULL,
	qualified_name TEXT NOT NULL,
	element_type   TEXT NOT NULL DEFAULT '',
	name           TEXT NOT NULL DEFAULT '',
	file_path      TEXT NOT NULL DEFAULT '',
	metadata       TEXT NOT NULL DEFAULT '{}',
	captured_at    INTEGER NOT NULL,
	PRIMARY KEY (env, qualified_name)
);
CREATE INDEX IF NOT EXISTS idx_env_snapshots_qn ON env_snapshots (qualified_name);
`})
	migrations = append(migrations, migration{11, "context-metrics", `
-- context_metrics: the persisted usage ledger (Rust src/db/pg/schema.sql
-- CREATE TABLE context_metrics, from the Cozo ::create in
-- src/db/sqlite_backend.rs): one row per served tool call.
--
-- No primary key: the Rust layout has none (Cozo has no key on this table),
-- because the ledger is append-only — the same call recorded twice is two
-- rows, and the readouts aggregate rather than deduplicate.
-- timestamp is epoch SECONDS. The six nullable columns are the "not measured /
-- not applicable" fields; every reader normalizes NULL to the zero value (see
-- store_metrics.go), exactly like Rust's unwrap_or(0).
-- The three indexes mirror the Rust ::index statements.
CREATE TABLE IF NOT EXISTS context_metrics (
	tool_name               TEXT NOT NULL,
	timestamp               INTEGER NOT NULL,
	project_path            TEXT NOT NULL,
	input_tokens            INTEGER NOT NULL,
	output_tokens           INTEGER NOT NULL,
	output_elements         INTEGER NOT NULL,
	execution_time_ms       INTEGER NOT NULL,
	baseline_tokens         INTEGER NOT NULL,
	baseline_lines_scanned  INTEGER NOT NULL,
	tokens_saved            INTEGER NOT NULL,
	savings_percent         REAL NOT NULL,
	correct_elements        INTEGER,
	total_expected          INTEGER,
	f1_score                REAL,
	query_pattern           TEXT,
	query_file              TEXT,
	query_depth             INTEGER,
	success                 INTEGER NOT NULL,
	is_deleted              INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_context_metrics_tool_name ON context_metrics (tool_name);
CREATE INDEX IF NOT EXISTS idx_context_metrics_timestamp ON context_metrics (timestamp);
CREATE INDEX IF NOT EXISTS idx_context_metrics_project_path ON context_metrics (project_path);
`})
}
