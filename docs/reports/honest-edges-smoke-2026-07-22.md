# Honest Edges Smoke — Wave 2a (2026-07-22)

**IDs:** `US-GF-04`, `FR-GF-07`, `FR-GF-08`, `FR-GF-09`, `REL-043`

## Scope

Persist and propagate edge provenance (`EXTRACTED` / `INFERRED` / `AMBIGUOUS`) across write path, reindex backfill, MCP tools, path ranking, and ui-v2.

## Write path (FR-GF-07 / FR-GF-08)

| Check | Result |
|-------|--------|
| `insert_relationship` / `insert_relationships` stamp `metadata.confidence_label` when absent | PASS — `Relationship::stamp_confidence_label_metadata()` |
| Mapping matches `confidence_label()` / `derive_confidence_label()` | PASS — unit tests in `src/db/models.rs` |
| Post-index backfill | PASS — `GraphEngine::backfill_confidence_labels()` hooked after `resolve_call_edges` in indexer |

## MCP propagation (FR-GF-09)

| Surface | Field | Result |
|---------|-------|--------|
| `get_call_graph` | `confidence`, `confidence_label` per edge | PASS — `CallGraphEdge` |
| `get_impact_radius` | `confidence_label` on `elements_with_confidence` | PASS |
| `get_files_for_doc` / `find_related_docs` | `confidence_label` via `r.confidence_label()` | PASS |
| `shortest_path` | hop `confidence_label`; equal-length prefers EXTRACTED | PASS — `shortest_path_prefers_extracted_on_equal_length` |
| `query_graph` / NL query | already carried labels | PASS (pre-existing) |

## ui-v2

| Check | Result |
|-------|--------|
| REST `GraphRelationship.confidenceLabel` | PASS — `src/web/handlers.rs` |
| Sigma edge hover tooltip | PASS — `hoveredEdgeRef` + `edgeReducer` label + `defaultDrawEdgeLabel` shows `RELTYPE · LABEL` on hover (`ui-v2 npm run build` required) |

## Tests

```text
cargo test --release --lib          → 684 passed
cargo test --release --test integration → 27 passed
```

Key unit tests: `insert_relationship_stamps_confidence_label_metadata`, `backfill_confidence_labels_noop_when_already_stamped`, `shortest_path_prefers_extracted_on_equal_length`.

## Follow-up

- Rebuild `src/embed/` from ui-v2 when shipping embedded UI in a release cut (`ui-v2 && npm run build` → copy to `src/embed/`).
- Wave **2b**: auto `GRAPH_REPORT.md` on index (`FR-GF-13`).
