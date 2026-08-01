# PR-22 session provenance + RRF live evidence — 2026-08-02

IDs: `US-SM-03` / `US-SM-04` / `FR-SM-07` / `FR-SM-08` / `FR-SM-09`

## Environment

- leankg version: 0.19.30 (worktree `prd/session-provenance-rrf`, rebased on PR-21 head `a178eff0`)
- Binary: `cargo build --release` (7m38s)
- MCP: stdio (`leankg mcp-stdio`), project dir `/tmp/leankg-pr22-smoke`

## Steps

1. `session_memory_write` with explicit `kind=decision`, `session_id`, `node_id`, `element_refs`
2. `session_memory_write` without kind → auto-classify `standing_rule`
3. `search_memory_rrf` with matching query tokens
4. Inspect `.leankg/sessions/recall_index.jsonl` for provenance fields

## Results

### 1. Write with provenance (FR-SM-07 / FR-SM-08)

```json
{"id":2,"result":{"content":[{"type":"text","text":"status: ok
tool: session_memory_write
data:
    id: sm
    kind: decision
    node_id: offload-009
    source: session_memory_write
    source_session_id: sess-live-02
    written: true"}],"isError":false}}
```

On-disk row (bit-for-bit provenance round trip):

```json
{"id":"sm","source":"session_memory_write","rank":5.0,"text":"decision: live smoke k=60",
 "provenance":{"source_session_id":"sess-live-02","node_id":"offload-009",
               "kind":"decision","element_refs":["src/search/mod.rs::fuse_ranked_lists"],
               "timestamp":1785612166}}
```

### 2. Auto-classified kind (FR-SM-08)

```json
{"id":"sm","source":"session_memory_write","rank":5.0,
 "text":"standing_rule: never pass host paths to Docker MCP project arg",
 "provenance":{"source_session_id":"sess-live-01","kind":"standing_rule","timestamp":1785611987}}
```

### 3. RRF search (FR-SM-09, k=60)

```text
tool: search_memory_rrf
data:
    count: 1
    results:
      id[1]{element_refs,id,kind,node_id,rank,score,source_session_id,sources,title}:
        ["src/search/mod.rs::fuse_ranked_lists"],sm,decision,offload-009,1,
        0.01639344262295082,sess-live-02,[session],"decision: live smoke k=60"
```

- Fused score `0.01639344262295082` = `1/61` exactly (k=60, rank 1) — RRF math verified live
- Provenance fields present on hit: `kind=decision`, `node_id=offload-009`, `source_session_id=sess-live-02`, `element_refs`, `sources=[session]`

## Unit coverage

- `tests/session_provenance_rrf_tests.rs` — 18 tests (kinds, provenance round trip + backfill, legacy index, RRF math/determinism/tie-break, hybrid search)
- `src/session/mod.rs` tests — `write_memory_with_provenance` seam
- `tests/mcp_tools_redundancy_tests.rs` — handler-level `session_memory_write` + `search_memory_rrf`

## Gates

| Gate | Result |
|------|--------|
| `cargo fmt --all -- --check` | PASS |
| `cargo clippy --all -- -D warnings` | PASS |
| `cargo test --lib` | PASS (779) |
| `cargo test session` | PASS (19) |
| `cargo test rrf` | PASS (8 + handler) |

## Tracker

- Mark `US-SM-03`, `US-SM-04`, `FR-SM-07`, `FR-SM-08`, `FR-SM-09` DONE after merge (PR-22).
