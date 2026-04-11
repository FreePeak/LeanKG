# LeanKG Test Coverage Status

**Last Updated:** 2026-03-24
**Status:** In Progress - Adding Unit Test Coverage

## Overview

This document tracks the unit test coverage status for each module in the LeanKG codebase.

## Test Coverage Matrix

| Module | File(s) | Test Location | Coverage Status |
|--------|---------|---------------|-----------------|
| **Config** | `src/config/project.rs` | Inline tests | ✅ Covered |
| **Database Models** | `src/db/models.rs` | Inline tests | ✅ Covered |
| **Database Schema** | `src/db/schema.rs` | `tests/integration.rs` | ⚠️ Partial |
| **Graph Engine** | `src/graph/query.rs` | `tests/integration.rs` | ⚠️ Partial |
| **Graph Cache** | `src/graph/cache.rs` | None | ❌ Missing |
| **Graph Context** | `src/graph/context.rs` | Inline tests | ✅ Covered |
| **Graph Traversal** | `src/graph/traversal.rs` | `tests/integration.rs` | ⚠️ Partial |
| **Indexer** | `src/indexer/mod.rs` | `tests/integration.rs` | ⚠️ Partial |
| **Parser Manager** | `src/indexer/parser.rs` | `tests/lib.rs` | ⚠️ Partial |
| **Entity Extractor** | `src/indexer/extractor.rs` | Inline tests | ✅ Covered |
| **Git Analyzer** | `src/indexer/git.rs` | Inline tests | ✅ Covered |
| **CLI** | `src/cli/mod.rs` | None | ❌ Missing |
| **MCP Tools** | `src/mcp/tools.rs` | `tests/mcp_tests.rs` | ✅ Covered |
| **MCP Handler** | `src/mcp/handler.rs` | `tests/mcp_tests.rs` | ✅ Covered |
| **MCP Auth** | `src/mcp/auth.rs` | `tests/mcp_tests.rs` | ✅ Covered |
| **MCP Protocol** | `src/mcp/protocol.rs` | `tests/mcp_tests.rs` | ✅ Covered |
| **MCP Server** | `src/mcp/server.rs` | `tests/mcp_tests.rs` | ✅ Covered |
| **Web Handlers** | `src/web/handlers.rs` | None | ❌ Missing |
| **Web Module** | `src/web/mod.rs` | `tests/web_ui.rs` | ✅ Covered |
| **Watcher** | `src/watcher/mod.rs` | None | ❌ Missing |
| **Notify Handler** | `src/watcher/notify_handler.rs` | None | ❌ Missing |
| **Doc Generator** | `src/doc/generator.rs` | `tests/doc_generation.rs` | ✅ Covered |
| **Doc Templates** | `src/doc/templates.rs` | `tests/doc_generation.rs` | ✅ Covered |

## Missing Test Coverage

### High Priority

1. **Graph Cache** (`src/graph/cache.rs`)
   - `QueryCache` struct and methods
   - Cache invalidation logic
   - Cache hit/miss tracking

2. **CLI Commands** (`src/cli/mod.rs`)
   - `CLICommand` enum parsing
   - Command argument handling
   - Clap derive tests

3. **Database Schema** (`src/db/schema.rs`)
   - `init_db` function
   - Schema creation
   - Database migrations

### Medium Priority

4. **Web Handlers** (`src/web/handlers.rs`)
   - API route handlers
   - Request/response handling
   - Error handling

5. **Watcher/Notify Handler** (`src/watcher/notify_handler.rs`)
   - File change detection
   - Debouncing logic
   - Event handling

6. **Parser Manager** (`src/indexer/parser.rs`)
   - Additional parser methods
   - Language detection
   - Parser pooling

## Test Execution

```bash
cargo test                    # Run all tests
cargo test -- --nocapture     # Show println output
```

## Coverage Goals

- [x] Core graph operations (query, traversal, context)
- [x] Database operations (CRUD for business logic)
- [x] MCP server and tools
- [x] Documentation generation
- [x] Entity extraction (Go, Python, TypeScript)
- [x] Business logic annotations
- [ ] CLI command parsing
- [ ] Graph cache operations
- [ ] Web API handlers
- [ ] File watcher

## Changelog

- 2026-03-24: Created status document, started adding missing tests
