-- Gemini embeddings via Google's OpenAI-compatible endpoint.
-- Default output dimensionality for both gemini-embedding-001 and
-- gemini-embedding-2 is 3072, so the collections below are vector(3072).
-- runtime ensure_model_collections() creates the same tables idempotently
-- when this migration already ran on an existing DB (or for CozoDB).

INSERT INTO embedding_models (model_id, provider, model_name, dimensions)
VALUES
    ('gemini-embedding-2-3072',    'openai', 'gemini-embedding-2', 3072),
    ('gemini-embedding-001-3072',  'openai', 'gemini-embedding-001', 3072)
ON CONFLICT (model_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS embedding_vectors_gemini_embedding_2_3072 (
    qualified_name TEXT PRIMARY KEY,
    vec            vector(3072) NOT NULL
);

-- pgvector < 0.9 hard-caps HNSW at 2000 dims, so the 3072-d index only builds
-- on pgvector >= 0.9 (the collection itself always stores fine). The runtime
-- ANN path (`ensure_model_collections`) tolerates a missing index, so swallow
-- the build error on older pgvector and note the skip (mirrors 002).
DO $$
BEGIN
    CREATE INDEX IF NOT EXISTS embedding_vectors_gemini_embedding_2_3072_vec_hnsw_idx
        ON embedding_vectors_gemini_embedding_2_3072 USING hnsw (vec vector_cosine_ops)
        WITH (m = 16, ef_construction = 200);
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'gemini-embedding-2 3072-d HNSW index skipped (pgvector dim cap): %', SQLERRM;
END $$;

CREATE TABLE IF NOT EXISTS embedding_state_gemini_embedding_2_3072 (
    qualified_name TEXT PRIMARY KEY,
    usearch_key    BIGINT NOT NULL DEFAULT 0,
    content_hash   TEXT NOT NULL,
    state          TEXT NOT NULL,
    embedded_at    TEXT
);

CREATE TABLE IF NOT EXISTS embedding_vectors_gemini_embedding_001_3072 (
    qualified_name TEXT PRIMARY KEY,
    vec            vector(3072) NOT NULL
);

DO $$
BEGIN
    CREATE INDEX IF NOT EXISTS embedding_vectors_gemini_embedding_001_3072_vec_hnsw_idx
        ON embedding_vectors_gemini_embedding_001_3072 USING hnsw (vec vector_cosine_ops)
        WITH (m = 16, ef_construction = 200);
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'gemini-embedding-001 3072-d HNSW index skipped (pgvector dim cap): %', SQLERRM;
END $$;

CREATE TABLE IF NOT EXISTS embedding_state_gemini_embedding_001_3072 (
    qualified_name TEXT PRIMARY KEY,
    usearch_key    BIGINT NOT NULL DEFAULT 0,
    content_hash   TEXT NOT NULL,
    state          TEXT NOT NULL,
    embedded_at    TEXT
);
