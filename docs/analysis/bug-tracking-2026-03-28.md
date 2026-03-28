# LeanKG Bug Tracking

**Date:** 2026-03-28
**Last Updated:** 2026-03-28 (Fixed)
**Verified by:** Auto Trigger/Auto Init Verification Session
**Reference:** `docs/analysis/auto-init-auto-trigger-deep-dive-2026-03-28.md`

---

## Summary

| Bug ID | Title | Severity | Status |
|--------|-------|----------|--------|
| BUG-001 | Files count always shows 0 in mcp_status | Low | FIXED |
| BUG-002 | Classes count always shows 0 in mcp_status | Low | FIXED |
| BUG-003 | index_on_first_call config not implemented | Medium | FIXED |

---

## Bug Details

### BUG-001: Files Count Always Shows 0

**Severity:** Low
**Component:** MCP handler
**File:** `src/mcp/handler.rs:315`
**Status:** FIXED

**Root Cause:**
```rust
// BEFORE (broken)
let files = elements.iter().filter(|e| e.element_type == "file").count();
```
The code filters elements by `element_type == "file"`, but the extractor never creates elements with this type.

**Fix Applied:**
```rust
// AFTER (fixed)
let unique_files: std::collections::HashSet<_> = elements.iter().map(|e| e.file_path.clone()).collect();
let files = unique_files.len();
```
Now counts unique file paths instead of filtering by non-existent element_type.

**Files Modified:**
- `src/mcp/handler.rs:315`
- `src/main.rs:511`

**Verification:**
```
$ leankg status (kubernetes repo)
Files: 1625  <-- FIXED (was 0)
```

---

### BUG-002: Classes Count Always Shows 0

**Severity:** Low
**Component:** MCP handler
**File:** `src/mcp/handler.rs:317`
**Status:** FIXED

**Root Cause:**
```rust
// BEFORE (broken)
let classes = elements.iter().filter(|e| e.element_type == "class").count();
```
Go uses `struct` not `class`, so this filter returned 0.

**Fix Applied:**
```rust
// AFTER (fixed)
let classes = elements.iter().filter(|e| e.element_type == "class" || e.element_type == "struct").count();
```
Now counts both `class` and `struct` elements as class-like.

**Files Modified:**
- `src/mcp/handler.rs:317`
- `src/main.rs:516-519`

**Verification:**
```
$ leankg status (kubernetes repo)
Classes: 1714  <-- FIXED (was 0)
```

---

### BUG-003: index_on_first_call Config Not Implemented

**Severity:** Medium
**Component:** MCP server
**File:** `src/config/project.rs:32,63`
**Status:** FIXED

**Root Cause:**
The config option `index_on_first_call` was defined but never used anywhere in the codebase - pure dead code.

**Fix Applied:**
Removed the unused `index_on_first_call` field from `McpConfig` struct and its Default implementation.

**Files Modified:**
- `src/config/project.rs:32` - Removed field from McpConfig
- `src/config/project.rs:63` - Removed from Default impl

**Verification:**
```bash
$ cargo build  # Passes
$ cargo test   # All tests pass (24 passed)
$ grep -rn "index_on_first_call" src/  # No matches (dead code removed)
```

---

## Test Results

```
$ cargo test
test result: ok. 24 passed; 0 failed; 0 ignored

Config tests:
test config::project::tests::test_default_config ... ok
test config::project::tests::test_config_documentation ... ok
test config::project::tests::test_config_indexer_excludes ... ok
test config::project::tests::test_config_project_settings ... ok
```

---

## Verification Evidence

### Auto Init Test (PASS)
```
Target: /Users/linh.doan/work/harvey/freepeak/kubernetes
Action: rm -rf .leankg && leankg mcp-stdio --watch

Result:
- .leankg/ created
- leankg.yaml created (default config)
- leankg.db created (26MB)
- 12,527 elements indexed
- 18,241 relationships created
```

### Auto Trigger Test (PASS)
```
Setup: Already initialized with fresh index
Last commit: 2026-03-27 04:50:17
DB modified: 2026-03-28 09:55

Logic (server.rs:244-250):
  if last_commit_time <= db_modified + threshold_seconds {
    // Skip - index is fresh
  }

Result: DB timestamp unchanged after server start (correctly skipped re-indexing)
```

---

## Related Documentation

| Document | Description |
|----------|-------------|
| `auto-init-auto-trigger-deep-dive-2026-03-28.md` | Full verification report |
| `implementation-status-2026-03-24.md` | Implementation status by FR |
| `prd-leankg.md` | Product requirements |
| `hld-leankg.md` | High-level design |

---

## Changelog

| Date | Bug ID | Change |
|------|--------|--------|
| 2026-03-28 | BUG-001 | Reported |
| 2026-03-28 | BUG-002 | Reported |
| 2026-03-28 | BUG-003 | Reported |
| 2026-03-28 | BUG-001 | FIXED - Count unique file paths instead of element_type filter |
| 2026-03-28 | BUG-002 | FIXED - Include struct in class count |
| 2026-03-28 | BUG-003 | FIXED - Removed unused index_on_first_call dead code |
| 2026-03-28 | ENH-001 | FIXED - Removed noisy debug eprintln logs from resolve_call_edges |
| 2026-03-28 | ENH-002 | FIXED - Added file nodes to graph visualization (file:: prefix) |
| 2026-03-28 | ENH-002 | FIXED - Graph duplicate edge error (added HashSet deduplication) |
