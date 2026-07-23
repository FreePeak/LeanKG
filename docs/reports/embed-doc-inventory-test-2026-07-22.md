# REL-068 — Embed doc + inventory master test report (v3.7.15)

**Date:** 2026-07-22  
**Revision:** `v3.7.15-embed-doc-inventory`  
**Mounts (placeholders):** `/workspace` (fixture), `/workspace-other` (mega)

---

## 1. Unit / integration (FR-TEST-ED-01)

**Command:**

```bash
cargo test --release --features embeddings --test embed_doc_inventory --test doc_join_quality
```

| Suite | Result |
|-------|--------|
| `embed_doc_inventory` | **PASS** — 7/7 |
| `doc_join_quality` | **PASS** — 3/3 |

**Notes:** `embed_doc_inventory` uses a process-wide `chdir` lock so parallel `cargo test` does not cross-contaminate TempDir fixtures.

---

## 2. Fixture live — `/workspace` (REL-065)

**Preconditions:** `curl -sf http://localhost:9699/health`; stop `leankg serve` when using RocksDB (serve + MCP contend on the same `LOCK` — use `LEANKG_SERVE_HTTP=0` or kill serve PID before MCP probes).

| Step | Tool / action | Result | Evidence |
|------|---------------|--------|----------|
| L1 | `mcp_status(project=/workspace, include_counts=true)` | **PASS** | `inventory` present; `total_vectors: 7825` (post `--full --types function,method,document,doc_section`) |
| L2 | `mcp_index_docs(path=./docs)` | **PASS** (prior session) | `total_documents: 205`, `total_doc_sections: 3611` in inventory |
| L3 | Offline `embed --full --types function,method,document,doc_section` | **PASS** | 8999 items embedded; HNSW rebuild ~5.5s; 0 orphans |
| L4 | `semantic_search` NL doc query | **PASS** | Query: `Consolidated Tracking Document software developers AI coding tools` → ≥1 `document`/`doc_section` hit (`limit=50`) |
| L5 | `get_files_for_doc(doc=docs/prd.md)` | **PASS** | `resolved_doc: docs/prd.md`; structural join non-empty |
| L6 | `embed_control(status, project=/workspace)` vs mega | **PARTIAL** | Routing fix landed in `resolve_project_db_path` + canonicalize (`src/mcp/server.rs`); published image must be rebuilt **without** macOS binary copy. Prior to fix both mounts reported workspace vector count. |

---

## 3. Mega ops — offline `--full --types perf` (REL-066 / REL-067)

**Command (MCP stopped):**

```bash
docker compose -f docker-compose.rocksdb.yml stop leankg
docker compose -f docker-compose.rocksdb.yml run --rm --no-deps --entrypoint leankg leankg \
  embed --wait --full --project /workspace-other \
  --workers 8 --batch-size 128 --types perf
```

> Local compose binds the mega tree as the **second** entry in `LEANKG_PROJECT_DIRS`; substitute `/workspace-other` when your override matches the plan placeholder.

| Metric | Before | After |
|--------|--------|-------|
| `total_vectors` (inventory) | 147,420 | **278,672** |
| Embed job items processed | — | 628,259 |
| HNSW rebuild | — | ~8.5 min |

**Coverage:** `--types perf` preset enrolls `function,method,class,interface,file,struct,property,constructor,document,doc_section`. Incremental-only runs remain blind to untracked QNs; one `--full` pass required after classify expansion.

**Discrepancy:** embed job row count (628k) vs `count_embedding_vectors` / inventory (278k) — job counts embedding work items processed; inventory counts persisted ANN rows after dedupe/type filters. Track for follow-up, not a release blocker for inventory gate.

---

## 4. Mega live probes (REL-066 / REL-067)

| Step | Result | Notes |
|------|--------|-------|
| M1 `mcp_status(include_counts=true)` on mega mount | **BLOCKED** on routing | Without rebuilt image, `project=` falls back to `/workspace` (17k elements). Fix: `resolve_project_db_path` + non-existent `.leankg` + RocksDB central path. |
| M2 `embed_control(status, project=…)` routing | **PARTIAL** | Same as L6; offline ops used correct `--project` path. |
| M3 Vectors ≫ 147k | **PASS** (offline) | 278,672 via inventory refresh after perf embed |
| M5 Bounded `semantic_search` on mega | **PASS** (prior session) | Short query returned hits without OOM when serve disabled |

---

## 5. Implementation summary

| FR area | Status |
|---------|--------|
| FR-DOCEMBED — Doc classify + metadata + stale-mark | **DONE** |
| FR-EMBED-TYPES — `--types perf` preset | **DONE** |
| FR-INDEX-INV — `index_inventory` + `mcp_status` / CLI | **DONE** |
| FR-TEST-ED — unit + live gates | **DONE** (L6/M1-M2 routing deploy follow-up) |
| MCP multi-mount routing (no on-disk `.leankg`) | **DONE** in source; rebuild Docker image |

---

## 6. Operator checklist

1. Prefer `LEANKG_SERVE_HTTP=0` for MCP-only RocksDB (avoids LOCK contention).
2. After doc index: `embed --full --types function,method,document,doc_section` on `/workspace` once (or combined types — **never** `--full` with docs-only or code vectors are orphaned).
3. Mega cold coverage: offline `embed --wait --full --types perf --project /workspace-other`.
4. Rebuild/publish Docker image after `src/mcp/server.rs` routing changes before claiming L6/M2 live PASS.

---

*Path hygiene: `/workspace` and `/workspace-other` placeholders only.*
