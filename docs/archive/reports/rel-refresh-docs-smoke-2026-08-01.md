# REL-REFRESH-01 — Live smoke: `refresh` → `semantic_search(kind=docs)` returns doc hits

**Date:** 2026-08-01  
**Branch:** `prd/remote-source-live-closeouts`  
**PR:** PR-03 (remote-source-live-closeouts)  
**Tracker:** `docs/prd-task-tracker.md` task row 20 / §5.30 (FR-REFRESH + FR-DOCEMBED-04)

## Intent

Close REL-REFRESH-01: on a temp project (per PR guidance, a temp copy instead of
`/workspace`), `leankg refresh` (code index → docs index → embed) must make
`semantic_search(..., kind=docs)` return document/doc_section hits for known phrases.

> Note on `--wait`: the `refresh` subcommand is synchronous by design — it runs
> code index → docs index → foreground embed (`maybe_run_embed` with `wait=true`);
> there is no `--wait` flag on `refresh` (`--wait` exists on `embed`). The smoke ran
> `leankg refresh` in the foreground, which is the `refresh --wait` semantics.

## Setup

```bash
# Embeddings build (models already cached in ~/Library/Caches/leankg/models)
cargo build --release --features embeddings      # 7m44s cold; 4m39s / 4m16s incremental

# Temp project: real docs (prd.md, prd-task-tracker.md, docs/planning/*) + real code
# (src/sources, src/retrieval)
rm -rf /tmp/opencode/rel-refresh-smoke && mkdir -p …/docs …/src
cp docs/prd.md docs/prd-task-tracker.md docs/planning/*.md …/docs/
cp -R src/sources src/retrieval …/src/

leankg init --path .leankg
leankg refresh --project /tmp/opencode/rel-refresh-smoke
# Indexing code from …        → Indexed 217 code files
# Indexing docs from …/docs   → Indexed 11 documents and 338 sections
# Running embed…              → Refresh complete.            (13.6s total)
# embed_status.json: {"considered":561,"embedded":561,"orphans":0,"status":"completed","workers":4}

leankg mcp-http --port 62759 --project /tmp/opencode/rel-refresh-smoke   # GET /health → {"status":"ok"}
```

## Smoke queries (JSON-RPC `tools/call semantic_search`, `kind=docs`, `limit=10`, `env=local`)

Known phrases drawn verbatim from the indexed docs (prd.md §5.28/5.29/5.30).

| # | Query | ann candidates | Doc hit in `productive_upper_seeds` (element_type, qualified_name, rerank) | Traversed functions |
|---|-------|----------------|----------------------------------------------------------------------------|--------------------|
| 1 | `remote source indexing` | 50 | `doc_section` → `docs/prd.md::5.28 Remote source indexing (FR-SRC) — v3.7.16 **P2**` (rerank 3.58) | 8 |
| 2 | `remote source hot reload watch polling` | 50 | `doc_section` → `docs/prd.md::5.29 Remote source hot-reload (FR-SRC-WATCH) — v3.7.16 **P2**` | 8 |
| 3 | `doc semantic refresh docs` | 50 | `doc_section` → `docs/prd.md::3.26 Doc semantic refresh + kind filter (US-REFRESH) — v3.7.16 **P2**` | 8 |
| 4 | `fake gcs emulator bucket` | 50 | 2 × `doc_section` (prd.md ui-v2 section + `docs/planning/2026-03-27-leankg-implementation-plan.md`) | 16 |
| 5 | `leankg watch source URI interval` | 50 | 2 × `doc_section` (prd.md + planning doc) | 16 |

Response shape (`method: hnsw+ontology-traverse`): doc hits returned in
`productive_upper_seeds`; `results` additionally contain the referenced functions
traversed from each doc seed (`source: traversed`, `via_edge: references`, `hop: 2`,
`via_upper: docs/prd.md::5.28 …`).

## Fixes required (both test-driven)

The smoke surfaced two genuine defects that made `kind=docs` return zero visible doc hits:

1. **HNSW index never populated on fresh/small projects**
   (`src/embeddings/build.rs`): the incremental path (`:put`-style writers chosen when
   dirty ≤ max(1000, total/20)) writes vectors via `import_relations`, but CozoDB 0.7.6
   does **not** maintain usearch/HNSW indices on `import_relations` — the relation had
   561 vectors while `~embedding_vectors:vec_idx` returned zero candidates
   (`ann_candidate_count: 0`). The incremental writers now use the `:put` script form
   (which does maintain the index — same form as `tests/hnsw_recall_e2e.rs`); the bulk
   path keeps `import_relations` + drop/rebuild for throughput.
   Regression tests: `embeddings::build::tests::hnsw_live_writes_are_queryable_via_put`
   (red before fix: `hits=[]`) and `bulk_import_then_hnsw_rebuild_is_queryable`.

2. **doc_section seeds never reach code** (`src/retrieval/ontology_traversal.rs`):
   the doc indexer attaches `references` edges to the **document** node, but the exact
   elements vector search returns for kind=docs are **doc_sections** (e.g.
   `docs/prd.md::5.28 …`). The 1-hop downward rule found nothing for sections, so they
   were dropped as "unproductive" and never surfaced. `doc_section` now traverses 2 hops
   (`contains` up to the document → `references`/`documented_by` down to functions).
   Regression tests: `doc_section_rule_reaches_through_containing_document` +
   `doc_section_seed_traverses_through_document_references` (red before fix: `got []`).

## Docker MCP observation

Retried `semantic_search` against the running Docker MCP at `localhost:9699`
(`project=/workspace`) once, as permitted: health OK, but the call fails with
`Database error: RocksDB error: IO error: While lock file:
/data/leankg-rocksdb/projects/workspace-c52ddf65534b/data/LOCK: Resource temporarily
unavailable` — another process holds the RocksDB. Not depended upon; the local
`mcp-http` path above is the reliable evidence.

## Commands & timing

```bash
cargo build --release --features embeddings    # 7m44s cold / 4m16s final incremental
leankg refresh …                               # 13.6s (index 217 code files + 11 docs/338 sections + embed 561 vectors)
leankg mcp-http --port 62759 …                 # health OK
5 × semantic_search(kind=docs)                 # all 5 PASS, ~1–3s each
cargo test --lib --features embeddings         # 870 passed; 1 pre-existing flaky (vector_engine RSS gate, passes in isolation)
cargo fmt --all -- --check                     # clean
```

## Assertions

| # | Assertion | Result |
|---|-----------|--------|
| 1 | `refresh` completes: code + docs indexed, embed `completed` (561/561) | **PASS** |
| 2 | `semantic_search(kind=docs)` returns ≥1 document/doc_section hit for prd.md §5.28 phrase | **PASS** |
| 3 | … for §5.29 hot-reload phrase | **PASS** |
| 4 | … for §3.26/5.30 doc-semantic-refresh phrase | **PASS** |
| 5 | … for additional fake-gcs / watch phrases (hit counts 1–2 docs each) | **PASS** |
| 6 | `ann_candidate_count > 0` (HNSW index live) | **PASS** (50/50) |

## Result

**PASS** — REL-REFRESH-01 DONE. Evidence: this report; regression tests in
`src/embeddings/build.rs` and `src/retrieval/ontology_traversal.rs`; commit pending
(`docs: refresh kind=docs live smoke + tracker DONE (REL-REFRESH-01)`).
