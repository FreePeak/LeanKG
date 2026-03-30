# LeanKG PRD - Consolidated Tracking Document

**Version:** 2.0-consolidated
**Date:** 2026-03-28
**Status:** Active Development
**Author:** Product Owner
**Target Users:** Software developers using AI coding tools (Cursor, OpenCode, Claude Code, etc.)

---

## Changelog (Merged)

### v2.0-consolidated - Merged from 3 source PRDs
- Source 1: `prd-leankg.md` (v1.7, 2026-03-27)
- Source 2: `prd-leankg-v2.0-enhancements.md` (v2.0, 2026-03-27)
- Source 3: `prd-leankg-gitnexus-enhancements.md` (v1.0, 2026-03-27)

### v1.16 (IN PROGRESS)
- AB Testing & Validation: OpenCode token parsing, context correctness validation, data store tests, summary report

### v1.15 (COMPLETED)
- Web UI Orphan Node Filtering Fix: Fixed orphan nodes appearing in webui graph view; `filterOrphanedNodes` now applies to ALL filter types; Fixed 'mapping' filter bug where `e.target` was not added to nodeIds

### v1.14 (COMPLETED)
- Web UI Integration: REMOVED `tools/graph-viewer/` (Python HTTP server + HTML); Wire up `src/web/mod.rs` with all routes; Added `axum = "0.7"` dependency; Updated `Serve` command; Added new `Web` CLI command; All web UI pages served directly from LeanKG binary

### v1.13 (COMPLETED)
- Terraform and CI/CD YAML Indexing: Add Terraform (.tf) file indexing with HCL extraction; Add CI/CD YAML (.yml, .yaml) file indexing; Extract resource/data/variable/output blocks; Extract pipeline/stage/step structure; Add `terraform` and `cicd` element types

### v1.12 (COMPLETED)
- P2 MCP Tool Improvements: Add `required` arrays to all MCP tools; Add `depth` and `max_results` params to `get_call_graph`; Add optional `file` param to `find_function`; Lower default `limit` for `search_code` to 20, cap at 50; Add `element_type` filter enum; Add warning to `get_impact_radius` description

### v1.11 (COMPLETED)
- Depth-limited get_call_graph_bounded: Add `get_call_graph_bounded` to prevent neighbor explosion; Unroll recursion manually for depth <= 3; Add `depth` and `max_results` parameters

### v1.10 (COMPLETED)
- Token Efficiency - signature_only Mode: Add `signature_only` mode to `get_context`; Add `max_tokens` parameter (default 4000); Update `extract_function` to capture function signature in metadata; Add `find_body_start_line` helper

### v1.9 (COMPLETED)
- AST Extraction Fixes: Fix `is_noise_call` filter with missing noise calls; Fix Go `implements` detection for embedded fields only

### v1.8 (COMPLETED)
- Documentation Indexing Fixes: Add `mcp_index_docs` MCP tool; Fix doc regex for any source extension; Fix code-block skipping; Fix `parse_doc_file` to extract headings; Add `resolve_call_edges` post-index resolution pass

### v1.7 (COMPLETED)
- Query Push-down Optimization + Security + Correctness Fixes: Add `search_by_name_typed` and `find_elements_by_name_exact` pushed-down queries; Add `run_element_query` helper; Add `escape_datalog` helper

### v1.6 (COMPLETED)
- Auto-Indexing on MCP Server Start: US-17 MCP server auto-indexes when starting if index is stale; US-18 Configurable auto-indexing via leankg.yaml

### v1.5 (COMPLETED)
- Phase 2 Features: US-10 Documentation-structure mapping; US-11 Enhanced business logic tagging with doc links; US-12 Impact analysis improvements; US-13 Additional MCP tools

---

## 1. Executive Summary

LeanKG is a lightweight, local-first knowledge graph solution designed for developers who use AI-assisted coding tools. The primary purpose is to provide AI models with accurate, concise codebase context without scanning unnecessary code, avoiding context window dilution, and ensuring documentation stays up-to-date with business logic mapping.

Unlike heavy frameworks like Graphiti that require external databases (Neo4j) and cloud infrastructure, LeanKG runs entirely locally on macOS and Linux with minimal resource consumption. It automatically generates and maintains documentation while mapping business logic to the existing codebase.

---

## 2. Problem Statement

### 2.1 Current Pain Points

| Pain Point | Description |
|------------|-------------|
| **Context Window Dilution** | AI tools scan entire codebases, including irrelevant files, wasting context window tokens |
| **Outdated Documentation** | Manual docs quickly become stale; AI receives wrong context |
| **Business Logic Disconnect** | No clear mapping between business requirements and code implementation |
| **Token Waste** | Redundant code scanning generates unnecessary token costs |
| **Poor Code Generation** | AI lacks accurate context, producing incorrect or suboptimal code |
| **Feature Transfer Difficulty** | Onboarding new developers requires extensive code exploration |
| **Impact radius lacks confidence grades** | `get_impact_radius` returns all edges at equal weight; LLM cannot distinguish "WILL BREAK" from "MIGHT BE AFFECTED" |
| **No pre-commit risk signal** | No tool exists to assess change risk before commit |
| **Flat search results** | `search_code` returns symbol matches with no grouping by functional area |

---

## 3. User Stories

### 3.1 Core MVP Stories (US-01 to US-18)

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-01 | Auto-index codebase so AI tools have accurate context | Must Have | DONE |
| US-02 | Generate and update documentation automatically | Must Have | DONE |
| US-03 | Map business logic to code for AI understanding | Must Have | DONE |
| US-04 | Expose MCP server for AI tool integration | Must Have | DONE |
| US-05 | Full CLI interface with query and MCP server commands | Must Have | DONE |
| US-06 | Minimal resource usage | Must Have | DONE |
| US-07 | Lightweight Web UI for graph visualization | Should Have | DONE |
| US-08 | Multi-language support (Go, TS, Python, Rust) | Must Have | DONE |
| US-09 | Pipeline information extraction from CI/CD configs | Should Have | DONE |
| US-10 | Documentation-structure mapping | Should Have | DONE |
| US-11 | Enhanced business logic tagging with doc links | Should Have | DONE |
| US-12 | Fix impact radius calculation for qualified names | Must Have | DONE |
| US-13 | Additional MCP tools for docs and pipeline queries | Should Have | DONE |
| US-14 | npm-based installation without Rust | Must Have | PENDING |
| US-15 | MCP server expose init/index/install tools | Should Have | DONE |
| US-16 | MCP server auto-initialize on startup | Should Have | DONE |
| US-17 | MCP server auto-re-index when starting if stale | Should Have | DONE |
| US-18 | Configurable auto-indexing via leankg.yaml | Should Have | DONE |

### 3.2 v2.0 Enhancement Stories (US-19 to US-27)

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-19 | Cross-file call edge resolution | Must Have | DONE |
| US-20 | Go `implements` edge extraction fix | Must Have | DONE |
| US-21 | Push-down Datalog queries + injection safety | Must Have | DONE |
| US-22 | Token-efficient `signature_only` context mode | Must Have | DONE |
| US-23 | Bounded depth call graph traversal | Should Have | DONE |
| US-24 | Fix `get_doc_for_file` query direction bug | Must Have | DONE |
| US-25 | Add `mcp_index_docs` MCP tool | Must Have | DONE |
| US-26 | Fix doc-code reference extraction | Should Have | DONE |
| US-27 | MCP tool definition quality improvements | Should Have | DONE |

### 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09)

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-GN-01 | Impact analysis with confidence scores and severity classifications | Must Have | DONE |
| US-GN-02 | Pre-commit `detect_changes` tool | Must Have | DONE |
| US-GN-03 | Multi-repo global registry | Should Have | PENDING |
| US-GN-04 | Cluster-grouped search results | Should Have | DONE |
| US-GN-05 | Auto-detect functional clusters | Should Have | DONE |
| US-GN-06 | 360-degree context view in single tool call | Should Have | DONE |
| US-GN-07 | Cluster-level SKILL.md generation | Could Have | PENDING |
| US-GN-08 | MCP Resources for overview context | Could Have | PENDING |
| US-GN-09 | Repository wiki generation | Won't Have | PENDING |

### 3.4 AB Testing Stories (US-AB-01 to US-AB-04)

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-AB-01 | OpenCode token parsing for benchmark comparison | Must Have | IN PROGRESS |
| US-AB-02 | Context correctness validation (precision/recall/F1) | Must Have | PENDING |
| US-AB-03 | CozoDB data store correctness tests | Must Have | PENDING |
| US-AB-04 | Token savings summary report with overall verdict | Should Have | PENDING |

---

## 4. Implementation Status Summary

### 4.1 Completed Features

| Feature | Source PRD | Implemented |
|---------|------------|-------------|
| Core indexing (Go, TS/JS, Python, Rust) | prd-leankg.md | DONE |
| Dependency graph with TESTED_BY edges | prd-leankg.md | DONE |
| CLI interface (init, index, query, generate, install, impact) | prd-leankg.md | DONE |
| MCP server with all required tools | prd-leankg.md | DONE |
| Documentation generation | prd-leankg.md | DONE |
| Business logic annotations | prd-leankg.md | DONE |
| Impact radius analysis | prd-leankg.md | DONE |
| Auto-install MCP config | prd-leankg.md | DONE |
| Web UI embedded in LeanKG binary | prd-leankg.md | DONE |
| Terraform (.tf) indexing | prd-leankg.md | DONE |
| CI/CD YAML indexing | prd-leankg.md | DONE |
| MCP tool schema improvements (required arrays, params) | prd-leankg-v2.0 | DONE |
| Signature-only context mode | prd-leankg-v2.0 | DONE |
| Cross-file call resolution | prd-leankg-v2.0 | DONE |
| Go implements edge fix | prd-leankg-v2.0 | DONE |
| Datalog injection prevention | prd-leankg-v2.0 | DONE |
| Bounded call graph | prd-leankg-v2.0 | DONE |
| get_doc_for_file fix | prd-leankg-v2.0 | DONE |
| mcp_index_docs tool | prd-leankg-v2.0 | DONE |
| Doc reference extraction fix | prd-leankg-v2.0 | DONE |
| Confidence scoring on relationships | prd-leankg-gitnexus | DONE |
| detect_changes tool | prd-leankg-gitnexus | DONE |
| get_clusters tool | prd-leankg-gitnexus | DONE |
| Cluster-grouped search | prd-leankg-gitnexus | DONE |
| Enhanced get_context with cluster info | prd-leankg-gitnexus | DONE |

### 4.2 Pending Features

| Feature | Source PRD | Priority | Notes |
|---------|------------|----------|-------|
| npm-based installation (FR-69 to FR-72) | prd-leankg.md | Must Have | Binary distribution |
| Global registry (FR-GN-08 to FR-GN-12) | prd-leankg-gitnexus | Should Have | Multi-repo support |
| Cluster-level SKILL.md generation (US-GN-07) | prd-leankg-gitnexus | Could Have | Depends on stable cluster detection |
| MCP Resources (FR-GN-20 to FR-GN-21) | prd-leankg-gitnexus | Could Have | Depends on multi-repo registry |
| Repository wiki generation (US-GN-09) | prd-leankg-gitnexus | Won't Have | Future consideration |

---

## 5. Functional Requirements

### 5.1 Core Features (DONE)

- [x] **FR-01 to FR-07**: Code Indexing and Dependency Graph
- [x] **FR-08 to FR-12**: Auto Documentation Generation
- [x] **FR-13 to FR-16**: Business Logic to Code Mapping
- [x] **FR-17 to FR-22**: Context Provisioning
- [x] **FR-23 to FR-27**: MCP Server Interface
- [x] **FR-28 to FR-36**: CLI Interface
- [x] **FR-37 to FR-41**: Lightweight Web UI
- [x] **FR-42 to FR-50**: Pipeline Information Extraction
- [x] **FR-51 to FR-56**: Documentation-Structure Mapping
- [x] **FR-57 to FR-60**: Enhanced Business Logic Tagging
- [x] **FR-61 to FR-64**: Impact Analysis Improvements
- [x] **FR-65 to FR-68**: Additional MCP Tools
- [x] **FR-73 to FR-76**: MCP Server Self-Initialization
- [x] **FR-77 to FR-79**: Terraform Infrastructure Indexing
- [x] **FR-80 to FR-82**: CI/CD YAML Indexing

### 5.2 GitNexus Enhancements

- [x] **FR-GN-01 to FR-GN-04**: Confidence Scoring on Relationships
- [x] **FR-GN-05 to FR-GN-07**: Pre-Commit Change Detection Tool
- [x] **FR-GN-08 to FR-GN-12**: Multi-Repo Global Registry (PARTIAL)
- [x] **FR-GN-13 to FR-GN-17**: Community Detection and Cluster-Grouped Search
- [x] **FR-GN-18 to FR-GN-19**: Enhanced 360-Degree Context Tool
- [x] **FR-GN-20 to FR-GN-21**: MCP Resources (PARTIAL)

### 5.3 AB Testing & Validation

- [ ] **FR-AB-01**: OpenCode token parsing for benchmark comparison
- [ ] **FR-AB-02**: Context correctness validation (precision/recall/F1 per task)
- [ ] **FR-AB-03**: CozoDB data store correctness tests (indexed elements, relationships, no duplicates)
- [ ] **FR-AB-04**: Prompt YAML format with `expected_files` field for ground truth
- [ ] **FR-AB-05**: Token savings summary report with overall verdict

---

## 6. Technical Architecture

### 6.1 Technology Stack

| Component | Technology | Status |
|-----------|------------|--------|
| Core Language | Rust | Active |
| Database | CozoDB (embedded SQLite) | Active |
| Code Parsing | tree-sitter | Active |
| MCP Server | Custom Rust | Active |
| CLI | Clap | Active |
| Web UI | Axum | Active |

### 6.2 Data Model

```
CodeElement:
  - qualified_name: string (PK)
  - type: file | function | class | import | export | pipeline | pipeline_stage | pipeline_step | terraform | cicd | document
  - name: string
  - file_path: string
  - line_start: int
  - line_end: int
  - language: string
  - parent_qualified: string (optional)
  - cluster_id: string (nullable)
  - cluster_label: string (nullable)
  - metadata: JSON (includes signature, headings)

Relationship:
  - id: integer (PK)
  - source_qualified: string (FK)
  - target_qualified: string (FK)
  - type: imports | implements | calls | contains | exports | tested_by | triggers | builds | depends_on | documented_by | references
  - confidence: float (0.0-1.0)
  - metadata: JSON

BusinessLogic:
  - id: integer (PK)
  - element_qualified: string (FK)
  - description: string
  - user_story_id: string (optional)
  - feature_id: string (optional)

Document:
  - id: integer (PK)
  - title: string
  - content: string
  - file_path: string
  - generated_from: string[]
  - last_updated: timestamp
```

---

## 7. Release Criteria

### 7.1 MVP (v1.x) - COMPLETED

- [x] Code indexing works for Go, TypeScript, Python, Rust
- [x] Dependency graph builds correctly with TESTED_BY edges
- [x] CLI commands functional (init, index, query, generate, install, impact)
- [x] MCP server exposes query tools including get_impact_radius and get_review_context
- [x] Documentation generation produces valid markdown
- [x] Business logic annotations can be created and queried
- [x] Impact radius analysis works (blast radius within N hops)
- [x] Auto-install MCP config works for Claude Code/OpenCode
- [x] Web UI shows basic graph visualization
- [x] Resource usage within targets
- [x] Documentation complete

### 7.2 v2.0 Release - COMPLETED

- [x] Cross-file call edges resolved correctly
- [x] Go implements edges only for embedded fields
- [x] Datalog injection prevention via escape_datalog
- [x] Push-down queries for search_code, find_function, query_file
- [x] signature_only mode for get_context
- [x] Bounded call graph with depth and max_results
- [x] mcp_index_docs tool functional
- [x] Doc reference extraction with code-block skipping

### 7.3 GitNexus Enhancements - IN PROGRESS

- [x] Confidence scoring on relationships
- [x] detect_changes tool
- [x] get_clusters tool
- [x] Cluster-grouped search
- [ ] Global registry (US-GN-03)
- [ ] MCP Resources (US-GN-08)

---

## 8. Roadmap

### Phase 1: MVP (v0.1.0) - COMPLETED
- Core indexing (Go, TS/JS, Python, Rust)
- Basic dependency graph
- CLI interface
- MCP server (basic queries)
- Documentation generation

### Phase 2: Enhanced Features (v0.2.0) - COMPLETED
- Pipeline information extraction
- Documentation-structure mapping
- Enhanced business logic tagging
- Impact analysis improvements
- Additional MCP tools
- Web UI embedded
- Terraform/CI-CD indexing

### Phase 3: v2.0 Corrections + GitNexus (v0.3.0) - NEARLY COMPLETE
- [DONE] Confidence scoring on relationships
- [DONE] Pre-commit change detection
- [DONE] Community detection
- [DONE] Enhanced context tool
- [PENDING] Multi-repo registry
- [PENDING] MCP Resources
- [PENDING] Cluster-level SKILL.md

### Phase 3.5: AB Testing & Validation (v0.3.1) - IN PROGRESS
- [IN PROGRESS] OpenCode token parsing for benchmark
- [PENDING] Context correctness validation (precision/recall/F1)
- [PENDING] CozoDB data store correctness tests
- [PENDING] Token savings summary report

### Phase 4: Advanced (v0.4.0) - FUTURE
- Vector embeddings
- Semantic search
- Cloud sync (optional)
- Team features

---

## 9. Non-Functional Requirements

| Metric | Target | Status |
|--------|--------|--------|
| Cold start time | < 2 seconds | TBD |
| Indexing speed | > 10,000 lines/second | TBD |
| Query response time | < 100ms | TBD |
| Memory usage (idle) | < 100MB | TBD |
| Memory usage (indexing) | < 500MB | TBD |
| detect_changes response time | < 2 seconds | TBD |
| get_context enhanced response size | < 4000 tokens | TBD |

---

## 10. Out of Scope

The following features are explicitly out of scope:

1. **Vector embeddings / semantic search** - Rule-based only (Phase 4)
2. **Cloud sync** - Fully local
3. **Multi-user / team features** - Single user only
4. **Advanced authentication** - Local token only
5. **Plugin system** - Future consideration
6. **Enterprise integrations** - Future consideration
7. **14 language support** - MVP focused: Go, TS/JS, Python, Rust
8. **Browser-based WebAssembly UI** - LeanKG targets CLI + MCP use case
9. **Symbol rename tool** - High complexity; better handled by AI agent
10. **Raw Datalog query passthrough** - Security risk

---

## 11. Glossary

| Term | Definition |
|------|------------|
| Knowledge Graph | Graph structure storing entities and relationships from codebase |
| Code Indexing | Process of parsing code and extracting structural information |
| MCP Server | Model Context Protocol server for AI tool integration |
| Context Window | AI model's input capacity; LeanKG minimizes tokens needed |
| Business Logic Mapping | Linking code to business requirements |
| Qualified Name | Natural node identifier: `file_path::parent::name` format |
| Blast Radius | All files affected by a change within N hops |
| Impact Radius | Same as blast radius |
| Pipeline | CI/CD workflow definition parsed into knowledge graph |
| Pipeline Stage | Named phase within a pipeline (build, test, deploy) |
| Pipeline Step | Individual action within a stage |
| Documentation Mapping | Linking documentation files to code elements |
| Traceability | Chain linking requirements -> documentation -> code |
| Confidence Score | Float 0.0-1.0 indicating edge reliability |
| Severity Classification | WILL BREAK / LIKELY AFFECTED / MAY BE AFFECTED |
| Cluster | Functional community of code elements |
| Unresolved Call Edge | `calls` relationship with `__unresolved__` prefix |
| Noise Call | Stdlib/trivial function excluded from graph |
| Signature-Only Mode | Context output with only function signature line |
| Datalog Injection | Security issue from unescaped user strings in queries |

---

## 12. References

- CozoDB: https://github.com/cozodb/cozo
- tree-sitter: https://tree-sitter.github.io/tree-sitter/
- MCP Protocol: https://modelcontextprotocol.io/
- GitNexus: https://github.com/abhigyanpatwari/GitNexus
- Leiden Algorithm: https://en.wikipedia.org/wiki/Leiden_algorithm

---

*Last updated: 2026-03-28*
