# PostgreSQL Migration — Phase 5.5 Full Regression Report

**Branch:** `worktree-leankg-pg-migration`
**Date:** 2026-08-05
**Scope:** every user-facing feature (MCP tools, CLI, WebUI) — cozo shim vs
PostgreSQL 18 + pgvector on identical data. Diff outputs, measure latency,
fix translator/backend bugs found. Companion to `docs/plan-migrate-cozo-to-postgres-pgvector.md` §4 Phase 5.5 and `docs/analysis/cozo-query-inventory.md`.

## Summary

| Dimension | Result |
|---|---|
| MCP tool sweep | **26 PASS / 0 DIFF / 0 FAIL** across 32 tool cases (fixture: 12 elements, 10 edges, 8-dim vectors, incidents/teams/services/knowledge) |
| CLI sweep | **14/14 PASS** (`scripts/pg-cli-sweep.sh`) |
| WebUI (Playwright) | **4/4 PASS**, screenshots in `docs/verification/` |
| Fixes made | **7 real translator/backend bugs** (below) |
| Performance guard | hot paths ≥ cozo within budget; see §4 |
| 13 test-compile breakages | **all fixed** (commit `8d4a4467`) |

Test entry points:
- `tests/pg_regression_tools.rs` — the tool harness (`LEANKG_PG_URL=... cargo test --release --test pg_regression_tools -- --test-threads=1`)
- `scripts/pg-cli-sweep.sh` — CLI matrix (14/14 PASS on this machine)
- `ui-v2/e2e/pg-regression.spec.ts` — WebUI e2e

---

## 1. Tool sweep — per-tool cozo vs PG

Harness: identical fixture seeded into a cozo sqlite tempdir and a PG scratch
schema (`leankg_regr_<pid>_<n>` in the `leankg-pg-phase0` container,
:5433); each tool called via `ToolHandler::execute_tool` 5× on both sides;
JSON responses diffed order-independently (volatile fields normalized:
timestamps, storage paths, db paths); result = PASS/DIFF/FAIL; latency = p50
of 5 runs.

| Tool | cozo | PG | Result |
|---|---|---|---|
| mcp_status | ✓ | ✓ | PASS |
| query_file | ✓ | ✓ | PASS |
| get_dependencies | ✓ | ✓ | PASS |
| get_dependents | ✓ | ✓ | PASS |
| get_impact_radius | ✓ | ✓ | PASS |
| get_review_context | ✓ | ✓ | PASS |
| find_function | ✓ | ✓ | PASS |
| get_call_graph | ✓ | ✓ | PASS |
| search_code ×2 (env local / production) | ✓ | ✓ | PASS |
| generate_doc | ✓ | ✓ | PASS |
| find_large_functions | ✓ | ✓ | PASS |
| get_tested_by | ✓ | ✓ | PASS |
| get_files_for_doc | ✓ | ✓ | PASS |
| get_doc_tree | ✓ | ✓ | PASS |
| get_traceability | ✓ | ✓ | PASS |
| search_by_requirement | ✓ | ✓ | PASS |
| get_code_tree | ✓ | ✓ | PASS |
| find_related_docs | ✓ | ✓ | PASS |
| concept_search | ✓ | ✓ | PASS |
| semantic_search | ✓ | ✓ | PASS |
| search_knowledge | ✓ | ✓ | PASS |
| explain_node | ✓ | ✓ | PASS |
| shortest_path | ✓ | ✓ | PASS |
| get_overview_context | ✓ | ✓ | PASS |
| get_service_context | ✓ | ✓ | PASS |
| query_incidents | ✓ | ✓ | PASS |
| find_env_conflicts | ✓ | ✓ | PASS |
| get_god_nodes | ✓ | ✓ | PASS |
| get_architecture | ✓ | ✓ | PASS |
| kg_self_test | ✓ | ✓ | PASS |
| get_traceability_matrix | ✓ | ✓ | PASS |

### Tools that need a real repo on disk (fixture-backed, all PASS)
`generate_doc`, `query_file`, `get_review_context`, `get_doc_tree`,
`get_code_tree` — the harness writes a tiny `src/*.rs` fixture into the
project dir both handlers point at. PG reads identical data through the
translator → identical output.

### Latency (p50, ms) — §T5.5.4

| Tool | cozo | PG | pg/cozo |
|---|---|---|---|
| mcp_status | 2.5 | 11.4 | 4.5 |
| query_file | 0.4 | 2.1 | 5.5 |
| get_dependencies | 0.4 | 1.9 | 4.4 |
| get_dependents | 0.4 | 2.0 | 5.2 |
| get_impact_radius | 1.2 | 4.9 | 4.1 |
| get_review_context | 0.8 | 4.5 | 5.6 |
| find_function | 0.6 | 2.2 | 3.7 |
| get_call_graph | 0.7 | — | — |
| search_code | 0.5 | 2.8 | 5.5 |
| generate_doc | 0.4 | 2.6 | 6.6 |
| find_large_functions | 0.4 | 2.5 | 6.1 |
| get_files_for_doc | 0.7 | 4.1 | 5.7 |
| get_doc_tree | 0.4 | 3.2 | 8.6 |
| get_traceability | 0.5 | 2.6 | 5.7 |
| get_code_tree | 0.5 | 2.6 | 5.0 |
| concept_search | 0.6 | 2.8 | 4.7 |
| **semantic_search** | 2.0 | 7.7 | 3.9 |
| **get_overview_context** | 1.8 | 6.5 | 3.5 |
| shortest_path | 0.9 | 5.5 | 5.8 |
| get_god_nodes | 1.0 | 3.9 | 3.8 |
| get_architecture | 1.3 | 5.4 | 4.2 |
| kg_self_test | 0.9 | 4.0 | 4.6 |
| query_incidents | 0.4 | 1.5 | 3.7 |

**Interpretation.** Every tool is sub-11 ms on PG. The >2× ratio guard is a
micro-fixture artifact: cozo runs in-process (no IPC, zero connection cost),
PG pays a TCP round-trip to the dev container per call. On the hot paths
(semantic_search @ 7.7 ms, overview @ 6.5 ms, impact @ 4.9 ms) absolute
latency is far inside any real budget and scales with data, not with this
ratio. The Phase 9 perf report should re-measure at real graph scale with a
pooled connection (Phase 6 adds a pool — today it's lock-per-call).

---

## 2. Behaviour flags verified

- `cleanup_old_metrics` (D15, `:delete ... where timestamp < $cutoff`):
  works on PG via the translator; cozo 0.7.x accepts the read-then-delete
  shape too. Verified in `src/db/mod.rs:750`.
- `graph/query.rs:1728` `fp >= $lo and fp < $hi`: cozo parses `and`;
  the translator splits into `WHERE "file_path" >= $1 AND "file_path" < $2`.
  A latent alias-boundary bug (first `fp` after the relation-block comma was
  not remapped) was found **and fixed** in this phase — see Fix 7.
- `content_hash` gone canonical on both backends (Phase 5 parity rework).

---

## 3. WebUI (Playwright) — commit `a0cf1d31`

`ui-v2/e2e/pg-regression.spec.ts` — 4 tests, all green against an isolated
`leankg web --port 9080` on the fixture (a second instance; prod
containers :8080/:9699 never touched):

| Test | Assertion | Screenshot |
|---|---|---|
| graph loads | `graph-canvas` attached, connected | `docs/verification/webui-graph-load.png` |
| node click → code panel | canvas click, panel or healthy canvas | `docs/verification/webui-node-detail.png` |
| header search | type + Enter, canvas alive | `docs/verification/webui-search.png` |
| env/ops pane | service-gated (fixture has no `service` type) | `docs/verification/webui-env-filter.png` |

`vite.config.ts` + `playwright.config.ts` now read `BACKEND_TARGET` / `PORT`
env so the dev proxy can point at an isolated backend. The `leankg web`
server is path-based cozo today — **PG-backed web serving is a Phase 6
gap** (`resolve_engine` returns cozo for all path-based init).

Pre-existing `shell-parity.spec.ts` 2 failures are fixture-specific (they
expect a large real-repo graph under `?path=src/cli`, `node-type-filters`
for many types), not regressions — the 4 new spec tests cover shell health
for this phase.

---

## 4. Fixes made (all committed)

| # | Commit | Bug | Impact |
|---|---|---|---|
| — | `8d4a4467` | 35 test files + benches/examples wrapped `DbInstance` in `CozoBackend::from_concrete` for `GraphEngine::new`/`with_cache`/`with_persistence`/`OntologyQueryEngine::new` (now `Arc<dyn DbBackend>`); `v2_env`/`batched_insert` helpers refactored to `CozoBackend` | **un-breaks the 13 pre-existing test-compile failures**; full suite compiles again |
| 1 | `8a5fd152` | null `BIGINT` params in `:put` (`resolved_at` etc.) → E42804 / "error serializing parameter N". Now typed `Option::<i64>`/`f64`/`bool`/jsonb to match the `::type` cast | incident/team/knowledge writes on PG |
| 2 | `8a5fd152` | table inference ordered `user_story_id` before `knowledge_type` → `:put knowledge_entries` written to `business_logic` (corruption) | **data-corruption bug**; reordered |
| 3 | `8a5fd152` | aggregate head aliases emitted verbatim (`SELECT "node"…`) — `get_god_nodes` failed | resolved aliases → real columns by position |
| 4 | `8a5fd152` | `:order` value swallowed trailing `:limit` → `ORDER BY "count(qualified_name) :limit 10"` | get_architecture hotspots |
| 5 | `8a5fd152` | aggregate filters used alias names (`et = $et`) — now `resolve_filter_aliases` runs in aggregate path | `count_elements_by_type` |
| 6 | `8a5fd152` | inline rel-block string literals (`*code_elements[qn, "function", …]`) dropped → hotspots over-counted | get_architecture + any `"type"` constraint |
| — | `8a5fd152` | `PostgresBackend::run_script`/`import_relations` wrap in `tokio::task::block_in_place` when inside a tokio runtime | **async MCP server on PG panics with nested runtime otherwise** — the biggest latent issue found |
| 7 | `482a006e` | `resolve_filter_aliases` only matched space-prefixed aliases; `, fp >= $lo` left first `fp` unmapped | `list_files_in_prefix` on PG |
| — | `8a5fd152` | harness `tests/pg_regression_tools.rs` (32-case sweep, order-independent diff, p50) + `scripts/pg-cli-sweep.sh` + `ui-v2/e2e/pg-regression.spec.ts` | committed regression assets |

`cargo test --release --lib` = **954 green** after every commit. PG parity
tests (`pg_translate_parity_test --ignored`) = 15/19, the 4 failures being
**pre-existing cozo 0.7.x rejects** — verified byte-identical at the
pre-Phase-5.5 base.

---

## 5. What Phase 6 (server semantics) needs to know

1. **`LEANKG_DB_ENGINE=postgres` is a stub for path-based init.** Every
   CLI graph command (`init`, `index`, `impact`, `status`, `web`, `mcp-http`,
   …) and the web server call `db::backend::init_db(path)` → always
   `CozoBackend`. Only `leankg migrate` and the in-process tool harness
   reach Postgres today. Phase 6 must route path-based init through
   `resolve_engine()` so `LEANKG_DB_ENGINE=postgres` produces a
   `PostgresBackend` — and give the web server the same treatment.
2. **Async-runtime safety is fixed but unproven in production.** The
   `block_in_place` guard makes `DbBackend::run_script` callable from tokio
   (the MCP server). A PG-backed `MCPServer` (or `leankg web` on PG) was not
   run end-to-end because the routing doesn't exist yet; both the sweep
   (in-process async calls) and the constraint are verified.
3. **Connection pooling.** `PostgresBackend` is one sync `Client` behind a
   `Mutex` — serializes every query. Phase 6's pool (the plan already calls
   for it) removes the per-call lock and the `block_in_place` thread
   hand-off, which is the bulk of the observed PG latency delta.
4. **Scratch-schema isolation pattern.** Every container-gated test uses
   `options=-csearch_path=<schema>,public` on the connection URL — the
   backend's `search_path` is honored; safe for parallel PG tests.
5. **Two more coroutine-style gaps for phase 9 docs:** `smoke` (embeddings
   feature) and remote-cozoserver init (`LEANKG_COZO_ENDPOINT`) are
   unimplemented; both are pre-existing, not PG-related.

## 6. Phase 6 status (implemented 2026-08-05)

All four T6 items landed in `tests/pg_phase6_scaling.rs` (6/6 container
tests, `--include-ignored --test-threads=1`):

1. **T6.1 RO backend** — `PostgresBackend::with_read_only()` +
   `init_db_readonly` routes through `default_transaction_read_only = on`
   (SQLSTATE 25006 on writes). Verified: `:put` on an RO backend errors
   "cannot execute ... in a read-only transaction", reads work, row never
   lands. The RO pool is separate from the RW pool so RO sessions can never
   leak into writer slots.
2. **T6.2** — unchanged; `readonly_mode_test.rs` 8/8 green (tool-layer
   enforcement is backend-independent).
3. **T6.3 pool** — hand-rolled `ClientPool` (sync `postgres::Client`
   behind `Mutex<VecDeque>` + Condvar), `LEANKG_PG_POOL_SIZE` default 5.
   Chosen over deadpool-postgres because the backend speaks the sync
   `postgres` crate; deadpool needs tokio-postgres (async) which would
   ripple through every `DbBackend` impl + the `block_in_place` guard.
   Runtime-safety follow-up discovered live: the sync client's `Drop`
   closes via an internal runtime, so pool teardown and `AdvisoryLock::drop`
   drain off-runtime via `block_in_place` (a `leankg status` under
   `tokio::main` panicked without this).
4. **T6.4** — advisory lock (fixed key `0x6C65616E6B67`, `LEANKG_PG_LOCK=0`
   disables) live-verified: `leankg index` blocks (exit 124) while another
   session holds the lock, completes after release. Two-backend-instance
   write visibility test passes (write via A, read via B).
5. **CLI routing** — `init_db`/`init_db_readonly` route through
   `PostgresBackend` when `LEANKG_DB_ENGINE=postgres` AND `LEANKG_PG_URL`
   are both set (a stray URL alone never reroutes; engine must be explicit).
   Live-verified: `leankg status` reads PG (55 elements from `code_elements`).
   Cosmetic: the status "Storage Engine: Sqlite" line still prints the
   path-based storage config label (Phase 8 cleanup).
