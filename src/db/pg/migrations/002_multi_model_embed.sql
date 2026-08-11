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

-- Qwen 2560-d collection (table-per-model when dims differ)
CREATE TABLE IF NOT EXISTS embedding_vectors_qwen3_emb_4b_2560 (
    qualified_name TEXT PRIMARY KEY,
    vec            vector(2560) NOT NULL
);

-- pgvector < 0.9 hard-caps HNSW at 2000 dims, so the 2560-d index only builds
-- on pgvector >= 0.9 (the qwen collection itself always stores fine). The
-- runtime ANN path (`ensure_model_collections`) tolerates a missing index, so
-- swallow the build error on older pgvector and note the skip.
DO $$
BEGIN
    CREATE INDEX IF NOT EXISTS embedding_vectors_qwen3_emb_4b_2560_vec_hnsw_idx
        ON embedding_vectors_qwen3_emb_4b_2560 USING hnsw (vec vector_cosine_ops)
        WITH (m = 16, ef_construction = 200);
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'qwen 2560-d HNSW index skipped (pgvector dim cap): %', SQLERRM;
END $$;

CREATE TABLE IF NOT EXISTS embedding_state_qwen3_emb_4b_2560 (
    qualified_name TEXT PRIMARY KEY,
    usearch_key    BIGINT NOT NULL DEFAULT 0,
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
