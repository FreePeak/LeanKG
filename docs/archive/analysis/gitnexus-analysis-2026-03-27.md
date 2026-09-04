# GitNexus Analysis: Ideas and Lessons for LeanKG

**Date:** 2026-03-27
**Author:** Engineering Analysis
**Source:** https://github.com/abhigyanpatwari/GitNexus
**Purpose:** Identify ideas from GitNexus that LeanKG should consider adopting or adapting

---

## 1. What GitNexus Is

GitNexus is a client-side code intelligence engine that indexes any codebase into a knowledge graph and exposes it through MCP tools so AI agents never miss code context. It positions itself as a complement to tools like DeepWiki: where DeepWiki helps you *understand* code, GitNexus lets you *analyze* it.

Two delivery modes:
- **Web UI** – client-side graph explorer and AI chat, runs entirely in WebAssembly in browser (no server)
- **CLI + MCP** – server-side indexer that gives AI agents (Cursor, Claude Code, Codex) deep architectural awareness

---

## 2. GitNexus Six-Phase Indexing Pipeline

GitNexus builds its knowledge graph through six explicit phases executed at index time:

| Phase | Name | What It Does |
|-------|------|-------------|
| 1 | Structure | Walk file tree, map folder/file relationships |
| 2 | Parsing | Extract functions, classes, methods, interfaces using Tree-sitter ASTs |
| 3 | Resolution | Resolve imports, function calls, heritage, constructor inference, `self`/`this` receiver types across files |
| 4 | Clustering | Group related symbols into functional communities (Leiden community detection algorithm) |
| 5 | Processes | Trace execution flows from entry points through call chains |
| 6 | Search | Build hybrid search indexes (graph + vector) for fast retrieval |

**Key difference from LeanKG:** GitNexus does *clustering* and *process tracing* at index time. LeanKG currently does phases 1-3 only (structure, parsing, resolution). Phases 4-6 are absent.

---

## 3. Precomputed Relational Intelligence — Core Innovation

GitNexus' most important architectural idea is **precomputing structure at index time rather than at query time**.

Traditional Graph RAG approach (what most tools do including LeanKG today):
```
User: "What depends on UserService?"
-> LLM receives raw graph
-> Query 1: Find callers
-> Query 2: What files?
-> Query 3: Filter tests?
-> Query 4: High-risk?
-> Answer after 4+ queries
```

GitNexus approach:
```
User: "What depends on UserService?"
-> impact(target: "UserService", direction: "upstream")
-> Pre-structured response: 8 callers, 3 clusters, all 90%+ confidence
-> Complete answer, 1 query
```

Three payoffs from precomputation:
1. **Reliability** – LLM cannot miss context because it is already in the tool response
2. **Token efficiency** – No multi-query chains to understand one function
3. **Model democratization** – Smaller LLMs work because tools do heavy lifting; large models not required

**Relevance to LeanKG:** LeanKG's current `get_impact_radius` returns raw edges. It does not precompute cluster membership or confidence scores. The response requires further LLM reasoning to interpret.

---

## 4. MCP Tool Design Analysis

GitNexus exposes **7 MCP tools** with clearly bounded responsibilities:

| Tool | Purpose | LeanKG Equivalent |
|------|---------|-------------------|
| `query` | Semantic/process-grouped search | `search_code` (no process grouping) |
| `context` | 360-degree symbol view (incoming + outgoing + processes) | `get_context` (no processes) |
| `impact` | Upstream/downstream blast radius with confidence scores | `get_impact_radius` (no confidence) |
| `detect_changes` | Pre-commit risk analysis against git diff | None |
| `rename` | Multi-file symbol rename with graph + text edit plan | None |
| `cypher` | Raw Cypher query passthrough for power users | None (Datalog raw query not exposed) |
| `list_repos` | Multi-repo registry management | None |

**Gap summary:**
- `detect_changes` – highest value gap; lets AI assess risk before committing
- `rename` – graph-aware refactor; generates both high-confidence graph edits and lower-confidence text-search edits
- `cypher` / raw query – power user escape hatch for complex traversals
- Confidence scoring on all relationship results

GitNexus also exposes **7 MCP Resources** (read-only URIs):
- `gitnexus://repos` – list all indexed repos
- `gitnexus://repo/{name}/context` – overview
- `gitnexus://repo/{name}/clusters` – cluster list
- `gitnexus://repo/{name}/cluster/{name}` – symbols in a cluster
- `gitnexus://repo/{name}/processes` – execution flow list
- `gitnexus://repo/{name}/process/{name}` – steps in a flow
- `gitnexus://repo/{name}/schema` – graph schema

LeanKG exposes zero MCP Resources today.

---

## 5. Multi-Repo Registry Architecture

GitNexus uses a **global registry** at `~/.gitnexus/registry.json` so one MCP server process serves all indexed repos. Each repo is analyzed independently and stores its index in `.gitnexus/` inside the repo directory. The MCP server reads the registry at startup and opens database connections lazily (max 5 concurrent, evicted after 5 min idle).

Benefits:
- One-time MCP config setup (`gitnexus setup`)
- AI agents specify `repo` parameter on tool calls
- Adding a new project doesn't require new MCP server instance

**LeanKG today:** Per-project `.leankg/` directory, no global registry, new MCP config required per project. This is a usability friction point.

---

## 6. Community Detection and Process Tracing

Two GitNexus capabilities that have no LeanKG equivalent:

### 6.1 Community Detection (Leiden Algorithm)
Groups symbols into functional clusters (e.g., "Authentication cluster", "Billing cluster"). Clusters are used to:
- Give `query` results process-grouped context (not just raw symbol matches)
- Power the `--skills` feature that generates per-cluster SKILL.md files
- Enrich LLM responses with architectural context

### 6.2 Execution Flow Tracing (Process Detection)
GitNexus traces execution paths from entry points through call chains and stores them as named "processes" (e.g., `LoginFlow`, `RegistrationFlow`). Each process has ordered steps referencing specific symbols. This means:
- `context` tool shows which flows a symbol participates in and at which step
- `impact` tool can show which flows would be disrupted by a change
- `query` tool groups results by process, not just by file

Both capabilities require precomputed graph structure that LeanKG does not currently build.

---

## 7. Auto-Generated Skills Feature

When run with `--skills`, GitNexus detects functional areas via community detection and generates a `SKILL.md` file for each cluster under `.claude/skills/generated/`. Each skill file describes:
- Module's key files and entry points
- Execution flows and cross-area connections
- Specific context for the AI agent about that area of code

Skills are regenerated on each `--skills` run. This is similar to LeanKG's `generate_doc` tool but scoped to functional communities rather than individual files.

**LeanKG equivalent:** `generate_doc` generates doc for a single file. No cluster-level skill generation exists.

---

## 8. Wiki Generation

GitNexus provides `gitnexus wiki [path]` that generates a full repository wiki from the knowledge graph using an LLM (default `gpt-4o-mini`). Wiki content is derived from the precomputed cluster and process structure, not from raw file reading.

**LeanKG equivalent:** No wiki generation. `generate_doc` produces per-file documentation only.

---

## 9. Confidence Scoring on Relationships

GitNexus assigns confidence scores to relationships between symbols:
- `CALLS 90%` – high confidence the function is called
- `IMPORTS 75%` – medium confidence

This scoring allows `impact` tool to filter by `minConfidence` and classify results as "WILL BREAK" vs "LIKELY AFFECTED". LeanKG stores relationships without confidence scores; all edges are treated as equally certain.

---

## 10. Browser-Based Zero-Install Mode

GitNexus runs entirely in the browser using:
- Tree-sitter WASM for parsing
- LadybugDB WASM for the graph database
- In-browser embeddings

This allows users to drop a ZIP file and immediately get an interactive knowledge graph with AI chat, with no installation. LeanKG has a web UI stub but it is not functional and requires the Rust binary.

---

## 11. Comparison Table: LeanKG vs GitNexus

| Feature | LeanKG | GitNexus |
|---------|--------|----------|
| Indexing language | Rust + tree-sitter | TypeScript + tree-sitter |
| Database | CozoDB (Datalog, embedded) | LadybugDB (custom, Cypher-like) |
| Database mode | **Embedded (no server process)** | Server-based |
| Supported languages | Go, TS/JS, Python, Rust | 14 languages |
| Community detection | No | Yes (Leiden algorithm) |
| Execution flow tracing | No | Yes (process detection) |
| Confidence scoring | No | Yes |
| Multi-repo registry | No | Yes (global registry) |
| Pre-commit change detection | No | Yes (`detect_changes`) |
| Symbol rename assistance | No | Yes (`rename`) |
| MCP Resources | No | Yes (7 resources) |
| Wiki generation | No | Yes (LLM-powered) |
| Cluster-level skills generation | No | Yes (`--skills`) |
| Browser-based UI | Embedded in LeanKG binary (Axum) | Full WebAssembly |
| Impact radius | Yes (no confidence) | Yes (with confidence + classification) |
| Doc-to-code traceability | Yes | No |
| Business logic tagging | Yes | No |
| Pipeline (CI/CD) indexing | In progress | No |
| Token-optimized context | Yes (`signature_only`) | No explicit mode |

---

## 12. Key Takeaways for LeanKG

Ranked by estimated value-to-effort ratio:

1. **Web UI integration** - **COMPLETED in v1.14**. Removed `tools/graph-viewer/`, embedded web UI in LeanKG binary via Axum. No external server dependency. Aligns with GitNexus "CLI + MCP + Web UI" combined delivery.

2. **LeanKG ALREADY HAS embedded architecture** – LeanKG uses `cozo::new_cozo_sqlite()` which is fully embedded (no separate CozoDB server process needed). This is an existing advantage over GitNexus's server-based approach.

3. **Confidence scoring on relationships** – High value, medium effort. Add confidence field to Relationship model and emit scores during call resolution. Enables better impact analysis output.

4. **`detect_changes` tool** – High value, medium effort. Diff current git state against indexed state, report affected symbols and risk level. AI agents can use this pre-commit.

5. **Multi-repo registry** – High value, medium effort. Global registry removes per-project MCP config friction. Important for teams working across multiple repos.

6. **Community detection** – High value, high effort. Requires implementing Leiden or equivalent clustering algorithm on the graph. Unlocks process grouping, skill generation, and architectural summaries.

7. **MCP Resources** – Medium value, low effort. Read-only URIs for repos, clusters, processes, schema. Reduces tool call overhead for overview information.

8. **Execution flow tracing** – Medium value, high effort. Requires entry-point detection and call-chain path enumeration. Needed for process-grouped search and full 360-degree context.

9. **Cluster-level skills generation** – Medium value, medium effort (depends on community detection). Auto-generate SKILL.md per functional area. Directly reduces per-query context token usage.

10. **Wiki generation** – Lower priority, medium effort. LLM-powered doc generation from graph structure. Useful but requires optional LLM API dependency.

### Current LeanKG Status: Web UI Changes COMPLETED (v1.14)

| Component | Before | After |
|-----------|--------|-------|
| `tools/graph-viewer/` | Python HTTP server + vis.js HTML | DELETED |
| `src/web/mod.rs` | Only `/` and `/health` routes | All pages + `/api/*` routes wired |
| `src/web/handlers.rs` | `#[allow(dead_code)]` - not wired | Now connected to router |
| Web UI | Required Python server | Served from LeanKG binary |
| CLI | `serve` command deprecated | `serve` and `web` commands work |

**Result:** LeanKG web UI is now fully embedded. No external server or Python dependency.

---

## 13. References

- GitNexus repository: https://github.com/abhigyanpatwari/GitNexus
- GitNexus web app: https://gitnexus.vercel.app
- LeanKG PRD: `docs/requirement/prd-leankg.md`
- LeanKG HLD: `docs/design/hld-leankg.md`
