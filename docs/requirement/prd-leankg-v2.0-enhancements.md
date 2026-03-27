# LeanKG PRD - Enhancement v2.0

**Version:** 2.0
**Date:** 2026-03-27
**Status:** Draft
**Author:** Product Owner
**Predecessor:** `prd-leankg.md` v1.5 (US-01 to US-18)
**Derives From:** `docs/analysis/leankg-architectural-review-2026-03-27.md`
**Target Users:** Software developers using AI coding tools (Cursor, OpenCode, Claude Code, etc.)

**Changelog:**
- v2.0 — Enhancement release derived from architectural review 2026-03-27:
  - US-19: Fix cross-file call edge resolution
  - US-20: Fix Go `implements` edge extraction
  - US-21: Push-down Datalog queries + injection safety
  - US-22: Add token-efficient `signature_only` context mode
  - US-23: Bounded depth call graph traversal
  - US-24: Fix `get_documented_by` query direction bug
  - US-25: Add `mcp_index_docs` MCP tool
  - US-26: Fix doc-code reference extraction
  - US-27: MCP tool definition quality improvements

---

## 1. Executive Summary

LeanKG v1.x is functionally operational: it indexes codebases, builds a knowledge graph, and exposes MCP tools that AI agents can query. However, a thorough architectural review in March 2026 identified a set of correctness bugs, performance bottlenecks, and missing capabilities that limit its value as a precision context engine for LLMs.

This PRD defines the v2.0 enhancement release. The goal is to fix silent correctness failures (wrong graph edges, self-referential query bugs), eliminate performance anti-patterns (full table scans on every tool call), and deliver two high-value new capabilities (token-efficient `signature_only` context and real code-documentation cross-edges).

The changes require no breaking schema migrations. All enhancements are backward-compatible with existing `.leankg` databases.

---

## 2. Problem Statement

### 2.1 Correctness Failures

| # | Problem | Evidence | Severity |
|---|---------|----------|----------|
| C1 | Call edges are file-local only | `extractor.rs:431` — `format!("{}::{}", file_path, name)` never resolves cross-file callees | Critical |
| C2 | `implements` fires on every struct field | `extractor.rs:267-301` — any non-`"struct"` type field creates an `implements` edge | High |
| C3 | `get_documented_by` returns self-referential results | `query.rs:382` — filters `target_qualified = element` but the edge stores code as `source`, doc as `target` | Critical |
| C4 | `get_dependencies` returns file's own symbols | `query.rs:98-142` — queries `code_elements` by `file_path`, not `relationships` | High |

### 2.2 Performance Anti-Patterns

| # | Problem | Evidence | Severity |
|---|---------|----------|----------|
| P1 | Full table scan on every search | `handler.rs:508`, `:332`, `:558`, `:694` — all call `all_elements()` and filter in Rust | High |
| P2 | No Datalog injection protection | `query.rs` throughout — user strings substituted with `format!()` directly | High |
| P3 | `get_call_graph` has no depth limit | `handler.rs:533-549` — no cap; future multi-hop expansion will explode | Medium |

### 2.3 Missing Capabilities

| # | Capability | Gap |
|---|-----------|-----|
| M1 | Token-efficient context mode | `get_context` always returns full line ranges; no `signature_only` shorthand |
| M2 | Doc indexing via MCP | `mcp_index` excludes `.md` files; `index_docs_directory` is never called from any MCP tool |
| M3 | Function signatures stored at index time | Signatures are not captured; LLMs cannot get a concise function header without reading the file |
| M4 | Heading metadata in doc elements | `extract_headings` exists but its result is never stored in `CodeElement.metadata` |

---

## 3. Goals

1. Eliminate all four correctness failures — the graph must reflect real cross-module relationships.
2. Remove full-table-scan patterns — all MCP tool queries must push predicates into Datalog.
3. Add injection safety — no user-controlled string may be substituted without escaping.
4. Deliver `signature_only` context mode — default `get_context` output must fit in a small token budget.
5. Deliver working code-documentation cross-edges — `get_doc_for_file` and `get_files_for_doc` must return real results.

---

## 4. User Stories

| ID | User Story | Priority |
|----|------------|----------|
| US-19 | As an AI agent, I want `get_call_graph` to trace calls across files so that I understand inter-module dependencies | Must Have |
| US-20 | As an AI agent, I want `implements` edges to reflect real interface embeddings, not every struct field | Must Have |
| US-21 | As an AI agent, I want MCP search tools to respond in under 50ms so that I can do rapid iterative lookups without waiting | Must Have |
| US-22 | As an AI agent, I want `get_context` to default to signature-only output so that I spend tokens on the right things | Must Have |
| US-23 | As an AI agent, I want `get_call_graph` to cap at a configurable depth so that I am never flooded with hundreds of irrelevant edges | Should Have |
| US-24 | As an AI agent, I want `get_doc_for_file` to return real documentation files, not the source file itself | Must Have |
| US-25 | As an AI agent, I want to call `mcp_index_docs` to index markdown documentation so that code-doc traceability edges exist | Must Have |
| US-26 | As a developer, I want LeanKG to detect references to source files in documentation narrative text, not only in paths starting with `src/` | Should Have |
| US-27 | As an AI agent, I want MCP tool schemas to include `required` arrays and accurate descriptions so I do not omit required parameters | Should Have |

---

## 5. Functional Requirements

### 5.1 AST & Edge Extraction Fixes (US-19, US-20)

**FR-77:** During indexing, when a function call is detected, store the callee as `__unresolved__{bare_name}` with `bare_name` and `callee_file_hint` in relationship metadata, instead of assuming the callee lives in the same file.

**FR-78:** After all files in a directory are indexed, run a resolution pass (`resolve_call_edges`) that:
1. Queries all relationships where `target_qualified` starts with `__unresolved__`
2. For each, does a name-lookup in `code_elements` preferring the same file, then falling back to any file
3. Replaces the unresolved edge with the resolved `qualified_name`
4. Deletes the placeholder edge regardless of resolution success

**FR-79:** The `is_noise_call` filter must exclude common Rust/Go/Python stdlib function identifiers and single-character names from being recorded as call edges.

**FR-80:** `extract_go_implementations` must only emit `implements` edges for embedded (anonymous) struct fields — fields that have no `name` child node in the tree-sitter AST. Named fields (e.g., `Name string`) must not produce `implements` edges.

### 5.2 Query Performance & Safety (US-21)

**FR-81:** Add an `escape_datalog(s: &str) -> String` function in `graph/query.rs` that escapes backslashes and double-quotes. Every place a user-controlled string is substituted into a Datalog query must call this function.

**FR-82:** Replace `all_elements()` call sites in `handler.rs` with new pushed-down `GraphEngine` methods:
- `search_by_name_typed(name, element_type, limit)` — Datalog predicate on `name` and `element_type`
- `find_elements_by_name_exact(name, element_type)` — Datalog predicate on exact `name` match

**FR-83:** Add a shared private `run_element_query(query: &str)` helper in `GraphEngine` that performs the standard 9-column `code_elements` row mapping, eliminating the repeated mapping code.

**FR-84:** Fix `get_dependencies` to query the `relationships` table filtered by `source_qualified = file_path` and `rel_type = "imports"`, returning the actual import target elements, not the file's own symbols.

### 5.3 Token-Efficient Context Mode (US-22)

**FR-85:** During indexing, `extract_function` must capture the first line of each function as its `signature` and store it in `CodeElement.metadata` alongside a `signature_line_end` field indicating the line just before the opening brace.

**FR-86:** Add a `find_body_start_line(node)` helper in the extractor that returns the row of the first `block` or `statement_block` child node.

**FR-87:** Update the `get_context` tool schema to add:
- `signature_only` (boolean, default `true`) — when true, return only `{qualified_name, name, type, file, line, signature, priority}`
- `max_tokens` (integer, default `4000`) — token budget cap already supported, now documented in schema

**FR-88:** Update `handler.rs` `get_context` to read `signature_only` from args (default `true`) and branch output accordingly. When `signature_only=true`, emit single-line output per element using the stored `signature` metadata field.

### 5.4 Bounded Call Graph (US-23)

**FR-89:** Add `get_call_graph_bounded(source_qualified, max_depth, max_results)` in `GraphEngine` with hand-unrolled Datalog hop rules for depth 1, 2, and 3. Apply `:limit max_results` at the query level.

**FR-90:** Update the `get_call_graph` MCP tool to accept `depth` (integer, default `2`) and `max_results` (integer, default `30`) parameters, routed to the new bounded implementation.

### 5.5 Code–Documentation Cross-Edges (US-24, US-25, US-26)

**FR-91:** Fix `get_documented_by` in `graph/query.rs` to filter `source_qualified = element_qualified` and `rel_type = "documented_by"`, then return `target_qualified` as the doc path. (The current query filters `target_qualified`, which is reversed.)

**FR-92:** Add `mcp_index_docs` as an MCP tool that accepts `path` (string, default `"./docs"`) and calls `doc_indexer::index_docs_directory`. The tool must return counts of documents, sections, and relationships indexed.

**FR-93:** Wire `"mcp_index_docs"` into the `execute_tool` match arm in `handler.rs`.

**FR-94:** Fix `extract_code_references` in `doc_indexer/mod.rs`:
1. Track `in_code_block` state across lines (toggle on ```` ``` ```` fence open/close) and skip inner lines
2. Widen the file regex to match bare filenames (`\b[\w\-]+\.(?:go|rs|ts|tsx|js|jsx|py)\b`) not just `src/`-prefixed paths
3. Remove the `qualified_pattern` (`::`-based) match that fires on every Rust expression in code snippets

**FR-95:** In `parse_doc_file`, call `extract_headings(&content)` and store the result in `CodeElement.metadata` alongside `category` and `title`:

```json
{
  "category": "design",
  "headings": ["Section A", "Section B"],
  "title": "LeanKG High Level Design"
}
```

### 5.6 MCP Tool Quality (US-27)

**FR-96:** All MCP tool schemas must include a `required` array listing mandatory parameters.

**FR-97:** Update tool descriptions and defaults:
- `get_call_graph`: description warns against `depth > 3`; add `depth` and `max_results` params
- `get_impact_radius`: description warns that depth 3 may return hundreds of nodes
- `find_function`: add optional `file` scoping parameter
- `search_code`: lower default `limit` to `20`; hard cap at `50` in handler
- `query_file`: add optional `element_type` filter parameter
- `get_context`: add `signature_only` and document `max_tokens`

---

## 6. Non-Functional Requirements

| Requirement | Target |
|-------------|--------|
| `search_code` P99 latency | < 50ms (currently O(n) Rust filter; with Datalog push-down should be < 10ms) |
| `get_context` token output (signature_only) | < 500 tokens for a 50-function file |
| `get_context` token output (full mode) | Unchanged — bounded by existing `max_tokens` cap |
| `resolve_call_edges` run time (1K files) | < 5 seconds |
| `mcp_index_docs` run time (20 docs) | < 2 seconds |
| No Datalog injection possible | All user strings pass through `escape_datalog` |
| No breaking schema changes | Existing `.leankg` databases remain readable; new `signature` metadata is additive |

---

## 7. Acceptance Criteria

### US-19 — Cross-File Call Edges

- **Given** two source files where `A.rs` calls function `foo` defined in `B.rs`
- **When** `mcp_index` is run on the project
- **Then** `get_call_graph { "function": "A.rs::caller" }` returns an edge to `B.rs::foo`

- **Given** a stdlib function like `println!` is called
- **When** `mcp_index` is run
- **Then** no `calls` edge to `println` appears in the graph (`is_noise_call` filters it)

### US-20 — Go Implements Edges

- **Given** a Go struct with a named field `Name string` and an embedded field `io.Reader`
- **When** `mcp_index` is run
- **Then** only one `implements` edge exists (for `io.Reader`), and no `implements` edge exists for `Name`

### US-21 — Query Performance

- **Given** a codebase with 7000+ elements
- **When** `search_code { "query": "extract" }` is called
- **Then** the response is returned in under 50ms (verified by MCP tool response time log)

- **Given** a qualified name containing a double-quote character
- **When** any MCP tool is called with that name as a parameter
- **Then** the tool returns an error or valid result — not a Datalog parse failure

### US-22 — Signature-Only Context

- **Given** a 200-line Rust file with 10 functions
- **When** `get_context { "file": "src/foo.rs" }` is called (no `signature_only` param — defaults to `true`)
- **Then** the response contains a `signature` field for each function element, no `line_end` field, and total token count is under 500

- **When** `get_context { "file": "src/foo.rs", "signature_only": false }` is called
- **Then** the response includes `line_start`, `line_end`, and `token_count` per element

### US-23 — Bounded Call Graph

- **Given** a function with a 10-hop deep call chain
- **When** `get_call_graph { "function": "src/main.rs::main", "depth": 2, "max_results": 30 }` is called
- **Then** the response contains at most 30 edges, none deeper than 2 hops

### US-24 — `get_doc_for_file` Returns Real Results

- **Given** docs have been indexed via `mcp_index_docs` and `hld-leankg.md` mentions `extractor.rs`
- **When** `get_doc_for_file { "file": "src/indexer/extractor.rs" }` is called
- **Then** the response lists `docs/design/hld-leankg.md` — not `src/indexer/extractor.rs`

### US-25 — `mcp_index_docs` Tool

- **Given** a project with a `docs/` directory containing 18 markdown files
- **When** `mcp_index_docs { "path": "./docs" }` is called
- **Then** the response reports `documents >= 18`, `relationships > 0`, `success: true`

- **Given** `mcp_index_docs` is called with a non-existent path
- **Then** the tool returns an error message: `"Docs path does not exist: <path>"`

### US-26 — Doc Reference Extraction

- **Given** `hld-leankg.md` contains the text `"The extractor.rs parser uses tree-sitter"`
- **When** `mcp_index_docs` is run
- **Then** a `references` relationship exists from `docs/design/hld-leankg.md` to `extractor.rs`

- **Given** a code fence in a markdown file contains `extractor.rs` inside a code block
- **Then** no `references` relationship is created for that occurrence

### US-27 — MCP Tool Schema Quality

- **Given** an LLM queries the MCP server for tool schemas
- **When** it inspects `get_call_graph`
- **Then** the schema includes `"required": ["function"]`, a `depth` parameter, and a description warning against `depth > 3`

- **Given** an LLM calls `search_code` without a `limit` parameter
- **Then** the handler defaults to `limit = 20` and returns at most 50 results

---

## 8. Implementation Order (Priority)

| Priority | User Story | FR | Effort |
|----------|-----------|-----|--------|
| P0 | US-21 (injection safety) | FR-81 | XS — single helper function |
| P0 | US-24 (fix `get_documented_by`) | FR-91 | XS — one-line query fix |
| P0 | US-21 (fix `get_dependencies`) | FR-84 | S — query rewrite |
| P1 | US-21 (push-down queries) | FR-82, FR-83 | M — new GraphEngine methods |
| P1 | US-19 (cross-file call edges) | FR-77, FR-78, FR-79 | M — extractor + resolution pass |
| P1 | US-20 (Go implements) | FR-80 | S — extractor AST condition fix |
| P1 | US-25 (mcp_index_docs) | FR-92, FR-93 | S — new handler + tool def |
| P2 | US-22 (signature_only) | FR-85, FR-86, FR-87, FR-88 | M — extractor + handler |
| P2 | US-23 (bounded call graph) | FR-89, FR-90 | S — new GraphEngine method |
| P2 | US-26 (doc reference extraction) | FR-94, FR-95 | S — regex + metadata fix |
| P2 | US-27 (tool schema quality) | FR-96, FR-97 | S — tools.rs updates |

Estimated total effort: 4–6 engineering days for a single developer familiar with the codebase.

---

## 9. Out of Scope for v2.0

- Semantic / vector search (deferred to v3.0)
- Cross-language call resolution (e.g., Go calling a Rust FFI) — resolution pass is single-language only
- Removing existing `all_elements()` callers that are not on hot paths (e.g., `find_oversized_functions`) — acceptable O(n) for rare calls
- Web UI changes
- Changes to `leankg.yaml` schema

---

## 10. Risks

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| `resolve_call_edges` creates false-positive edges (wrong cross-file resolution) | Medium | Prefer same-file matches; delete unresolved edges after one attempt; add `confidence` metadata field for future filtering |
| Widened doc regex matches too many tokens and floods graph with noise | Low-Medium | Cap at 200 `references` edges per doc; add a minimum filename length filter (>= 5 chars) |
| `escape_datalog` misses a call site | Low | Add a `clippy` lint or unit test for every `format!()` call in `query.rs` that includes user input |
| `signature_only` default breaks existing integrations that parse `line_end` from `get_context` | Low | `line_end` is still available via `signature_only=false`; add to release notes |

---

## 11. Verification Plan

After implementing all FRs, run the following checks:

```bash
# 1. Build succeeds, no warnings
cargo build 2>&1 | grep -E "^error"

# 2. Unit tests pass
cargo test

# 3. Re-index LeanKG's own codebase
leankg index ./src

# 4. Verify cross-file calls exist
# Expect: edges between handler.rs and query.rs functions
leankg impact src/mcp/handler.rs 2

# 5. Index docs
# (via MCP) mcp_index_docs { "path": "./docs" }

# 6. Verify doc-code cross-edges
# (via MCP) get_doc_for_file { "file": "src/indexer/extractor.rs" }
# Expected: at least one .md file returned

# 7. Verify signature_only
# (via MCP) get_context { "file": "src/mcp/handler.rs" }
# Expected: "signature_only": true, no "line_end" in elements

# 8. Verify injection safety
# (via MCP) search_code { "query": "\"dangerous\"" }
# Expected: valid JSON response, not a Datalog parse error
```

---

## 12. Glossary Additions

| Term | Definition |
|------|-----------|
| Unresolved call edge | A `calls` relationship where `target_qualified` starts with `__unresolved__`, indicating the callee was not yet resolved to a global qualified name |
| Resolution pass | Post-index step that replaces unresolved call edges with actual cross-file targets |
| Noise call | A call to a stdlib or trivial function (e.g., `println`, `unwrap`) that is excluded from the graph to reduce noise |
| Signature-only mode | `get_context` output mode where only the function signature line is returned per element instead of full line range metadata |
| Datalog injection | A correctness/security issue where unescaped user strings in a Datalog `format!()` query produce malformed or malicious queries |
| `escape_datalog` | Helper function that escapes backslashes and double-quotes before substitution into Datalog query strings |
