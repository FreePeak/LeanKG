# Smoke Report: Wave 2b Auto GRAPH_REPORT

**Date:** 2026-07-25
**Branch:** `feature/auto-graph-report`
**CLI version:** 0.19.8 (pre-bump)

## What was tested

1. CLI `leankg index` auto-writes `.leankg/GRAPH_REPORT.md`
2. Content includes new "Surprising Cross-Cluster Edges" section
3. Skip-unchanged works on repeated identical index
4. ui-v2 builds with graph report collapsible card
5. All unit tests pass; integration tests pass (serial)
6. REST endpoint `GET /api/graph/report` returns markdown

## CLI smoke (clean temp project)

```bash
leankg index /tmp/smoke-graph-report
```

Log output:
```
INFO leankg::report::write: Wrote /private/tmp/.leankg/GRAPH_REPORT.md
```

### Generated report sections

The report contains all required sections: Overview, Confidence Distribution, **Surprising Cross-Cluster Edges** (new), Top God Nodes, Suggested Questions.

### Skip-unchanged

Second identical `leankg index --incremental` produced **no** "Wrote" log line — file was byte-identical. Third call (no index change) also skipped.

## Test results

```
cargo test --release --lib
703 passed; 0 failed; 3 ignored

cargo test --release --test integration -- --test-threads=1
27 passed; 0 failed
```

The `database is locked` failures on parallel runs are pre-existing SQLite flakiness (confirmed on main, passes with `--test-threads=1`).

## Files changed

| File | Change |
|------|--------|
| `src/report/write.rs` | New auto-write helper with skip-unchanged |
| `src/report/mod.rs` | New module |
| `src/lib.rs` | Added `mod report` |
| `src/main.rs` | Hook after index (CLI index + incremental) |
| `src/mcp/handler.rs` | Hook after `mcp_index`/`mcp_index_docs`; persist in `get_graph_report` |
| `src/graph/query.rs` | Added `SurprisingEdge` struct, section in `to_markdown()`, generation logic |
| `src/web/mod.rs` | Route `GET /api/graph/report` |
| `src/web/handlers.rs` | Handler `api_graph_report` |
| `ui-v2/src/App.tsx` | Graph report state + collapsible card on Overview page |
| `ui-v2/src/services/backend-client.ts` | `fetchGraphReport()` client |
| `Cargo.toml` | Added `[[example]] required-features` for embed examples |
| `examples/bench_fastembed_smoke.rs` | Same (pre-existing fix) |
| `docs/cli-reference.md` | Added `report` command row, index auto-write note |

## Status

| ID | Status | Notes |
|----|--------|-------|
| `US-GF-06` | DONE | Auto-write on every `leankg index`; ui-v2 collapsible card |
| `FR-GF-13` | DONE | Auto-write, soft-fail, skip-unchanged, MCP hook, REST endpoint, surprising edges |
