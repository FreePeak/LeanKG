# LeanKG Performance Analysis: Memory & CPU Root Causes

**Date:** 2026-04-28
**Status:** Open
**Branch:** `fix/perf-memory-cpu`
**Scale:** 25,937 elements, 89,983 relationships, 637 files

---

## Executive Summary

LeanKG's MCP server consumes excessive memory and CPU because nearly every tool call performs **full table scans** of the entire dataset. The `mcp_status` tool — called before every other tool per CLAUDE.md mandate — loads all 25K+ elements and 90K+ relationships into memory on every invocation. Additionally, subprocess spawning for baseline estimation adds CPU overhead per call.

---

## Issue #1: `mcp_status` Loads the Entire Dataset (CRITICAL)

**File:** `src/mcp/handler.rs:606-665`
**Impact:** ~115K struct allocations per status call, called before every other tool

`mcp_status` is the gateway tool mandated by CLAUDE.md to be called first. Each invocation:

1. Calls `all_elements()` to check if tables exist (line 608) → **25,937 CodeElement structs**
2. Calls `all_elements()` again to count elements (line 622) → **25,937 structs again**
3. Calls `all_relationships()` (line 626) → **89,983 Relationship structs** + builds secondary index (`HashMap<String, Vec<usize>>` with ~180K entries)
4. Calls `all_business_logic()` (line 629) → **all BusinessLogic entries**

**Total per call:** ~50K element structs, ~90K relationship structs, ~180K index entries, 25K+ JSON metadata parses.

### Fix

Replace full table scans with COUNT queries:

```sql
-- Count elements directly
?[count(n)] := *code_elements[n, ...], n = n :collect count
-- Count relationships directly
?[count(n)] := *relationships[n, ...], n = n :collect count
```

Alternatively, add a lightweight `is_initialized()` method that runs a bounded query (`:limit 1`) instead of loading everything.

---

## Issue #2: Most MCP Handlers Call `all_elements()` + `all_relationships()` (CRITICAL)

**File:** `src/mcp/handler.rs` (multiple tool methods)
**Impact:** Full dataset loaded on every tool invocation

The following tools all call `all_elements()` and/or `all_relationships()`, then filter results in Rust:

| Tool | Lines | What it does after loading all data |
|------|-------|-------------------------------------|
| `query_file` | 859-895 | Linear scan for file path match |
| `search_code` | 1197-1223 | Delegates to `search_by_name_typed` (has DB query, but handler still loads all) |
| `generate_doc` | 1296-1312 | Linear scan for file elements |
| `find_large_functions` | 1314-1341 | Linear scan for oversized functions |
| `get_code_tree` | 1565-1617 | Loads all, groups by file |
| `get_doc_tree` | 1520-1563 | Loads all, filters by type |
| `get_doc_structure` | 1414-1446 | Loads all, filters by type |
| `search_annotations` | 1225-1294 | Loads all elements + all relationships |
| `detect_changes` | 697-857 | Loads all elements + all relationships |
| `get_nav_graph` | 1700-1757 | Loads all elements + all relationships |
| `find_route` | 1759-1786 | Loads all elements + all relationships |
| `get_screen_args` | 1788-1825 | Loads all elements + all relationships |
| `get_nav_callers` | 1827-1852 | Loads all relationships |
| `get_cluster_context` | 1854-1934 | Loads all + runs clustering |

### Fix

Replace with targeted CozoDB queries using `regex_matches`, `file_path =`, `element_type =` etc. CozoDB supports all these filters natively. For example, `find_large_functions` already has `find_oversized_functions()` in `GraphEngine` that uses a targeted DB query — but the handler ignores it and loads all elements instead.

---

## Issue #3: `estimate_baseline()` Spawns Subprocesses Every Call (HIGH)

**File:** `src/mcp/handler.rs:255-338`
**Impact:** CPU overhead from `fork()` + `exec()` on every tool call

`execute_tool()` calls `estimate_baseline()` for `search_code`, `find_function`, `query_file`, `get_dependencies`, `get_dependents`, `get_context`, and `get_impact_radius`. Each spawns a shell command:

- `Command::new("grep").args(["-rn", ...])` — scans `./src`
- `Command::new("find").args([...])` — walks filesystem
- `std::fs::read_to_string(file)` — reads entire files

These run synchronously, blocking the tool execution, and their cost scales with codebase size.

### Fix

Options:
1. **Remove entirely** — the baseline comparison is a development metric, not needed in production
2. **Make async and optional** — gate behind an env var like `LEANKG_BASELINE_METRICS=1`
3. **Cache results** — reuse baseline estimates across calls within a session

---

## Issue #4: `CommunityDetector` Creates New `GraphEngine` Without Caches (HIGH)

**File:** `src/graph/clustering.rs:10-14`
**Impact:** Re-fetches 25K+ elements and 90K+ relationships from scratch

```rust
pub fn new(db: &CozoDb) -> Self {
    Self { graph_engine: GraphEngine::new(db.clone()) }  // Fresh engine, no caches!
}
```

When `get_clusters` or `get_cluster_context` is called, a new `GraphEngine` is created that re-fetches all data. The Louvain algorithm then runs up to 10 O(n*m) iterations over all nodes.

### Fix

Accept `&GraphEngine` reference instead of creating a new one:

```rust
pub fn new(graph_engine: &GraphEngine) -> Self {
    Self { graph_engine: graph_engine.clone() }
}
```

---

## Issue #5: File Watcher Creates New DB + ParserManager Per Change (HIGH)

**File:** `src/mcp/watcher.rs:8-18`
**Impact:** Heavy resource allocation on every file save

Each file change:
- Opens a **new** CozoDB connection via `init_db()`
- Creates a **new** `GraphEngine` (empty caches)
- Creates a **new** `ParserManager` and initializes all tree-sitter parsers

### Fix

Share a single `GraphEngine` and `ParserManager` across the watcher's lifetime. Pass them in during initialization rather than creating fresh instances per change event.

---

## Issue #6: Regex Compiled on Every Call in Hot Path (MEDIUM)

**File:** `src/indexer/extractor.rs:289-299, 342-343`
**Impact:** Unnecessary CPU on every Kotlin/Java file index

`extract_find_view_by_id()` compiles 4 regex patterns per call:
```rust
for pattern in &patterns {
    let re = Regex::new(pattern).unwrap(); // Compiled every time
```

`extract_viewbinding_access()` compiles a dynamic regex per binding class name.

### Fix

Move static patterns to `Lazy<Regex>` in `regex_cache.rs`, matching the existing pattern used by other regexes in the file.

---

## Issue #7: PersistentCache Doubles Memory (MEDIUM)

**File:** `src/graph/persistent_cache.rs`
**Impact:** Every cached value stored as JSON in both HashMap AND CozoDB

The `PersistentCache` writes every cache entry to:
1. In-memory `HashMap<String, CacheEntry>` (full JSON string)
2. CozoDB `query_cache` table (same JSON string)

Combined with the `QueryCache` having 3 separate `TimedCache` instances, data gets cached at multiple redundant levels.

### Fix

Use the in-memory HashMap as a read-through cache only. Write to DB on insert, read from DB on miss. Don't store the full JSON string in both places simultaneously — or use a size-bounded cache with eviction.

---

## Issue #8: `get_elements_in_folder` Loads 5000 Rows Then Filters in Rust (LOW)

**File:** `src/graph/query.rs:520-529`
**Impact:** Wasteful DB → Rust roundtrip

For root-level children, the code loads up to 5000 rows from CozoDB, then filters to direct children only in Rust:

```rust
let query_str = format!("... :limit 5000 :offset 0", ...);
// Then filters in Rust:
let is_direct = file_path.starts_with("./") && !file_path[2..].contains('/');
```

### Fix

Use a CozoDB query that filters for direct children natively, or use the `get_top_level_directories` method's range-scan approach.

---

## Data Flow Diagram

```
Every Tool Call
│
├── mcp_status (MANDATORY first call)
│   ├── all_elements() ──────────────► 25,937 structs + JSON parse
│   ├── all_elements() ──────────────► 25,937 structs AGAIN
│   ├── all_relationships() ─────────► 89,983 structs + 180K index
│   └── all_business_logic() ────────► annotations
│
├── Actual Tool (e.g., query_file)
│   ├── all_elements() ──────────────► 25,937 structs (3rd time!)
│   ├── [some tools] all_relationships() ► 89,983 structs (2nd time!)
│   └── estimate_baseline()
│       └── Command::new("grep") ───► fork + exec + filesystem scan
│
└── record_metric()
    └── DB write (fast)
```

---

## Priority Matrix

| # | Priority | Effort | Fix | Expected Impact |
|---|----------|--------|-----|-----------------|
| 1 | P0 | Small | `mcp_status` use COUNT queries | **~60% memory reduction per session** |
| 2 | P0 | Medium | Targeted DB queries in handlers | **~80% memory reduction per tool call** |
| 3 | P1 | Small | Remove/gate `estimate_baseline()` | **~30% CPU reduction per tool call** |
| 4 | P1 | Small | Reuse GraphEngine in clustering | **Eliminate redundant full scans** |
| 5 | P2 | Medium | Share state in file watcher | **Reduce per-change overhead** |
| 6 | P2 | Small | Move regex to `Lazy<Regex>` | **Small CPU savings on indexing** |
| 7 | P3 | Medium | Deduplicate cache layers | **Moderate memory savings** |
| 8 | P3 | Small | DB-level folder filtering | **Small DB query optimization** |

---

## Recommended Implementation Order

1. **Phase 1 (Quick wins):** Fix #1 (mcp_status COUNT), Fix #3 (baseline removal), Fix #6 (regex caching)
2. **Phase 2 (Handler refactor):** Fix #2 (targeted queries) — migrate handlers one by one
3. **Phase 3 (Architecture):** Fix #4 (clustering), Fix #5 (watcher), Fix #7 (cache dedup)

---

*Document generated from source code analysis. All file paths and line numbers are accurate as of commit `d46cf79`.*
