# Roadmap

> **Status SoT:** [`prd-task-tracker.md`](prd-task-tracker.md) (waves + Focus). Narrative: [`prd.md`](prd.md) §1.1.  
> **Now (2026-07-21):** P1 company-adoption waves 0–4. P0 ontology auto-update **DONE**.  
> **P2 follow-on:** Doc↔code join quality (`US-DOCJOIN-*` / `FR-DOCJOIN-*`) — [`prd.md`](prd.md) §3.19 / §5.22; after Wave 1a.

## Current implementation order (P1 waves)

| Wave | Focus | What |
|-----:|-------|------|
| 0a | Docs | ROI brief README link (`REL-058`) |
| 0b | UI | ui-v2 cutover evidence (`REL-057`) |
| **1a** | **MCP surface** | Hard-delete `wake_up` / `search_by_environment` + sync skills/rules/guidelines/setup (`US-SURF-06..07`, `FR-SURF-07..11`, `REL-062`) |
| 1b | Agent cost | Three-verb narrative (`FR-GF-22`) |
| 1c | Agent cost | Always-on hooks (`FR-GF-24`) |
| 2 | Packaging | Honest edges → GRAPH_REPORT → HTML export |
| 3 | UI | NL Query FAB |
| 4 | UI | Single-repo expand |

## Phase 1 -- Structural Parity vs codebase-memory-mcp

**Canonical PRD:** [`prd.md`](prd.md) Section 3.11 / 5.10 (v3.5-unified SoT)

| Feature | PRD ID | Status | Description |
|---------|--------|--------|-------------|
| `resolution_method` + confidence on calls | FR-B01, FR-B02 | **Done** | `name` / `name_file_hint` / `unresolved`; `typed` reserved |
| Default call resolve on index | FR-A05 / FR-B07 | **Done** | Soft-fail name resolve; never blocks index |
| `get_architecture` MCP | FR-B20 | **Done** | Languages, entry points, routes, clusters, hotspots, counts |
| `get_graph_schema` MCP | FR-B21 | **Done** | Element + relationship type counts |
| `find_dead_code` MCP | FR-B23 | **Done** | Zero callers, no `tested_by`; `min_lines` filter |
| Route + `http_calls` extractors | FR-B10..B12, B14 | **Done** | Go chi/gin/echo; TS express/fastify (`route_extractor.rs`) |
| `typed_resolve` feature flag | FR-B08 | **Done** | Flag + hybrid Go/TS MVP path exists; default LSP servers still empty |
| Typed resolve Go/TS quality | FR-B03..B05 | P2 | Benchmark harness demoted from P1; deepen after packaging |
| Architecture token budget | FR-B22 | **Done** | Per-section `max_items` truncation + `truncated_sections` metadata |
| Clones / cross-repo | FR-B30..B33 | **Removed / stubs** | `find_clones` MCP + `leankg clones` hard-removed 2026-07-20 |
| Event edges | FR-B15 | **Done** | EMITS / LISTENS_ON |
| 3D graph UI (Track E) | FR-E* / REL-041 | **P3 backlog** | New `graph-ui/`; keep 2D `ui/`; **do not** interrupt P1 waves |

## Phase 2 -- Enhanced MCP Tools (GitNexus-Inspired)

> Most items shipped earlier; remaining work is Cluster SKILL.md / MCP Resources (P2/P3).

| Feature | Status | Description |
|---------|--------|-------------|
| **Confidence Scoring** | **Done** | Numeric confidence + severity on impact |
| **Pre-Commit Change Detection** | **Done** | `detect_changes` MCP tool |
| **Multi-Repo Registry** | **Done** | Global registry + multi-project HTTP |
| **Community Detection** | **Done** | Leiden clusters + `get_clusters` |
| **Cluster-Grouped Search** | **Done** | Search includes cluster context |
| **Enhanced Context** | **Done** | `get_context` / `get_review_context` / `orchestrate` |
| **Cluster-Level Skills** | Planned (P2) | US-GN-07 |
| **MCP Resources** | Planned (P3) | US-GN-08 |

## Phase 3 -- Intelligence / Typed Resolve

| Feature | Status | Description |
|---------|--------|-------------|
| **Typed call resolve Go+TS deepen** | P2 after waves | FR-B03..B05 |
| **CBM comparison + scale report** | P2 | FR-C06, FR-C07, FR-D04 |
| **Wiki Generation** | **Done** | Markdown wiki from structure |
| **Wake-up / overview context** | **Done** | `wake_up` soft-deprecated → `get_overview_context` |

## Phase 4 -- Graphify packaging (company adoption)

PRD: [`prd.md`](prd.md) §1.1 · Analysis: [`analysis/graphify-vs-leankg-2026-07-20.md`](analysis/graphify-vs-leankg-2026-07-20.md)

| Feature | PRD ID | Status | Description |
|---------|--------|--------|-------------|
| Shortest path / explain / NL query | US-GF-01..03 | **Done** (MCP) | Remaining gap is packaging + UI |
| Edge provenance labels | US-GF-04 / FR-GF-07..09 | **P1 Wave 2a** | EXTRACTED / INFERRED / AMBIGUOUS |
| GRAPH_REPORT.md | US-GF-06 / FR-GF-13 | **P1 Wave 2b** | Architecture brief on index |
| HTML export | US-GF-13 / FR-GF-21 | **P1 Wave 2c** | Bounded single-file share |
| Three-verb + always-on hooks | US-GF-14 / US-GF-17 | **P1 Wave 1** | Primary cost lever |
| God-node ranking polish | US-GF-05 / FR-GF-10..12 | P2 | Index-time hub importance |
| Rationale / ADR depth | US-GF-07 / FR-GF-16 | P2 | Parser exists; productize |
| PR community triage polish | US-GF-08 | Done / P2 polish | Merge-order risk |
| Work-memory reflect skill | US-GF-09 / US-GF-16 | P2 | Productize as default guidance |
| Portable snapshot | US-GF-11 | **Done** | MCP export |

## Future / P3

| Feature | Description |
|---------|-------------|
| **Track E 3D galaxy UI** | `graph-ui/` WebGL — after P1 waves |
| **Language breadth** | Swift/Vue/Svelte/SQL index-walk (`REL-032`) — selective, not 36-lang race |
| **Conversation mining** | MemPalace US-MP-03 |
| **Windows / SLSA / pkg channels** | FR-C08..C11 |

## Completed (recent)

| Feature | Version | Description |
|---------|---------|-------------|
| Procedural ontology auto-update | 0.19.x / v3.7.9 | Watch YAML + boot marker + post-index |
| UI v2 expand / load-more / folder sidebar | 0.19.x / v3.7.10 | PR #92 |
| Mega-safe semantic + concept + query_graph | 0.19.x | FR-SEM-07 / REL-055 |
| Day-2 embed resume | 0.19.x | FR-EMBED-RESUME-* |
| Structural aggregators | 0.17.x | `get_architecture`, `get_graph_schema`, `find_dead_code` |

## References

- [Consolidated PRD + HLD](prd.md) (single source of truth)
- [Task tracker (waves)](prd-task-tracker.md)
- [Graphify vs LeanKG 2026-07-20](analysis/graphify-vs-leankg-2026-07-20.md)
- [Architecture overview](architecture.md)
- [MCP Tools](mcp-tools.md)
- [CLI Reference](cli-reference.md)
