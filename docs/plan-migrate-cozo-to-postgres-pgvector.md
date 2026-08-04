# LeanKG: Migrate CozoDB → PostgreSQL + pgvector

**Status:** In progress — Phase 3 (SQL translator) underway. Decisions D1–D5 resolved (2026-08-04).
**Target version:** v0.20.0
**Date:** 2026-08-04
**Scope:** Replace the CozoDB data layer (graph store + vector HNSW) with PostgreSQL 18+ (relational) + pgvector (ANN), enabling horizontally-scalable server deployments.

> **Progress tracker (2026-08-04):** Phases 0–2 **done + verified** (parity spike, DbBackend trait + Arc threading, schema+migrations; see §9). Phase 3 (translator) **in progress**. Phases 4–8 not started. Work happens on branch `worktree-leankg-pg-migration` (git worktree); do NOT touch the prod containers `leankg-leankg-1` (:9699) / `leankg-enterprise-cozoserver-1`. Dev Postgres runs as container `leankg-pg-phase0` (pgvector pg18, host port 5433).

---

## 1. Why

CozoDB is the only embedded, transactional, graph-Datalog-vector store in the stack. It served LeanKG well as a single-binary local tool, but blocks the server-scaling goal:

| Concern | CozoDB today | After (Postgres + pgvector) |
|---|---|---|
| Community/maintenance | Issue #301 "Is cozo still being maintained?" (Dec 2025) — slow releases, 0.7.6 last | Postgres: massive community, 3 major releases/yr |
| Multi-node HA | `cozoserver` sidecar (Dockerfile.cozoserver) is experimental, single-writer-per-path RocksDB, read-only replicas don't exist (only SQLite RO, RocksDB `init_db_readonly` is a same-handle workaround, `src/db/schema.rs:114-120`) | RDS / Neon / Supabase: replicas, failover, connection pooling built-in |
| Vector search | CozoDB native `::hnsw` on `embedding_vectors` (`~embedding_vectors:vec_idx`), single-writer rebuild drops index mid-run (embed-during-serve caveat) | pgvector HNSW index, concurrent reads + index rebuild via `REINDEX CONCURRENTLY` |
| Ops | RocksDB tuning knobs are log-only (`LEANKG_ROCKSDB_*`, schema.rs:215-226 "intent not applied") | Real Postgres knobs, `EXPLAIN ANALYZE`, standard tooling |
| Hiring/knowledge | Datalog — niche | SQL + pgvector — commodity |

**What does NOT change:** graph algorithms (`shortest_path`, impact radius) are implemented in Rust (`src/graph/query.rs`), not Datalog. Cozo is only a relational store for nodes/edges + a filter layer. The vector HNSW is a single query shape. Both translate 1:1 to SQL.

---

## 2. Current state (measured, 2026-08-04)

### 2.1 Cozo surface area (non-test code)

| File | Datalog query strings | Table access |
|---|---|---|
| `src/graph/query.rs` (GraphEngine) | ~100 | code_elements, relationships, business_logic, incidents, knowledge_entries, feature_workflow_links, query_cache, index_inventory, api_keys |
| `src/db/mod.rs` | 41 | business_logic, context_metrics, knowledge_entries, feature_workflow_links, incidents, service_metadata, teams, team_invites |
| `src/db/schema.rs` | 17 (DDL) + `::index` ×18 | 10 tables + 18 indexes |
| `src/embeddings/build.rs` | 6 (write) + `~vec_idx` (read) | embedding_vectors, embedding_state |
| `src/embeddings/state.rs` | 5 | embedding_state |
| `src/ontology/query.rs` | 8 | code_elements, relationships, business_logic, knowledge_entries |
| `src/mcp/tracking_db.rs` | 3 | context_metrics |
| `src/mcp/handler.rs` | 6 | (via GraphEngine) |
| `src/graph/clustering.rs` | 3 | code_elements, relationships |
| `src/graph/inventory.rs` | 2 | index_inventory |
| `src/retrieval/pipeline.rs` | 1 | **embedding_vectors (ANN)** |
| `src/indexer/content_hash.rs` | 2 | index_hashes |
| `src/doc_indexer/paths.rs` | 2 (tests) | — |
| `src/main.rs`, `src/graph/persistent_cache.rs`, `src/embeddings/control.rs`, `src/db/write_bus.rs`, `src/db/keys.rs`, `src/mcp/token_budget.rs` | 1 each | query_cache, api_keys |

**Operator usage** (real queries only, via parser): `:limit` ×21, `:offset` ×5, `:order` ×3, `~vec_idx` ×3. **No `:group`/`:join`/recursion in query strings** — group/join happen in Rust. All queries are single-relation scans + equality filters. This is the key simplification: SQL translation is mechanical.

### 2.2 Table inventory (canonical DDL)

| Table | Columns (cozo) | Notes |
|---|---|---|
| `code_elements` | qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified?, cluster_id?, cluster_label?, metadata, env, ontology_layer | 13 cols, `:index` ×4 (file_path, qualified_name, element_type, parent_qualified) |
| `relationships` | source_qualified, target_qualified, rel_type, confidence, metadata, env | 6 cols, `:index` ×3 (rel_type, target, source) |
| `business_logic` | element_qualified, description, user_story_id?, feature_id? | |
| `context_metrics` | tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements?, total_expected?, f1_score?, query_pattern?, query_file?, query_depth?, success, is_deleted | index on tool_name, timestamp, project_path |
| `query_cache` | cache_key, value_json, created_at, ttl_seconds, tool_name, project_path, metadata | **DROPPED (D2)** — moka L1 only |
| `service_metadata` | service_name, env, team?, on_call?, repo_url?, language?, health_endpoint?, slo_p99_ms?, incident_count, last_incident?, tags, version?, deploy_envs, created_at, updated_at | index (service_name, env) |
| `teams` | id, name, description, owner_id, created_at, updated_at, graph_read_users, graph_write_users, members | index owner_id |
| `team_invites` | token, team_id, email?, role, created_by, created_at, expires_at, accepted, accepted_by? | index (team_id, token) |
| `migrations` | id, applied_at | |
| `knowledge_entries` | id, knowledge_type, title, content, element_qualified?, user_story_id?, feature_id?, tags, environment, branch?, author, created_at, updated_at | index ×4 |
| `feature_workflow_links` | feature_id, workflow_id | index feature_id |
| `incidents` | id, env, title, severity, occurred_at, resolved_at?, root_cause, resolution, affected_services, trigger_pattern?, prevention?, tags, author, linked_ticket? | index severity |
| `index_inventory` | (const in graph/inventory.rs) | |
| `api_keys` | id, name, key_hash, created_at, last_used_at?, revoked_at? | (db/keys.rs) |
| `embedding_state` | qualified_name => usearch_key: Int, content_hash, state, embedded_at | keyed table (qualified_name PK) |
| `embedding_vectors` | qualified_name, vec (dim 384) | HNSW via `::hnsw create embedding_vectors:vec_idx { dim: 384 }` |
| `index_hashes` | path, hash | keyed by path |

Total: **16 tables** (query_cache dropped per D2).

### 2.3 Cozo API surface used by callers

| Entry point | Signature | Callers |
|---|---|---|
| `run_script(db, query, params)` | `&CozoDb, &str, BTreeMap<String, Json>` → `NamedRows` | Everything (thin wrapper over `cozo::DbInstance::run_script`) |
| `run_raw_query` (GraphEngine) | `&self, &str, params` | graph/query.rs ×6, content_hash ×2, mcp/handler ×2, mcp/tools ×1, web/handlers ×1 |
| `init_db(path)` / `init_db_readonly(path)` | → `CozoDb` | main.rs ×8, mcp/server, web/mod, api/mod, ctags_export, pack/mod, graph/query `open_readonly` |
| `CozoDb` type | `type CozoDb = cozo::DbInstance` | 26 files |
| `db.run_script` (2-arg internal) | — | rare |

### 2.4 Semantic-search flow (deepest cozo coupling)

```
MCP semantic_search (handler.rs:2237)
 └─ embeddings_index_available? (state::has_any → embedding_state LIMIT 1)
     └─ run_hnsw_semantic_search (handler.rs:4867)
         └─ SemanticRetrievalPipeline::retrieve (retrieval/pipeline.rs:234)
             ├─ embed query (fastembed, BGE-small-en-v1.5, dim 384)
             ├─ hnsw_retrieve (pipeline.rs:351)
             │   └─ ?[dist, qualified_name] := ~embedding_vectors:vec_idx {{...}}  ← THE ONLY ANN QUERY
             ├─ fetch_elements_batch (keyed QN lookup → code_elements)
             ├─ filter: worktree / env / test / node_type
             └─ cross-encoder rerank (rerank.rs)
```

- `adaptive_k()`: 50→300 by index size; `resolve_ef()`: `max(k*2, 50)`, `LEANKG_HNSW_EF` override.
- Only **1 ANN query shape** exists. `pgvector` `ORDER BY embedding <-> $q LIMIT $k` replaces it 1:1. Distance semantics: Cozo HNSW returns cosine distance; pgvector `<->` is cosine distance on normalized vectors — identical (verify in Phase 2 test).

### 2.5 Write path (bulk embed)

- `upsert_pairs_to_db` (build.rs:1307): `import_relations` bulk load for fast cold embed (~700 vec/s vs ~85 with parameterized `:put`); Redis side-store (`LEANKG_EMBED_VECTOR_STORE=redis`) already exists but **is write-only today** — no reader consumes it. Do not build on it.

---

## 3. Target architecture

### 3.1 Single service, two deps

```
leankg (single binary, Rust)
 ├─ [graph store]  PostgreSQL 18+  (nodes, edges, metrics, keys)
 │     ├─ sqlx (async, compile-time checked queries) or tokio-postgres + deadpool
 │     └─ pgvector extension (HNSW index on embedding)
 ├─ [vector store] same Postgres, `embedding` column (vector(384)) + HNSW index
 └─ [graph algorithms] unchanged Rust (src/graph/query.rs logic)
```

- Postgres connection: `LEANKG_PG_URL` env var (default `postgres://postgres@localhost/leankg`). **Required — Postgres is the only storage engine (D4).**
- No new long-running processes in-process: Postgres is external; LeanKG connects over TCP. This is the scaling win — N LeanKG instances can share one Postgres.
- **Local dev flow changed (D4):** `docker compose up postgres` before `leankg init / index / serve`. The embedded sqlite/rocksdb single-binary path is removed.

### 3.2 What stays local/embedded

- tree-sitter extraction (stateless, per-file)
- fastembed inference (CPU ONNX, stateless)
- BFS/shortest_path/impact radius (Rust, reads from Postgres)
- moka in-memory query cache (L1); **`query_cache` table dropped (D2)** — no L2 cache

### 3.3 What leaves the codebase

- `cozo = "0.7.6"` (Cargo.toml:139)
- `src/db/schema.rs` Datalog DDL + `run_script`/`run_raw_query` + `mutability_for`
- `src/vector_engine/` custom Rust HNSW (~5k lines: hnsw.rs, tier1-3, dual_write, gc, recovery, simd, memory) — **replaced by pgvector**, keeping only the engine-selection gate if desired
- `Dockerfile.cozoserver`, `docker-compose.enterprise.yml` (cozoserver sidecar)
- `src/embeddings/redis_store.rs` (write-only, superseded by pgvector)
- `index_hashes` table → Postgres (D3, uniform)
- `query_cache` table + `src/graph/persistent_cache.rs` (D2 — dropped)
- Embedded backends (sqlite/rocksdb) + `LEANKG_DB_ENGINE` env (D4 — Postgres-only; `DbBackend` trait + `CozoBackend` survive only as a migration shim, deleted in Phase 8)

---

## 4. Phased migration

**Overall: 8 phases, 1-2 weeks FT (est. 10-14 working days).**

### Phase 0 — Spike: prove pgvector distance parity (0.5 day)

- [ ] T0.1 Spin up Postgres 18 + pgvector (Docker or Neon free tier)
- [ ] T0.2 Verify `ORDER BY vec <-> $q LIMIT k` on a 10k-row sample returns same top-k as cozo `~vec_idx` for dim-384 BGE embeddings
- [ ] T0.3 Verify HNSW index build speed + `REINDEX CONCURRENTLY` works while reads run
- **Exit:** distance semantics parity confirmed; if not, use `<=>` (cosine for normalized) — record in ADR

### Phase 1 — Storage abstraction (2-3 days)

Goal: swap the physical backend without touching query logic. Introduce `DbBackend` trait with two impls: `PostgresBackend` (production, **default**) + `CozoBackend` (temporary **migration shim** — parity-test comparison only, deleted in Phase 8, D4).

- [ ] T1.1 Create `src/db/backend.rs`: `trait DbBackend { run_script / run_raw_query / … }` matching today's call surface (`run_script`, `run_raw_query`, `CozoDb`-like handle, `init_db`/`init_db_readonly`)
- [ ] T1.2 `CozoBackend` — move current `run_script`/`mutability_for`/`init_db` under the trait, no behavior change; marked migration-shim
- [ ] T1.3 Wire `GraphEngine`, `db/mod.rs`, `embeddings/*`, `retrieval/*` through the trait (replace `CozoDb` type with `Arc<dyn DbBackend>`)
- [ ] T1.4 `PostgresBackend` — connects, validates URL, runs schema; **default engine**. Local dev / CI: `docker compose up postgres` (D4)
- [ ] T1.5 `LEANKG_DB_ENGINE` = `postgres` (default) | `cozo` (migration shim only); both values removed after Phase 8
- **Exit:** `cargo test` green with postgres backend; `LEANKG_DB_ENGINE=cozo` still selects the shim for parity tests

### Phase 2 — Postgres schema + migrations (2 days)

- [ ] T2.1 `src/db/pg/schema.sql`: DDL for all 16 tables (see §2.2, query_cache dropped) + indexes + FKs (qualified_name cross-refs)
- [ ] T2.2 `src/db/pg/migrations.rs`: embedded `sqlx::migrate!` or a simple `migrations` table (mirror cozo's `migrations` relation, but use Postgres `timestamp` instead of Int)
- [ ] T2.3 Data types: cozo Int → `BIGINT`/`INTEGER`; Float → `REAL`/`DOUBLE PRECISION`; String? → nullable TEXT; Bool → `BOOLEAN`; metadata JSON string → `JSONB` (migrate `metadata`/`tags`/`members`/`deploy_envs` to JSONB — one-time schema change, verify serde code accepts JSONB)
- [ ] T2.4 `embedding_vectors`: `CREATE TABLE embedding_vectors (qualified_name TEXT PK, vec vector(384))` + `CREATE INDEX ON embedding_vectors USING hnsw (vec vector_cosine_ops)`; vector dim as single `const VEC_DIM` (D5 — 384 today, future upgrade = one-line change + re-embed)
- [ ] T2.5 `embedding_state`: mirror cozo keyed table → `(qualified_name TEXT PK, usearch_key BIGINT, content_hash TEXT, state TEXT, embedded_at TEXT)`
- [ ] T2.6 `index_inventory`, `api_keys`, `index_hashes` — move DDL from graph/inventory.rs, db/keys.rs, indexer/content_hash.rs into schema.sql
- **Exit:** `docker compose up postgres` + `leankg migrate` creates all tables; `psql \dt` matches inventory

### Phase 3 — SQL translator: `run_script` → SQL (3-4 days, the bulk)

Since every cozo query is single-relation + equality filters (+`:limit`/`:offset`/`:order`), write a **mechanical translator**, not hand-rewrites:

- [ ] T3.1 `src/db/pg/translate.rs`: parse cozo query string → table, columns, where-clauses (`= $param`), limit/offset/order → SQL `SELECT cols FROM table WHERE ... LIMIT .. OFFSET ..`
- [ ] T3.2 Handle `:put`/`:rm`/`:replace`/`:create` → `INSERT ... ON CONFLICT (pk) DO UPDATE` / `DELETE` / `CREATE TABLE IF NOT EXISTS`
- [ ] T3.3 `~embedding_vectors:vec_idx {{...}}` → `SELECT dist, qualified_name FROM (SELECT embedding <-> $1 AS dist, qualified_name FROM embedding_vectors ORDER BY embedding <-> $1 LIMIT $2) x`
- [ ] T3.4 Count queries (`count(n)`), `:group [..]` (env_query), `:order` → `SELECT count(*)`, `GROUP BY`, `ORDER BY` — the few non-trivial ones hand-write
- [ ] T3.5 `run_script` reimplemented over `PostgresBackend` calling the translator; `run_raw_query` exposed for the few raw SQL callers (web/handlers:3189)
- [ ] T3.6 Row → `serde_json::Value` mapping (keep `row[0].get_str()` etc. working via a `NamedRows`-compatible shim) so downstream code (which indexes rows positionally) does NOT change
- **Exit:** full test suite's db-dependent tests pass on the postgres backend (translator correctness == cozo semantics, verified against `LEANKG_DB_ENGINE=cozo` shim)

### Phase 4 — Vector stack swap (1-2 days)

- [ ] T4.1 Delete `src/vector_engine/` custom HNSW (hnsw.rs, tier1/2/3.rs, simd.rs, memory.rs, gc.rs, recovery.rs, dual_write.rs, gate.rs, engine.rs, bench.rs, kpi.rs) — replaced by pgvector
- [ ] T4.2 `retrieval/pipeline.rs::hnsw_retrieve` → direct SQL `ORDER BY embedding <-> $q` (no translator; it's 1 query)
- [ ] T4.3 `embeddings/build.rs::upsert_pairs_to_db` → `INSERT ... ON CONFLICT (qualified_name) DO UPDATE` batched (copy in batches of 1000; keep 8× bulk speed via `COPY` if needed)
- [ ] T4.4 `embeddings/state.rs` → upsert embedding_state alongside; `has_any` → `SELECT EXISTS(SELECT 1 FROM embedding_vectors LIMIT 1)`
- [ ] T4.5 Remove `::hnsw create` from `schema.rs`; `embedding_vectors:vec_idx` → pgvector index (already in T2.4)
- [ ] T4.6 Keep `LEANKG_HNSW_EF` → map to pgvector `hnsw.ef_search` (GUC or per-session `SET LOCAL`)
- **Exit:** `cargo test --features embeddings` green on Postgres; `semantic_search` returns same results as cozo on a golden fixture

### Phase 5 — db/mod.rs, ontology, clustering, inventory (2 days)

- [ ] T5.1 `src/db/mod.rs` (41 queries) — run through translator; verify by parity tests (run same query against cozo temp-db and Postgres temp-db, compare rows)
- [ ] T5.2 `src/ontology/query.rs` (8), `src/graph/clustering.rs` (3), `src/graph/inventory.rs` (2) — same
- [ ] T5.3 `src/mcp/tracking_db.rs`, `src/mcp/token_budget.rs`, `src/db/keys.rs` — same; delete `src/graph/persistent_cache.rs` + `query_cache` table (D2)
- [ ] T5.4 `src/mcp/handler.rs` raw queries (2) + `src/mcp/tools.rs` (1) + `src/web/handlers.rs` (1) — hand-verify
- **Exit:** all 26 cozo-touching files run through the trait; grep for `cozo::` = 0 non-test hits

### Phase 6 — Read-only + server scaling semantics (1 day)

- [ ] T6.1 `init_db_readonly`: now trivial — Postgres connection with `default_transaction_read_only = on` (true RO, replaces the RocksDB same-handle workaround)
- [ ] T6.2 `MCPServer::read_only` still enforced at tool layer (unchanged)
- [ ] T6.3 Connection pool: `deadpool-postgres` or sqlx pool; `LEANKG_PG_POOL_SIZE` (default 5), read replicas via `LEANKG_PG_URL_RO` (optional)
- [ ] T6.4 Multi-instance: `run_kg_self_test_on_startup` no longer needs single-writer lock; document that `leankg index` writes are exclusive (use `LEANKG_PG_LOCK` advisory lock on the index job to serialize writers)
- **Exit:** 2 leanKG instances → 1 Postgres, both serve reads; one writes, other sees changes (verify with `index` on instance A, query on B)

### Phase 7 — Embedding bulk-load (0.5-1 day)

- [ ] T7.1 Replace `import_relations` bulk path with `COPY` (batch 10k) or `INSERT ... ON CONFLICT` — measure v/s (target ≥ cozo's ~700 v/s)
- [ ] T7.2 Drop index during bulk, `REINDEX` after (pgvector `CREATE INDEX` is fast; `REINDEX CONCURRENTLY` for live)
- **Exit:** cold embed of workspace-be (~371k functions) on Postgres < cozo time

### Phase 8 — Deploy + docs + cleanup (1-2 days)

- [ ] T8.1 Docker: `postgres:18` + `pgvector/pgvector:pg18` image in compose; `leankg` env `LEANKG_PG_URL=...` (no engine switch — postgres is the only engine, D4)
- [ ] T8.2 Remove `Dockerfile.cozoserver`, `docker-compose.enterprise.yml`; update `docker-compose.rocksdb.yml` → `docker-compose.yml` (Postgres backend)
- [ ] T8.3 Render: swap cozoserver/rocksdb for managed Postgres (Render Postgres); verify `Dockerfile` multi-stage builds without cozo deps (rocksdb → pg native client: `openssl-sys` still needed for TLS)
- [ ] T8.4 Delete cozo from Cargo.toml; remove `src/db/schema.rs` cozo remnants; **remove `DbBackend` trait + `CozoBackend` shim + `LEANKG_DB_ENGINE`** (D4); delete `src/graph/persistent_cache.rs`; delete `src/embeddings/redis_store.rs` if unused
- [ ] T8.5 Docs: update `docs/prd.md`, README (env vars, `docker compose up postgres leankg`), `docs/enterprise-docker.md` → Postgres guide
- [ ] T8.6 `docs/analysis/` migration report (what was translated, parity results, perf numbers)
- **Exit:** `docker compose up` runs LeanKG against Postgres; `cargo test` all green; README reflects Postgres-first

---

## 5. TODOs (flat list, in order)

### P0 — Spike & foundation
1. [ ] Phase 0: pgvector distance-parity spike (T0.1-T0.3)
2. [ ] Create `src/db/backend.rs` `DbBackend` trait
3. [ ] Move current `run_script`/`init_db` under `CozoBackend` (migration shim)
4. [ ] Thread `Arc<dyn DbBackend>` through GraphEngine + db/mod + embeddings + retrieval
5. [ ] `PostgresBackend` (default engine) + `LEANKG_DB_ENGINE=cozo` migration shim (D4)

### P1 — Postgres schema
6. [ ] `src/db/pg/schema.sql` for 16 tables + indexes + FKs (query_cache dropped — D2)
7. [ ] `src/db/pg/migrations.rs` (sqlx migrate or custom)
8. [ ] JSONB conversion for metadata/tags/members/deploy_envs
9. [ ] `embedding_vectors` + pgvector HNSW index (dim 384, cosine; dim as `const VEC_DIM` — D5)
10. [ ] `embedding_state`, `index_inventory`, `api_keys`, `index_hashes` DDL
11. [ ] `leankg migrate` subcommand

### P2 — Query translation
12. [ ] `src/db/pg/translate.rs` (cozo query string → SQL)
13. [ ] `:put`/`:rm`/`:replace`/`:create` → upsert/delete/DDL
14. [ ] `~vec_idx` → `ORDER BY <->` translation
15. [ ] count/group/order queries hand-written
16. [ ] `run_script` + `run_raw_query` over PostgresBackend
17. [ ] `NamedRows` shim (positional row access preserved)
18. [ ] Parity tests: cozo vs Postgres on identical data

### P3 — Vector swap
19. [ ] Delete `src/vector_engine/` custom HNSW
20. [ ] `hnsw_retrieve` → direct SQL
21. [ ] `upsert_pairs_to_db` → batched upsert/COPY
22. [ ] `embedding_state` upserts + `has_any` → EXISTS
23. [ ] `LEANKG_HNSW_EF` → pgvector ef_search

### P4 — Remaining modules
24. [ ] db/mod.rs (41 queries)
25. [ ] ontology/query.rs (8)
26. [ ] clustering.rs (3) + inventory.rs (2)
27. [ ] tracking_db.rs + token_budget.rs + keys.rs; delete persistent_cache.rs + query_cache table (D2)
28. [ ] mcp/handler.rs + tools.rs + web/handlers.rs raw queries
29. [ ] grep `cozo::` → 0 non-test hits

### P5 — Server semantics
30. [ ] `init_db_readonly` → Postgres RO transaction
31. [ ] Connection pool + `LEANKG_PG_POOL_SIZE`
32. [ ] Multi-instance read/write verification
33. [ ] Advisory lock for exclusive `leankg index`

### P6 — Bulk embed
34. [ ] `COPY`-based bulk load (≥ cozo 700 v/s)
35. [ ] Drop/reindex strategy

### P7 — Deploy + docs
36. [ ] Docker: postgres:16 + pgvector in compose; local dev flow = `docker compose up postgres` first (D4)
37. [ ] Remove cozoserver compose files
38. [ ] Render managed Postgres
39. [ ] Remove cozo dep from Cargo.toml; delete DbBackend trait + CozoBackend + LEANKG_DB_ENGINE (D4); delete dead files (persistent_cache.rs, redis_store.rs)
40. [ ] Update prd.md + README + enterprise-docker.md
41. [ ] Migration report in docs/analysis/

---

## 6. Risks & mitigations

| Risk | Mitigation |
|---|---|
| **Datalog semantics ≠ SQL** on some query (e.g. null-safe equality, `?` optional columns, `:group` with `:order` combined) | Translator tests compare cozo vs Postgres row-for-row on a golden dataset (T5.1); hand-write the ~5 non-trivial queries |
| **`run_script` is the entire DB API** — a translator bug silently corrupts | Postgres is default from T1.4; keep `LEANKG_DB_ENGINE=cozo` shim until T5.4 for parity tests; parity tests gate each phase |
| **pgvector recall** vs cozo HNSW (ef vs m, ef_construction) | Phase 0 spike measures recall@k; tune `m=16, ef_construction=200`; accept ±2% (semantic search is fuzzy anyway) |
| **JSONB migration breaks serde code** that expects `metadata` as String | T2.3: one-time conversion; grep all `metadata` readers; keep `metadata` as TEXT in a compat column if needed |
| **Postgres not installed in CI / local** | Postgres is required (D4): CI + local spin up `postgres:18` + pgvector via docker compose; `cozo` shim exists only during migration for parity tests |
| **`all_elements()` / mega-graph memory** (147k rows) | Already avoided in code (FR-SEM-07); Postgres `SELECT` with `LIMIT` + server-side cursor keeps it that way |
| **Embed while serving** (index drop mid-run) | pgvector `REINDEX CONCURRENTLY` + `LEANKG_EMBED_BACKGROUND` unchanged; index rebuild doesn't lock reads |

---

## 7. Decisions (resolved 2026-08-04)

| # | Decision | Chosen | Consequence |
|---|---|---|---|
| D1 | SQL client | **sqlx** (async, compile-checked, migrate built-in) | `sqlx::migrate!` for Phase 2; dynamic translator SQL uses non-macro `query()`; parity tests are the safety net |
| D2 | `query_cache` table | **drop** | moka L1 is the only cache; `persistent_cache.rs` deleted (T5.3); 16 tables total |
| D3 | `index_hashes` | **Postgres** (uniform) | DDL moves into schema.sql (T2.6); no second storage engine anywhere |
| D4 | Embedded sqlite/rocksdb backends | **remove** — Postgres-only | Local dev requires `docker compose up postgres`; `LEANKG_DB_ENGINE` is migration-only, deleted in Phase 8; `CozoBackend` survives only as parity-test shim |
| D5 | Vector dim | **keep 384** (BGE-small) | No re-embed; dim as single `const VEC_DIM` (T2.4) so future upgrade = one-line change + re-embed |

---

## 8. Appendix

### 8.1 Cozo query → SQL examples

```datalog
-- get_business_logic (db/mod.rs:58)
?[element_qualified, description, user_story_id, feature_id] :=
    *business_logic[element_qualified, description, user_story_id, feature_id],
    element_qualified = $eq
```
```sql
SELECT element_qualified, description, user_story_id, feature_id
FROM business_logic WHERE element_qualified = $1;
```

```datalog
-- env_query (graph/query.rs, `:group`)
?[qualified_name, env, count(n)] := *code_elements[n, a, b, qualified_name, c, d, e, f, g, h, env, _]
  :group [qualified_name, env] :order count(n) desc
```
```sql
SELECT qualified_name, env, count(*) FROM code_elements
GROUP BY qualified_name, env ORDER BY count(*) DESC;
```

```datalog
-- ANN (retrieval/pipeline.rs:362)
?[dist, qualified_name] := ~embedding_vectors:vec_idx {{
    qualified_name | query: vec([...]), k: {k}, ef: {ef}, bind_distance: dist }}
```
```sql
SELECT embedding <-> $1 AS dist, qualified_name
FROM embedding_vectors
ORDER BY embedding <-> $1
LIMIT $2;
```

### 8.2 Files to delete after migration

- `src/vector_engine/` (whole dir, custom HNSW)
- `src/embeddings/redis_store.rs` (write-only, superseded)
- `Dockerfile.cozoserver`, `docker-compose.enterprise.yml`, `docker-compose.enterprise.local.yml`
- `src/db/schema.rs` cozo DDL remnants
- `src/db/backend.rs` `DbBackend` trait + `CozoBackend` (after Phase 8, D4)
- `src/graph/persistent_cache.rs` + `query_cache` table (D2)
- Embedded sqlite/rocksdb init paths (D4)

### 8.3 Env vars added

| Var | Purpose |
|---|---|
| `LEANKG_PG_URL` | Postgres connection string (required — only engine, D4) |
| `LEANKG_PG_POOL_SIZE` | Pool size (default 5) |
| `LEANKG_PG_URL_RO` | Optional read-replica URL |
| `LEANKG_DB_ENGINE` | `postgres` (default, only engine) / `cozo` (migration shim, parity tests only) — **removed in Phase 8** |

### 8.4 Success criteria

1. `cargo test --release` green on Postgres backend (no cozo; cozo shim deleted)
2. `cargo test --release --features embeddings` green; `semantic_search` parity on golden fixture
3. `docker compose up postgres leankg` → MCP :9699 healthy; local dev flow = Postgres required (D4)
4. 2 LeanKG instances serve reads against 1 Postgres; writes serialize via advisory lock
5. No `cozo` in Cargo.toml; grep `cozo::` = 0; no `LEANKG_DB_ENGINE`
6. Cold embed ≥ cozo's ~700 v/s via `COPY`

### 8.5 Releasing v0.20.0

The release pipeline is fully automated (release-please, see `docs/workflow-opencode-agent.md`), but the migration is a **breaking change** (Postgres-only, D4 — embedded sqlite/rocksdb path removed) and lives on a **non-`main` worktree branch**, so the hand-off needs two explicit steps that CI cannot do alone.

**How the auto-CI picks the version:** Release Please scans conventional commits on every push to `main` since the last `v*` tag. Bump rules: `feat:` (or `bump-minor-pre-major`) → **minor** (`0.19.32 → 0.20.0`); `fix:`/`perf:`/`refactor:` → patch; `docs:`/`chore:`/`test:`/`style:`/`ci:`/`build:` → **excluded** (no release PR at all if only those land). It reads the base from `manifest.json` (`".": "0.19.32"`), **not** from a manual `Cargo.toml` bump — a self-bump is ignored/overwritten on merge of the release PR. It opens/updates a release PR; merging it pushes the `v0.20.0` tag + GitHub Release, which triggers `release.yml` to build/publish artifacts.

**Required sequence:**
1. Finish Phases 3–8 in the worktree, all committed; `cargo test --release` green.
2. Ensure the migration commits are **`feat:` / `fix:` / `refactor:`** conventional types (the `feat(pg):` / `fix(pg):` / `refactor(db):` headers already used qualify). `docs:`/`test:` commits do not move the version.
3. Merge `worktree-leankg-pg-migration` → `main` (PR or `git merge`), then push. This is the trigger — release-please runs on `main` only.
4. Push → release-please opens a release PR bumping to `0.20.0` (minor, via `feat:` commits). **Do not** manually bump `Cargo.toml`; it is ignored.
5. Merge the release PR → `v0.20.0` tag + GitHub Release + artifacts published. Cargo.toml/CHANGELOG are updated by the release PR itself.

**If 0.20.0 must be treated as a breaking MAJOR** (SemVer-correct for removing the embedded engine): release-please **never auto-majors** (config `bump-minor-pre-major: true`). Instead bump `manifest.json` `"."` to the target major by hand (per the documented manual-major procedure) and push — CI then releases from that base.

---

## 9. Progress tracker (2026-08-04)

Worktree: `worktree-leankg-pg-migration` (worktree under `.claude/worktrees/`). Dev Postgres: container `leankg-pg-phase0` (pgvector pg18, host `:5433`, db `leankg`, user/pass `postgres`/`postgres`). Prod containers (`leankg-leankg-1`, `leankg-enterprise-cozoserver-1`) untouched.

| Phase | Status | Evidence |
|---|---|---|
| 0. Spike (pgvector parity) | ✅ done | `tests/pg_phase0_spike.rs` (4 unit + 3 container tests); `docs/analysis/pg-phase0-spike.md`. 100% recall, identical order, dist <1e-5; HNSW build 2599ms / query 4ms; REINDEX CONCURRENTLY unblocks reads. Commits `c1b4e013`, `1a8c66e8`. |
| 1. DbBackend abstraction | ✅ done | `src/db/backend.rs` (trait + `CozoBackend` shim + `PostgresBackend` stub + `Arc<dyn DbBackend>`); `Arc<dyn DbBackend>` threaded through 43 files. 960 lib tests green. Commit `e73c3298`. |
| 2. Postgres schema + migrations | ✅ done | `src/db/pg/schema.sql` (16 tables, JSONB, pgvector HNSW, vector(384)); `src/db/pg/migrations.rs`; `leankg migrate` subcommand. Live-verified (16 tables, no query_cache). `tests/pg_schema_test.rs` 6/6 container tests pass. Commits `e749cad5`, `92f4b6f7`, `0182575f`, `7b115d42`. |
| 3. SQL translator | 🚧 in progress | `src/db/pg/translate.rs` (2374 lines, committed `a9d83fc5`); PostgresBackend real impl + `tests/pg_translate_parity_test.rs` in flight. |
| 4. Vector stack swap | ⬜ pending | — |
| 5. Remaining modules + grep `cozo::` = 0 | ⬜ pending | — |
| 6. Read-only + server scaling | ⬜ pending | — |
| 7. Embedding bulk-load | ⬜ pending | — |
| 8. Deploy + docs + cleanup | ⬜ pending | — |

Current implementation state is tracked in the task list; the plan's §4 checkboxes are updated as each task completes.

### Phase 9 — Performance verification: indexing + embed + query on large codebase (1-2 days)

Beyond "it works", prove it scales. Postgres is now the only engine (D4) — a slow index/embed/query path on a real codebase is a release blocker.

- [ ] T9.1 **Cold-index a real large codebase** — index workspace-be (or the largest repo available, ~371k functions per plan §2.4) through the Postgres backend (`LEANKG_DB_ENGINE=postgres` + `LEANKG_PG_URL`). Measure wall-clock vs the cozo/RocksDB baseline (historical numbers in `docs/verification/leanKG-0.19.32-docker-rebuild-full-spectrum-report.md`). Target: no worse than 2x cozo index time.
- [ ] T9.2 **Embed with the real model** — run `leankg embed` (fastembed BGE-small-en-v1.5, dim 384) into Postgres. Measure v/s vs cozo's ~700 v/s target (§7.1). If `import_relations` per-row INSERT is slow, switch `embedding_vectors` + `embedding_state` writes to the multi-row INSERT / COPY path (Phase 7 machinery).
- [ ] T9.3 **Query latency on the large graph** — run the heavy read paths against the big index: `all_elements()`, `get_overview_context`, `get_impact_radius`, `semantic_search` (top-k + rerank), env-filtered queries. Measure p50/p95 via `EXPLAIN ANALYZE` on the translated SQL. Watch for: seq-scan on filter columns, HNSW recall drift, JSONB `metadata` filters lacking GIN indexes, `ORDER BY count(*)` group queries.
- [ ] T9.4 **Index review** — verify every `::index` from the cozo schema (§2.2) has a Postgres equivalent, and add what's missing for the real query mix: `code_elements(file_path)`, `code_elements(qualified_name)`, `code_elements(element_type)`, `relationships(source_qualified, rel_type)`, `context_metrics(tool_name, timestamp)`, JSONB GIN on `metadata`/`tags`, `embedding_vectors` HNSW (`vector_cosine_ops`) + `qualified_name` PK. Use `EXPLAIN` to prove index use, not assumption.
- [ ] T9.5 **Autovacuum/ANALYZE health** — after bulk embed, run `ANALYZE` (the Phase 4 seam) and confirm planner estimates; document expected table sizes + autovacuum thresholds for a 371k-function workspace. Verify `REINDEX CONCURRENTLY` works on the live index without blocking reads (Phase 0 proved it; confirm at scale).
- [ ] T9.6 **Report** — `docs/analysis/pg-perf-large-codebase.md`: index time, embed v/s, query p50/p95 (with the SQL + EXPLAIN output), index inventory with EXPLAIN evidence, any schema changes made (new indexes), and the go/no-go vs cozo baselines.

**Exit:** `semantic_search` and `get_overview_context` on the large codebase are at-or-better than cozo latency; embed ≥ 700 v/s; every hot query in `EXPLAIN` uses an index; numbers in the report.

**Current status:** pending (after Phase 8).
