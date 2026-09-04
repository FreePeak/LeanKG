# Doc↔Code Join Quality Smoke — 2026-07-21

**Branch:** `feature/doc-join-quality`  
**REL:** REL-063  
**PRD:** §5.22 FR-DOCJOIN-01..05

## Fixture tests (local, authoritative)

Run from worktree:

```bash
cd .worktrees/feature/doc-join-quality
cargo test --test doc_join_quality
cargo test --lib doc_indexer::paths
cargo test test_get_files_for_doc test_find_related_docs
```

| Test | Result |
|------|--------|
| `doc_join_round_trip_via_graph` | PASS — `docs/guide.md` → `references` → `./src/widget.rs`; reverse `documented_by` |
| `doc_join_mcp_tools_with_aliases` | PASS — `get_files_for_doc` aliases (`docs/…`, `./docs/…`, `guide.md`); `find_related_docs` aliases; miss returns `tried[]` |
| `doc_join_skips_unresolved_refs` | PASS — no invented edges for `src/missing.rs` |
| `doc_indexer::paths` unit (5) | PASS — alias tables + resolve helpers |
| `mcp_tools_full_tests` doc tools | PASS — existing handlers still succeed |

## Live MCP (`:9699`)

| Step | Result |
|------|--------|
| `curl http://localhost:9699/health` | OK (`{"status":"ok"}`) |
| `mcp_status(project="/workspace")` | **BLOCKED** — RocksDB `LOCK` on `/workspace` volume (contention with running container) |

**Note:** Live round-trip against Docker `/workspace` was not executed in this session because the RocksDB lock prevented `mcp_status`. Fixture TempDir tests prove join behavior for the feature branch code. Re-run live smoke after container restart or against a free mount once this branch is deployed.

## Implementation summary

| FR | Delivered |
|----|-----------|
| FR-DOCJOIN-01 | `resolve_code_ref` on write in `doc_indexer`; skips unresolved refs |
| FR-DOCJOIN-02 | `resolve_doc_key` / `resolve_file_key` in MCP handlers; `tried[]` on miss |
| FR-DOCJOIN-03 | `metadata.context` (≤100 chars) + `confidence_label=EXTRACTED` |
| FR-DOCJOIN-04 | `tests/doc_join_quality.rs` + `src/doc_indexer/paths.rs` unit tests |
| FR-DOCJOIN-05 | `docs/mcp-tools.md`, `AGENTS.md`, `CLAUDE.md`, `using-leankg` prefer-order |
| FR-DOCJOIN-06 | **NOT_DONE** (deferred) |

## Files touched

- `src/doc_indexer/paths.rs` (new)
- `src/doc_indexer/mod.rs`
- `src/mcp/handler.rs` (`get_files_for_doc`, `find_related_docs`)
- `tests/doc_join_quality.rs` (new)
