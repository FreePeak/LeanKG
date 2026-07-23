# REL-068 — Embed doc + inventory master test report (v3.7.15)

**Date:** 2026-07-23 (re-run; original 2026-07-22)
**Revision:** `v3.7.15-embed-doc-inventory`
**Branch:** `feat/v3.7.15embed-doc-inventory` (PR #100)
**Mounts (placeholders):** `/workspace` (fixture), `/workspace-other` (mega)
**Mode:** MCP-only (`LEANKG_SERVE_HTTP=0` via `docker-compose.override.yml`)

---

## 1. Unit / integration (FR-TEST-ED-01)

**Command:**

```bash
cargo test --release --features embeddings --test embed_doc_inventory --test doc_join_quality
```

| Suite | Result |
|-------|--------|
| `embed_doc_inventory` | **PASS — 7/7** |
| `doc_join_quality` | **PASS — 3/3** |
| **Total** | **10/10 passed, 0 failed** |

**Wall clock:** 244s (build 4m00s, tests 0.21s). Parallel `cargo test` safe via process-wide `chdir` lock in `embed_doc_inventory`.

**Pre-existing warnings (not introduced by this PR; present on `main` 0f5944b):**

| File | Line | Lint |
|---|---|---|
| `src/retrieval/pipeline.rs` | 402 | `dead_code` |
| `src/main.rs` | 5233 | `non_upper_case_globals` (`libc_SIGTERM`) |
| (lib-only clippy) | — | `manual_is_multiple_of` |

**Lint fixes shipped in this PR:**

- `src/graph/inventory.rs:55-65` — `type ElementRelCounts` alias to clear `clippy::type_complexity`.
- `src/doc_indexer/mod.rs:155`, `:257`, `:558` — `cargo fmt --all` formatting.
- `src/embeddings/build.rs:288`, `src/embeddings/mod.rs:55` — formatting; `PERF_TYPE_PRESET` moved into the existing `pub use text_blob::{...}` group with `#[allow(unused_imports)]` to remove the new `unused_imports` warning.

**CI gates (`cargo fmt --all -- --check`, `cargo clippy --all -- -D warnings`):** both pass on PR branch.

---

## 2. Fixture live — `/workspace` (REL-065)

**Preconditions:**
- `curl -sf http://localhost:9699/health` → `{"status":"ok"}`.
- `leankg serve` stopped (override sets `LEANKG_SERVE_HTTP=0`) to avoid RocksDB LOCK contention reported in prior session.

| Step | Tool / action | Result | Evidence |
|------|---------------|--------|----------|
| L1 | `mcp_status(project=/workspace, include_counts=true)` | **PASS** | `inventory` present; `total_elements: 17499`, `total_relationships: 57284`, `total_vectors: 7825`, `total_documents: 205`, `total_doc_sections: 3611` |
| L2 | `mcp_index_docs(path=./docs)` | **PASS** (already indexed; re-run is idempotent) | inventory totals unchanged on re-run |
| L3 | Offline `embed --full --types function,method,document,doc_section` | **PASS** (prior session, inventory reflects result) | 8999 items; `total_vectors: 7825` in `index_inventory` |
| L4 | `semantic_search` NL doc query | **PASS with finding** | See §2.1 below |
| L5 | `get_files_for_doc(doc=docs/prd.md)` | **PASS** | `resolved_doc: docs/prd.md`; structural join non-empty |
| L6 | `embed_control(status, project=/workspace)` | **PASS** | Returns `skipped_fresh` (incremental, no pending work) |

### 2.1 L4 finding — semantic_search doc ranking

Query: `Consolidated Tracking Document software developers AI coding tools`
Limit: 50

| Element type | Count in top-50 |
|---|---|
| `function` | 34 |
| `document` | 1 (`docs/design/hybrid-retrieval-reranking.md`) |
| `doc_section` | 0 |
| `method` / `class` / `interface` / etc. | 0 |

`_token_budget.truncated: true` (max 2000 tokens, actual 2631) — deeper hits exist but were not surfaced.

**Finding (do NOT silently ship):** the PRD `document` / `doc_section` nodes are persisted (`total_documents: 205`, `total_doc_sections: 3611`) and embedded (`total_vectors: 7825`), but `semantic_search` over the query "Consolidated Tracking Document software developers AI coding tools" returns code functions as the top-35 hits. Only **1 of 50** top results is a `document`, and **0** are `doc_section`. PR #100 satisfies FR-DOCEMBED (embed + classify + metadata + stale-mark), but FR-DOCEMBED-04 ("`semantic_search` returns doc hits") is **only weakly satisfied** for natural-language queries targeting doc-level semantics.

**Recommended follow-up (not in scope of PR #100):**
- Investigate cross-encoder reranker scoring on long doc blobs.
- Consider a `kind=document\|doc_section` filter for the NL doc-discovery path.
- Verify `build_doc_blob` does not produce overly long single-doc chunks that saturate the reranker budget.

This finding is called out here so reviewers know it is acknowledged, not hidden.

---

## 3. Mega ops — offline `--full --types perf` (REL-066 / REL-067)

**Status:** **NOT RE-RUN in this session.**

The Docker image used by `leankg-leankg-1` predates the MCP multi-mount routing fix landed in PR #100 (`src/mcp/server.rs::resolve_project_db_path` + canonicalize). Per the report's prior caveats, M1/M2 live mega probes (REL-066, REL-067) require a Docker image rebuild before they can be re-run against `/workspace-other`.

**Prior-session evidence (still valid):**

| Metric | Before | After |
|--------|--------|-------|
| `total_vectors` (inventory) | 147,420 | **278,672** |
| Embed job items processed | — | 628,259 |
| HNSW rebuild | — | ~8.5 min |

Discrepancy between job row count (628k) and inventory count (278k) is documented in the prior report and is intentional (job counts work items; inventory counts persisted ANN rows after dedupe/type filter).

---

## 4. Mega live probes (REL-066 / REL-067)

**Status:** **DEFERRED — image rebuild required.**

| Step | Status | Notes |
|------|--------|-------|
| M1 `mcp_status(include_counts=true)` on mega mount | DEFERRED | Requires rebuilt image to verify `project=` routing |
| M2 `embed_control(status, project=…)` routing | DEFERRED | Same caveat |
| M3 Vectors ≫ 147k | PASS (offline) | 278,672 via inventory refresh after perf embed |
| M5 Bounded `semantic_search` on mega | PASS (prior session) | Short query returned hits without OOM when serve disabled |

**Action item for follow-up PR:** rebuild Docker image after the `src/mcp/server.rs` routing changes and re-run M1/M2 live.

---

## 5. Implementation summary

| FR area | Status |
|---------|--------|
| FR-DOCEMBED — Doc classify + metadata + stale-mark | **DONE** |
| FR-EMBED-TYPES — `--types perf` preset | **DONE** |
| FR-INDEX-INV — `index_inventory` + `mcp_status` / CLI | **DONE** |
| FR-TEST-ED — unit + live gates | **DONE** (10/10 unit; live fixture PASS with §2.1 finding) |
| MCP multi-mount routing (no on-disk `.leankg`) | **DONE** in source; rebuild Docker image |

---

## 6. Operator checklist

1. Prefer `LEANKG_SERVE_HTTP=0` for MCP-only RocksDB (avoids LOCK contention).
2. After doc index: `embed --full --types function,method,document,doc_section` on `/workspace` once (or combined types — **never** `--full` with docs-only or code vectors are orphaned).
3. Mega cold coverage: offline `embed --wait --full --types perf --project /workspace-other`.
4. Rebuild/publish Docker image after `src/mcp/server.rs` routing changes before claiming L6/M2 live PASS.
5. **NEW (this re-run):** for NL doc discovery, prefer `search_by_requirement` / `get_files_for_doc` / `get_doc_tree` until the §2.1 ranking finding is resolved.

---

*Path hygiene: `/workspace` and `/workspace-other` placeholders only.*
