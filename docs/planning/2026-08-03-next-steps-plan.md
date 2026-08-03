# Next-step plan — workspace-be embed/perf/tool fixes

**Date:** 2026-08-03
**Owner:** LeanKG
**Goal:** reduce cold-embed to 15min, fix 2 broken tools, address 5 slow semantic tools, with TDD (unit + live).

---

## Priority order (driven by user pain + blast radius)

| # | Item | Why now | Est. cost | TDD scope |
|---|------|---------|-----------|-----------|
| 1 | **Cold-embed 15min target** | user explicit; biggest pain | 1-2 hr | unit + live embed run |
| 2 | **`shortest_path` Go source path bug** | tool broken | 30 min | unit + live |
| 3 | **`add_documentation` relative path bug** | tool broken | 30 min | unit + live |
| 4 | **5 slow semantic tools (>300s)** | hits report | 2-4 hr | unit + live |
| 5 | **Auto-arm embed off by default** | prevents OOM on restart | 30 min | unit |

---

## Item 1 — Cold-embed 15min target

**Hypothesis:** raise `LEANKG_EMBED_MAX_MB` to 12000 + `cpus: 10` + `mem_limit: 14g` in `docker-compose.embed.yml`. Soft cap = 90% × 12000 = 10800MB. 8 workers × ~350MB + 900MB base + ~2GB block cache ≈ 5.7GB, well under soft cap. Flat-out throughput ≈ 8 × 60 = 480 vectors/s → 381k / 480 ≈ 13min + 3 min HNSW rebuild ≈ **16min**.

**Why we believe 60/s/worker:** measured on 2-worker run (123k in ~33min including 192s HNSW = ~60 embed/sec/worker). HNSW rebuild itself is ~100-200s on 381k.

**Test plan (TDD):**

### Unit tests (Rust, `cargo test --lib`)

1. `plan_embed_memory_with_budget(workers=8, batch=128, max_rss_mb=12000)` returns `workers=8, batch_size=128, max_rss_mb=12000` (no auto-cap).
2. `resolve_embed_runtime(workers=8, batch=128, fast=true)` returns `kind=BgeInt8, workers=8, intra=1, omp=1` (or whatever runtime decides under `cpus=10`).
3. `wait_for_embed_rss_headroom(max_rss_mb=12000)` does NOT block when RSS < 10800 (the 90% cap).
4. `effective_upsert_chunk(=5000)` unchanged — keeps the throughput-unlocking bulk chunk.

### Live tests (Docker only; no external deps)

1. **Synthetic micro-benchmark** (1k vectors): runs in <30s with 4 workers, 12g mem_limit. Confirms no regression.
2. **Workspace-be full cold embed** (381k): wall-clock must be ≤ 15min; HNSW rebuild ≤ 3min. Pass criterion: `Rate: ≥ 380 vectors/sec` (per the embed-status report).
3. **Resume-after-kill** (already proven): kill at 50% → resume incremental → completes remaining ≤ 15min total.

### Files to touch

- `docker-compose.embed.yml`: `LEANKG_EMBED_MAX_MB: "12000"`, `cpus: "10"`, `mem_limit: 14g`
- `docs/validation/2026-08-03-leankg-mcp-validation.md`: append cold-embed v2 results
- `docs/index-embed-flow.md`: update Operational Guide section with the new numbers

### Rollback

The current `LEANKG_EMBED_MAX_MB: 5500` was the throttle point. If 12g causes OOM on other (non-be) mounts, fall back to 8g.

---

## Item 2 — `shortest_path` Go source path bug

**Bug:** `shortest_path("source=/workspace-be/platform-saas/be-x-engine/cmd/server/server.go", ...)` returns `source '…' not found`.

**Root cause hypothesis:** `shortest_path` tokenizes the source path as a function/element name and looks it up in `code_elements.qualified_name` — but the path is a file, not a QN. The tool needs to resolve a file path to a starting node (file QN or function-in-file QN).

**Test plan (TDD):**

### Unit tests

1. `fn shortest_path_resolves_file_path_to_function_qns` — given a file path that exists, returns the QNs of all functions in that file.
2. `fn shortest_path_resolves_function_qn_directly` — given a function QN, uses it as-is.
3. `fn shortest_path_errors_on_nonexistent_path` — file path not in index → clear error.
4. `fn shortest_path_errors_on_empty_source` — empty source → 400.

### Live tests

1. `shortest_path(source=/workspace-be/.../server.go, target=/workspace-be/.../main.go, max_hops=3)` → returns a path with ≥1 hop pairs.
2. `shortest_path(source='./platform-saas/be-x-engine/cmd/server/server.go::main', target=...)` → named-QN source works.

### Files to touch

- `src/mcp/handler.rs` — `shortest_path` handler
- `src/graph/query.rs` — helper for "path → QN list" (likely reuses an existing `find_elements_by_path`)
- New tests: `src/mcp/handler.rs` test module + `src/graph/query.rs` test module

---

## Item 3 — `add_documentation` relative path bug

**Bug:** `add_documentation(file_path="docs/food-customer-search-flow.md")` returns `File not found`, even though the file exists at `/workspace-be/docs/food-customer-search-flow.md`.

**Root cause:** `add_documentation` is not in `should_resolve_tool_paths` (currently only `mcp_index`, `mcp_index_docs`, `mcp_init`, `detect_changes`). The handler reads `file_path` relative to cwd (`/workspace`), not `project=`.

**Test plan (TDD):**

### Unit tests

1. `fn add_documentation_resolves_relative_path_against_project` — given `project=/workspace-be` and `file_path="docs/x.md"`, resolves to `/workspace-be/docs/x.md`.
2. `fn add_documentation_accepts_absolute_path` — already-absolute path works.
3. `fn add_documentation_errors_on_missing_file` — clear error.

### Fix

Either:
- (a) Add `add_documentation` to `should_resolve_tool_paths` — simplest, consistent with other tools.
- (b) Resolve `file_path` against `project=` in the handler if not absolute.

**Prefer (a)** — same pattern as `mcp_index`, `detect_changes`. Less code duplication.

### Files to touch

- `src/mcp/server.rs` — add `"add_documentation"` to `should_resolve_tool_paths`
- `src/mcp/handler.rs` — `add_documentation` tests

### Live tests

1. `add_documentation(file_path="docs/food-customer-search-flow.md", project=/workspace-be)` → returns OK with the doc card.
2. `add_documentation(file_path="/workspace-be/docs/food-customer-search-flow.md", project=/workspace-be)` → also OK.

---

## Item 4 — 5 slow semantic tools (>300s)

**Tools:** `semantic_search`, `kg_context`, `kg_concept_map`, `kg_trace_workflow`, `kg_ontology_status`.

**Root cause hypothesis:** `run_hnsw_semantic_search` calls `pipeline.retrieve` which does `hnsw_retrieve` (50 ANN candidates) + `fetch_elements_batch` + rerank + traverse. The HNSW query at 137k vectors on a RocksDB-backed CozoDB is the bottleneck. Specific calls take 60-300s; some exceed 300s.

**Fix candidates:**

1. **Reduce default `top_k` from 50 to 10** — at 137k vectors, top-10 HNSW is fast; lower per-call latency.
2. **Skip the rerank step** when `kind=code` (rerank adds ~1-2s per call and isn't always wanted).
3. **Paginate the post-HNSW `fetch_elements_batch`** — currently fetches all elements at once for 50 QNs.
4. **Make the HNSW `ef` parameter configurable** via `LEANKG_HNSW_EF` (smaller ef = faster search, lower recall).

**Test plan (TDD):**

### Unit tests

1. `fn hnsw_search_top_10_under_50ms` (against a 137k-vector fixture) — pass if < 50ms.
2. `fn retrieve_with_skip_rerank` returns the same ANN candidates but skips the heavy cross-encoder pass.
3. `fn build_pipeline_default_top_k_is_10` (after the change).

### Live tests

1. `semantic_search(query="order flow", limit=3, project=/workspace-be)` ≤ 5s.
2. `kg_context(query="order flow", project=/workspace-be)` ≤ 5s.
3. `kg_ontology_status(project=/workspace-be)` ≤ 5s (this one is just a status call — should be fast already).

### Files to touch

- `src/retrieval/pipeline.rs` — `top_k` default, `ef` config
- `src/mcp/handler.rs` — `kg_*` handlers (skip rerank option)
- `src/embeddings/runtime.rs` — `resolve_ef(k)` currently scales `ef` with `k`; smaller defaults

---

## Item 5 — Auto-arm embed off by default

**Why:** `LEANKG_EMBED_AUTO_ARM=1` in the override auto-arms the embed on every idle pass. On a mega-graph it triggers OOM. Move the trigger behind an explicit opt-in (or `LEANKG_EMBED_AUTO_ARM=0` default + the override sets `1` only when the user knows what they're doing).

**Test plan (TDD):**

### Unit tests

1. `fn auto_arm_idle_does_not_resume_when_disabled` — start server with `LEANKG_EMBED_AUTO_ARM=0`, ensure no resume.
2. `fn auto_arm_idle_resumes_when_enabled` — start server with `LEANKG_EMBED_AUTO_ARM=1`, verify resume triggers.

### Files to touch

- `docker-compose.override.yml` — keep `LEANKG_EMBED_AUTO_ARM="1"` only in the explicit offline embed profile, remove from the MCP service.
- `docs/validation/2026-08-03-leankg-mcp-validation.md` — update accordingly.

---

## TDD workflow (per item)

For each item, the cycle is:

1. **Write failing unit test** — `cargo test --lib --no-run` should compile, the test fails.
2. **Implement the fix** — make the test pass.
3. **Run full unit test suite** — `cargo test --lib` must stay green.
4. **Write the live test** — a shell command that exercises the tool against the workspace-be MCP server.
5. **Run the live test** — confirm the fix works end-to-end.
6. **Update the validation report** — record the new result.
7. **Commit** — atomic commit per item.

---

## Validation cadence

After each item:

- `cargo test --lib` (unit)
- `cargo test --release` (unit + integration)
- `docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml up -d leankg`
- `curl -fsS http://localhost:9699/health`
- Run the item-specific live test
- `embed_control(action=status, project=/workspace-be)` to confirm no regression

---

## Tracking

This file is the single source of truth for the next steps. Mark items DONE with commit hashes. Re-validate the whole list weekly while the mega-graph remains in the loop.

---

## Open questions

- For the 15min target: do we need `--workers 8` explicitly, or will the runtime resolve to 8 with `cpus=10`? The latter is cleaner (no override needed for `--workers`).
- For the slow semantic tools: do we cap `top_k` everywhere, or expose it as a per-call arg? The MCP schema allows additional args. The MCP user-experience tradeoff is real.
- For the auto-arm change: do we keep the override env in the offline embed profile only, or also keep it in the MCP service but gated by a `LEANKG_EMBED_AUTO_ARM_FREQ` to run once per day?

(Decisions captured during implementation, not blockers.)
