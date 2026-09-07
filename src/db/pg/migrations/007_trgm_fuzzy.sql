-- FR-ZCP-05: pg_trgm fuzzy bridge tier for the L2 keyword rung (PRD v4.3.0).
--
-- Ships trigram fuzzy + prefix matching BEFORE full-text search:
--   * GIN trigram indexes on code_elements(name, qualified_name) and
--     knowledge_entries(title, content) — serve wildcard ILIKE recall and
--     the `%` / `<%` similarity operators without a sequential scan.
--   * One b-tree text_pattern_ops index on code_elements(name) for
--     anchored-prefix LIKE, usable regardless of database collation.
--
-- The wave-2 FR-ZCP-03 capability router calls this tier through
-- DbBackend::fuzzy_find_elements / suggest_element_names (src/db/backend.rs).
--
-- pg_trgm lives in postgresql-contrib and CAN be absent on stripped-down
-- builds. Degradation, not failure: the install is wrapped in EXCEPTION
-- with a NOTICE (mirrors 003_gemini_embed.sql's guarded HNSW builds) and
-- the GIN indexes degrade with it. The Rust seam catches the resulting
-- undefined-function (SQLSTATE 42883) errors and falls back to ILIKE-only
-- recall, so a missing extension costs ranking quality, never availability.
-- pg_trgm is a trusted extension, so the database owner can install it; on
-- a locked-down host that refuses the install, the EXCEPTION below records
-- the degradation and the seam fallback covers resolution gaps (the
-- already-installed-elsewhere case no-ops via IF NOT EXISTS).
--
-- ORDERING INVARIANT: this migration indexes code_elements and
-- knowledge_entries, both created by 001_schema (embedded in schema.sql).
-- The runner (run_migrations, src/db/pg/migrations.rs) applies MIGRATIONS
-- strictly in ledger order 001..007, so 007 can only ever run after 001
-- created the base tables. It MUST NOT be reordered or applied standalone
-- to a schema whose ledger is missing 001..006.

DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'FR-ZCP-05: pg_trgm unavailable (%); L2 fuzzy bridge degrades to ILIKE-only', SQLERRM;
END $$;

-- Anchored-prefix scan: `name LIKE 'Foo%'` (case-sensitive prefix) served
-- by an index scan. text_pattern_ops (not the default collation opclass) so
-- the index is usable whatever the database collation is. NOT
-- extension-gated — this one always applies.
CREATE INDEX IF NOT EXISTS code_elements_name_text_pattern_idx
    ON code_elements (name text_pattern_ops);

-- GIN trigram indexes (extension-gated: skipped with a NOTICE when pg_trgm
-- is absent — same guarded pattern as 003_gemini_embed.sql's HNSW builds).
DO $$
BEGIN
    CREATE INDEX IF NOT EXISTS code_elements_name_trgm_idx
        ON code_elements USING gin (name gin_trgm_ops);
    CREATE INDEX IF NOT EXISTS code_elements_qualified_name_trgm_idx
        ON code_elements USING gin (qualified_name gin_trgm_ops);
    CREATE INDEX IF NOT EXISTS knowledge_entries_title_trgm_idx
        ON knowledge_entries USING gin (title gin_trgm_ops);
    CREATE INDEX IF NOT EXISTS knowledge_entries_content_trgm_idx
        ON knowledge_entries USING gin (content gin_trgm_ops);
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'FR-ZCP-05: trgm GIN indexes skipped (%); fuzzy recall degrades to ILIKE-only', SQLERRM;
END $$;
