-- Multi-model embedding registry + table-per-model vector collections.
-- Legacy `embedding_vectors` / `embedding_state` remain the default BGE (384-d).

CREATE TABLE IF NOT EXISTS embedding_models (
    model_id     TEXT PRIMARY KEY,
    provider     TEXT NOT NULL,
    model_name   TEXT NOT NULL,
    dimensions   INT NOT NULL,
    distance     TEXT NOT NULL DEFAULT 'cosine',
    config_json  JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS embedding_active (
    scope    TEXT PRIMARY KEY DEFAULT 'default',
    model_id TEXT NOT NULL REFERENCES embedding_models(model_id)
);

INSERT INTO embedding_models (model_id, provider, model_name, dimensions)
VALUES
    ('bge-small-en-v1.5-384', 'local', 'bge-small-en-v1.5', 384),
    ('qwen3-emb-4b-2560', 'openai', 'Qwen/Qwen3-Embedding-4B', 2560),
    ('jina-embeddings-v3-1024', 'openai', 'jina-embeddings-v3', 1024)
ON CONFLICT (model_id) DO NOTHING;

INSERT INTO embedding_active (scope, model_id)
VALUES ('default', 'bge-small-en-v1.5-384')
ON CONFLICT (scope) DO NOTHING;

-- Qwen 2560-d collection (table-per-model when dims differ).
--
-- No vector index here: pgvector (0.8.x) rejects both HNSW and ivfflat above
-- 2000 dimensions, so a 2560-d index cannot be created — and it is never
-- queried locally (Qwen is a remote openai provider, not the default bge-384
-- collection). Keeping a CREATE INDEX on this table would break fresh-DB
-- `leankg migrate` with "column cannot have more than 2000 dimensions".
-- A local scan is the correct access path for this collection.
CREATE TABLE IF NOT EXISTS embedding_vectors_qwen3_emb_4b_2560 (
    qualified_name TEXT PRIMARY KEY,
    vec            vector(2560) NOT NULL
);

CREATE TABLE IF NOT EXISTS embedding_state_qwen3_emb_4b_2560 (
    qualified_name TEXT PRIMARY KEY,
    usearch_key    BIGINT NOT NULL,
    content_hash   TEXT NOT NULL,
    state          TEXT NOT NULL,
    embedded_at    TEXT
);

-- Jina 1024-d collection (free API smoke path)
CREATE TABLE IF NOT EXISTS embedding_vectors_jina_embeddings_v3_1024 (
    qualified_name TEXT PRIMARY KEY,
    vec            vector(1024) NOT NULL
);

CREATE INDEX IF NOT EXISTS embedding_vectors_jina_embeddings_v3_1024_vec_hnsw_idx
    ON embedding_vectors_jina_embeddings_v3_1024 USING hnsw (vec vector_cosine_ops)
    WITH (m = 16, ef_construction = 200);

CREATE TABLE IF NOT EXISTS embedding_state_jina_embeddings_v3_1024 (
    qualified_name TEXT PRIMARY KEY,
    usearch_key    BIGINT NOT NULL DEFAULT 0,
    content_hash   TEXT NOT NULL,
    state          TEXT NOT NULL,
    embedded_at    TEXT
);
