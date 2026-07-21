# REL-059 — Procedural ontology auto-update smoke (2026-07-21)

**Branch:** `feature/ontology-proc-auto`  
**PRD:** §3.18 / §5.21 — `US-ONT-PROC-01` / `FR-ONT-PROC-01..03` / `REL-059`  
**Codebase:** LeanKG 0.19.2  

**Live harness:** local `./target/release/leankg mcp-http --port 19799` (feature branch).

## Root cause (duplicates on rename) — fixed

Cozo `code_elements` `:put` keys the **full composite tuple** (including `name` + `metadata`). Ontology sync used bare `insert_element`, so renaming a step left old rows with the same GID.

**Fix (best practice = declarative replace, same idea as `reindex_file_sync`):**

1. `GraphEngine::clear_ontology_layer` — bulk `:rm` ontology relationships (join on `ontology://`) then bulk `:rm` ontology elements
2. `GraphEngine::upsert_element_by_qualified_name` — rm-by-qn then put (available for single-row cases)
3. `sync_from_dir` — clear layer → batch `insert_elements` → insert relationships; global `ONTOLOGY_SYNC_LOCK` + SQLite lock retries

## Acceptance matrix

| # | AC | Evidence | Result |
|--:|----|----------|--------|
| 1 | Edit YAML → `kg_trace_workflow` updates without restart | Live watcher + explicit `ontology_control(sync)` | **PASS** |
| 2 | Boot marker considers `workflows.yaml` | Shell simulation of `entrypoint.sh` | **PASS** |
| 3 | Sync does not block `/health` | Health ok throughout live runs | **PASS** |
| 4 | Rename replaces (no duplicate step rows) | Unit `sync_from_dir_rename_replaces_not_duplicates`; live A/B below | **PASS** |
| 5 | Removed step disappears | Unit `sync_from_dir_removed_step_disappears` | **PASS** |

## Live checks (post-fix, :19799)

| Check | Result |
|-------|--------|
| Explicit sync: rename → single row with marker | PASS |
| Explicit sync: restore → marker gone, single row | PASS |
| Watcher: rename → marker + single row | PASS |
| Watcher: restore → cleared + single row | PASS |
| `/health` throughout | PASS |

## Unit / build

```bash
cargo test --release --lib ontology::sync::tests
cargo build --release
```

4/4 sync tests passed (load, rename-no-dup, remove-step, env resolve).

## Notes

- Docker `:9699` image still needs rebuild from this branch for production.
- Concurrent SQLite readers during a large clear can briefly see `database is locked`; sync retries; clients should retry reads (test harness does).
