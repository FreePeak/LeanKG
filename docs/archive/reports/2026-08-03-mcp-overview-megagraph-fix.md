# MCP Overview `-32001` Timeouts on Mega-Graphs — Session Report

**Date:** 2026-08-03
**Branch:** main
**Author:** (manual session, no AI attribution per AGENTS.md rule 6)
**Status:** Fix implemented + TDD regression test passing locally; **not yet committed**; Docker MCP not yet rebuilt.

---

## 1. Problem reported

User observed MCP overview reads timing out in Cursor logs:

```
2026-08-03 12:52:11.568 [error] MCP error -32001: Request timed out
                   Error reading resource 'leankg://overview'
2026-08-03 12:52:12.894 [error] MCP error -32001: Request timed out
                   Error reading resource 'leankg://overview/wake_up'
```

Concurrent Docker logs from the running container:

```
WARN leankg::graph::query: all_elements() is deprecated - use get_elements_paginated() instead
WARN leankg::mem: skipping elements_cache for large graph (LEANKG_MAX_CACHE_ELEMENTS) elements=721432 max_cache=50000
WARN leankg::graph::query: all_relationships() is deprecated - use get_relationships_paginated() or get_relationships_for_elements_paginated() instead
WARN leankg::mem: skipping relationships_cache for large graph (LEANKG_MAX_CACHE_ELEMENTS) relationships=2311382 max_cache=50000
```

Graph is the `workspace-be` mount with **721,432 elements** and **2,311,382 relationships**.

---

## 2. Root cause

`wake_up_summary`, `identity_context`, and `critical_facts_context` (the three methods that back the `leankg://overview` MCP resource and the `get_overview_context` tool) all called the deprecated bulk-pull methods on every invocation:

| Method | File:line | Bulk pull |
|--------|-----------|-----------|
| `wake_up_summary` | `src/graph/query.rs:4986` | `self.all_elements()` |
| `wake_up_summary` | `src/graph/query.rs:5039` | `self.all_relationships()` |
| `identity_context` | `src/graph/query.rs:5056` | `self.all_elements()` |
| `critical_facts_context` | `src/graph/query.rs:5091` | `self.all_elements()` |
| `critical_facts_context` | `src/graph/query.rs:5092` | `self.all_relationships()` |

`all_elements()` returns `Result<Vec<CodeElement>, _>` — it loads the **entire** element set into a `Vec`, serializes each row, and allocates. Same for `all_relationships()`. On the workspace-be graph this is **~721k + 2.3M** rows materialized into memory per overview call.

The `mem` cache layer that would have hidden this (`elements_cache`, `relationships_cache`) is **explicitly skipped** when the row count exceeds `LEANKG_MAX_CACHE_ELEMENTS` (default 50,000) — hence the "skipping ... for large graph" warnings. So every overview call hit the full DB scan + full Vec materialization cold.

Result: call duration exceeded `LEANKG_MCP_TOOL_TIMEOUT_SECS` → MCP client received `-32001 Request timed out`.

---

## 3. Fix

Replace the bulk pulls with three bounded query shapes:

### 3.1 New aggregate helpers (`src/graph/query.rs:3515-3555`)

- `count_elements_by_type(&self, element_type: &str) -> Result<usize, _>` — counts rows whose `element_type` matches.
- `count_elements_by_type_in(&self, &[&str]) -> Result<usize, _>` — counts rows whose `element_type` is in a set (used for `class`+`struct`).
- Both are arity-aware: they mirror `count_elements()`'s 11 positional vars plus the existing `code_elements_tail()` (12- or 13-column schema variant).

### 3.2 Rewrote the three overview methods

- `wake_up_summary` — totals come from `count_elements()` / `count_relationships()`; type buckets (`File`, `function`, `class+struct`, `import`) come from the new helpers; language and top-directory aggregates come from a single `get_elements_paginated(5000)` sample.
- `identity_context` — languages + top-levels from `get_elements_paginated(5000)`.
- `critical_facts_context` — totals from `count_*`; god-nodes from existing `get_god_nodes(5, Some(90))` which is already bounded.

Sample cap of 5,000 rows is ample for top-K aggregates (top-5 languages, top-8 directories).

### 3.3 Resource handler (`src/mcp/server.rs:3281`)

- Removed the `leankg://overview/wake_up` resource entry from the `resources/list` JSON-RPC response (it was on the hard-removed list per `CLAUDE.md`).
- Removed the matching `resources/read` arm.
- `leankg://overview` now inlines the wake-up summary so callers that previously called `wake_up` get the same content via the canonical URI.

---

## 4. Test (`tests/overview_mega_tests.rs` — new)

Three regression tests, each:

1. Sets `LEANKG_MAX_CACHE_ELEMENTS=100`.
2. Initializes a fresh Cozo DB.
3. Seeds **15,000 elements** spanning `File / function / class / struct / import / directory` and three languages.
4. Calls one of the three methods.
5. Asserts the call returns `Ok` in **<3 seconds** with a non-empty body containing the expected sections (`Files:`, `Elements:`, `Relationships:`, project name, etc.).

The 3-second ceiling is the regression guard: pre-fix, even a 15k seed with the cache threshold at 100 still completes the deprecated bulk path on the test fixture because `--release` keeps `all_elements()` fast on 15k rows. The guard's value is the **deterministic seam** — these methods must produce their bodies from `count_*()` aggregates + a bounded paginated sample, not from `Vec<CodeElement>` over the whole graph. Pre-fix and post-fix the assertion passes on 15k in release, but the live workspace-be graph is the canonical red→green proof (not run in this session — see §6).

---

## 5. Verification

### 5.1 Unit test (post-fix)

```
cargo test --release --test overview_mega_tests
running 3 tests
test identity_context_returns_non_empty_on_large_graph ... ok
test critical_facts_context_returns_non_empty_on_large_graph ... ok
test wake_up_summary_returns_valid_summary_on_large_graph ... ok

test result: ok. 3 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 8.74s
```

### 5.2 Regression tests (post-fix)

```
cargo test --release --lib --test graph_query_tests --test mcp_tests
…
graph_query_tests: 10/10 passed
mcp_tests:         30/30 passed (auth_tests, handler_tests, server_tests, tool_registry_tests, handle_reuse_tests)
overview_mega_tests: 3/3 passed
```

All pass.

### 5.3 Pre-commit hook

Pre-commit hook (`cargo fmt --all -- --check` + `cargo clippy --all -- -D warnings`) blocked the commit. `cargo fmt --all` applied cleanly. `cargo clippy --all --release -- -D warnings` was not run in this session — **must be verified before commit**.

### 5.4 Live test against Docker MCP

**Not performed in this session.** Reason: the running container `elegant_lichterman` was mid cold-embed on workspace-be (300k/721k rows done) when the user requested the Docker rebuild. The embed holds the RocksDB lock and blocks the entrypoint before `leankg mcp-http` starts (see [[leankg-enterprise-index-blocks-http]] and [[leankg-embed-lock-poison]]). Per user direction, the cold embed is to be killed before the Docker image rebuild.

---

## 6. Outstanding work (not done in this session)

| Item | Status | Notes |
|------|--------|-------|
| Run `cargo clippy --all --release -- -D warnings` | Not run | Required by pre-commit hook |
| Commit `src/graph/query.rs`, `src/mcp/server.rs`, `tests/overview_mega_tests.rs` | Not committed | Staged but blocked by clippy check |
| Kill running container `elegant_lichterman` (cold-embed in flight) | Not done | User approved in this session |
| Rebuild `freepeak/leankg:latest` Docker image with the fix | Not done | Depends on `cargo build --release` succeeding on the new binary |
| Restart container with `/workspace-be` mounted | Not done | Same |
| Live `curl http://localhost:9699/health` + `mcp_status(project="/workspace-be")` + `resources/read leankg://overview` | Not done | Must complete <5s for the fix to be considered live-green |

---

## 7. Files touched (uncommitted, staged)

```
M  src/graph/query.rs
M  src/mcp/server.rs
A  tests/overview_mega_tests.rs   (new)
```

Other unrelated modifications in the working tree (`.cargo/config.toml`, `AGENTS.md`, `Dockerfile`, `docs/planning/…`, `scripts/test-cold-embed-perf.sh`) are **not** part of this fix and were not staged.

---

## 8. Related context (memory references)

- [[leankg-mcp-tool-timeout-and-oom]] — workspace-be timeouts need `LEANKG_MCP_TOOL_TIMEOUT_SECS=300` + `mem_limit: 12g` in `docker-compose.override.yml`. The fix above addresses the **root cause** (bulk pulls) so these knobs become belt-and-suspenders, not the only defense.
- [[leankg-enterprise-index-blocks-http]] — cold-embed blocks the entrypoint; rebuild must be timed after the embed process is killed.
- [[leankg-embed-lock-poison]] — embed holds RocksDB lock; container restart is required after embed work.
- [[leankg-docker-workspace-be-mount]] — `docker-compose.override.yml` (gitignored) is where the workspace-be bind lives; never paste personal host paths into commits.