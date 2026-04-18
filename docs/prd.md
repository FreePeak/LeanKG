# LeanKG PRD - Consolidated Tracking Document

**Version:** 3.2-toon-format
**Date:** 2026-04-18
**Status:** Active Development
**Author:** Product Owner
**Target Users:** Software developers using AI coding tools (Cursor, OpenCode, Claude Code, Gemini CLI, etc.)
**Codebase Version:** 0.11.1

---

## Changelog

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

**Key Metrics (v0.11.1):**
- 35 MCP tools (all fully implemented)
- 28+ CLI commands
- 10 languages with full extraction + 3 parser-only
- 8 compression/read modes
- Smart orchestrator with persistent cache
- Git hooks (pre-commit, post-commit, post-checkout)
- REST API server with auth
- Context metrics tracking
- Global multi-repo registry

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
| US-MP-07 | Wake-up Context Protocol — standardized `wake_up` MCP tool that loads ~170 tokens of critical project facts at session start | Should Have | PENDING |
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

---

## 4. Implementation Status Summary

### 4.1 Completed Features

| Feature | Implementation Detail |
|---------|-----------------------|
| Core indexing | 10 languages fully extracted: Go, TS/JS, Python, Rust, Java, Kotlin, C++, C#, Ruby, PHP |
| Dependency graph | Imports, Calls, References, TestedBy, Tests, Contains, Defines, Implements, Implementations edges |
| CLI interface | 28+ commands including init, index, query, generate, web, mcp-stdio, impact, export, annotate, trace, benchmark, register, api-serve, hooks, wiki, metrics, run |
| MCP server | 35 tools via stdio transport using rmcp crate |
| Documentation generation | AGENTS.md, CLAUDE.md generation with template engine |
| Business logic annotations | Create, update, delete, search, traceability |
| Impact radius analysis | BFS traversal with confidence scores, severity classification |
| Auto-install MCP config | .mcp.json generation for Cursor, OpenCode, Claude, Gemini, Kilo, Codex |
| Web UI | 20+ routes: dashboard, graph viewer, code browser, docs, annotate, quality, export, settings |
| Terraform indexing | .tf file parsing with resource, data, variable, output, module extraction |
| CI/CD YAML indexing | GitHub Actions, GitLab CI, Azure Pipelines |
| Pipeline impact analysis | Blast radius extended to pipelines and deployment targets |
| Documentation mapping | docs/ directory indexing, documented_by/references edges |
| Traceability | Requirements -> documentation -> code chain |
| Confidence scoring | 0.0-1.0 confidence + WILL_BREAK/LIKELY_AFFECTED/MAY_BE_AFFECTED severity |
| Change detection | Pre-commit risk analysis with critical/high/medium/low classification |
| Cluster detection | Community detection with Leiden algorithm, cluster-grouped search |
| 360-degree context | get_review_context + orchestrate with cache-graph-compress flow |
| RTK compression | 8 read modes, 3 specialized compressors, entropy analysis, response compression |
| Orchestrator | Intent parsing (7 query types), persistent cache, adaptive compression |
| Git hooks | pre-commit (critical file blocking), post-commit (auto-reindex), post-checkout (branch switch) |
| Context metrics | 18-field schema with tool_name, tokens, savings, F1 score |
| REST API | Health, status, search endpoints with CORS and auth middleware |
| Global registry | Multi-repo management: register, unregister, list, status-repo, setup |
| Wiki generation | Markdown wiki from code structure |
| Graph export | JSON, DOT/Mermaid, HTML (interactive), SVG, GraphML, Neo4j |
| API keys | Argon2-hashed key store with create, list, revoke |
| Shell runner | `leankg run` with optional RTK compression |

### 4.2 Pending Features

| Feature | Priority | Notes |
|---------|----------|-------|
| npm-based installation (US-14) | Must Have | Binary distribution via npm |
| Single-repo root expansion (FR-MG-03) | Must Have | Treat root as service on double-click |
| Cluster-level SKILL.md generation (US-GN-07) | Could Have | Depends on stable cluster detection |
| MCP Resources (US-GN-08) | Could Have | MCP resource endpoints |
| Dart entity extraction (US-LANG-01) | Should Have | Parser exists, needs extractor |
| Swift entity extraction (US-LANG-02) | Should Have | Parser exists, needs extractor |
| XML entity extraction (US-LANG-03) | Could Have | Parser exists, needs extractor |
| REST API completion | Should Have | Auth wiring, mutation endpoints |

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

---

## 6. Technical Architecture

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
├── mcp/                 # MCP tools (35), handler, server (rmcp), auth, write tracker
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

---

## 7. MCP Tools (35 total)

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
- [x] MCP server exposes 35 query tools
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
| MCP tool count | 35 tools (0 stubs) | DONE |

---

## 10. Out of Scope

1. **Vector embeddings / semantic search** - Rule-based only
2. **Cloud sync** - Fully local
3. **Multi-user / team features** - Single user only
4. **Plugin system** - Future consideration
5. **Enterprise integrations** - Future consideration
6. **Raw Datalog query passthrough** - Security risk

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
| Blast Radius | All files affected by a change within N hops |
| Impact Radius | Same as blast radius |
| Confidence Score | Float 0.0-1.0 indicating edge reliability |
| Severity Classification | WILL BREAK / LIKELY AFFECTED / MAY BE AFFECTED |
| Cluster | Functional community of code elements (Leiden algorithm) |
| RTK (Rust Token Killer) | Compression module reducing LLM token consumption by 60-90% |
| Orchestrator | Smart query routing with intent parsing and persistent cache |
| Read Mode | File compression mode: adaptive, full, map, signatures, diff, aggressive, entropy, lines |
| GitWatcher | Component that monitors git events and triggers reindexing |
| Global Registry | Multi-repo management system for cross-project queries |
| Entropy Analysis | Shannon entropy, Jaccard similarity, Kolmogorov adjustment for information density |
| Temporal Graph | Relationships with valid_from/valid_to timestamps enabling historical queries |
| Context Layer (L0-L3) | L0: Identity (~50 tok), L1: Critical facts (~120 tok), L2: Cluster (on demand), L3: Deep search (on demand) |
| Tunnel | Cross-cluster relationship linking the same domain concept across different modules |
| Consistency Check | Detection of stale/broken links between graph elements and actual code state |
| Wake-up Protocol | Loading minimal L0+L1 context (~170 tokens) at session start for instant project awareness |

---

## 12. References

- CozoDB: https://github.com/cozodb/cozo
- tree-sitter: https://tree-sitter.github.io/tree-sitter/
- MCP Protocol: https://modelcontextprotocol.io/
- rmcp: https://crates.io/crates/rmcp
- Leiden Algorithm: https://en.wikipedia.org/wiki/Leiden_algorithm
- MemPalace: https://github.com/milla-jovovich/mempalace (competitive analysis source for US-MP stories)

---

*Last updated: 2026-04-16 (v3.1-massive-graph, service expand optimization + filter UI)*
