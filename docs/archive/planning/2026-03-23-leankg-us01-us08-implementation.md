# LeanKG Implementation Plan - US-01 to US-08

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

> **NOTE:** This plan was written before the SurrealDB-to-CozoDB migration (2026-03-25). The actual implementation uses CozoDB (embedded SQLite-backed relational-graph with Datalog queries) instead of SurrealDB.

**Goal:** Implement all 8 high-priority user stories for LeanKG MVP

**Architecture:** LeanKG is a Rust-based knowledge graph system using tree-sitter for parsing, CozoDB for storage, with CLI and MCP interfaces. Core modules: indexer, graph, db, doc, mcp, cli, web.

**Tech Stack:** Rust 1.70+, CozoDB (embedded SQLite-backed), tree-sitter, Axum, Clap, Tokio

---

## Context Summary

### Existing Codebase State:
- **lib.rs/main.rs**: Basic application shell with CLI commands defined
- **src/cli**: CLI commands (Init, Index, Query, Serve, Impact, Generate, etc.)
- **src/db**: CozoDB models and schema functions
- **src/indexer**: ParserManager, EntityExtractor, GitAnalyzer for file indexing
- **src/graph**: GraphEngine, ImpactAnalyzer, traversal, context modules
- **src/doc**: DocGenerator and templates
- **src/mcp**: MCP protocol implementation (server, tools, handler, auth)

### What's Implemented vs Needed:

| US | Description | Current State |
|----|-------------|---------------|
| US-01 | Auto index codebase | Partial: ParserManager exists, need testing |
| US-02 | Auto documentation | Partial: DocGenerator exists, needs full AGENTS.md/CLAUDE.md |
| US-03 | Business logic mapping | Partial: DB functions exist, CLI annotate works |
| US-04 | MCP server | Partial: server.rs exists, needs full tool implementations |
| US-05 | CLI interface | Mostly complete: All commands defined |
| US-06 | Minimal resources | Need verification/optimization |
| US-07 | Web UI | Stub only: web module exists but not functional |
| US-08 | Multi-language | Partial: Parsers for Go, TS, Python exist |

---

## Implementation Tasks

### Task 1: US-01 - Codebase Auto-Indexing
**Owner:** Agent-1

**Files to create/modify:**
- src/indexer/parser.rs: Ensure all parsers work correctly
- src/indexer/extractor.rs: Extract functions, classes, imports, exports
- src/indexer/git.rs: Git-based change detection
- tests/indexer_tests.rs: Add comprehensive tests

**Steps:**
1. Review existing parser.rs implementation
2. Add missing tree-sitter parser initialization
3. Verify entity extraction for Go, TypeScript, Python
4. Add unit tests for file indexing
5. Test incremental indexing flow
6. Ensure FR-04 (incremental), FR-05 (watch), FR-06 (TESTED_BY), FR-07 (dependent files)

---

### Task 2: US-02 - Auto Documentation Generation
**Owner:** Agent-2

**Files to create/modify:**
- src/doc/generator.rs: Implement full documentation generation
- src/doc/templates/agents.rs: AGENTS.md template
- src/doc/templates/claude.rs: CLAUDE.md template
- src/doc/templates/mod.rs: Template registry
- tests/doc_tests.rs: Documentation generation tests

**Steps:**
1. Review existing generator.rs
2. Implement AGENTS.md template generation (FR-10)
3. Implement CLAUDE.md template generation
4. Add template engine for custom docs (FR-11)
5. Implement doc sync on code changes (FR-09)
6. Add comprehensive tests

---

### Task 3: US-03 - Business Logic Mapping
**Owner:** Agent-3

**Files to create/modify:**
- src/db/models.rs: Ensure BusinessLogic model complete
- src/db/schema.rs: Ensure schema complete
- src/graph/context.rs: FR-16 query support
- tests/business_logic_tests.rs: Annotation tests

**Steps:**
1. Review existing BusinessLogic model
2. Ensure FR-13 (annotate code) is complete
3. Ensure FR-14 (map stories/features) works
4. Implement FR-15 (feature-to-code traceability)
5. Implement FR-16 (business logic queries)
6. Add tests for all business logic operations

---

### Task 4: US-04 - MCP Server
**Owner:** Agent-4

**Files to create/modify:**
- src/mcp/server.rs: MCP server implementation
- src/mcp/tools.rs: MCP tool definitions
- src/mcp/handler.rs: Request handler
- src/mcp/protocol.rs: MCP protocol types
- tests/mcp_tests.rs: MCP integration tests

**Steps:**
1. Review existing MCP server implementation
2. Implement all MCP tools from HLD 5.2:
   - query_file, get_dependencies, get_dependents
   - get_impact_radius, get_review_context
   - find_function, get_call_graph
   - search_code, get_context
   - generate_doc, find_large_functions, get_tested_by
3. Implement MCP authentication (FR-26)
4. Ensure FR-25 (context retrieval) works
5. Add MCP protocol tests

---

### Task 5: US-05 - CLI Interface
**Owner:** Agent-5

**Files to create/modify:**
- src/cli/mod.rs: Ensure all commands complete
- src/main.rs: Ensure all commands wired correctly
- tests/cli_tests.rs: CLI integration tests

**Steps:**
1. Review existing CLI commands (FR-28 to FR-36)
2. Ensure init, index, query, generate, install, impact all work
3. Implement FR-29 (index with options) fully
4. Implement FR-30 (CLI query) fully
5. Implement FR-33 (start/stop MCP) - currently stub
6. Implement FR-36 (find oversized functions) - currently stub
7. Add CLI tests

---

### Task 6: US-06 - Minimal Resource Usage
**Owner:** Agent-6

**Files to create/modify:**
- src/indexer/parser.rs: Parser pooling
- src/graph/mod.rs: Query caching
- src/db/mod.rs: Connection pooling
- benchmarks/performance_tests.rs: Performance benchmarks

**Steps:**
1. Review current resource usage patterns
2. Implement parser pooling in ParserManager
3. Add query result caching
4. Optimize CozoDB connection usage
5. Add memory benchmarks
6. Verify NFR targets: cold start <2s, indexing >10K LOC/s, query <100ms

---

### Task 7: US-07 - Web UI
**Owner:** Agent-7

**Files to create/modify:**
- src/web/mod.rs: Web server implementation
- src/web/routes.rs: HTTP route handlers
- src/web/graph_viz.rs: Graph visualization
- templates/*.html: Web UI templates
- tests/web_tests.rs: Web UI tests

**Steps:**
1. Review existing web module
2. Implement /graph route for visualization (FR-37)
3. Implement /browse route for code browser (FR-38)
4. Implement /annotate route (FR-39)
5. Implement /docs route (FR-40)
6. Implement HTML graph export (FR-41)
7. Add Web UI tests

---

### Task 8: US-08 - Multi-Language Support
**Owner:** Agent-8

**Files to create/modify:**
- src/indexer/parser.rs: Language parser registration
- src/indexer/extractor.rs: Language-specific extraction
- src/indexer/languages/go.rs: Go parser
- src/indexer/languages/typescript.rs: TypeScript parser
- src/indexer/languages/python.rs: Python parser
- tests/language_tests.rs: Language parsing tests

**Steps:**
1. Review existing parser implementations
2. Ensure Go parser extracts: functions, structs, interfaces, imports
3. Ensure TypeScript parser extracts: functions, classes, interfaces, imports, exports
4. Ensure Python parser extracts: functions, classes, imports, decorators
5. Add TESTED_BY relationship detection for all languages
6. Add comprehensive language parsing tests

---

## Verification Steps

After all agents complete:

1. **Build verification:**
   ```bash
   cargo build --release
   ```

2. **Test verification:**
   ```bash
   cargo test
   ```

3. **Lint verification:**
   ```bash
   cargo fmt -- --check
   cargo clippy
   ```

4. **Feature verification:**
   - US-01: Run `cargo run -- index ./src`
   - US-02: Run `cargo run -- generate`
   - US-04: Run `cargo run -- serve` and test MCP tools
   - US-07: Run `cargo run -- serve` and check web UI

---

## Notes

- Each agent should commit after completing their US
- Agents work independently - no shared state
- If conflicts arise, resolve by later commit wins
- Update this plan status after each US completion