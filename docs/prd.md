# LeanKG PRD - Consolidated Tracking Document

**Version:** 3.5-unified
**Date:** 2026-07-13
**Status:** Active Development — **single source of truth** for product requirements + HLD
**Author:** Product Owner
**Target Users:** Software developers using AI coding tools (Cursor, OpenCode, Claude Code, Gemini CLI, etc.)
**Codebase Version:** 0.17.8

> All prior PRD/HLD files under `docs/requirement/`, `docs/design/hld-leankg.md`, and duplicate `.docs/` PRDs have been merged here. Do not recreate split PRDs — update this file only.

---

## Changelog

### v3.5-unified - Single PRD+HLD document
- Merged `docs/requirement/prd-leankg.md` (v2 team infrastructure) → Section 3.12 / 5.11
- Merged `docs/design/hld-leankg.md` → Section 6.4–6.9 (HLD)
- Merged `leankg update` CLI PRD → US-UPD-01
- Confirmed CBM structural parity already in 3.11 / 5.10; deleted redundant source files
- Codebase status refresh: env/incident/service tools, vacuum scheduler, `kg_self_test`, `leankg update` marked DONE where implemented
- Removed: `docs/requirement/prd-*.md`, `docs/design/hld-leankg.md`, `docs/LeanKG_v2_PRD.html`, duplicate `.docs` PRD stubs

### v3.4-cbm-structural-merge - Merge structural parity (CBM) PRD + codebase status refresh
- Merged CBM structural parity into this document (Section 3.11 US-CBM, Section 5.10 FR-CBM); source file removed in v3.5
- Codebase audit (2026-07-13, v0.17.8): **65 MCP tools** in `src/mcp/tools.rs`; Phase 1 aggregators DONE; Routes/HTTP_CALLS extractors DONE; typed resolve / clones / cross-repo / 3D UI still PENDING
- Updated executive metrics, pending table, and roadmap Phase 1 status to match code
- Source CBM PRD retained as archive with pointer to this document as SoT

### v3.3-graphify-parity - Graphify competitive enhancements
- Competitive analysis of [Graphify](https://github.com/Graphify-Labs/graphify) (v8 / ~83k stars) vs LeanKG deploy + agent tooling
- Full comparison: `docs/analysis/graphify-comparison-2026-07-13.md`
- Added US-GF-01..12 user stories for Graphify-inspired agent graph UX, edge provenance, reports, PRs, and learning loop
- Added FR-GF-01..20 functional requirements (Section 5.9)
- Priority focus: shortest-path / explain / NL subgraph query, EXTRACTED|INFERRED|AMBIGUOUS edge labels, god-node ranking, GRAPH_REPORT.md — not rewriting LeanKG's stronger RocksDB multi-project deploy

### v3.2-toon-format - TOON response format for MCP tools (~40% token reduction)
- Added US-TOON-01 user story for TOON (Token-Oriented Object Notation) format adoption
- TOON is a compact notation that reduces field name repetition in arrays
- Example: `elements[2]{qualified_name,type}: src/main.rs::main,function` vs JSON with full field names
- TOON spec: https://github.com/toon-format/toon
- Added Section 7.5 TOON Response Templates for all MCP tool categories

### v3.1-massive-graph - Massive graph service expansion
- Added US-MG-01..05 user stories for service node double-click behavior
- Added FR-MG-01..08 functional requirements for expand-service optimization and filter UI
- Expand-service API optimized: targeted folder query (7.7k vs 1.5M elements), ~30% faster
- FR-MG-01..02, 04..08 implemented: expand-service returns all edge types, double-click calls expandService directly, filter panel always shows all 14 types, defaults = Service/Folder/File/Function
- FR-MG-03 (single-repo root expansion) still pending

### v3.0-consolidated - Full codebase audit
- Deep dive codebase analysis: 35 MCP tools verified (0 stubs), 28+ CLI commands, 10 language extractors
- Updated language support: 10 fully extracted (Go, TS/JS, Python, Rust, Java, Kotlin, C++, C#, Ruby, PHP) + 3 parser-only (Dart, Swift, XML)
- Updated all user story statuses based on actual implementation
- Added missing feature sections: Git Hooks, Context Metrics, REST API, Wiki Generation, Global Registry, Graph Export, Orchestrator
- Unified RTK Compression status: ResponseCompressor (FR-RTK-11..15) now marked DONE
- Fixed US-GN-03 (Global Registry) status: DONE (was PENDING)
- Fixed AB Testing stories: US-AB-02..04 marked DONE
- Removed outdated references to non-existent features
- Added new user stories for recently implemented features

### v2.0-consolidated - Merged from 3 source PRDs
- Source 1: `prd-leankg.md` (v1.7, 2026-03-27)
- Source 2: `prd-leankg-v2.0-enhancements.md` (v2.0, 2026-03-27)
- Source 3: `prd-leankg-gitnexus-enhancements.md` (v1.0, 2026-03-27)

---

## 1. Executive Summary

LeanKG is a lightweight, local-first knowledge graph solution designed for developers who use AI-assisted coding tools. The primary purpose is to provide AI models with accurate, concise codebase context without scanning unnecessary code, avoiding context window dilution, and ensuring documentation stays up-to-date with business logic mapping.

Unlike heavy frameworks like Graphiti that require external databases (Neo4j) and cloud infrastructure, LeanKG runs entirely locally on macOS and Linux with minimal resource consumption. It automatically generates and maintains documentation while mapping business logic to the existing codebase.

**Key Metrics (v0.17.8 — audited 2026-07-13):**
- **65 MCP tools** defined in `src/mcp/tools.rs` (stdio + HTTP/SSE)
- 28+ CLI commands
- 10+ languages with extraction (Go, TS/JS, Python, Rust, Java, Kotlin, C++/C, C#, Ruby, PHP) + Dart/Swift/XML partial + Terraform/CI/Android
- 8 compression/read modes + TOON responses
- Smart orchestrator with persistent cache
- Git hooks (pre-commit, post-commit, post-checkout)
- REST API server with auth
- Context metrics tracking
- Global multi-repo registry
- RocksDB multi-project HTTP deploy
- Structural aggregators: `get_architecture`, `get_graph_schema`, `find_dead_code` (DONE)
- Route + `http_calls` extractors for Go (chi/gin/echo) and TS (express/fastify) (DONE)
- `wake_up` MCP tool (DONE)
- Call edges carry `resolution_method` + numeric `confidence` (`name` / `name_file_hint` / `unresolved`; `typed` not yet produced)

**Competitive notes:**
- vs [Graphify](https://github.com/Graphify-Labs/graphify): see Section 3.10 / `docs/analysis/graphify-comparison-2026-07-13.md`
- vs [codebase-memory-mcp](https://github.com/DeusData/codebase-memory-mcp): see Section 3.11 / 5.10 — Lean into business-context depth; close structural gaps; do **not** chase 158-language / Pure-C parity

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
| **No shortest-path / explain verbs** | Agents cannot ask "how do A and B connect?" or get a single-node dossier (Graphify gap) |
| **Opaque edge provenance** | Agents cannot tell EXTRACTED vs INFERRED vs AMBIGUOUS relationships at a glance |
| **No architecture brief artifact** | Missing god-node + surprising-connection report (`GRAPH_REPORT.md`) after index |
| **No query outcome learning** | Context metrics exist, but agents cannot report whether a graph answer was useful |

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
| US-08 | Multi-language support (Go, TS, Python, Rust, Java, Kotlin, C++, C#, Ruby, PHP) | Must Have | DONE |
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
| US-GN-03 | Multi-repo global registry | Should Have | DONE |
| US-GN-04 | Cluster-grouped search results | Should Have | DONE |
| US-GN-05 | Auto-detect functional clusters | Should Have | DONE |
| US-GN-06 | 360-degree context view in single tool call | Should Have | DONE |
| US-GN-07 | Cluster-level SKILL.md generation | Could Have | PENDING |
| US-GN-08 | MCP Resources for overview context | Could Have | PENDING |
| US-GN-09 | Repository wiki generation | Could Have | DONE |

### 3.4 AB Testing Stories (US-AB-01 to US-AB-05)

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-AB-01 | OpenCode token parsing for benchmark comparison | Must Have | DONE |
| US-AB-02 | Context correctness validation (precision/recall/F1) | Must Have | DONE |
| US-AB-03 | CozoDB data store correctness tests | Must Have | DONE |
| US-AB-04 | Token savings summary report with overall verdict | Should Have | DONE |
| US-AB-05 | Prompt YAML format with `expected_files` field for ground truth | Should Have | DONE |

### 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15)

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-RTK-01 | LeanKGCompressor for internal command compression | Must Have | DONE |
| US-RTK-02 | CargoTestCompressor with failures-only mode (85%+ savings) | Must Have | DONE |
| US-RTK-03 | GitDiffCompressor with stats extraction (70%+ savings) | Must Have | DONE |
| US-RTK-04 | ShellCompressor extended with leankg-specific patterns | Should Have | DONE |
| US-RTK-05 | 8 read modes: adaptive, full, map, signatures, diff, aggressive, entropy, lines | Must Have | DONE |
| US-RTK-06 | Entropy analysis (Shannon, Jaccard, Kolmogorov) | Should Have | DONE |
| US-RTK-07 | ResponseCompressor for MCP JSON responses | Must Have | DONE |
| US-RTK-08 | Compress impact_radius, call_graph, search_code responses | Must Have | DONE |
| US-RTK-09 | `compress_response` parameter on graph tools | Should Have | DONE |
| US-RTK-10 | `--compress` CLI flag for shell command output | Should Have | DONE |

### 3.6 Infrastructure Stories (US-INF-01 to US-INF-10)

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-INF-01 | Git pre-commit hook with critical file blocking | Must Have | DONE |
| US-INF-02 | Git post-commit hook with auto-incremental reindex | Should Have | DONE |
| US-INF-03 | Git post-checkout hook with branch-switch reindex | Should Have | DONE |
| US-INF-04 | GitWatcher for continuous index freshness | Should Have | DONE |
| US-INF-05 | Context metrics tracking with schema (18 fields) | Should Have | DONE |
| US-INF-06 | REST API server with health/status/search endpoints | Should Have | DONE |
| US-INF-07 | API key management with Argon2 hashing | Should Have | DONE |
| US-INF-08 | Wiki generation from code structure | Could Have | DONE |
| US-INF-09 | Graph export to HTML, SVG, GraphML, Neo4j formats | Should Have | DONE |
| US-INF-10 | Smart orchestrator with intent parsing and persistent cache | Should Have | DONE |

### 3.7 Additional Language Stories (US-LANG-01 to US-LANG-03)

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-LANG-01 | Dart parser (tree-sitter-dart) | Should Have | PARTIAL (parser only, no extraction) |
| US-LANG-02 | Swift parser (tree-sitter-swift) | Should Have | PARTIAL (parser only, no extraction) |
| US-LANG-03 | XML parser (tree-sitter-xml) | Could Have | PARTIAL (parser only, no extraction) |

### 3.8 Massive Graph Stories (US-MG-01 to US-MG-05)

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-MG-01 | Double-click Service node loads ALL elements and edges in one shot | Must Have | DONE |
| US-MG-02 | Single-repo projects expand fully on service double-click (no multi-level drilling) | Must Have | PARTIAL (expand-service called, FR-MG-03 pending) |
| US-MG-03 | Filter UI always shows ALL node type toggles regardless of loaded data | Must Have | DONE |
| US-MG-04 | Default visible filters: Service, Folder, File, Function ON; rest OFF | Must Have | DONE |
| US-MG-05 | Expand-service API optimized: targeted DB query instead of full scan | Must Have | DONE |

### 3.9 TOON Format Stories (US-TOON-01)

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-TOON-01 | MCP tool responses use TOON format for ~40% token reduction vs JSON | Must Have | DONE |

**Detailed Feature Descriptions:**

<details>
<summary>US-TOON-01: TOON Response Format</summary>

**Problem:** JSON responses from MCP tools include repetitive field names in arrays, wasting tokens.

**Solution:** Adopt TOON (Token-Oriented Object Notation) format which omits field names within array items when they match the schema template.

**Example comparison:**

JSON (312 tokens):
```json
{
  "elements": [
    {"qualified_name": "src/main.rs::main", "type": "function", "language": "rust"},
    {"qualified_name": "src/lib.rs::init", "type": "function", "language": "rust"}
  ],
  "tokens": 312
}
```

TOON (187 tokens, 40% reduction):
```
{
  elements[2]{qualified_name,type,language}:
    src/main.rs::main,function,rust
    src/lib.rs::init,function,rust
  tokens: 187
}
```

**Specification:** https://github.com/toon-format/toon

**Implementation:**
- All MCP tool responses wrapped in Response Format Envelope
- Envelope: `{status: ok, tool: <name>, format: toon|json, tokens: <count>, data: <toon_string>}`
- Default format is `toon`; clients can request `json` via `format=json` parameter

</details>

**Detailed Feature Descriptions:**

<details>
<summary>US-MG-01: Service node loads all elements and edges</summary>

**Problem:** Previously, double-clicking a Service node in the graph only returned a subset of relationship types (`contains`, `defines`, `imports`, `calls`). Other edges like `extends`, `implements`, `references`, `tested_by` were missing from the expanded view.

**Behavior:**
- Double-click on a Service node → `/api/graph/expand-service?path=<absolute_path>` returns ALL elements under that service folder AND ALL relationship types between them
- Backend must NOT filter by relationship type — let the frontend filter UI control visibility
- All node types are returned: `service`, `folder`, `directory`, `file`, `module`, `class`, `struct`, `interface`, `enum`, `function`, `method`, `constructor`, `property`, `decorator`

**Backend changes:**
- `api_graph_expand_service` handler removes the `matches!(r.rel_type.as_str(), "contains" | "defines" | "imports" | "calls")` filter
- Returns ALL relationships where source is in the service folder

**Frontend changes:**
- Filter UI is the sole mechanism for controlling what's visible
- User toggles edge types on/off to see calls, imports, contains, etc.
</details>

<details>
<summary>US-MG-02: Single-repo full expansion</summary>

**Problem:** When a service has many nested folder layers (e.g., `platform-transport/be-engagement/internal/handler/v2/`), the user must double-click through each folder level to see contents. This loses the overall service context.

**Behavior:**
- Double-click on a Service node loads the ENTIRE service tree at once
- All folders, sub-folders, files, and functions are loaded in a single API call
- The filter UI controls visibility: by default, only `Service`, `Folder`, `File`, `Function` nodes are shown
- User can toggle on `Method`, `Class`, etc. to see more detail without making another API call
- For single-repo projects (no multi-service layout), the same behavior applies — the root is treated as the "service"

**Rationale:** Loading everything at once is fast (~13s for 7.7k elements after optimization) and avoids the UX problem of losing the chart context during multi-level drilling.
</details>

<details>
<summary>US-MG-03: Filter UI always shows all node types</summary>

**Problem:** Previously `discoveredNodeTypes` was computed from loaded graph data. If the current view only has `File` and `Function` nodes, the filter panel only shows `File` and `Function` toggles.

**Behavior:**
- The filter panel ALWAYS shows ALL node types from `DEFAULT_NODE_TYPE_ORDER`: `Service`, `Folder`, `Directory`, `File`, `Module`, `Class`, `Struct`, `Interface`, `Enum`, `Function`, `Method`, `Constructor`, `Property`, `Decorator`
- This is a static list, not data-driven
- Types not present in current data still appear but are visually dimmed or show "(0)" count

**Implementation:**
- `discoveredNodeTypes` in `App.tsx` uses `DEFAULT_NODE_TYPE_ORDER` directly instead of computing from `data.nodes`
</details>

<details>
<summary>US-MG-04: Default visible filters</summary>

**Problem:** Previously the default visible labels included `Service`, `Folder`, `Directory`, `File` — missing `Function` which is the most important code-level type. Also, ALL types started as visible, making the graph too noisy.

**Behavior:**
- **Default ON (visible):** `Service`, `Folder`, `File`, `Function`
- **Default OFF (hidden):** `Directory`, `Module`, `Class`, `Struct`, `Interface`, `Enum`, `Method`, `Constructor`, `Property`, `Decorator`
- `resetToStructuralDefaults()` resets to these 4 types
- After double-clicking a service, filters reset to these 4 defaults

**Implementation:**
- `DEFAULT_VISIBLE_LABELS` = `['Service', 'Folder', 'File', 'Function']`
- `useGraphFilters` initial state uses `DEFAULT_VISIBLE_LABELS`
- `resetToStructuralDefaults()` uses `DEFAULT_VISIBLE_LABELS`
</details>

<details>
<summary>US-MG-05: Expand-service API optimization</summary>

**Problem:** The original `api_graph_expand_service` handler called `g.all_elements()` and `g.all_relationships()` which loaded ALL 1.5M elements and ALL 1.6M relationships into memory, then filtered in Rust. This took ~19 seconds.

**Solution (DONE):**
- Added `get_elements_in_folder()` to `GraphEngine` using CozoDB `regex_matches(file_path, $pat)` with bound parameter
- Handler converts absolute paths to DB format: `/Users/.../be-engagement` → `./platform-transport/be-engagement`
- Only loads ~7.7k relevant elements instead of 1.5M
- Response time: ~13s (30% improvement). Remaining time is from loading all 1.6M relationships.
</details>

### 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08)

> **Source:** Competitive analysis of [MemPalace](https://github.com/milla-jovovich/mempalace) — the highest-scoring AI memory system on LongMemEval (96.6% R@5 raw mode). Key differentiator: raw verbatim storage without summarization, structured spatial navigation (wings/rooms/closets/drawers), temporal entity graph with validity windows, and a 4-layer memory stack (L0-L3) for token-efficient context loading.

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-MP-01 | Temporal Knowledge Graph — relationships have valid_from/valid_to; historical queries ("what dependencies existed before the refactor?") | Must Have | PENDING |
| US-MP-02 | Layered Context Loading (L0-L3) — explicit token budgets per layer: L0 identity (~50 tok), L1 critical facts (~120 tok), L2 cluster context (on demand), L3 deep search (on demand) | Must Have | PENDING |
| US-MP-03 | Conversation/Decision Mining — import Claude/ChatGPT/Slack transcripts; auto-extract decisions, preferences, milestones that explain *why* code changed | Should Have | PENDING |
| US-MP-04 | Specialist Agent Contexts — define agent personas (reviewer, architect, ops) each with a focused lens on the codebase and their own session diary | Should Have | PENDING |
| US-MP-05 | Contradiction & Staleness Detection — detect when stored context contradicts current code state; flag stale annotations, outdated docs, broken traceability chains | Should Have | PENDING |
| US-MP-06 | Cross-Domain Tunnels — auto-link clusters across projects/modules that share the same domain concept (e.g., "auth" in both user-service and gateway) | Could Have | PENDING |
| US-MP-07 | Wake-up Context Protocol — standardized `wake_up` MCP tool that loads ~170 tokens of critical project facts at session start | Should Have | DONE |
| US-MP-08 | Folder Structure as Graph Edges — directories as first-class `directory` nodes with `contains` edges (dir→dir, dir→file, file→element), mirroring MemPalace's wing/room/closet/drawer hierarchy | Must Have | PENDING |

**Detailed Feature Descriptions:**

<details>
<summary>US-MP-01: Temporal Knowledge Graph</summary>

**MemPalace inspiration:** Entity relationships have validity windows (`valid_from`, `valid_to`). When something stops being true, it's invalidated but retained for historical queries.

**LeanKG adaptation:**
- Add `valid_from` and `valid_to` (nullable) fields to `Relationship` table
- When re-indexing detects a removed import/call, set `valid_to = now()` instead of deleting
- New MCP tool: `temporal_query` — "what did the dependency graph look like before commit X?"
- New MCP tool: `invalidate_edge` — manually mark an edge as no longer current
- Timeline view: chronological story of how a code element's dependencies evolved
</details>

<details>
<summary>US-MP-02: Layered Context Loading (L0-L3)</summary>

**MemPalace inspiration:** 4-layer memory stack where L0+L1 (~170 tokens) are always loaded, L2 is on-demand, L3 is deep search.

**LeanKG adaptation:**
- **L0 — Project Identity** (~50 tokens): Project name, languages, top-level directories, architecture pattern.
- **L1 — Critical Facts** (~120 tokens): Module map, critical dependencies, recent change hotspots.
- **L2 — Cluster Context** (on demand): When a query touches a specific area, load the relevant cluster's symbols.
- **L3 — Deep Search** (on demand): Full graph traversal, impact analysis, cross-cluster queries.
- New MCP tools: `wake_up` (L0+L1), `load_layer` (L2/L3)
</details>

<details>
<summary>US-MP-03: Conversation/Decision Mining</summary>

**MemPalace inspiration:** Mines conversation exports (Claude, ChatGPT, Slack) to extract decisions, preferences, milestones. Stores raw verbatim.

**LeanKG adaptation:**
- New indexer module: `conversation_indexer` — parses Claude/ChatGPT/Slack export JSON
- Extracts: decisions, preferences, milestones, problems
- Creates `decision`, `preference`, `milestone`, `problem` element types
- Links decisions to code elements via `decided_about` relationship
- Store raw verbatim — no summarization
- New CLI command: `leankg mine-conversations ~/chats/ --format claude|chatgpt|slack`
</details>

<details>
<summary>US-MP-04: Specialist Agent Contexts</summary>

**MemPalace inspiration:** Define agent personas (reviewer, architect, ops) each with their own wing and diary.

**LeanKG adaptation:**
- Agent config in `.leankg/agents/*.json` — focus areas and context filters
- Each agent gets a filtered view of the graph
- Agent diary: per-agent CozoDB table storing session notes
- New MCP tools: `agent_focus`, `agent_diary_write`, `agent_diary_read`
</details>

<details>
<summary>US-MP-05: Contradiction & Staleness Detection</summary>

**MemPalace inspiration:** `fact_checker.py` validates assertions against stored entity facts.

**LeanKG adaptation:**
- New module: `consistency_checker` — runs on `detect_changes` or standalone
- Checks: annotations referencing deleted code, documented_by links to moved files, stale clusters
- Severity: 🔴 BROKEN, 🟡 STALE, 🟢 CURRENT
- New MCP tool: `check_consistency`, new CLI: `leankg check-consistency`
</details>

<details>
<summary>US-MP-06: Cross-Domain Tunnels</summary>

**MemPalace inspiration:** "Tunnels" auto-connect rooms from different wings when the same topic appears.

**LeanKG adaptation:**
- Auto-detect shared domain concepts across clusters
- Create `tunnel` relationship type linking related clusters
- New MCP tool: `find_tunnels`
- Enhance `orchestrate` to follow tunnels
</details>

<details>
<summary>US-MP-07: Wake-up Context Protocol</summary>

**MemPalace inspiration:** `mempalace wake-up` loads ~170 tokens of L0+L1.

**LeanKG adaptation:**
- New MCP tool: `wake_up` — returns compressed project summary (~170 tokens)
- Content: project name, languages, top directories (wings), recent hotspots, critical files
- Cached in `.leankg/wake_up.txt`, regenerated on re-index
</details>

<details>
<summary>US-MP-08: Folder Structure as Graph Edges</summary>

**MemPalace inspiration:** MemPalace's wing → room → closet → drawer is a spatial hierarchy. Each level is a navigable node with typed edges.

**LeanKG adaptation:**
- **`directory` element type** — every indexed directory becomes a first-class node
- **`contains` edges for full hierarchy:**
  - `directory → directory` (e.g., `src/` contains `src/graph/`)
  - `directory → file` (e.g., `src/graph/` contains `query.rs`)
  - `file → function/class` (existing behavior)
- **qualified_name format:** `src/graph/` for directories (trailing slash distinguishes from files)
- **metadata on directory nodes:** `child_count`, `language_distribution`, `total_lines`
- **Impact analysis at directory level:** `get_impact_radius("src/indexer/")` shows all affected modules
- **Cluster-to-directory alignment:** When Leiden clusters map to physical directories, link them
- **Wake-up context:** L0/L1 lists top-level directories as "palace wings"
- **Folder-scoped search:** `search_code` and `query_file` accept directory qualified names

```
Palace Mapping:

  Wing (project area)     →  src/            [directory node]
    Room (module)         →  src/graph/      [directory node]
      Closet (file)       →  src/graph/query.rs  [file node]
        Drawer (element)  →  query.rs::GraphEngine  [function node]

  All connected by `contains` edges. Traversal = BFS from any directory.
```
</details>

### 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12)

> **Source:** Competitive analysis of [Graphify](https://github.com/Graphify-Labs/graphify) — AI coding-assistant skill that builds a queryable knowledge graph from code (tree-sitter, no LLM) plus optional docs/media. Key differentiators: `path` / `explain` / `query` agent verbs, every edge tagged `EXTRACTED|INFERRED|AMBIGUOUS`, god-node + surprising-connection reports, WHY/ADR rationale nodes, PR community conflict triage, and a work-memory reflect loop. Full matrix: `docs/analysis/graphify-comparison-2026-07-13.md`.
>
> **LeanKG keep / do not regress:** TOON/RTK token compression, requirement↔code traceability, microservice topology, severity-graded impact radius, CozoDB/RocksDB persistence, multi-project HTTP deploy.

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-GF-01 | Shortest path between two symbols/concepts (`leankg path A B` + MCP `shortest_path`) | Must Have | PENDING |
| US-GF-02 | Explain a node: source location, community/cluster, degree, labeled neighbors | Must Have | PENDING |
| US-GF-03 | Natural-language scoped subgraph query (`query_graph "what connects auth to DB?"`) | Must Have | PENDING |
| US-GF-04 | Edge provenance labels `EXTRACTED` / `INFERRED` / `AMBIGUOUS` on all relationships (unify with `resolution_method`) | Must Have | PENDING |
| US-GF-05 | God-node / hub ranking exposed via CLI + MCP (top-degree concepts; exclude utility super-hubs) | Must Have | PENDING |
| US-GF-06 | Generate `GRAPH_REPORT.md`: god nodes, surprising cross-module links, suggested questions, confidence summary | Should Have | PENDING |
| US-GF-07 | Extract rationale nodes from `# WHY:` / `# NOTE:` / `# HACK:` comments and ADR/RFC citations; link to code | Should Have | PENDING |
| US-GF-08 | PR impact dashboard: graph-aware PR review, community overlap / merge-order risk | Should Have | PENDING |
| US-GF-09 | Work-memory reflect loop: record Q&A outcomes; aggregate lessons that bias future query ranking | Should Have | PENDING |
| US-GF-10 | Expand language extractors toward Graphify breadth (Vue/Svelte, Scala, Lua, Zig, shell, Apex, …) | Could Have | PENDING |
| US-GF-11 | Portable graph snapshot export + optional git merge driver for team-committed graph artifacts | Could Have | PENDING |
| US-GF-12 | Live SQL / Postgres schema introspection into the same graph (tables, FKs, views ↔ app code) | Could Have | PENDING |

**Detailed Feature Descriptions:**

<details>
<summary>US-GF-01: Shortest Path</summary>

**Graphify inspiration:** `graphify path "FastAPI" "ModelField"` returns hop-by-hop edges with relation + confidence tags.

**LeanKG adaptation:**
- New MCP tool: `shortest_path(source, target, max_hops?)`
- New CLI: `leankg path <a> <b> [--max-hops N]`
- Resolve inputs by qualified_name, symbol name, or fuzzy label
- Return ordered hops: `{from, to, relation, confidence_label, source_file}`
- Prefer EXTRACTED edges when multiple equal-length paths exist
</details>

<details>
<summary>US-GF-02: Explain Node</summary>

**Graphify inspiration:** `graphify explain "APIRouter"` shows source, community, degree, and neighbor list.

**LeanKG adaptation:**
- New MCP tool: `explain_node(name_or_qn)`
- Aggregate: definition site, cluster membership, degree (in/out), top neighbors by relation type, importance/god-node rank if available
- Reuse `get_clusters`, dependents/dependencies, call graph — single agent-facing response
</details>

<details>
<summary>US-GF-03: NL Scoped Subgraph Query</summary>

**Graphify inspiration:** `graphify query "what connects auth to the database?"` returns a budgeted subgraph, not a full dump.

**LeanKG adaptation:**
- New MCP tool: `query_graph(question, token_budget?)`
- Pipeline: seed retrieval (keyword + optional embeddings) → bounded BFS/DFS expand → budget trim → TOON response
- Distinct from `orchestrate` (routing) and `kg_semantic_context` (embed pipeline): oriented to *connection* questions
- Surface confidence_label on every returned edge
</details>

<details>
<summary>US-GF-04: Edge Provenance Labels</summary>

**Graphify inspiration:** Every edge is `EXTRACTED` (explicit in source), `INFERRED` (resolver-derived), or `AMBIGUOUS` (needs review).

**LeanKG adaptation:**
- Map existing `resolution_method` (`name`, `name_file_hint`, `unresolved`, future `typed`) to provenance labels
- Store `confidence_label` on Relationship metadata; keep numeric confidence for impact severity
- Propagate labels through `get_impact_radius`, `get_call_graph`, `shortest_path`, `query_graph`, Web UI edge tooltips
</details>

<details>
<summary>US-GF-05: God-Node Ranking</summary>

**Graphify inspiration:** Report highlights most-connected concepts; optional hub exclusion for utilities.

**LeanKG adaptation:**
- Precompute degree / PageRank-like importance at index time (aligns with enhancement-analysis Priority 2)
- Expose via `get_architecture` hotspots and new `get_god_nodes(limit, exclude_hubs_percentile?)`
- CLI: `leankg gods [--limit N]`
</details>

<details>
<summary>US-GF-06: GRAPH_REPORT.md</summary>

**Graphify inspiration:** Three artifacts after build: `graph.html`, `GRAPH_REPORT.md`, `graph.json`.

**LeanKG adaptation:**
- On `index` / `leankg report`: write `.leankg/GRAPH_REPORT.md`
- Sections: god nodes, surprising cross-cluster edges, confidence distribution, 4–5 suggested agent questions
- Web UI link + MCP `get_graph_report`
</details>

<details>
<summary>US-GF-07: Rationale / WHY Nodes</summary>

**Graphify inspiration:** `# NOTE:` / `# WHY:` / `# HACK:` and ADR/RFC citations become first-class nodes linked to code.

**LeanKG adaptation:**
- Extractor pass for comment markers + markdown ADR/RFC links
- New element type: `rationale` with `explains` relationship to code elements
- Searchable via `search_code` / `search_annotations` / `query_graph`
</details>

<details>
<summary>US-GF-08: PR Impact Dashboard</summary>

**Graphify inspiration:** `graphify prs`, `--triage`, `--conflicts` (PRs sharing communities = merge-order risk).

**LeanKG adaptation:**
- CLI: `leankg prs [number] [--triage] [--conflicts]`
- MCP: `list_prs`, `get_pr_impact`, `triage_prs`
- Combine `detect_changes` + cluster membership of touched files
- Conflicts: PRs whose changed files share clusters
</details>

<details>
<summary>US-GF-09: Work-Memory Reflect Loop</summary>

**Graphify inspiration:** `save-result` + `reflect` → `LESSONS.md` and learning overlay that biases `explain` / `query`.

**LeanKG adaptation:**
- MCP: `report_query_outcome(question, nodes[], outcome: useful|dead_end|corrected)`
- Aggregate into `.leankg/reflections/LESSONS.md`
- Optional overlay tags on nodes: preferred / tentative / contested
- Feeds context-quality loop from enhancement-analysis Priority 6
</details>

<details>
<summary>US-GF-10..12: Coverage & Portability</summary>

**US-GF-10 Languages:** Prioritize high-demand gaps (Vue/Svelte/Astro, shell, Scala) before long-tail grammars.

**US-GF-11 Portable snapshot:** Export merge-friendly `graph-snapshot.json` (relative paths); optional git merge driver. Complements RocksDB deploy — does not replace it.

**US-GF-12 SQL schema:** Optional extractor for `.sql` + `leankg extract --postgres <dsn>` creating table/FK nodes linked to ORM/repository code when detectable.
</details>

### 3.11 CBM Structural Parity Stories (US-CBM) — merged from `prd-structural-parity-cbm.md`

> **Source:** Competitive analysis of [codebase-memory-mcp (CBM)](https://github.com/DeusData/codebase-memory-mcp) v0.9.0 vs LeanKG 0.17.8. Deep comparison notes also in `docs/analysis/` historical stubs.
>
> **Product rule:** Lean into business-context depth (ontology, knowledge, env, Android, req↔code). Close structural gaps that erode agent trust. Do **not** chase Pure-C / 158-language parity.
>
> **Tracks:** A Activate · B Structural · C Platform · D Dual-run escape hatch · E 3D graph UI
>
> **Codebase status audit:** 2026-07-13 against `src/` (v0.17.8)

#### Positioning (summary)

| Dimension | LeanKG | CBM |
|-----------|--------|-----|
| Stack | Rust + CozoDB/RocksDB | Pure C + SQLite |
| MCP | 65 tools, stdio + HTTP/SSE + REST | ~14 tools, stdio |
| Strength | Ontology, knowledge, env/incidents, Android, Docker+RocksDB, RTK | Speed, 158 langs, Hybrid LSP, clones, CROSS_*, static binary |
| Call resolve today | `name` / `name_file_hint` / `unresolved` + confidence | Hybrid LSP Tier 1/2/3 |

#### User stories — Track A Activate

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-CBM-A1 | Correct MCP `project` routing (multi-mount ≠ wrong RocksDB project) | Must Have | PENDING (ops; freepeak≠be historically) |
| US-CBM-A2 | Ontology online (`kg_ontology_status`, `concept_search` non-empty after sync) | Must Have | PARTIAL (tools exist; sync/activation ops-dependent) |
| US-CBM-A3 | Default call-edge resolution on index for Go/TS | Must Have | DONE (`src/indexer/call_graph.rs`) |
| US-CBM-A4 | Moat smoke (ontology + routing) gates Phase 1 “done” | Must Have | PENDING |

#### User stories — Track B Structural

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-CBM-B1 | Typed call resolution Go + TypeScript MVP (`resolution_method=typed`) | Must Have | PENDING |
| US-CBM-B2 | HTTP Route nodes + `http_calls` edges (≥2 Go + ≥2 TS frameworks) | Must Have | DONE (`src/indexer/route_extractor.rs`: chi/gin/echo + express/fastify) |
| US-CBM-B3 | `get_architecture` single-call overview | Must Have | DONE |
| US-CBM-B4 | `get_graph_schema` label/edge counts | Must Have | DONE |
| US-CBM-B5 | Dead code detection (`find_dead_code`) | Should Have | DONE |
| US-CBM-B6 | Event channel edges (EMITS / LISTENS_ON) | Should Have | PENDING |
| US-CBM-B7 | Clone / near-duplicate detection (`find_clones`, `similar_to`) | Should Have | PENDING (rel types stubbed in `models.rs` only) |
| US-CBM-B8 | Cross-repo edges on multi-repo registry | Should Have | PENDING (`cross_repo_similar` stub only) |
| US-CBM-B9 | Call `resolution_method` + numeric `confidence` on edges | Must Have | DONE (`name`/`name_file_hint`/`unresolved`; `typed` reserved) |
| US-CBM-B10 | Feature flag `typed_resolve=off\|go,ts\|all` | Must Have | PENDING |
| US-CBM-B11 | Architecture/schema honor token budgets / truncation | Must Have | PENDING (FR-B22) |
| US-CBM-B12 | ≥10 `run_raw_query` recipes in skills/docs | Should Have | PENDING |

#### User stories — Track C Platform

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-CBM-C1 | Docker image: embeddings / semantic tools OOTB | Should Have | PENDING |
| US-CBM-C2 | Query hot-path cache (search/schema/architecture/find_function) | Should Have | PENDING |
| US-CBM-C3 | Selective language expansion with quality tiers | Should Have | PENDING |
| US-CBM-C4 | Large-scale + Go/TS vs CBM benchmarks | Must/Should | PENDING |
| US-CBM-C5 | Windows build + smoke | Could Have | PENDING |

#### User stories — Track D Dual-run

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-CBM-D1 | Skills remain LeanKG-first; optional CBM escape hatch only | Must Have | DONE (policy in AGENTS/skills) |
| US-CBM-D2 | Do not auto-install CBM into default `.mcp.json` | Must Have | DONE |
| US-CBM-D3 | Re-evaluate dual-run after typed-resolve Phase | Must Have | PENDING |

#### User stories — Track E 3D Visualization

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-CBM-E1 | New 3D graph UI (`graph-ui/`) with WebGL galaxy + Bloom (keep existing 2D `ui/`) | Should Have | PENDING (`graph-ui/` absent; `ui/` is 2D) |
| US-CBM-E2 | Server-computed 3D layout in Rust + `get_graph_layout` / `/api/graph` | Should Have | PENDING |
| US-CBM-E3 | Adaptive rendering (InstancedMesh &lt;75k; point sprites above) | Should Have | PENDING |
| US-CBM-E4 | Node detail + edge-type filter panels | Should Have | PENDING |

<details>
<summary>US-CBM detailed notes (implementation evidence)</summary>

**DONE evidence:**
- `get_architecture` / `get_graph_schema` / `find_dead_code` — `src/mcp/tools.rs`, `src/mcp/handler.rs`, `src/graph/query.rs`
- `resolution_method` + `confidence` — `src/indexer/call_graph.rs`
- Routes + `http_calls` — `src/indexer/route_extractor.rs`, `RelationshipType::HttpCalls` in `src/db/models.rs`
- `wake_up` — `src/mcp/handler.rs` (also closes MemPalace US-MP-07)

**PENDING evidence:**
- No `typed` / `typed_resolve` matches in `src/`
- No `find_clones` MCP tool; `SimilarTo` / `CrossRepoSimilar` enum-only
- No `graph-ui/` directory; no `get_graph_layout`

**Won’t Have (this program):** Full 158-language parity; Pure-C rewrite; replace Cozo/RocksDB; full Hybrid LSP for all CBM families in one release; drop HTTP/SSE/REST or Docker team path.
</details>

### 3.12 Team Knowledge Infrastructure (US-V2) — merged from `prd-leankg.md` v2

> **Vision:** Evolve from local-first single-dev tool to shared knowledge backbone for multi-service teams: environment-scoped graph, incident knowledge, CI freshness, token-budgeted MCP tools.
>
> **Codebase status audit:** 2026-07-13 (v0.17.8)

| ID | User Story | Priority | Status |
|----|------------|----------|--------|
| US-V2-01 | Environment namespacing (`local` / `staging` / `production` / `upcoming`) on nodes/edges; env-scoped queries | Must Have | DONE (`env` on models; MCP `env` args) |
| US-V2-02 | Incident knowledge layer — contribute/query incidents linked to services | Must Have | DONE (`query_incidents`, `leankg incident add`) |
| US-V2-03 | Enhanced service context (deps, incidents, env) in one MCP call | Must Have | DONE (`get_service_context`) |
| US-V2-04 | Surface environment conflicts before promote/push | Must Have | DONE (`find_env_conflicts`) |
| US-V2-05 | Team/knowledge contribution via MCP (`add_knowledge`, annotations) | Must Have | DONE |
| US-V2-06 | Semantic search natural language → graph nodes | Should Have | DONE (`semantic_search`; embeddings optional) |
| US-V2-07 | Token budget enforcement on MCP responses | Must Have | DONE (TOON + token_budget + RTK) |
| US-V2-08 | Scheduled DB vacuum on long-lived MCP servers | Should Have | DONE (`LEANKG_VACUUM_INTERVAL_HOURS`) |
| US-V2-09 | Ontology `kg_self_test` + HTTP startup self-test WARN | Should Have | DONE (`kg_self_test`) |
| US-V2-10 | Multi-repo / shared RocksDB HTTP backend for teams | Must Have | DONE (registry + mcp-http + RocksDB compose) |
| US-UPD-01 | `leankg update` installs latest GitHub release binary | Should Have | DONE (`Update` CLI subcommand) |

**v2 success metrics (targets):**

| Metric | Target |
|--------|--------|
| Full service context in Cursor | < 5s |
| Context tokens per session (LeanKG queries) | < 2,000 |
| Graph freshness after release hook | < 3 minutes |
| Env conflict catch rate (schema) | > 80% |

<details>
<summary>US-V2 data model (from HLD/PRD v2)</summary>

**Incident node fields:** id, env, title, severity (P0–P3), occurred_at, resolved_at, root_cause, resolution, affected services, trigger_pattern, prevention, tags, author, linked_ticket

**v2 edges:** `caused_incident`, `resolved_by`, `conflicts_with`, `deployed_to`, `supercedes`

**Service metadata:** version, deploy_env, slo_p99_ms, health_endpoint, on_call, incident_count, last_incident, tags
</details>

---

## 4. Implementation Status Summary

### 4.1 Completed Features

| Feature | Implementation Detail |
|---------|-----------------------|
| Core indexing | 10+ languages extracted: Go, TS/JS, Python, Rust, Java, Kotlin, C++/C, C#, Ruby, PHP (+ Android/Terraform/CI) |
| Dependency graph | Imports, Calls, References, TestedBy, Contains, Defines, Implements, HttpCalls, … |
| CLI interface | 28+ commands including init, index, query, generate, web, mcp-stdio, mcp-http, impact, export, annotate, trace, benchmark, register, api-serve, hooks, wiki, metrics, run |
| MCP server | **65 tools** via stdio + HTTP/SSE (`src/mcp/tools.rs`) |
| Structural aggregators | `get_architecture`, `get_graph_schema`, `find_dead_code` (CBM Phase 1) |
| Call edge metadata | `resolution_method` + numeric `confidence` on CALLS |
| HTTP routes | `route` elements + `http_calls` for Go (chi/gin/echo) and TS (express/fastify) |
| Wake-up context | `wake_up` MCP tool (~L0/L1 identity summary) |
| Documentation generation | AGENTS.md, CLAUDE.md generation with template engine |
| Business logic annotations | Create, update, delete, search, traceability |
| Impact radius analysis | BFS traversal with confidence scores, severity classification |
| Auto-install MCP config | .mcp.json generation for Cursor, OpenCode, Claude, Gemini, Kilo, Codex, Antigravity |
| Web UI | 2D force-directed graph (`ui/`); 20+ routes |
| Terraform indexing | .tf file parsing with resource, data, variable, output, module extraction |
| CI/CD YAML indexing | GitHub Actions, GitLab CI, Azure Pipelines |
| Documentation mapping | docs/ directory indexing, documented_by/references edges |
| Traceability | Requirements -> documentation -> code chain |
| Confidence scoring | 0.0-1.0 confidence + WILL_BREAK/LIKELY_AFFECTED/MAY_BE_AFFECTED severity |
| Change detection | Pre-commit risk analysis with critical/high/medium/low classification |
| Cluster detection | Community detection with Leiden algorithm, cluster-grouped search |
| 360-degree context | get_review_context + orchestrate with cache-graph-compress flow |
| RTK compression | 8 read modes, specialized compressors, entropy analysis, response compression, TOON |
| Orchestrator | Intent parsing, persistent cache, adaptive compression |
| Git hooks | pre-commit, post-commit, post-checkout, GitWatcher |
| Context metrics | 18-field schema with tool_name, tokens, savings, F1 score |
| REST API | Health, status, search endpoints with CORS and auth middleware |
| Global registry | Multi-repo management: register, unregister, list, status-repo, setup |
| Wiki generation | Markdown wiki from code structure |
| Graph export | JSON, DOT/Mermaid, HTML (interactive), SVG, GraphML, Neo4j |
| API keys | Argon2-hashed key store with create, list, revoke |
| Shell runner | `leankg run` with optional RTK compression |
| Ontology / semantic | Concept tools + optional embeddings (`--features embeddings`) |

### 4.2 Pending Features

| Feature | Priority | Notes |
|---------|----------|-------|
| Typed call resolve Go/TS (US-CBM-B1, FR-B03..05) | Must Have | `typed` method not produced yet |
| `typed_resolve` feature flag (FR-B08) | Must Have | Slipped with typed resolve |
| Architecture token budget (FR-B22) | Must Have | DONE (per-section max_items truncation + truncated_sections metadata) |
| MCP project routing smoke (US-CBM-A1/A4) | Must Have | Ops / multi-mount |
| Graphify path/explain/query (US-GF-01..03) | Must Have | Agent graph primitives |
| Edge provenance labels (US-GF-04) | Must Have | EXTRACTED/INFERRED/AMBIGUOUS |
| God-node ranking (US-GF-05) | Must Have | Hub ranking at index time |
| Event edges EMITS/LISTENS (US-CBM-B6) | Should Have | Phase 2 CBM |
| Clone detection (US-CBM-B7) | Should Have | Enum stub only |
| Cross-repo edges (US-CBM-B8) | Should Have | Enum stub only |
| Embeddings Docker default (US-CBM-C1) | Should Have | Platform friction |
| Hot-path query cache (US-CBM-C2) | Should Have | Latency |
| 3D graph UI Track E (US-CBM-E*) | Should Have | New `graph-ui/`; keep 2D |
| GRAPH_REPORT.md / WHY / PR / reflect (US-GF-06..09) | Should Have | Graphify |
| MemPalace temporal/layers (US-MP-* except wake_up) | Must/Should | US-MP-07 wake_up DONE |
| npm-based installation (US-14) | Must Have | Binary distribution via npm |
| Single-repo root expansion (FR-MG-03) | Must Have | Treat root as service on double-click |
| Cluster-level SKILL.md (US-GN-07) | Could Have | |
| MCP Resources (US-GN-08) | Could Have | |
| Language breadth / Windows / SLSA | Could Have | CBM Track C/B4 |

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

### 5.2 GitNexus Enhancements (DONE)

- [x] **FR-GN-01 to FR-GN-04**: Confidence Scoring on Relationships
- [x] **FR-GN-05 to FR-GN-07**: Pre-Commit Change Detection Tool
- [x] **FR-GN-08 to FR-GN-12**: Multi-Repo Global Registry
- [x] **FR-GN-13 to FR-GN-17**: Community Detection and Cluster-Grouped Search
- [x] **FR-GN-18 to FR-GN-19**: Enhanced 360-Degree Context Tool

### 5.3 AB Testing & Validation (DONE)

- [x] **FR-AB-01**: OpenCode token parsing for benchmark comparison
- [x] **FR-AB-02**: Context correctness validation (precision/recall/F1 per task)
- [x] **FR-AB-03**: CozoDB data store correctness tests
- [x] **FR-AB-04**: Prompt YAML format with `expected_files` field
- [x] **FR-AB-05**: Token savings summary report with overall verdict

### 5.4 RTK Compression (DONE)

- [x] **FR-RTK-01**: LeanKGCompressor struct for CLI command compression
- [x] **FR-RTK-02**: CargoTestCompressor with failures-only mode (85%+ savings)
- [x] **FR-RTK-03**: GitDiffCompressor with stats extraction (70%+ savings)
- [x] **FR-RTK-04**: ShellCompressor with leankg-specific patterns
- [x] **FR-RTK-05**: 8 read modes via FileReader (adaptive, full, map, signatures, diff, aggressive, entropy, lines)
- [x] **FR-RTK-06**: EntropyAnalyzer (Shannon, Jaccard, Kolmogorov, repetitive patterns)
- [x] **FR-RTK-07**: ResponseCompressor for MCP JSON responses
- [x] **FR-RTK-08**: Compressed responses for impact_radius, call_graph, search_code, dependencies, dependents, context
- [x] **FR-RTK-09**: `compress_response` parameter on get_impact_radius and other graph tools
- [x] **FR-RTK-10**: `--compress` CLI flag on `leankg run` command

### 5.5 Infrastructure Features (DONE)

- [x] **FR-INF-01**: Git pre-commit hook with critical file blocking
- [x] **FR-INF-02**: Git post-commit hook triggers `leankg index --incremental`
- [x] **FR-INF-03**: Git post-checkout hook triggers reindex on branch switch
- [x] **FR-INF-04**: GitWatcher for continuous index freshness via commit hash markers
- [x] **FR-INF-05**: Context metrics tracking (18-field CozoDB schema)
- [x] **FR-INF-06**: REST API server (Axum) with /health, /api/v1/status, /api/v1/search
- [x] **FR-INF-07**: API key management (Argon2 hash, create/list/revoke)
- [x] **FR-INF-08**: Wiki generation from code structure
- [x] **FR-INF-09**: Graph export (HTML interactive, SVG, GraphML, Neo4j, JSON, DOT/Mermaid)
- [x] **FR-INF-10**: Orchestrator with intent parsing (7 types) and persistent cache

### 5.6 MemPalace-Inspired Features (PENDING)

- [ ] **FR-MP-01**: Add `valid_from` (timestamp) and `valid_to` (nullable timestamp) to Relationship schema
- [ ] **FR-MP-02**: On re-index, set `valid_to = now()` on removed edges instead of deleting them
- [ ] **FR-MP-03**: New MCP tool `temporal_query` — query graph state as of a given timestamp or commit
- [ ] **FR-MP-04**: New MCP tool `timeline` — chronological evolution of a code element's relationships
- [ ] **FR-MP-05**: Generate `.leankg/identity.md` (L0 context, ~50 tokens) on `init` and `index`
- [ ] **FR-MP-06**: Generate `.leankg/critical_facts.md` (L1 context, ~120 tokens) from graph stats + git log
- [ ] **FR-MP-07**: New MCP tool `wake_up` — returns L0+L1 in ~170 tokens, cached and regenerated on re-index
- [ ] **FR-MP-08**: New MCP tool `load_layer` — load L2 (cluster) or L3 (deep) context on demand
- [ ] **FR-MP-09**: New conversation_indexer module: parse Claude export JSON format
- [ ] **FR-MP-10**: New conversation_indexer module: parse ChatGPT export JSON format
- [ ] **FR-MP-11**: New conversation_indexer module: parse Slack export JSON format
- [ ] **FR-MP-12**: Extract decisions, preferences, milestones, problems from conversations as new element types
- [ ] **FR-MP-13**: New CLI command `mine-conversations` with `--format` and `--project` flags
- [ ] **FR-MP-14**: New MCP tool `check_consistency` — detect stale/broken links, outdated annotations
- [ ] **FR-MP-15**: New CLI command `check-consistency` with `--severity` filter
- [ ] **FR-MP-16**: New relationship type `tunnel` for cross-cluster domain links
- [ ] **FR-MP-17**: New MCP tool `find_tunnels` — discover cross-cluster connections
- [ ] **FR-MP-18**: Agent config system: `.leankg/agents/*.json` with focus and filter definitions
- [ ] **FR-MP-19**: New MCP tools `agent_focus`, `agent_diary_write`, `agent_diary_read`
- [ ] **FR-MP-20**: Enhance `orchestrate` intent parser to follow tunnels and use L0-L3 layer strategy
- [ ] **FR-MP-21**: `directory` element type — every indexed directory becomes a first-class graph node
- [ ] **FR-MP-22**: `contains` edges for full hierarchy: directory→directory, directory→file (extends existing file→element)
- [ ] **FR-MP-23**: Directory metadata: `child_count`, `language_distribution`, `total_lines` in metadata JSON
- [ ] **FR-MP-24**: `get_impact_radius` accepts directory qualified names (e.g., `"src/indexer/"`) for module-level analysis
- [ ] **FR-MP-25**: `search_code` and `query_file` accept directory nodes for folder-scoped search
- [ ] **FR-MP-26**: Cluster-to-directory alignment: when Leiden cluster maps to a physical directory, store `cluster_directory` in cluster metadata

### 5.7 Massive Graph UI (DONE)

- [x] **FR-MG-01**: `api_graph_expand_service` returns ALL relationship types (remove `matches!(r.rel_type, "contains" | "defines" | "imports" | "calls")` filter)
- [x] **FR-MG-02**: Double-click Service node loads entire service tree in single API call
- [ ] **FR-MG-03**: Single-repo projects treated as single service — root double-click loads everything
- [x] **FR-MG-04**: Filter panel always shows ALL node types from `DEFAULT_NODE_TYPE_ORDER` (static list, not data-driven)
- [x] **FR-MG-05**: Default visible node types: `Service`, `Folder`, `File`, `Function` (all others OFF by default)
- [x] **FR-MG-06**: `resetToStructuralDefaults()` resets to `DEFAULT_VISIBLE_LABELS` (Service, Folder, File, Function)
- [x] **FR-MG-07**: `get_elements_in_folder()` targeted DB query for expand-service (regex_matches with bound param)
- [x] **FR-MG-08**: Handler converts absolute folder paths to DB format (`./platform-transport/...`)

### 5.8 Multi-Language Support

| Language | Extensions | Extractor Status | Parser |
|----------|-----------|-----------------|--------|
| Go | `.go` | DONE | tree-sitter-go |
| TypeScript/JavaScript | `.ts`, `.tsx`, `.js`, `.jsx` | DONE | tree-sitter-typescript |
| Python | `.py` | DONE | tree-sitter-python |
| Rust | `.rs` | DONE | tree-sitter-rust |
| Java | `.java` | DONE | tree-sitter-java |
| Kotlin | `.kt`, `.kts` | DONE | tree-sitter-kotlin-ng |
| C/C++ | `.cpp`, `.cxx`, `.cc`, `.hpp`, `.h`, `.c` | DONE | tree-sitter-cpp |
| C# | `.cs` | DONE | tree-sitter-c-sharp |
| Ruby | `.rb` | DONE | tree-sitter-ruby |
| PHP | `.php` | DONE | tree-sitter-php |
| Dart | `.dart` | PARTIAL (parser only) | tree-sitter-dart |
| Swift | `.swift` | PARTIAL (parser only) | tree-sitter-swift |
| XML | `.xml` | PARTIAL (parser only) | tree-sitter-xml |
| Terraform | `.tf` | DONE (regex) | Custom extractor |
| CI/CD YAML | `.yml`, `.yaml` | DONE (custom) | GitHub Actions, GitLab CI, Azure Pipelines |
| Markdown | `.md` | DONE (doc indexer) | pulldown-cmark |

### 5.9 Graphify-Inspired Features (PENDING)

> Evidence: `docs/analysis/graphify-comparison-2026-07-13.md`. Deploy parity with Graphify HTTP MCP is **not** a gap — LeanKG RocksDB multi-project compose is competitive. Focus requirements on agent query UX and edge honesty.

- [ ] **FR-GF-01**: MCP tool `shortest_path(source, target, max_hops?)` returns ordered hops with relation + `confidence_label`
- [ ] **FR-GF-02**: CLI `leankg path <a> <b>` wrapping FR-GF-01
- [ ] **FR-GF-03**: MCP tool `explain_node(name_or_qn)` — definition, cluster, degree, neighbors
- [ ] **FR-GF-04**: CLI `leankg explain <name>` wrapping FR-GF-03
- [ ] **FR-GF-05**: MCP tool `query_graph(question, token_budget?)` — seed → expand → budget trim → TOON
- [ ] **FR-GF-06**: CLI `leankg query "<question>"` wrapping FR-GF-05
- [ ] **FR-GF-07**: Relationship metadata field `confidence_label` ∈ {EXTRACTED, INFERRED, AMBIGUOUS}
- [ ] **FR-GF-08**: Map `resolution_method` → `confidence_label` at edge write time; backfill on reindex
- [ ] **FR-GF-09**: Propagate `confidence_label` in impact, call_graph, path, query_graph, Web UI
- [ ] **FR-GF-10**: Index-time god-node / importance scoring (degree + optional PageRank-like)
- [ ] **FR-GF-11**: MCP `get_god_nodes(limit, exclude_hubs_percentile?)` + CLI `leankg gods`
- [ ] **FR-GF-12**: Include god nodes in `get_architecture` hotspots section
- [ ] **FR-GF-13**: Generate `.leankg/GRAPH_REPORT.md` on index/report (god nodes, surprises, suggested questions)
- [ ] **FR-GF-14**: MCP `get_graph_report` returns report markdown or structured sections
- [ ] **FR-GF-15**: Extractor for `# WHY:` / `# NOTE:` / `# HACK:` → `rationale` elements + `explains` edges
- [ ] **FR-GF-16**: ADR/RFC citation extraction from docs → rationale nodes linked to code
- [ ] **FR-GF-17**: CLI `leankg prs` + MCP `list_prs` / `get_pr_impact` / `triage_prs` using clusters + detect_changes
- [ ] **FR-GF-18**: Community conflict detection: PRs whose changed files share clusters (merge-order risk)
- [ ] **FR-GF-19**: MCP `report_query_outcome` + `leankg reflect` → `.leankg/reflections/LESSONS.md` + optional node overlay
- [ ] **FR-GF-20**: Portable `graph-snapshot.json` export (relative paths) + documented optional git merge driver

### 5.10 CBM Structural Parity Requirements (merged)

> Canonical FR IDs retained from `prd-structural-parity-cbm.md` (FR-A/B/C/D/E). Status audited 2026-07-13.

#### Track A — Activate

- [ ] **FR-A01**: MCP `project` resolves to correct RocksDB project for multi-mount setups
- [ ] **FR-A02**: Automate/document ontology sync for concepts + workflows YAML
- [ ] **FR-A03**: Verify ontology/knowledge tools after sync
- [ ] **FR-A04**: Index per `leankg.yaml`; expose counts
- [x] **FR-A05**: Default call-edge resolution for Go/TS on index
- [ ] **FR-A06**: Smoke: ontology + routing must pass before Phase 1 “fully done”
- [x] **FR-A07**: Agent operating-model: LeanKG-first; moat tools mandatory

#### Track B — Structural

- [x] **FR-B01**: `resolution_method`: unresolved \| name \| name_file_hint \| typed (typed reserved, not produced)
- [x] **FR-B02**: Numeric `confidence` consistent with method
- [ ] **FR-B03**: Go typed resolve MVP
- [ ] **FR-B04**: TS/TSX typed resolve MVP
- [ ] **FR-B05**: Benchmark harness vs CBM (50-edge samples)
- [ ] **FR-B06**: Python + Rust typed resolve (Could)
- [x] **FR-B07**: Fail soft: fall back to name resolve; never block index
- [ ] **FR-B08**: Feature flag `typed_resolve=off\|go,ts\|all`
- [x] **FR-B10**: `route` element type (method, path, handler, framework)
- [x] **FR-B11**: ≥ 2 Go + ≥ 2 TS framework extractors (chi/gin/echo + express/fastify)
- [x] **FR-B12**: `http_calls` edges with confidence
- [ ] **FR-B13**: Extend `service_calls` beyond k8s DNS regex (Should)
- [x] **FR-B14**: Routes searchable; included in `get_architecture`
- [ ] **FR-B15**: EMITS / LISTENS_ON for Go/TS (Should)
- [ ] **FR-B16**: Runtime trace ingestion (Could)
- [x] **FR-B20**: `get_architecture`
- [x] **FR-B21**: `get_graph_schema`
- [x] **FR-B22**: Honor token budgets / truncation on architecture/schema
- [x] **FR-B23**: `find_dead_code`
- [ ] **FR-B30**: Near-clone detection → similarity edges (Should)
- [ ] **FR-B31**: `find_clones` MCP (Should)
- [ ] **FR-B32**: Cross-repo edges across registry (Should)
- [ ] **FR-B33**: Cross-repo summary in tool or architecture (Should)
- [ ] **FR-B40..B44**: IaC Resource/Module, ADR, snapshot, DATA_FLOWS (Could)
- [ ] **FR-B50**: ≥ 10 `run_raw_query` recipes (Should)
- [ ] **FR-B51**: Optional openCypher→Cozo subset (Could)

#### Track C — Platform

- [ ] **FR-C01**: Docker embeddings OOTB (Should)
- [ ] **FR-C02**: Document smaller-model / batch-size options (Should)
- [ ] **FR-C03**: Hot-path cache (Should)
- [ ] **FR-C04**: Profile impact-radius latency (Should)
- [ ] **FR-C05**: Incremental languages with tier notes (Should)
- [ ] **FR-C06**: Per-language quality tier template; Go/TS first (Must Go/TS)
- [ ] **FR-C07**: Large-repo benchmark ≥ 1M nodes or documented ceiling (Should)
- [ ] **FR-C08..C11**: Windows, pkg channel, SLSA, install targets (Could)

#### Track D — Dual-run

- [x] **FR-D01**: Skills remain LeanKG-first
- [x] **FR-D02**: Documented CBM escape hatch when confidence low / lang unsupported (Should — documented as policy)
- [x] **FR-D03**: No auto-install CBM into default `.mcp.json`
- [ ] **FR-D04**: Re-evaluate dual-run after Phase 3 typed resolve

#### Track E — 3D Graph UI (all PENDING)

- [ ] **FR-E01..E05**: Vite/React/R3F/shadcn stack in `graph-ui/`
- [ ] **FR-E10..E14**: Rust 3D layout + `get_graph_layout` / `/api/graph`
- [ ] **FR-E20..E28**: R3F scene, Bloom, adaptive LOD, edge colors
- [ ] **FR-E30..E36**: Detail/filter panels, settings, multi-repo galaxies
- [ ] **FR-E40..E43**: HTTP integration; embed or static serve; keep 2D `ui/` untouched

### 5.11 Team Infrastructure / v2 Requirements (merged from `prd-leankg.md`)

- [x] **FR-V2-01**: `env` field on elements/relationships; default `local`
- [x] **FR-V2-02**: Incident data model + CLI/MCP contribute & query
- [x] **FR-V2-03**: `get_service_context` with env + incident summary
- [x] **FR-V2-04**: `find_env_conflicts` with risk levels
- [x] **FR-V2-05**: Knowledge contribution (`add_knowledge` / annotations)
- [x] **FR-V2-06**: `semantic_search` (embeddings feature-flagged)
- [x] **FR-V2-07**: Per-tool token budgets / TOON compression
- [x] **FR-V2-08**: Vacuum scheduler (`LEANKG_VACUUM_INTERVAL_HOURS`; RocksDB no-op)
- [x] **FR-V2-09**: `kg_self_test` MCP + HTTP startup WARN (non-gating)
- [x] **FR-V2-10**: Multi-project RocksDB HTTP deploy + registry
- [x] **FR-UPD-01**: `leankg update` from GitHub releases
- [ ] **FR-V2-11**: CI/CD auto-graph update on release (< 3 min freshness) — PARTIAL (hooks exist; formal GHA template may need docs)
- [ ] **FR-V2-12**: `get_team_map` ownership/on-call tool — PENDING (if not covered by knowledge)

---

## 6. Technical Architecture / HLD

### 6.1 Technology Stack

| Component | Technology | Version |
|-----------|------------|---------|
| Core Language | Rust | 1.70+ (edition 2021) |
| Database | CozoDB (embedded SQLite-backed) | 0.2 |
| Code Parsing | tree-sitter | 0.25 |
| MCP Server | rmcp (Rust MCP library) | 1.2 |
| CLI Framework | Clap | 4 |
| Web UI | Axum | 0.7 |
| Async Runtime | Tokio | 1 |
| File Watching | notify | 7 |
| Parallel Processing | rayon | 1.10 |
| Markdown Parsing | pulldown-cmark | 0.12 |
| Auth (API keys) | Argon2 | 0.5 |
| CORS | tower-http | 0.6 |

### 6.2 Data Model

```
CodeElement:
  - qualified_name: string (PK) - format: "path/to/file.rs::function_name" or "path/to/dir/" for directories
  - element_type: string - directory | file | function | class | import | export | pipeline | pipeline_stage | pipeline_step | terraform | cicd | document | doc_section
  - name: string
  - file_path: string
  - line_start: int
  - line_end: int
  - language: string
  - parent_qualified: string? (nullable)
  - cluster_id: string? (nullable)
  - cluster_label: string? (nullable)
  - metadata: JSON (includes signature, headings, ci_platform, child_count for directories, etc.)

Relationship:
  - source_qualified: string (FK)
  - target_qualified: string (FK)
  - rel_type: string - imports | calls | references | documented_by | tested_by | tests | contains | defines | implements | implementations | tunnel | decided_about
  - confidence: float (0.0-1.0)
  - metadata: JSON
  Indexes: rel_type_index, target_qualified_index

> **Folder-as-Graph Design (MemPalace-inspired):** Directories are first-class `directory` nodes in the graph. The `contains` edge is overloaded to represent the full hierarchy: `directory → directory`, `directory → file`, `file → function/class`. This mirrors MemPalace's wing → room → closet → drawer spatial architecture:
>
> | MemPalace | LeanKG | Edge |
> |-----------|--------|------|
> | Wing (project/person) | Top-level directory (`src/`, `docs/`) | `contains` |
> | Room (topic) | Sub-directory (`src/graph/`, `src/mcp/`) | `contains` |
> | Closet (summary) | File (`src/graph/query.rs`) | `contains` |
> | Drawer (verbatim) | Function/class within file | `contains` |
>
> Benefits:
> - **Impact analysis at directory level:** "What modules are affected if I change anything in `src/indexer/`?"
> - **Cluster-to-directory alignment:** Auto-detect when a Leiden cluster maps to a physical directory
> - **Wake-up context includes module map:** L0/L1 can list top-level directories as the "palace wings"
> - **Tunnel edges between directories:** Link `src/auth/` and `src/middleware/` when they share domain concepts
> - **Folder search:** `query_file` and `search_code` can scope to directory nodes

BusinessLogic:
  - element_qualified: string (PK, FK)
  - description: string
  - user_story_id: string? (nullable)
  - feature_id: string? (nullable)

ContextMetric:
  - tool_name: string (indexed)
  - timestamp: int (indexed)
  - project_path: string (indexed)
  - input_tokens: int
  - output_tokens: int
  - output_elements: int
  - execution_time_ms: int
  - baseline_tokens: int
  - baseline_lines_scanned: int
  - tokens_saved: int
  - savings_percent: float
  - (+ optional fields: correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted)

QueryCache:
  - cache_key: string (unique)
  - value_json: string
  - created_at: int
  - ttl_seconds: int
  - tool_name: string
  - project_path: string
  - metadata: JSON

ApiKey:
  - id: string (UUID)
  - name: string
  - key_hash: string (Argon2)
  - created_at: int
  - last_used_at: int?
  - revoked_at: int?
```

### 6.3 Module Map

```
src/
├── main.rs              # CLI entry point (28+ commands)
├── lib.rs               # Library exports
├── cli/                 # Clap command enum + ShellRunner
├── config/              # ProjectConfig, IndexerConfig, DocConfig, McpConfig
├── db/                  # CozoDB models, schema, operations, API key store
├── doc/                 # DocGenerator, template rendering, wiki generation
├── doc_indexer/         # Documentation indexing (docs/ → documented_by edges)
├── graph/               # GraphEngine, queries, context, traversal, clustering, cache, export (HTML/SVG/GraphML/Neo4j)
├── indexer/             # tree-sitter parsers (13), extractors, git analysis, Terraform, CI/CD
├── mcp/                 # MCP tools (65), handler, server (rmcp), auth, write tracker
├── orchestrator/        # Query orchestration with intent parsing and persistent cache
├── compress/            # RTK-style compression: 8 read modes, response/shell/cargo/git compressors, entropy analysis
├── web/                 # Axum web UI (20+ routes, embedded HTML/CSS/JS)
├── api/                 # REST API handlers, auth middleware
├── watcher/             # notify-based file watcher for auto-indexing
├── hooks/               # Git hooks (pre-commit, post-commit, post-checkout, GitWatcher)
├── benchmark/           # Benchmark runner (LeanKG vs OpenCode/Gemini/Kilo)
├── registry.rs          # Global repository registry (multi-repo management)
└── runtime.rs           # Tokio runtime utilities
```

### 6.4 HLD — System Overview (merged from `hld-leankg.md`)

```
+-----------------------------------------------------+
|                   LeanKG Backend                    |
|            (Axum + CozoDB / RocksDB)                |
|                                                     |
|  +--------------+  +--------------+  +----------+  |
|  |  production  |  |   staging    |  |  local   |  |
|  |  namespace   |  |  namespace   |  |namespace |  |
|  +--------------+  +--------------+  +----------+  |
|                                                     |
|  +-----------------------------------------------+  |
|  |  CozoDB (Datalog) + optional HNSW embeddings  |  |
|  +-----------------------------------------------+  |
+-----------------------------------------------------+
         ^                    ^                ^
         |                    |                |
  +------+------+    +--------+-----+   +------+------+
  |  MCP server |    |  REST API    |   |  Web UI     |
  |  stdio/HTTP |    |  /api/...    |   |  2D (+3D E) |
  +------+------+    +--------+-----+   +-------------+
         |                    |
  +------+------+    +--------+---------------------+
  | AI assistants|   | CI/CD hooks / GitHub Actions |
  +--------------+   +------------------------------+
```

### 6.5 HLD — Component Design

**Data layer:** `env` on `code_elements` / `relationships`; incidents + service metadata tables; all queries filter by env (default `local`).

**Graph engine tools (v2):** `get_service_context`, `find_env_conflicts`, `query_incidents`, env-aware impact.

**MCP auth headers (HTTP):** `X-LeanKG-Token` / Bearer; optional engineer + env headers.

**CLI (v2):** `leankg incident add|list|show`, `leankg update`, note/pattern/env helpers as implemented.

**Vacuum scheduler:** tokio task; `LEANKG_VACUUM_INTERVAL_HOURS` (default 1, `0` disables); Sqlite VACUUM; RocksDB debug no-op; invalidate caches after success.

**Ontology self-test:** `kg_self_test` + HTTP startup WARN (non-gating) for arity/schema drift on `kg_*` tools.

### 6.6 HLD — Key Data Flows

**Incident contribution:** CLI/API → validate Incident → CozoDB → available to MCP queries.

**Env conflicts:** fetch service across envs → compare schema/config/endpoints/deploy → risk HIGH/MEDIUM/LOW.

**Vacuum:** boot → spawn loop → vacuum → log → sleep.

**kg_self_test:** bind HTTP → run OntologyQueryEngine::self_test → info if OK, warn per failure → still serve.

### 6.7 HLD — Implementation Phases (v2)

| Phase | Scope | Status |
|-------|-------|--------|
| 1 Data model & schema (`env`, Incident) | Schema | DONE |
| 2 Graph engine env/incident queries | Engine | DONE |
| 3 MCP tools + token budgets | MCP | DONE |
| 4 CLI incident/update | CLI | DONE |
| 5 Integration tests / CI template | Test/Docs | PARTIAL |

### 6.8 HLD — Interface Sketches

`query_incidents` input: `{service, pattern?, env, limit}` → incidents[].  
`find_env_conflicts` input: `{service}` → conflicts[{type, detail, risk}].  
`leankg incident add --title … --severity P1 --affected svc --env production`.

### 6.9 HLD — Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Schema migration breaks v1 data | High | Default `env=local` for existing rows |
| Token budgets too tight | Medium | Configurable budgets + TOON |
| Scale to large multi-service graphs | High | RocksDB, caches, pagination |
| Concurrent writes | Medium | Cozo transactions |
| Unbounded DB growth | Medium | Hourly vacuum |
| Ontology arity drift → MCP -32603 | High | `kg_self_test` startup WARN |

---

## 7. MCP Tools (65 total — audited 2026-07-13)

### Project Management (5)
| Tool | Description |
|------|-------------|
| `mcp_init` | Initialize LeanKG project |
| `mcp_index` | Index codebase |
| `mcp_index_docs` | Index docs directory |
| `mcp_install` | Create .mcp.json |
| `mcp_status` | Show index status |

### Impact & Dependency (6)
| Tool | Description |
|------|-------------|
| `mcp_impact` | Calculate blast radius |
| `get_impact_radius` | Affected files within N hops with confidence/severity |
| `detect_changes` | Pre-commit risk analysis |
| `get_dependencies` | Direct imports of a file |
| `get_dependents` | Files depending on target |
| `get_review_context` | Focused subgraph + review prompt |

### Code Search (7)
| Tool | Description |
|------|-------------|
| `search_code` | Search by name/type |
| `find_function` | Locate function definition |
| `query_file` | Find file by pattern |
| `get_callers` | Find callers of a function |
| `get_call_graph` | Bounded call chain |
| `get_code_tree` | Codebase structure |
| `find_large_functions` | Oversized functions by line count |

### Context & Compression (3)
| Tool | Description |
|------|-------------|
| `get_context` | AI-optimized file context |
| `ctx_read` | Read file with 8 compression modes |
| `orchestrate` | Smart query routing with cache |

### Testing & Docs (7)
| Tool | Description |
|------|-------------|
| `get_tested_by` | Test coverage info |
| `get_doc_for_file` | Docs referencing code element |
| `get_files_for_doc` | Code elements in a doc |
| `get_doc_structure` | Documentation directory structure |
| `get_doc_tree` | Doc tree with hierarchy |
| `generate_doc` | Generate documentation |
| `find_related_docs` | Docs related to code change |

### Traceability (2)
| Tool | Description |
|------|-------------|
| `get_traceability` | Full traceability chain |
| `search_by_requirement` | Code for a requirement |

### Clustering & Graph (3)
| Tool | Description |
|------|-------------|
| `get_clusters` | Functional communities |
| `get_cluster_context` | Cluster symbols and dependencies |
| `generate_graph_report` | Comprehensive graph analysis |

### Export & Utility (2)
| Tool | Description |
|------|-------------|
| `export_graph` | Export in json/html/svg/graphml/neo4j |
| `mcp_hello` | Health check / debug |

### 7.5 TOON Response Templates

All MCP tool responses use TOON (Token-Oriented Object Notation) format by default for ~40% token reduction. See [TOON Specification](https://github.com/toon-format/toon) for details.

**Response Format Envelope:**
```
{
  status: ok|error
  tool: <tool_name>
  format: toon|json
  tokens: <token_count>
  data: <response_data>
}
```

**TOON Format Examples:**

1. **Search/Query Results:**
```
{
  status: ok
  tool: search_code
  format: toon
  tokens: 156
  data:
    results[3]{qualified_name,type,language}:
      src/main.rs::main,function,rust
      src/lib.rs::init,function,rust
      src/cli.rs::run,function,rust
}
```

2. **Impact Radius:**
```
{
  status: ok
  tool: get_impact_radius
  format: toon
  tokens: 203
  data:
    impact[4]{qualified_name,type,severity,confidence}:
      src/main.rs::main,function,WILL_BREAK,1.0
      src/lib.rs::init,function,LIKELY_AFFECTED,0.85
      src/config.rs::load,function,LIKELY_AFFECTED,0.72
      tests/main_test.rs::test_main,test,MAY_BE_AFFECTED,0.31
}
```

3. **Dependencies/Dependents:**
```
{
  status: ok
  tool: get_dependencies
  format: toon
  tokens: 98
  data:
    dependencies[2]{qualified_name,type}:
      src/lib.rs,file
      src/config.rs,file
}
```

4. **Call Graph:**
```
{
  status: ok
  tool: get_call_graph
  format: toon
  tokens: 187
  data:
    calls[3]{from,to,depth}:
      src/main.rs::main,src/lib.rs::init,1
      src/main.rs::main,src/cli.rs::run,1
      src/lib.rs::init,src/config.rs::load,2
}
```

5. **Context/Compression:**
```
{
  status: ok
  tool: get_context
  format: toon
  tokens: 412
  data:
    context:
      sig[1]: src/main.rs::main->()
      imports[2]: src/lib.rs,src/config.rs
      calls[1]: src/lib.rs::init
}
```

6. **Cluster/Graph Data:**
```
{
  status: ok
  tool: get_clusters
  format: toon
  tokens: 234
  data:
    clusters[2]{id,name,members}:
      c1,mcp_tools,12
      c2,graph_core,8
}
```

**JSON Fallback:** Clients can request JSON format by adding `format=json` parameter to any MCP tool call.

---

## 8. Release Criteria

### 8.1 MVP (v1.x) - COMPLETED

- [x] Code indexing works for 10 languages
- [x] Dependency graph builds correctly with 10 relationship types
- [x] CLI commands functional (28+ commands)
- [x] MCP server exposes 65 tools (audited 2026-07-13 in `src/mcp/tools.rs`)
- [x] Documentation generation produces valid markdown
- [x] Business logic annotations can be created and queried
- [x] Impact radius analysis works with confidence scores
- [x] Auto-install MCP config works for 7 AI tools
- [x] Web UI shows interactive graph visualization (20+ routes)
- [x] Resource usage within targets

### 8.2 v2.0 Release - COMPLETED

- [x] Cross-file call edges resolved correctly
- [x] Go implements edges only for embedded fields
- [x] Datalog injection prevention via escape_datalog
- [x] Push-down queries for search_code, find_function, query_file
- [x] signature_only mode for get_context
- [x] Bounded call graph with depth and max_results
- [x] mcp_index_docs tool functional
- [x] Doc reference extraction with code-block skipping

### 8.3 v3.0 Release (Current: v0.11.1) - NEARLY COMPLETE

- [x] RTK compression (8 modes, response compression)
- [x] Smart orchestrator with persistent cache
- [x] Git hooks (pre/post-commit, post-checkout, GitWatcher)
- [x] Context metrics tracking
- [x] REST API server with auth
- [x] Global multi-repo registry
- [x] Wiki generation
- [x] Graph export (HTML, SVG, GraphML, Neo4j)
- [x] Cluster detection and cluster-grouped search
- [x] Pre-commit change detection with severity
- [x] Benchmark runner (vs OpenCode, Gemini, Kilo)
- [ ] npm-based installation (US-14)
- [ ] Dart/Swift/XML entity extraction
- [ ] REST API auth wiring + mutation endpoints

---

## 9. Non-Functional Requirements

| Metric | Target | Status |
|--------|--------|--------|
| Cold start time | < 2 seconds | TBD |
| Indexing speed | > 10,000 lines/second (parallel via rayon) | TBD |
| Query response time | < 100ms | TBD |
| Memory usage (idle) | < 100MB | TBD |
| Memory usage (indexing) | < 500MB | TBD |
| detect_changes response time | < 2 seconds | TBD |
| get_context enhanced response size | < 4000 tokens | TBD |
| Batch insert size | 5000 rows/batch | DONE |
| Supported parser count | 13 parsers (10 fully extracted) | DONE |
| MCP tool count | 65 tools (`src/mcp/tools.rs`) | DONE (audited 2026-07-13) |

---

## 10. Out of Scope

1. **Full multi-modal PDF/image/video graph ingest (Graphify-style)** - Code + docs + infra first
2. **Cloud SaaS hosting of LeanKG** - Self-hosted only (team HTTP MCP / RocksDB is in scope)
3. **Multi-user collaborative editing of the graph** - Single writer per project DB; shared read via HTTP MCP is OK
4. **Plugin system** - Future consideration
5. **Raw Datalog query passthrough** - Security risk (except controlled `run_raw_query`)
6. **Replacing CozoDB/RocksDB with NetworkX-only primary store** - Snapshot export is additive
7. **Full 158-language / Pure-C rewrite (CBM chase)** - Selective languages only
8. **Split PRD/HLD documents** - This file is the only SoT; do not recreate `docs/requirement/prd-*.md` or `docs/design/hld-leankg.md`

---

## 11. Glossary

| Term | Definition |
|------|------------|
| Knowledge Graph | Graph structure storing entities and relationships from codebase |
| Code Indexing | Process of parsing code and extracting structural information |
| MCP Server | Model Context Protocol server for AI tool integration (rmcp) |
| Context Window | AI model's input capacity; LeanKG minimizes tokens needed |
| Business Logic Mapping | Linking code to business requirements |
| Qualified Name | Natural node identifier: `file_path::parent::name` format |
| Blast Radius / Impact Radius | All files affected by a change within N hops |
| Confidence Score | Float 0.0-1.0 indicating edge reliability |
| Confidence Label | EXTRACTED / INFERRED / AMBIGUOUS provenance |
| Severity Classification | WILL BREAK / LIKELY AFFECTED / MAY BE AFFECTED |
| Cluster | Functional community (Leiden) |
| God Node | High-degree hub concept |
| Environment Namespace | `local` / `staging` / `production` / `upcoming` partition of graph data |
| Incident Node | Structured outage/knowledge record linked to services |
| Vacuum Scheduler | Periodic SQLite VACUUM on long-lived MCP servers |
| RTK | Rust Token Killer — compression reducing LLM tokens |
| Orchestrator | Intent parsing + persistent cache |
| Global Registry | Multi-repo management for cross-project queries |
| Temporal Graph | Relationships with valid_from/valid_to |
| Wake-up Protocol | Minimal L0+L1 context at session start |
| HLD | High-Level Design — architecture and flows in Section 6.4–6.9 |

---

## 12. References

- CozoDB: https://github.com/cozodb/cozo
- tree-sitter: https://tree-sitter.github.io/tree-sitter/
- MCP Protocol: https://modelcontextprotocol.io/
- rmcp: https://crates.io/crates/rmcp
- Leiden Algorithm: https://en.wikipedia.org/wiki/Leiden_algorithm
- MemPalace: https://github.com/milla-jovovich/mempalace
- Graphify: https://github.com/Graphify-Labs/graphify — `docs/analysis/graphify-comparison-2026-07-13.md`
- codebase-memory-mcp: https://github.com/DeusData/codebase-memory-mcp — see Section 3.11 / 5.10
- Context enhancement analysis: `docs/analysis/enhancement-analysis-2026-07-09.md`
- Roadmap: `docs/roadmap.md`
- MCP tool reference: `docs/mcp-tools.md`
- CLI reference: `docs/cli-reference.md`

---

*Last updated: 2026-07-13 (v3.5-unified — single PRD+HLD SoT; redundant PRD/HLD files removed)*
