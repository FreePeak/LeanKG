-- Cleanup for DBs where 002 was already applied before the qwen 2560-d HNSW
-- index was removed from 002 (fresh DBs now never create it).
--
-- The qwen index never builds on pgvector < 0.9 (2000-d HNSW cap) and is
-- never queried locally, so dropping it is safe. The usearch_key DEFAULT is
-- cosmetic — code always writes usearch_key explicitly.
ALTER TABLE embedding_state_qwen3_emb_4b_2560 ALTER COLUMN usearch_key DROP DEFAULT;

DROP INDEX IF EXISTS embedding_vectors_qwen3_emb_4b_2560_vec_hnsw_idx;
