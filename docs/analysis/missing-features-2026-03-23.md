# LeanKG MVP - Missing Features Analysis

**Date:** 2026-03-23  
**Status:** Superseded - See [implementation-status-2026-03-23.md](./implementation-status-2026-03-23.md)  
**Based on:** PRD v1.2 vs Implementation

> **NOTE:** This document is superseded. See [implementation-status-2026-03-23.md](./implementation-status-2026-03-23.md) for current status.

---

## Summary

**41 Functional Requirements** defined in PRD  
**~18 Fully/Partially Implemented** (45%)  
**23 Not Implemented or Stub Only** (55%)

---

## 1. Missing Features by Category

### 1.1 Business Logic Mapping (FR-12 to FR-16)

| FR | Requirement | Current State | Priority |
|----|-------------|---------------|----------|
| FR-12 | Include business logic descriptions in generated docs | Not implemented | High |
| FR-13 | Annotate code with business logic descriptions | Not implemented | High |
| FR-14 | Map user stories/features to code | Not implemented | High |
| FR-15 | Generate feature-to-code traceability | Not implemented | Medium |
| FR-16 | Business logic queries ("which code handles auth?") | Not implemented | High |

**Impact:** Cannot link business requirements to code elements. Core value proposition incomplete.

**Implementation Required:**
- `BusinessLogic` table with CRUD operations
- CLI command: `leankg annotate <element> --description "..."`
- CLI command: `leankg link <story-id> <element>`
- Query support in graph engine for business logic searches
- Integration with doc generator to include annotations

---

### 1.2 Incremental Indexing (FR-04, FR-06, FR-07)

| FR | Requirement | Current State | Priority |
|----|-------------|---------------|----------|
| FR-04 | Incremental via git-based change detection | Not implemented | High |
| FR-06 | Extract TESTED_BY relationships | Not implemented | High |
| FR-07 | Track dependents - re-index files depending on changed file | Not implemented | High |

**Impact:** Full re-index required on every change. Performance unacceptable for large codebases.

**Implementation Required:**
- Git integration to detect changed files since last index
- `git diff --name-only HEAD` to find modified files
- TESTED_BY edge detection: if `file_test.go` imports/calls `file.go`, create tested_by edge
- Reverse dependency tracking: when file changes, find all files that depend on it
- Update only delta instead of full re-index

---

### 1.3 MCP Server (FR-23, FR-25, FR-26)

| FR | Requirement | Current State | Priority |
|----|-------------|---------------|----------|
| FR-23 | Expose knowledge graph via MCP protocol | Partially implemented (tools defined) | High |
| FR-25 | Context retrieval for AI operations | Not implemented | High |
| FR-26 | Authenticate MCP connections | Not implemented | Medium |

**Impact:** AI tools cannot query knowledge graph. MCP integration incomplete.

**Implementation Required:**
- Full MCP protocol handler (JSON-RPC 2.0)
- WebSocket support for MCP (stdio or HTTP)
- Tool execution handlers for all 11 defined tools
- Token-based authentication for MCP endpoints
- Request/response serialization per MCP spec

---

### 1.4 Web UI (FR-37 to FR-41)

| FR | Requirement | Current State | Priority |
|----|-------------|---------------|----------|
| FR-37 | Visualize code dependency graph | Placeholder only | Medium |
| FR-38 | Browse and search code elements | Placeholder only | Medium |
| FR-39 | View and edit business logic annotations | Not implemented | Medium |
| FR-40 | Simple documentation viewer | Placeholder only | Low |
| FR-41 | Export interactive graph as HTML | Stub only | Low |

**Impact:** No visual interface for exploring the knowledge graph.

**Implementation Required:**
- Graph visualization (D3.js or vis.js integration)
- File tree browser
- Search interface with filters
- Annotation editor UI
- HTML export with embedded graph data

---

### 1.5 Documentation Generation (FR-09, FR-11)

| FR | Requirement | Current State | Priority |
|----|-------------|---------------|----------|
| FR-09 | Maintain doc freshness - update on code changes | Not implemented | High |
| FR-11 | Support custom documentation templates | Not implemented | Medium |

**Impact:** Docs become stale immediately after generation.

**Implementation Required:**
- Template engine with custom template loading
- Watch integration to regenerate docs on file changes
- Template variables: `{{qualified_name}}`, `{{element_type}}`, `{{relationships}}`
- User-defined templates stored in `.leankg/templates/`

---

### 1.6 Token Optimization (FR-18)

| FR | Requirement | Current State | Priority |
|----|-------------|---------------|----------|
| FR-18 | Calculate and minimize token usage for context queries | Not implemented | Medium |

**Impact:** Cannot optimize context for AI consumption.

**Implementation Required:**
- Token counter (estimate based on text length)
- Context pruning: limit to most relevant N elements
- Priority ranking: recently changed > imported > contained

---

## 2. Stubs Needing Full Implementation

| Command/Tool | Current | Needed |
|--------------|---------|--------|
| `leankg query` | Stub prints message | Full graph query parsing and execution |
| `leankg watch` | Stub prints message | Integration with FileWatcher module |
| `leankg quality` | Stub prints message | Large function detection (line count > threshold) |
| `leankg export` | Stub prints message | HTML generation with graph data |
| `get_context` MCP tool | Defined | Return minimal context for AI |
| `search_code` MCP tool | Defined | Full-text search implementation |

---

## 3. Implementation Priority

### P0 - Must Have (MVP Release Blocker)

1. **MCP Server Full Implementation** - AI tools need this
2. **Incremental Indexing** - Performance requirement
3. **TESTED_BY Relationships** - Core graph edge type

### P1 - Should Have (MVP Quality)

4. **Business Logic Annotations** - Core value proposition
5. **Doc Freshness on Change** - Docs must stay current
6. **`get_context` Tool** - Token optimization for AI

### P2 - Nice to Have (Post-MVP)

7. Web UI graph visualization
8. Custom doc templates
9. HTML export
10. Full-text search

---

## 4. Code State

### Current Implementation Path

```
src/
  cli/       # Commands: init, index, serve, impact, status - WORKING
  config/    # Config loading - WORKING
  db/        # SurrealDB schema + models - WORKING
  doc/       # Basic markdown generation - PARTIAL
  graph/     # Query engine + BFS - WORKING
  indexer/   # tree-sitter parsing - PARTIAL (no TESTED_BY)
  mcp/       # Protocol types + tools - DEFINED ONLY
  watcher/   # notify integration - CREATED (not wired)
  web/       # Axum handlers - PLACEHOLDER
```

### Database Schema Status

```sql
-- CODE_ELEMENTS: Implemented ✓
-- RELATIONSHIPS: Implemented ✓ (imports edge only)
-- BUSINESS_LOGIC: Schema only, no CRUD
-- DOCUMENTS: Not implemented
-- USER_STORIES: Not implemented
-- FEATURES: Not implemented
```

---

## 5. Recommended Next Steps

### Option A: Complete MCP Server First
1. Implement MCP protocol handler (src/mcp/)
2. Wire up tool execution to graph engine
3. Add authentication
4. Test with Claude Code/Cursor

### Option B: Complete Incremental Indexing First
1. Add git integration to indexer
2. Implement TESTED_BY edge detection
3. Add dependent file tracking
4. Optimize re-index to only changed files

### Option C: Complete Business Logic Mapping First
1. Add annotation CRUD to graph engine
2. Add `leankg annotate` CLI command
3. Add business logic to doc generator
4. Add query support

---

## Changelog (2026-03-23)

Since this analysis, 6 high-priority items were implemented:

1. **MCP Server Full Implementation** - WebSocket, JSON-RPC 2.0, 11 tools wired, token auth
2. **Incremental Indexing** - Git-based change detection, dependent tracking
3. **TESTED_BY Relationships** - Test file detection + edge creation
4. **Business Logic Annotations** - CRUD, CLI commands (`annotate`, `link`, `search-annotations`)
5. **Doc Freshness on Change** - Watcher wired to doc regeneration, custom templates
6. **get_context Tool** - Token optimization (4 chars/token), priority ranking (RecentlyChanged > Imported > Contained)

See [implementation-status-2026-03-23.md](./implementation-status-2026-03-23.md) for full details.

---

## Appendix: Feature Checklist

### Code Indexing
- [x] FR-01: Parse files (partial)
- [x] FR-02: Build dependency graph (basic)
- [ ] FR-03: Multi-language (no Rust parser)
- [ ] FR-04: Incremental indexing
- [ ] FR-05: Auto-update on file change
- [ ] FR-06: TESTED_BY relationships
- [ ] FR-07: Track dependents

### Documentation
- [x] FR-08: Generate markdown
- [ ] FR-09: Freshness on change
- [ ] FR-10: AGENTS.md/CLAUDE.md (AGENTS only)
- [ ] FR-11: Custom templates
- [ ] FR-12: Business logic in docs

### Business Logic
- [ ] FR-13: Annotate code
- [ ] FR-14: Map user stories
- [ ] FR-15: Feature traceability
- [ ] FR-16: Business logic queries

### Context Provisioning
- [ ] FR-17: Targeted context
- [ ] FR-18: Token minimization
- [ ] FR-19: Context templates
- [ ] FR-20: Query by relevance
- [ ] FR-21: Review context
- [x] FR-22: Impact radius

### MCP Server
- [x] FR-23: Expose via MCP (partial)
- [x] FR-24: Query tools (defined)
- [ ] FR-25: Context retrieval
- [ ] FR-26: Authenticate
- [x] FR-27: Auto-generate config

### CLI
- [x] FR-28: Init
- [x] FR-29: Index
- [ ] FR-30: Query
- [x] FR-31: Generate docs (partial)
- [ ] FR-32: Annotations
- [ ] FR-33: Start/Stop MCP
- [x] FR-34: Impact radius
- [x] FR-35: Install MCP config
- [ ] FR-36: Quality metrics

### Web UI
- [ ] FR-37: Graph visualization
- [ ] FR-38: Browse/search
- [ ] FR-39: View/edit annotations
- [ ] FR-40: Doc viewer
- [ ] FR-41: HTML export
