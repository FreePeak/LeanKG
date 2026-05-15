# MCP Tools Fixes Implementation Plan

**Date:** 2026-05-10
**Source:** `docs/analysis/mcp-full-test-report-2026-05-10.md`
**Commit:** `7c259aa`
**Binary version:** v0.17.0
**Test Pass Rate:** 85.7% (42/49 tools)
**Issues to Fix:** 5 (1 critical, 1 high, 2 medium, 1 low)

---

## Executive Summary

This document provides detailed implementation instructions to fix the 6 failing/partial MCP tool issues identified in the full test report. Each issue includes: root cause analysis, files to modify, implementation steps, and testing verification.

**Issue Summary:**

| Priority | Issue | Tools Affected | Current Status |
|----------|-------|----------------|----------------|
| CRITICAL | Impact Radius Timeout at depth >= 2 | `get_impact_radius`, `mcp_impact` | FAIL (timeout) |
| HIGH | query_file returns empty or wrong data | `query_file` | FAIL / PARTIAL |
| MEDIUM | find_function fuzzy matching false positives | `find_function` | PASS (caveat) |
| MEDIUM | Worktree duplicate pollution | `search_code`, `find_function`, `get_callers`, `generate_doc` | PASS (caveat) |
| LOW | Oversized tree responses | `get_doc_tree`, `get_code_tree`, `get_doc_structure` | PASS (oversized) |

---

## Issue 1: CRITICAL - Impact Radius Timeout (depth >= 2)

### Problem
`get_impact_radius` and `mcp_impact` consistently timeout at depth >= 2 due to graph traversal explosion. With 111,335 relationships, transitive closure at depth 2+ generates exponential paths.

**Evidence:**
- `get_impact_radius("src/main.rs", depth=2)` - timeout (3 attempts)
- `get_impact_radius("src/graph/query.rs", depth=3)` - timeout (3 attempts)
- `mcp_impact("src/main.rs", depth=2)` - timeout (multiple attempts)
- `mcp_impact("src/graph/query.rs", depth=3)` - timeout
- Depth=1 works correctly (e.g., `mcp_impact("src/main.rs", depth=1)` returns 137 affected elements)

### Root Cause
The recursive graph traversal query in `ImpactAnalyzer` lacks:
1. Cycle detection (re-visiting same nodes)
2. Result capping / early termination
3. Server-side depth enforcement with progressive timeouts

### Files to Modify
1. `src/graph/traversal.rs` - Core impact analysis logic
2. `src/mcp/handler.rs` - MCP tool wrappers (timeout handling)
3. `src/graph/query.rs` - Graph engine query optimization

### Implementation Steps

#### Step 1.1: Add Cycle Detection to Impact Traversal
In `src/graph/traversal.rs`, modify the recursive traversal to track visited nodes:

```rust
// Current: likely uses raw recursive CTE or iterative expansion without visited set
// Fix: Add visited node tracking to prevent cycles

// Pseudocode for the fix:
fn compute_impact_radius(
    &self,
    file: &str,
    depth: usize,
) -> Result<ImpactResult> {
    let max_depth = depth.min(3); // Hard cap at depth 3
    let max_results = 1000; // Cap total results
    // ... implement visited set tracking
}
```

**Specific changes:**
- Add `visited: HashSet<String>` to track already-visited element IDs
- Skip expansion of nodes already in visited set
- Return `Err` immediately if depth > 3 with a clear message

#### Step 1.2: Add Result Capping
- Cap total affected elements at 1,000 per query
- When cap is reached, return partial results with `truncated: true` flag
- Log warning when truncation occurs

#### Step 1.3: Optimize CozoDB Query
The CozoDB recursive query should use:
```cozo
// Use distinct paths, limit recursion depth, add cycle check
?[element_id, depth] <- recurse[
    start_element -> related,
    depth <= :max_depth,
    cycle_check
]
:limit 1000
```

**Key query changes:**
- Add `distinct` to eliminate duplicate paths
- Add `:limit` clause to cap results at DB level
- Add early termination when no new nodes found in an iteration

#### Step 1.4: Update MCP Handler Timeout
In `src/mcp/handler.rs`:
- Add 30-second timeout wrapper for `get_impact_radius` and `mcp_impact`
- Return user-friendly error: "Impact radius calculation exceeded time limit. Try depth=1 or a smaller file."
- Pre-validate depth parameter: reject depth > 3 at API level

### Testing Steps
1. `cargo test` - ensure existing tests pass
2. Manual test: `cargo run -- impact src/main.rs --depth 2` should complete in < 10 seconds
3. Manual test: `cargo run -- impact src/graph/query.rs --depth 3` should complete in < 10 seconds
4. Verify depth=1 still works: `cargo run -- impact src/main.rs --depth 1`
5. Verify large files: test on `src/mcp/handler.rs` at depth 2

### Acceptance Criteria
- [ ] `get_impact_radius(file, depth=2)` completes in < 15 seconds for any file
- [ ] `get_impact_radius(file, depth=3)` completes in < 30 seconds for any file
- [ ] Depth > 3 rejected with clear error message
- [ ] Results capped at 1,000 elements with `truncated: true` when exceeded
- [ ] No false positives (elements not actually affected)
- [ ] Depth=1 behavior unchanged

---

## Issue 2: HIGH - query_file Returns Empty or Wrong Data

### Problem
`query_file` returns empty for existing files (`src/main.rs`) and returns documentation references instead of code elements for other files (`src/db/models.rs`).

**Evidence:**
- `query_file("src/main.rs")` -> `[]` (empty, file exists in index)
- `query_file("src/db/models.rs")` -> 6 `doc_section` results (documentation references, not code elements)

### Root Cause
The tool queries documents that reference the file path string instead of querying code elements that are **contained in** the file. The pattern matching logic likely:
1. Matches against doc section content that mentions the filename
2. Does not query `CodeElement` table with `file_path = ?` filter
3. May require exact path format different from user input

### Files to Modify
1. `src/mcp/handler.rs` - `query_file` tool handler
2. `src/graph/query.rs` - `GraphEngine::query_file` or equivalent
3. `src/db/models.rs` - verify CodeElement schema has file_path field

### Implementation Steps

#### Step 2.1: Fix Query Logic
In the handler or graph query layer, change `query_file` to:

```rust
fn query_file(&self, pattern: &str) -> Result<Vec<CodeElement>> {
    // 1. Normalize the pattern (strip ./ prefix, ensure relative path)
    let normalized = normalize_path(pattern);
    
    // 2. Query CodeElement table where file_path matches
    let query = r#"
        ?[id, name, element_type, file_path, line_number, qualified_name] :=
            *CodeElement { id, name, element_type, file_path, line_number, qualified_name },
            file_path = $pattern
        :limit 50
    "#;
    
    // 3. Also try substring match as fallback
    // 4. Return empty ONLY if file truly has no indexed elements
}
```

**Specific changes:**
- Query `CodeElement` table directly, not `Document` or `doc_section`
- Use exact `file_path` match first
- If exact match fails, try `file_path LIKE '%pattern%'`
- Normalize user input: strip `./` prefix, handle absolute vs relative paths

#### Step 2.2: Add Path Normalization
Create or reuse a path normalization helper:
```rust
fn normalize_file_path(path: &str) -> String {
    let p = Path::new(path);
    // Strip ./ prefix
    // Convert absolute to relative if under project root
    // Return cleaned string
}
```

#### Step 2.3: Return Proper Element Types
Ensure results include:
- `file` element type (the file itself)
- `function` elements defined in the file
- `struct`/`class` elements defined in the file
- `module` elements

Do NOT return `doc_section` elements unless explicitly requested.

### Testing Steps
1. `cargo test`
2. `cargo run -- query --file src/main.rs` (CLI equivalent) should return file + functions
3. MCP test: `query_file("src/main.rs")` should return ~36 functions + 1 file element
4. MCP test: `query_file("src/db/models.rs")` should return `CodeElement`, `Relationship`, `BusinessLogic`, `Document` structs + test functions
5. Test with `./src/main.rs` (with leading ./) - should work same as `src/main.rs`

### Acceptance Criteria
- [ ] `query_file("src/main.rs")` returns the file element + all functions/classes in that file
- [ ] `query_file("src/db/models.rs")` returns code elements, not doc references
- [ ] Input with `./` prefix works correctly
- [ ] Non-existent file returns empty array with clear message
- [ ] Response time < 2 seconds

---

## Issue 3: MEDIUM - find_function Fuzzy Matching False Positives

### Problem
Searching for "main" returns functions like `find_by_domain` and `find_by_business_domain` because they contain "main" as a substring ("do**main**").

**Evidence:**
- `find_function("main")` -> 50 results including `find_by_domain`, `find_by_business_domain`
- Expected: only functions named exactly `main` or starting with `main_`

### Root Cause
The current implementation uses substring/ILIKE matching (`name LIKE '%main%'`) instead of word-boundary or exact matching.

### Files to Modify
1. `src/graph/query.rs` - `GraphEngine::find_function` or equivalent
2. `src/mcp/handler.rs` - optional: add `exact` parameter

### Implementation Steps

#### Step 3.1: Implement Exact Match Priority
Modify the query to use exact match first, then fallback to prefix match:

```rust
fn find_function(&self, name: &str, file: Option<&str>) -> Result<Vec<CodeElement>> {
    // Priority 1: Exact match (case-insensitive)
    let exact_query = r#"
        ?[id, name, element_type, file_path, line_number, qualified_name, score] :=
            *CodeElement { id, name, element_type, file_path, line_number, qualified_name },
            element_type = 'function',
            lowercase(name) = lowercase($name),
            score = 100
    "#;
    
    // Priority 2: Starts with (case-insensitive)
    let prefix_query = r#"
        ?[id, name, element_type, file_path, line_number, qualified_name, score] :=
            *CodeElement { id, name, element_type, file_path, line_number, qualified_name },
            element_type = 'function',
            starts_with(lowercase(name), lowercase($name)),
            score = 50
    "#;
    
    // Priority 3: Contains as word boundary (optional, lower priority)
    // Combine and sort by score descending
}
```

**Specific changes:**
- Use `lowercase(name) = lowercase($name)` for exact match
- Use `starts_with()` for prefix match
- Remove pure substring matching (or deprioritize heavily)
- Sort results: exact match first, then prefix, then substring (if included)

#### Step 3.2: Add Match Score to Response
Include a `match_type` field in response:
- `"exact"` - exact match
- `"prefix"` - starts with query
- `"substring"` - contains query (only if no exact/prefix matches)

#### Step 3.3: Optional MCP Parameter
Add optional parameter to `find_function` MCP tool:
- `exact: bool` - default false (for backward compatibility)
- When `exact=true`, only return exact matches

### Testing Steps
1. `cargo test`
2. `find_function("main")` should return only functions named exactly `main` (across languages)
3. `find_function("main")` should NOT return `find_by_domain`
4. `find_function("handle_")` should return functions starting with `handle_`
5. `find_function("nonexistent_function_xyz")` should return empty

### Acceptance Criteria
- [ ] `find_function("main")` excludes substring-only matches like `find_by_domain`
- [ ] Exact matches appear first in results
- [ ] Prefix matches appear second
- [ ] Substring matches (if any) appear last and are clearly marked
- [ ] Empty result for non-existent function names
- [ ] Response time < 2 seconds

---

## Issue 4: MEDIUM - Worktree Duplicate Pollution

### Problem
Results from `search_code`, `find_function`, `get_callers`, `generate_doc` include duplicate entries from `.worktrees/` directory paths.

**Evidence:**
- `search_code("CodeElement")` returns results from `src/db/models.rs` AND worktree copies
- `find_function("main")` returns duplicates across original and worktree paths
- `get_callers("query_file")` includes worktree duplicates
- `generate_doc("src/main.rs")` lists every function 4 times (original + 3 worktree copies)

### Root Cause
The indexer includes `.worktrees/` directory during indexing, or queries do not filter out worktree paths.

### Files to Modify
1. `src/indexer/extractor.rs` - Indexing logic (exclude `.worktrees/`)
2. `src/config/project.rs` - Default exclude patterns
3. `src/mcp/handler.rs` - Add filtering in MCP handlers
4. `src/graph/query.rs` - Add filtering in graph queries

### Implementation Steps

#### Step 4.1: Exclude Worktrees from Indexing
In `src/indexer/extractor.rs` or `src/config/project.rs`, add `.worktrees/` to default excludes:

```rust
// In ProjectConfig or IndexerConfig
default_excludes: vec![
    ".git",
    ".worktrees",  // ADD THIS
    "target",
    "node_modules",
    "dist",
    "build",
],
```

**Specific changes:**
- Add `.worktrees` to default exclude list in project config
- Ensure indexer skips `.worktrees/` directory recursively
- If already indexed, may need re-index: `cargo run -- index ./src`

#### Step 4.2: Add Runtime Filtering
In `src/graph/query.rs` and `src/mcp/handler.rs`, add worktree filter to all query functions:

```rust
fn filter_worktrees(elements: Vec<CodeElement>) -> Vec<CodeElement> {
    elements.into_iter()
        .filter(|e| !e.file_path.contains("/.worktrees/"))
        .collect()
}
```

Apply this filter to:
- `search_code`
- `find_function`
- `get_callers`
- `get_call_graph`
- `generate_doc`
- `get_dependencies`
- `get_dependents`
- `get_impact_radius`

#### Step 4.3: Add Optional Parameter
Add optional `include_worktrees: bool` parameter (default false) to relevant MCP tools for backward compatibility.

### Testing Steps
1. `cargo test`
2. Re-index: `cargo run -- index ./src`
3. `search_code("CodeElement")` should return only 1 result per actual file
4. `find_function("main")` should not include paths containing `/.worktrees/`
5. `generate_doc("src/main.rs")` should list each function exactly once
6. Verify `.worktrees/` directory exists in project and has copies of source files

### Acceptance Criteria
- [ ] No query results include paths containing `/.worktrees/`
- [ ] Each function/class appears at most once per actual source file
- [ ] Re-indexing does not add worktree files to the database
- [ ] Response sizes reduced (e.g., generate_doc under 2,000 tokens)
- [ ] Backward compatibility maintained (can opt-in to include worktrees)

---

## Issue 5: LOW - Oversized Tree Responses

### Problem
`get_doc_tree`, `get_code_tree`, and `get_doc_structure` return responses ranging from 142KB to 3MB, exceeding LLM token limits.

**Evidence:**
- `get_doc_structure("README.md")` -> 142,462 characters
- `get_doc_tree` -> 392,569 characters
- `get_code_tree` -> 3,032,430 characters (3MB!)

### Root Cause
These tools return entire trees without pagination, size limits, or truncation.

### Files to Modify
1. `src/mcp/handler.rs` - MCP tool handlers for tree responses
2. `src/graph/query.rs` - Tree query implementation
3. `src/compress/response.rs` - Response compression (if exists)

### Implementation Steps

#### Step 5.1: Add Pagination Support
Update tool signatures to accept `offset` and `limit` parameters:

```rust
// MCP tool definition
struct GetCodeTreeParams {
    #[serde(default = "default_offset")]
    offset: usize,
    #[serde(default = "default_limit")]
    limit: usize,  // default 50, max 200
}

fn default_offset() -> usize { 0 }
fn default_limit() -> usize { 50 }
```

#### Step 5.2: Add Response Size Caps
Hard caps at the query level:
- `get_doc_tree`: max 50 categories per request
- `get_code_tree`: max 50 files per request
- `get_doc_structure`: max 100 sections per request

```rust
fn get_code_tree(&self, offset: usize, limit: usize) -> Result<CodeTree> {
    let limit = limit.min(200); // Hard max
    // Query with offset and limit
}
```

#### Step 5.3: Add Total Count to Response
Include pagination metadata:
```json
{
  "items": [...],
  "total": 1119,
  "offset": 0,
  "limit": 50,
  "has_more": true
}
```

#### Step 5.4: Add Summarization Mode
Add optional `summary: bool` parameter that returns:
- Top-level categories only (for doc tree)
- File count per directory (for code tree)
- Section headers only (for doc structure)

### Testing Steps
1. `cargo test`
2. `get_code_tree(offset=0, limit=10)` should return exactly 10 items
3. `get_code_tree(offset=0, limit=10)` should include `total`, `has_more` fields
4. `get_doc_tree(offset=0, limit=20)` should return <= 20 categories
5. `get_doc_structure("README.md", limit=50)` should return <= 50 sections
6. Verify response sizes are under 50KB per request

### Acceptance Criteria
- [ ] `get_code_tree` accepts `offset` and `limit` parameters
- [ ] `get_doc_tree` accepts `offset` and `limit` parameters
- [ ] `get_doc_structure` accepts `limit` parameter
- [ ] Default limit is 50, hard max is 200
- [ ] Response includes `total`, `offset`, `limit`, `has_more` metadata
- [ ] No single response exceeds 100KB
- [ ] Backward compatible (default parameters work)

---

## Cross-Cutting Concerns

### Re-indexing Requirement
After fixing indexing exclusions (Issue 4), you MUST re-index:
```bash
cargo run -- index ./src
cargo run -- index_docs ./docs
```

### Database Schema Check
Before modifying queries, verify the CozoDB schema:
```bash
cargo run -- status
```
Ensure `CodeElement` table has columns: `id`, `name`, `element_type`, `file_path`, `line_number`, `qualified_name`

### MCP Tool Registration
When adding parameters to MCP tools, update:
1. `src/mcp/tools.rs` - Tool schema definitions
2. `src/mcp/handler.rs` - Parameter parsing and handler logic
3. `docs/mcp-tools.md` - Documentation

### Testing Command Reference
```bash
# Build
cargo build --release

# Run all tests
cargo test

# Start MCP server for manual testing
cargo run -- serve

# Test impact radius (CLI)
cargo run -- impact src/main.rs --depth 2
cargo run -- impact src/main.rs --depth 3  # should now work or give clear error

# Test query (CLI)
cargo run -- query --file src/main.rs

# Test find function (CLI)
cargo run -- query --kind name main
```

---

## Work Estimates

| Issue | Complexity | Estimated Time | File Changes |
|-------|-----------|----------------|--------------|
| 1. Impact Radius Timeout | High | 4-6 hours | 2-3 files |
| 2. query_file Fix | Medium | 2-3 hours | 2 files |
| 3. find_function Exact Match | Low | 1-2 hours | 1-2 files |
| 4. Worktree Filtering | Low | 1-2 hours | 3-4 files |
| 5. Tree Pagination | Medium | 2-3 hours | 2-3 files |
| **Total** | | **10-16 hours** | **5-7 files** |

**Recommended Order:**
1. Issue 4 (Worktree) - reduces noise for testing other fixes
2. Issue 3 (find_function) - small, independent fix
3. Issue 2 (query_file) - related to Issue 3
4. Issue 5 (Tree Pagination) - independent
5. Issue 1 (Impact Timeout) - most complex, save for last

---

## Sign-off Checklist

Before declaring this work complete:
- [ ] All 5 issues have implementation PRs merged
- [ ] `cargo test` passes 100%
- [ ] Re-run MCP full test suite: pass rate >= 95%
- [ ] `get_impact_radius` depth=2 completes in < 15s
- [ ] `query_file("src/main.rs")` returns > 0 results
- [ ] `find_function("main")` does not include `find_by_domain`
- [ ] No results contain `/.worktrees/` paths
- [ ] `get_code_tree(limit=10)` returns exactly 10 items
- [ ] Update `docs/analysis/mcp-full-test-report-YYYY-MM-DD.md` with new results

---

*Implementation plan generated from test report: `docs/analysis/mcp-full-test-report-2026-05-10.md`*
