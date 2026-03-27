# PRD: LeanKG GitNexus-Inspired Enhancements

**Version:** 1.0
**Date:** 2026-03-27
**Status:** Draft
**Author:** Product Owner
**Parent PRD:** `docs/requirement/prd-leankg.md` (v1.5)
**Reference Analysis:** `docs/analysis/gitnexus-analysis-2026-03-27.md`
**Target Milestone:** v0.3.0

---

## 1. Executive Summary

This PRD defines a set of enhancements to LeanKG inspired by the architectural patterns and features of GitNexus. The goal is to make LeanKG's MCP tools produce richer, more actionable responses so AI agents require fewer tool calls and produce better-quality edits.

The core principle adopted from GitNexus is **precomputed relational intelligence**: structure computed at index time, not at query time. This converts LeanKG from a raw-edge graph query engine into a high-confidence context engine.

---

## 2. Problem Statement

### 2.1 Current Pain Points

| Pain Point | Evidence |
|------------|---------|
| Impact radius lacks confidence grades | `get_impact_radius` returns all edges at equal weight; LLM cannot distinguish "WILL BREAK" from "MIGHT BE AFFECTED" |
| No pre-commit risk signal | No tool exists to assess change risk before commit; developers discover breakage after CI |
| Single-project friction | Each project requires its own MCP config; teams with multiple repos repeat setup |
| Flat search results | `search_code` returns symbol matches with no grouping by functional area or execution flow |
| No cluster-level context | No concept of "functional area" exists; context must be assembled symbol by symbol |
| Context requires multi-tool chains | Getting a 360-degree view of a function requires 3-4 separate tool calls |

### 2.2 Why These Matter

AI agents using LeanKG must issue multiple tool calls to synthesize context that GitNexus returns in a single call. This increases token cost, latency, and the probability of the agent missing relevant context due to depth limits or hallucination during synthesis.

---

## 3. User Stories

| ID | User Story | Priority | Phase |
|----|------------|----------|-------|
| US-GN-01 | As an AI agent, I want impact analysis results to include confidence scores and severity classifications so I can distinguish critical from peripheral dependencies | Must Have | v0.3.0 |
| US-GN-02 | As a developer, I want a `detect_changes` tool that shows which symbols changed and which processes they affect so I can assess risk before committing | Must Have | v0.3.0 |
| US-GN-03 | As a developer working across multiple repos, I want a global LeanKG registry so I configure MCP once and it serves all my projects | Should Have | v0.3.0 |
| US-GN-04 | As an AI agent, I want search results grouped by functional community so I understand which architectural area a symbol belongs to | Should Have | v0.3.0 |
| US-GN-05 | As a developer, I want LeanKG to auto-detect functional clusters in my codebase so I understand its architecture at a high level | Should Have | v0.3.0 |
| US-GN-06 | As an AI agent, I want a 360-degree context view of a symbol that includes its cluster membership and execution flows in a single tool call | Should Have | v0.3.0 |
| US-GN-07 | As a developer, I want LeanKG to generate cluster-level SKILL.md files so my AI agent gets targeted context for the code area it is editing | Could Have | v0.4.0 |
| US-GN-08 | As an AI agent, I want to read overview resources (cluster list, process list, schema) without issuing tool calls so startup context is free | Could Have | v0.4.0 |
| US-GN-09 | As a developer, I want LeanKG to generate a full repository wiki from the knowledge graph so I can share architectural documentation | Won't Have (v0.3.0) | v0.5.0 |

---

## 4. Functional Requirements

### 4.1 Confidence Scoring on Relationships (US-GN-01)

**FR-GN-01:** Add `confidence` field (float 0.0-1.0) to the `Relationship` model in `src/db/models.rs`.

**FR-GN-02:** During call resolution in `src/indexer/extractor.rs`, emit confidence scores based on resolution quality:
- Direct same-file call resolved to exact qualified name: `1.0`
- Cross-file call resolved via import graph: `0.9`
- Cross-file call resolved by name match only (no import edge): `0.7`
- Unresolved call (kept as `__unresolved__`): `0.3`

**FR-GN-03:** Update `get_impact_radius` MCP tool response to include per-dependency confidence and severity classification:
- Depth 1 + confidence >= 0.85: `WILL BREAK`
- Depth 1 + confidence 0.60-0.84: `LIKELY AFFECTED`
- Depth 2+ or confidence < 0.60: `MAY BE AFFECTED`

**FR-GN-04:** Add optional `min_confidence` parameter to `get_impact_radius` tool (default: `0.0`, no filter). Enable agents to request only high-confidence results.

**Acceptance Criteria:**
- `get_impact_radius` response includes `confidence` and `severity` fields per result
- Filtering by `min_confidence: 0.8` excludes results with confidence below 0.8
- Confidence scores match the resolution quality rules in FR-GN-02

---

### 4.2 Pre-Commit Change Detection Tool (US-GN-02)

**FR-GN-05:** Add new MCP tool `detect_changes` that computes the diff between current working tree and the last indexed commit.

Tool parameters:
- `scope`: `"staged"` (git staged files), `"unstaged"`, `"all"` (default: `"all"`)
- `min_confidence`: filter affected symbols below threshold (default: `0.0`)

Tool response structure:
```
{
  "summary": {
    "changed_files": 3,
    "changed_symbols": 12,
    "affected_symbols": 8,
    "risk_level": "medium"  // "low" | "medium" | "high" | "critical"
  },
  "changed_symbols": [...],
  "affected_symbols": [...],
  "risk_reasons": [...]
}
```

**FR-GN-06:** Risk level classification rules:
- `critical`: any changed symbol has >= 10 dependents at depth 1
- `high`: any changed symbol has >= 5 dependents at depth 1, or public API changed
- `medium`: 2-4 dependents or cross-module dependency affected
- `low`: <= 1 dependent, contained within a single cluster

**FR-GN-07:** `detect_changes` must run without network access; it uses only the local git index and the LeanKG database.

**Acceptance Criteria:**
- Tool returns non-empty response when files differ from last indexed commit
- Risk level "critical" is assigned correctly when high-fan-out symbol is changed
- Tool completes in under 2 seconds for repos <= 100K LOC

---

### 4.3 Multi-Repo Global Registry (US-GN-03)

**FR-GN-08:** Create a global registry file at `~/.leankg/registry.json` mapping repo names to absolute paths.

Registry schema:
```json
{
  "version": 1,
  "repos": {
    "my-service": "/Users/alice/work/my-service",
    "shared-lib": "/Users/alice/work/shared-lib"
  }
}
```

**FR-GN-09:** Add CLI commands for registry management:
- `leankg register [name]` – register current directory in global registry
- `leankg unregister [name]` – remove from registry
- `leankg list` – list all registered repos with last-indexed timestamps
- `leankg status [name]` – show index staleness for a registered repo

**FR-GN-10:** MCP server reads global registry on startup and can serve any registered repo. When only one repo is registered, all tool calls default to that repo. When multiple repos are registered, a `repo` parameter is accepted on all tools.

**FR-GN-11:** Database connections to registered repos are opened lazily on first tool call and closed after 10 minutes of inactivity. Maximum 5 concurrent connections.

**FR-GN-12:** `leankg setup` configures global MCP config once (writes to `~/.cursor/mcp.json`, `~/.claude/mcp.json`, `~/.opencode/mcp.json` as applicable) so no per-project MCP config is needed.

**Acceptance Criteria:**
- `leankg register` adds repo to `~/.leankg/registry.json`
- MCP server with 2 registered repos correctly routes tool calls to the right repo based on `repo` parameter
- `leankg list` shows all registered repos with stale/fresh status

---

### 4.4 Community Detection and Cluster-Grouped Search (US-GN-04, US-GN-05)

**FR-GN-13:** Implement community detection on the code element graph at index time. Use a simple label propagation or greedy modularity algorithm (Leiden algorithm is ideal but may be replaced by a simpler Rust library).

**FR-GN-14:** Store cluster membership in the `CodeElement` model as `cluster_id` and `cluster_label` (human-readable name derived from dominant file path prefix or function naming patterns).

**FR-GN-15:** Expose clusters via new MCP tool `get_clusters`:
- Returns list of clusters with ID, label, symbol count, representative files
- No parameters required

**FR-GN-16:** Update `search_code` response to include `cluster_id` and `cluster_label` per result so agents can see which functional area each symbol belongs to.

**FR-GN-17:** Add MCP tool `get_cluster_context`:
- Parameter: `cluster_id` or `cluster_label`
- Returns all symbols in the cluster, entry points, and inter-cluster dependencies

**Acceptance Criteria:**
- After indexing, each `CodeElement` has a non-null `cluster_id`
- `get_clusters` returns >= 2 clusters for repos with >= 10 files
- `search_code` response includes `cluster_label` field per result
- `get_cluster_context` response lists all symbols in the given cluster

---

### 4.5 Enhanced 360-Degree Context Tool (US-GN-06)

**FR-GN-18:** Enhance `get_context` MCP tool to return cluster membership and (if process tracing is implemented) execution flow participation in a single response.

Updated response fields:
```
{
  "element": { ... existing fields ... },
  "cluster": { "id": "...", "label": "Authentication" },
  "incoming": { "calls": [...], "imports": [...] },
  "outgoing": { "calls": [...], "imports": [...] },
  "dependents_count": 12,
  "dependencies_count": 4
}
```

**FR-GN-19:** Add `dependents_count` and `dependencies_count` summary fields so the agent does not need to call `get_impact_radius` just to know the fan-in/fan-out scope.

**Acceptance Criteria:**
- `get_context` response includes `cluster` field with non-null label
- `dependents_count` matches count returned by `get_impact_radius` at depth 1
- Response is returned in a single tool call without requiring follow-up calls

---

### 4.6 MCP Resources for Overview Context (US-GN-08)

**FR-GN-20:** Expose read-only MCP Resources (not tools) for high-level overview data:
- `leankg://repos` – list of registered repos with status
- `leankg://repo/{name}/clusters` – cluster list for a repo
- `leankg://repo/{name}/schema` – graph schema (node types, relationship types)

**FR-GN-21:** Resources are served as JSON. Agents read them to understand overall graph structure without issuing tool calls.

**Acceptance Criteria:**
- MCP client can read `leankg://repos` without calling a tool
- `leankg://repo/{name}/clusters` returns cluster list consistent with `get_clusters` tool

---

## 5. Non-Functional Requirements

| Metric | Target |
|--------|--------|
| `detect_changes` response time | < 2 seconds for repos <= 100K LOC |
| `get_context` enhanced response size | < 4000 tokens with `signature_only: true` |
| Community detection time at index | < 10% overhead on total index time |
| Registry file size | < 1MB for 100 registered repos |
| Database connections per MCP server | Max 5 concurrent; idle-evicted after 10 min |

---

## 6. Data Model Changes

### 6.1 Relationship Model

```
Relationship (updated):
  - id: integer (PK)
  - source_qualified: string (FK)
  - target_qualified: string (FK)
  - type: imports | calls | contains | exports | tested_by | ...
  - confidence: float (NEW, default 1.0)
  - metadata: JSON
```

### 6.2 CodeElement Model

```
CodeElement (updated):
  - qualified_name: string (PK)
  - type: file | function | class | ...
  - name: string
  - file_path: string
  - line_start: int
  - line_end: int
  - language: string
  - parent_qualified: string
  - cluster_id: string (NEW, nullable until community detection runs)
  - cluster_label: string (NEW, nullable)
  - metadata: JSON
```

### 6.3 Global Registry File

New file: `~/.leankg/registry.json`
```json
{
  "version": 1,
  "repos": {
    "<repo-name>": {
      "path": "/absolute/path",
      "last_indexed": "2026-03-27T10:00:00Z",
      "element_count": 1234
    }
  }
}
```

---

## 7. Implementation Phases

### Phase v0.3.0 (Must Have + Should Have)

Priority order within this phase:

1. **Confidence scoring** (FR-GN-01 to FR-GN-04) — Low risk, high value, no schema migrations needed beyond adding one field
2. **`detect_changes` tool** (FR-GN-05 to FR-GN-07) — New tool, depends on git integration already present for incremental indexing
3. **Multi-repo registry** (FR-GN-08 to FR-GN-12) — Usability improvement, independent of graph changes
4. **Enhanced `get_context`** (FR-GN-18 to FR-GN-19) — Low risk enhancement to existing tool
5. **Community detection + cluster search** (FR-GN-13 to FR-GN-17) — Largest implementation effort; do last in this phase

### Phase v0.4.0 (Could Have)

1. **Cluster-level skills generation** (US-GN-07) — Depends on community detection being stable
2. **MCP Resources** (FR-GN-20 to FR-GN-21) — Depends on multi-repo registry

### Phase v0.5.0 (Future)

1. **Wiki generation** (US-GN-09) — Optional LLM dependency; lowest priority

---

## 8. Out of Scope

The following GitNexus features are explicitly out of scope for LeanKG:

| Feature | Reason |
|---------|--------|
| Browser-based WebAssembly UI | LeanKG targets CLI + MCP use case; browser mode requires significant WebAssembly porting effort |
| Symbol rename tool | High complexity; requires text-level file editing from within the graph engine; better handled by the AI agent's editor |
| Raw Cypher/Datalog query passthrough | LeanKG uses CozoDB Datalog; exposing raw Datalog to untrusted MCP clients is a security risk without sandboxing |
| Vector embeddings and semantic search | Already in Phase 3 roadmap; tracked in parent PRD |
| 14 language support | LeanKG focuses on Go, TS/JS, Python, Rust; expanding language coverage is tracked separately |

---

## 9. Success Metrics

| Metric | Target |
|--------|--------|
| `get_impact_radius` includes confidence scores | 100% of results |
| `detect_changes` coverage of modified symbols | > 90% (validated against manual diff) |
| AI agent tool calls per context-gathering session | Reduce from avg 5 calls to avg 2 calls |
| Multi-repo MCP config setup time | < 1 minute one-time setup |
| Community detection clusters F-measure vs manual grouping | > 0.70 |

---

## 10. Acceptance Criteria Summary

| User Story | Acceptance Criteria |
|------------|---------------------|
| US-GN-01 | `get_impact_radius` returns `confidence` and `severity` fields per result; `min_confidence` filter works |
| US-GN-02 | `detect_changes` returns changed + affected symbols and correct risk level in < 2 seconds |
| US-GN-03 | `leankg register`/`unregister`/`list` work; MCP serves 2+ repos via `repo` parameter |
| US-GN-04 | `search_code` results include `cluster_label` field |
| US-GN-05 | `get_clusters` returns >= 2 distinct clusters after indexing a real project |
| US-GN-06 | `get_context` response includes `cluster`, `dependents_count`, `dependencies_count` in one call |
| US-GN-07 | `leankg index --skills` generates SKILL.md per cluster under `.leankg/skills/` |
| US-GN-08 | `leankg://repos` and `leankg://repo/{name}/clusters` resources readable by MCP client |

---

## 11. References

- GitNexus analysis: `docs/analysis/gitnexus-analysis-2026-03-27.md`
- LeanKG core PRD: `docs/requirement/prd-leankg.md`
- LeanKG HLD: `docs/design/hld-leankg.md`
- GitNexus repository: https://github.com/abhigyanpatwari/GitNexus
- Leiden community detection algorithm: https://en.wikipedia.org/wiki/Leiden_algorithm
- MCP Resources specification: https://modelcontextprotocol.io/docs/concepts/resources
