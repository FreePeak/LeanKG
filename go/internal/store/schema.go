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
}
