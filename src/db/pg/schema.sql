-- LeanKG PostgreSQL schema (Phase 2 of the legacy-engine → PostgreSQL + pgvector migration).
--
-- Source of truth for every column: the legacy query-shape inventory analysis §1,
-- re-derived from src/db/schema.rs (:create DDL), src/embeddings/state.rs,
-- src/graph/inventory.rs, src/db/keys.rs, src/indexer/content_hash.rs.
--
-- Mapping rules (plan T2.3):
--   legacy Int    → BIGINT        (timestamps kept BIGINT; see per-column notes)
--   legacy Float  → DOUBLE PRECISION
--   legacy String → TEXT
--   legacy String?→ TEXT (nullable)
--   legacy Bool   → BOOLEAN
--   JSON-string cols (metadata/tags/members/deploy_envs) → JSONB
--     All writers `serde_json::to_string()` a serde_json::Value / Vec / struct;
--     all readers parse the string back. JSONB round-trips identically and the
--     Postgres translator's row shim returns the JSON text (see pg-schema.md).
--   legacy <F32; 384> → vector(384)  (pgvector)
--
-- VEC_DIM: 384 (BGE-small-en-v1.5 embedding dim, plan decision D5). Keep this
-- in sync with the Rust const `VEC_DIM` in src/db/pg/migrations.rs.

CREATE EXTENSION IF NOT EXISTS vector;

-- ---------------------------------------------------------------------------
-- code_elements — NOT keyed in the legacy engine (composite tuple key: :put
-- with the same
-- qualified_name but different columns creates duplicate rows, and callers
-- rely on delete-then-insert, e.g. G53/G44). Therefore NO PRIMARY KEY here;
-- the qualified_name index mirrors the legacy qualified_name_index, the file_path
-- index mirrors file_path_index, etc.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS code_elements (
    qualified_name  TEXT NOT NULL,
    element_type    TEXT NOT NULL,
    name            TEXT NOT NULL,
    file_path       TEXT NOT NULL,
    line_start      BIGINT NOT NULL,
    line_end        BIGINT NOT NULL,
    language        TEXT NOT NULL,
    parent_qualified TEXT,
    cluster_id      TEXT,
    cluster_label   TEXT,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    env             TEXT NOT NULL DEFAULT 'local',
    ontology_layer  TEXT NOT NULL DEFAULT 'procedural'
);

CREATE INDEX IF NOT EXISTS code_elements_file_path_index ON code_elements (file_path);
CREATE INDEX IF NOT EXISTS code_elements_qualified_name_index ON code_elements (qualified_name);
CREATE INDEX IF NOT EXISTS code_elements_element_type_index ON code_elements (element_type);
CREATE INDEX IF NOT EXISTS code_elements_parent_qualified_index ON code_elements (parent_qualified);

-- FR-ZCP-05 bridge tier (mirrors 007_trgm_fuzzy.sql): trigram fuzzy +
-- anchored-prefix matching for the L2 keyword rung. The extension install
-- is degradation-safe; here it is assumed present (same stance as the
-- pgvector install above), and the runtime seam falls back to ILIKE-only
-- when the operators are missing.
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE INDEX IF NOT EXISTS code_elements_name_text_pattern_idx
    ON code_elements (name text_pattern_ops);
CREATE INDEX IF NOT EXISTS code_elements_name_trgm_idx
    ON code_elements USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS code_elements_qualified_name_trgm_idx
    ON code_elements USING gin (qualified_name gin_trgm_ops);

-- ---------------------------------------------------------------------------
-- relationships — not keyed in the legacy engine. No PK. Indexes mirror the
-- legacy ::index create statements (rel_type, target_qualified, source_qualified).
-- No FK to code_elements: targets may be absent (e.g. "tested_by" / external
-- symbols; the legacy engine never enforced referential integrity and removal
-- order is delete-relationships-then-elements). Index + comment instead of FK.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS relationships (
    source_qualified TEXT NOT NULL,
    target_qualified TEXT NOT NULL,
    rel_type         TEXT NOT NULL,
    confidence       DOUBLE PRECISION NOT NULL,
    metadata         JSONB NOT NULL DEFAULT '{}'::jsonb,
    env              TEXT NOT NULL DEFAULT 'local'
);

CREATE INDEX IF NOT EXISTS relationships_rel_type_index ON relationships (rel_type);
CREATE INDEX IF NOT EXISTS relationships_target_qualified_index ON relationships (target_qualified);
CREATE INDEX IF NOT EXISTS relationships_source_qualified_index ON relationships (source_qualified);

-- ---------------------------------------------------------------------------
-- business_logic — no PK in the legacy engine, none here.
-- No FK to code_elements.qualified_name: code_elements has no PK (see above)
-- and business_logic rows can reference non-indexed symbols (e.g. docs for
-- removed code). Index mirrors query patterns (lookup by element_qualified,
-- user_story_id, feature_id).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS business_logic (
    element_qualified TEXT NOT NULL,
    description      TEXT NOT NULL,
    user_story_id    TEXT,
    feature_id       TEXT
);

CREATE INDEX IF NOT EXISTS business_logic_element_qualified_index ON business_logic (element_qualified);
CREATE INDEX IF NOT EXISTS business_logic_user_story_id_index ON business_logic (user_story_id);
CREATE INDEX IF NOT EXISTS business_logic_feature_id_index ON business_logic (feature_id);

-- ---------------------------------------------------------------------------
-- context_metrics — no PK in the legacy engine, none here. timestamp is epoch
-- seconds (a legacy Int, written as as_secs() — db/mod.rs:640). Indexes mirror
-- the three legacy ::index statements.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS context_metrics (
    tool_name               TEXT NOT NULL,
    timestamp               BIGINT NOT NULL,
    project_path            TEXT NOT NULL,
    input_tokens            BIGINT NOT NULL,
    output_tokens           BIGINT NOT NULL,
    output_elements         BIGINT NOT NULL,
    execution_time_ms       BIGINT NOT NULL,
    baseline_tokens         BIGINT NOT NULL,
    baseline_lines_scanned  BIGINT NOT NULL,
    tokens_saved            BIGINT NOT NULL,
    savings_percent         DOUBLE PRECISION NOT NULL,
    correct_elements        BIGINT,
    total_expected          BIGINT,
    f1_score                DOUBLE PRECISION,
    query_pattern           TEXT,
    query_file              TEXT,
    query_depth             BIGINT,
    success                 BOOLEAN NOT NULL,
    is_deleted              BOOLEAN NOT NULL
);

CREATE INDEX IF NOT EXISTS context_metrics_tool_name_index ON context_metrics (tool_name);
CREATE INDEX IF NOT EXISTS context_metrics_timestamp_index ON context_metrics (timestamp);
CREATE INDEX IF NOT EXISTS context_metrics_project_path_index ON context_metrics (project_path);

-- ---------------------------------------------------------------------------
-- service_metadata — the legacy schema is NOT keyed on service_name
-- (composite tuple key),
-- so no PK here; (service_name, env) is the natural identity and the legacy
-- index. tags/deploy_envs: serde_json strings → JSONB (models.rs ServiceMetadata,
-- writer db/mod.rs:1517). created_at/updated_at are epoch Ints → BIGINT.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS service_metadata (
    service_name    TEXT NOT NULL,
    env             TEXT NOT NULL DEFAULT 'local',
    team            TEXT,
    on_call         TEXT,
    repo_url        TEXT,
    language        TEXT,
    health_endpoint TEXT,
    slo_p99_ms      BIGINT,
    incident_count  BIGINT NOT NULL,
    last_incident   BIGINT,
    tags            JSONB NOT NULL DEFAULT '[]'::jsonb,
    version         TEXT,
    deploy_envs     JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at      BIGINT NOT NULL,
    updated_at      BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS service_metadata_svc_name_index ON service_metadata (service_name);
CREATE INDEX IF NOT EXISTS service_metadata_svc_env_index ON service_metadata (env);

-- ---------------------------------------------------------------------------
-- teams — no PK in the legacy engine, none here. members/graph_read_users/graph_write_users
-- are Vec<T> serialized with serde_json (db/mod.rs:1621-1629) → JSONB.
-- created_at/updated_at epoch Ints → BIGINT.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS teams (
    id                 TEXT NOT NULL,
    name               TEXT NOT NULL,
    description        TEXT NOT NULL,
    owner_id           TEXT NOT NULL,
    created_at         BIGINT NOT NULL,
    updated_at         BIGINT NOT NULL,
    graph_read_users   JSONB NOT NULL DEFAULT '[]'::jsonb,
    graph_write_users  JSONB NOT NULL DEFAULT '[]'::jsonb,
    members            JSONB NOT NULL DEFAULT '[]'::jsonb
);

CREATE INDEX IF NOT EXISTS teams_owner_index ON teams (owner_id);

-- ---------------------------------------------------------------------------
-- team_invites — no PK in the legacy engine, none here. token is the natural lookup key.
-- created_at/expires_at epoch Ints → BIGINT.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS team_invites (
    token       TEXT NOT NULL,
    team_id     TEXT NOT NULL,
    email       TEXT,
    role        TEXT NOT NULL,
    created_by  TEXT NOT NULL,
    created_at  BIGINT NOT NULL,
    expires_at  BIGINT NOT NULL,
    accepted    BOOLEAN NOT NULL,
    accepted_by TEXT
);

CREATE INDEX IF NOT EXISTS team_invites_team_index ON team_invites (team_id);
CREATE INDEX IF NOT EXISTS team_invites_token_index ON team_invites (token);

-- ---------------------------------------------------------------------------
-- migrations — versioned-schema bookkeeping (plan T2.2). The legacy relation
-- was {id: String, applied_at: Int}; per the plan, applied_at is a real
-- Postgres timestamp. `leankg migrate` is the only writer.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS migrations (
    id         TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- knowledge_entries — no PK in the legacy engine, none here (id is a UUID,
-- unique per
-- writer, but the legacy :put keys the full tuple). tags is a JSON string
-- (entry.tags.clone() — db/mod.rs:848) → JSONB. created_at/updated_at epoch
-- Ints → BIGINT. Indexes mirror the four legacy ::index statements.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS knowledge_entries (
    id               TEXT NOT NULL,
    knowledge_type   TEXT NOT NULL,
    title            TEXT NOT NULL,
    content          TEXT NOT NULL,
    element_qualified TEXT,
    user_story_id    TEXT,
    feature_id       TEXT,
    tags             JSONB NOT NULL DEFAULT '[]'::jsonb,
    environment      TEXT NOT NULL,
    branch           TEXT,
    author           TEXT NOT NULL,
    created_at       BIGINT NOT NULL,
    updated_at       BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS knowledge_entries_type_index ON knowledge_entries (knowledge_type);
CREATE INDEX IF NOT EXISTS knowledge_entries_element_index ON knowledge_entries (element_qualified);
CREATE INDEX IF NOT EXISTS knowledge_entries_env_index ON knowledge_entries (environment);
CREATE INDEX IF NOT EXISTS knowledge_entries_author_index ON knowledge_entries (author);
-- Slice 7 audit (2026-08-06): the only `knowledge_entries` exact-match
-- path is `id = $id` (update_knowledge / delete_knowledge at db/mod.rs:894
-- and :rm at 921). Without this index every update/delete is a sequential
-- scan. The legacy comment notes `id` is "unique per writer" — promote that
-- intent to a real constraint; it also makes ON CONFLICT (id) DO UPDATE
-- viable if a future writer ever double-puts.
CREATE UNIQUE INDEX IF NOT EXISTS knowledge_entries_id_uniq ON knowledge_entries (id);

-- FR-ZCP-05 bridge tier (mirrors 007_trgm_fuzzy.sql): trigram indexes over
-- knowledge content for the L2 fuzzy/prefix rung.
CREATE INDEX IF NOT EXISTS knowledge_entries_title_trgm_idx
    ON knowledge_entries USING gin (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS knowledge_entries_content_trgm_idx
    ON knowledge_entries USING gin (content gin_trgm_ops);

-- ---------------------------------------------------------------------------
-- feature_workflow_links — no PK in the legacy engine (composite tuple key), none here.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS feature_workflow_links (
    feature_id  TEXT NOT NULL,
    workflow_id TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS feature_workflow_links_feature_id_index ON feature_workflow_links (feature_id);

-- ---------------------------------------------------------------------------
-- incidents — no PK in the legacy engine, none here. affected_services/tags are Vec<String>
-- serialized with serde_json (db/mod.rs:1191) → JSONB. occurred_at/resolved_at
-- epoch Ints → BIGINT. Indexes mirror the three legacy ::index statements.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS incidents (
    id                 TEXT NOT NULL,
    env                TEXT NOT NULL,
    title              TEXT NOT NULL,
    severity           TEXT NOT NULL,
    occurred_at        BIGINT NOT NULL,
    resolved_at        BIGINT,
    root_cause         TEXT NOT NULL,
    resolution         TEXT NOT NULL,
    affected_services  JSONB NOT NULL DEFAULT '[]'::jsonb,
    trigger_pattern    TEXT,
    prevention         TEXT,
    tags               JSONB NOT NULL DEFAULT '[]'::jsonb,
    author             TEXT NOT NULL,
    linked_ticket      TEXT
);

CREATE INDEX IF NOT EXISTS incidents_env_index ON incidents (env);
CREATE INDEX IF NOT EXISTS incidents_severity_index ON incidents (severity);
CREATE INDEX IF NOT EXISTS incidents_author_index ON incidents (author);

-- ---------------------------------------------------------------------------
-- index_inventory — KEYED in the legacy engine (key => ...), single row per key
-- (inventory.rs:10-24, :put with key => computed_at). PRIMARY KEY on key.
-- estimated_* are Ints → BIGINT. *json columns are serde_json strings → JSONB.
-- computed_at is epoch-seconds text (now_iso(), inventory.rs:187) — kept TEXT
-- to preserve semantics.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS index_inventory (
    key                       TEXT PRIMARY KEY,
    computed_at               TEXT NOT NULL,
    total_elements            BIGINT NOT NULL,
    total_relationships       BIGINT NOT NULL,
    total_vectors             BIGINT NOT NULL,
    total_documents           BIGINT NOT NULL,
    total_doc_sections        BIGINT NOT NULL,
    elements_by_type_json     JSONB NOT NULL DEFAULT '{}'::jsonb,
    relationships_by_type_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    vectors_by_type_json      JSONB NOT NULL DEFAULT '{}'::jsonb,
    estimated_vector_bytes    BIGINT NOT NULL,
    estimated_hnsw_bytes      BIGINT NOT NULL,
    notes                     TEXT NOT NULL
);

-- ---------------------------------------------------------------------------
-- api_keys — no PK in the legacy engine (keys.rs:50), none here (id is the
-- natural lookup).
-- All timestamps are epoch-seconds TEXT (chrono_timestamp(), keys.rs:266) —
-- kept TEXT, NOT converted to TIMESTAMPTZ, to preserve semantics. The legacy
-- dialect's null-equality semantics (`revoked_at = null` matches NULL,
-- keys.rs:201) translate to `revoked_at IS NULL` in the translator.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS api_keys (
    id           TEXT NOT NULL,
    name         TEXT NOT NULL,
    key_hash     TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
);

CREATE INDEX IF NOT EXISTS api_keys_id_index ON api_keys (id);
-- The translator's `pk_for_table("api_keys") = key_hash` drives `:put
-- api_keys` → `ON CONFLICT ("key_hash")`. That needs a UNIQUE index on
-- key_hash or every api_keys write fails ("no unique or exclusion
-- constraint matching the ON CONFLICT specification").
CREATE UNIQUE INDEX IF NOT EXISTS api_keys_key_hash_uniq ON api_keys (key_hash);

-- ---------------------------------------------------------------------------
-- embedding_state — KEYED in the legacy engine (qualified_name => usearch_key),
-- single row per qualified_name → PRIMARY KEY. usearch_key is legacy
-- schema-compat (written as 0, risk note 20) → BIGINT NOT NULL DEFAULT 0.
-- embedded_at is a free-form String in the legacy schema (state.rs:25) — kept
-- TEXT. Indexes mirror the legacy ::index statements (usearch_key, state).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS embedding_state (
    qualified_name TEXT PRIMARY KEY,
    usearch_key    BIGINT NOT NULL DEFAULT 0,
    content_hash   TEXT NOT NULL,
    state          TEXT NOT NULL,
    embedded_at    TEXT
);

CREATE INDEX IF NOT EXISTS embedding_state_usearch_key_index ON embedding_state (usearch_key);
CREATE INDEX IF NOT EXISTS embedding_state_state_index ON embedding_state (state);

-- ---------------------------------------------------------------------------
-- embedding_vectors — KEYED in the legacy engine (qualified_name => vector)
-- → PRIMARY KEY. VEC_DIM = 384 (plan D5). HNSW index mirrors the legacy
-- `::hnsw create embedding_vectors:vec_idx` (dim 384, distance Cosine) —
-- pgvector cosine
-- opclass, m=16 / ef_construction=200 per plan T2.4.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS embedding_vectors (
    qualified_name TEXT PRIMARY KEY,
    vec            vector(384) NOT NULL
);

CREATE INDEX IF NOT EXISTS embedding_vectors_vec_hnsw_idx
    ON embedding_vectors USING hnsw (vec vector_cosine_ops)
    WITH (m = 16, ef_construction = 200);

-- ---------------------------------------------------------------------------
-- index_hashes — KEYED in the legacy engine (path => hash; auto-created on
-- first :put, no :create DDL exists — inventory §1.3). PRIMARY KEY on path.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS index_hashes (
    path TEXT PRIMARY KEY,
    hash TEXT NOT NULL
);

-- ---------------------------------------------------------------------------
-- query_cache is intentionally ABSENT — dropped per plan decision D2 (moka
-- in-memory L1 cache only; src/graph/persistent_cache.rs deleted in T5.3).
-- ---------------------------------------------------------------------------
