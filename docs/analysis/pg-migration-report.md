# LeanKG: CozoDB → PostgreSQL + pgvector Migration Report

**Date:** 2026-08-05
**Status:** Phases 0–8 core DONE — Postgres-only binary, `cozo` dependency removed.
**Plan:** [docs/plan-migrate-cozo-to-postgres-pgvector.md](../plan-migrate-cozo-to-postgres-pgvector.md)
**Decisions:** D1–D5 (plan §7)

---

## 1. What was migrated

### 1.1 Query translation

The CozoDB Datalog query surface was translated to SQL by a single mechanical
translator at `src/db/pg/translate.rs` (~3.6k lines). It covers the ~115 query
shapes in `docs/analysis/cozo-query-inventory.md` §2:

- **Reads**: `?[cols] := *rel[...]` (positional + attribute syntax), `==`/`=`
  equality, null equality (`col = null` → `IS NULL`), range filters, `in [...]`
  lists, `regex_matches`, `str_includes`, `str_contains`, `starts_with`,
  top-level and parenthesized `or` chains, `not *rel[...]` (NOT EXISTS),
  `:limit` / `:offset`, head-alias expressions (`span = line_end - line_start`),
  positional-alias resolution to real columns.
- **Writes**: `:put` → `INSERT ... ON CONFLICT DO UPDATE` (keyed tables) or
  plain `INSERT` (non-keyed), `:rm` → `DELETE`, `:delete ... where`,
  `:create` / `:replace` / `::index` / `::hnsw` / `PRAGMA` / `VACUUM` →
  no-op DDL (the schema is pre-created by `schema.sql`).
- **ANN**: `~embedding_vectors:vec_idx { query, k, ef, bind_distance }` →
  `ORDER BY vec <-> $1 LIMIT $2` with `SET LOCAL hnsw.ef_search` via a GUC.
- **`::relations`** → `information_schema.tables` introspection.

### 1.2 Schema

`src/db/pg/schema.sql` defines the 16 tables (query_cache dropped per D2),
JSONB columns for `metadata`/`tags`/`members`/`deploy_envs`, and the pgvector
`embedding_vectors.vec vector(384)` + HNSW index (dim = `VEC_DIM` const, D5).
`src/db/pg/migrations.rs` runs versioned migrations (idempotent).

### 1.3 Backend

`src/db/backend.rs` hosts `PostgresBackend` with:
- lazy connection pool (`LEANKG_PG_POOL_SIZE`, default 5) behind a hand-rolled
  `VecDeque<Client>` + Condvar pool,
- read-only mode (`LEANKG_PG_URL_RO` semantics via
  `default_transaction_read_only = on`, T6.1),
- PG advisory lock for exclusive `leankg index` (`LEANKG_PG_LOCK=0` disables),
- `import_relations` → batched COPY + `ON CONFLICT` upsert (T7.1).

## 2. Phase 8 cleanup (this change)

Removed everything CozoDB:

| Item | What |
|------|------|
| `cozo` Cargo.toml dep | deleted (with `storage-rocksdb` feature) |
| `redis` Cargo.toml dep + `src/embeddings/redis_store.rs` | deleted (Redis HNSW side-store unused; PG is the only vector store) |
| `DbBackend` trait + `CozoBackend` shim | deleted — `SharedDb` is now `Arc<PostgresBackend>`, `run_script` is an inherent method |
| `LEANKG_DB_ENGINE` | deleted everywhere — Postgres is the only engine |
| `src/db/schema.rs` cozo remnants | Datalog DDL, `init_db_cozo`/`init_db_readonly_cozo`, `run_script_cozo`, `mutability_for`, `StorageEngine`, RocksDB tuning, `CozoDb` — all removed |
| `src/graph/persistent_cache.rs` | deleted (D2 — moka L1 is the only cache; `with_persistence` → `new`) |
| RocksDB central-path probing in MCP server | removed (auto-index now checks "has elements", not a file) |
| `arg2` salt RNG | fixed via `rand_core` `getrandom` feature (was transitively enabled by cozo) |

The `DataValue`/`NamedRows`/`Num` positional-row contract the codebase consumes
lives on as a self-contained `src/db/value.rs` (no cozo dependency).

**Result:** `grep cozo` in `Cargo.toml` = 0; `grep "cozo::" src/` = 0;
`LEANKG_DB_ENGINE` = 0 occurrences.

## 3. Verification

- `cargo test --release --lib`: **936 passed, 0 failed**.
- `cargo test --release --lib --features embeddings`: green.
- `cargo check --tests`: 0 errors (all integration-test targets compile).
- Container-gated tests (`--test-threads=1`, dev container `leankg-pg-phase0`):
  - `pg_schema_test`: 6/6
  - `pg_translate_parity_test`: 11/11 (cozo comparison arm removed — PG-only
    execution assertions now)
  - `pg_phase4_vector`, `pg_phase7_bulk`, `pg_phase6_scaling`,
    `pg_regression_tools`: pass (PG-only tool sweep).

## 4. Performance

| Path | Result |
|------|--------|
| Bulk embed (COPY) | **7,695–9,579 v/s** (target ≥ 700) — T7.1 |
| HNSW ANN (pgvector) | **~4 ms** on dev data — Phase 0 spike |
| Translator overhead | per-query string→SQL, negligible vs round-trip |

## 5. Parity results

Phase 5.5 regression reported 26/0/0 MCP tools (PASS/DIFF/FAIL) on PG vs cozo.
The parity test's cozo arm was removed in Phase 8 (the shim no longer exists);
each parity test now asserts the translator produces correct SQL + rows on PG
directly.

## 6. Env vars

| Var | Purpose | Default |
|-----|---------|---------|
| `LEANKG_PG_URL` | Postgres connection URL (**required**) | — |
| `LEANKG_PG_POOL_SIZE` | pool size (clamped ≥ 1) | 5 |
| `LEANKG_PG_LOCK` | `0` disables the index advisory lock | on |
| `LEANKG_EMBED_COPY` | `0` opts out of COPY bulk path | on |
| `LEANKG_EMBED_BULK_REINDEX_THRESHOLD` | drop/recreate HNSW after N rows | 100k |
| `LEANKG_HNSW_M` / `LEANKG_HNSW_EF_CONST` / `LEANKG_HNSW_EF` | pgvector HNSW knobs | 16/20/100 |

## 7. Deferred (Phase 9 / ops)

- Docker/Render deploy of the Postgres backend (T8.1–8.3) — deferred per scope.
- workspace-be end-to-end `semantic_search` recall@k ≥ 98% (T9.2b).
- The parity test's cozo arm removal is complete; a follow-up can restore
  golden-SQL assertions if desired.
