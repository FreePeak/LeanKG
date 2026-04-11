# LeanKG Auto-Init & Auto-Trigger Deep Dive Analysis

**Date:** 2026-03-28
**Author:** Researcher
**Scope:** Auto-init, auto-trigger, grep-fallback patterns across AI coding tools

---

## 1. Executive Summary

This document provides deep dive analysis and design specification for:
1. **Auto-init**: LeanKG automatically initializes and indexes on first use
2. **Auto-trigger**: LeanKG auto-indexes when the MCP server starts
3. **Grep Replacement**: LeanKG as mandatory first resort with grep fallback

**Target Tools:** Cursor, OpenCode, Claude Code, Gemini CLI (Antigravity), Kilo Code

---

## 2. Current Auto-Init Mechanism

### 2.1 Implementation Location

File: `src/mcp/server.rs` (lines 105-298)

### 2.2 Flow Diagram

```
MCP Server Start
       |
       v
auto_init_if_needed()
       |
       +---> .leankg exists? --YES--> auto_index_if_needed()
       |                                      |
       NO                                     v
       |                              Check config:
       v                              auto_index_on_start
leankg.yaml exists?                          |
       |                              NO --> SKIP
       NO                                     |
       v                                      YES
Check filesystem                             |
writable?                                    v
       |                              Compare git commit time
       NO --> ERR                    vs db modified time
       |                              |
       v                              THRESHOLD OK? --> SKIP
Create .leankg/                                  |
Create leankg.yaml                               NO
Initialize DB                                    v
Index all files                            Run incremental index
Index docs/ if exists                     Or full index fallback
```

### 2.3 Key Parameters

| Parameter | Location | Default | Description |
|-----------|----------|---------|-------------|
| `auto_index_on_start` | `leankg.yaml` | `true` | Enable auto-indexing on server start |
| `auto_index_threshold_minutes` | `leankg.yaml` | `5` | Skip index if commits newer than (db_modified + threshold) |

### 2.4 Configuration Schema

```yaml
# leankg.yaml
mcp:
  auto_index_on_start: true
  auto_index_threshold_minutes: 5
  # If index stale (new commits since last index), auto-reindex
```

---

## 3. Auto-Trigger Specification

### 3.1 Trigger Points

| Trigger | Mechanism | Behavior |
|---------|-----------|----------|
| MCP server start | `auto_init_if_needed()` | Full init if no `.leankg`, incremental index if stale |
| File change (watch mode) | `src/mcp/watcher.rs` | Incremental re-index on file save |
| `mcp_init` tool call | Tool handler | Reinitialize with new path |
| `mcp_index` tool call | Tool handler | Force full re-index |

### 3.2 Watch Mode

When `--watch` flag is passed to `leankg mcp-stdio --watch`:
- File watcher monitors source directories
- On file save: incremental index for that file
- Debounced to prevent rapid re-indexing

---

## 4. Grep Replacement Design

### 4.1 Principle: LeanKG First, Grep Fallback

**MANDATORY WORKFLOW (Agent Must Follow):**

```
1. User asks "Where is X?" or searches for code
          |
          v
2. Agent checks: mcp_status
          |
          +-- NOT READY --> mcp_init with project .leankg path
          |
          v (READY)
3. Agent uses LeanKG tools FIRST:
   - search_code("X") for symbol search
   - find_function("X") for function definitions
   - query_file("X") for file search
          |
          v
4. LeanKG returns results:
          |
          +-- EMPTY --> Fall back to grep/ripgrep
          |
          v (HAS RESULTS)
5. Return LeanKG results (token-optimized)
```

### 4.2 LeanKG Tool → Grep Fallback Mapping

| Task | LeanKG Tool | Grep Fallback |
|------|-------------|---------------|
| Find function definition | `find_function` | `grep -rn "fn X" --include="*.rs"` |
| Search symbol by name | `search_code` | `grep -rn "X" --include="*.py"` |
| Find file by pattern | `query_file` | `find . -name "*X*"` |
| Find tests for function | `get_tested_by` | `grep -rn "X" tests/` |
| Get impact radius | `get_impact_radius` | N/A (no grep equivalent) |

### 4.3 Implementation in Agent Instructions

**Each AI tool's bootstrap must include:**

```markdown
## Workflow: LeanKG First, Grep Fallback

### MANDATORY: Use LeanKG First
Before ANY codebase search/navigation, you MUST:

1. Check if LeanKG is available: `mcp_status`
2. If not initialized, run: `mcp_init({ path: "/path/to/project/.leankg" })`
3. Use LeanKG tools: `search_code`, `find_function`, `query_file`, etc.
4. ONLY if LeanKG returns empty results, fall back to grep/ripgrep

### Fallback Pattern
If LeanKG tools return empty:
- `grep -rn "<query>" --include="*.rs"` (Rust)
- `grep -rn "<query>" --include="*.go"` (Go)
- `grep -rn "<query>" --include="*.py"` (Python)
- `grep -rn "<query>" --include="*.ts"` (TypeScript)
```

---

## 5. AI Coding Tool Integration Matrix

### 5.1 Cursor

| Aspect | Details |
|--------|---------|
| Extension System | MCP via `~/.cursor/mcp.json` |
| Plugin Bootstrap | `leankg-bootstrap.md` in `.cursor-plugin/` |
| Auto-init on Start | YES - MCP server auto-init when tools called |
| Installation | `/add-plugin leankg` or marketplace |
| Grep Fallback | Must be in bootstrap instructions |

**Bootstrap File:** `.cursor-plugin/leankg-bootstrap.md`

### 5.2 OpenCode

| Aspect | Details |
|--------|---------|
| Extension System | Plugin in `opencode.json` |
| Plugin Bootstrap | `leankg-bootstrap.md` in `.opencode/` |
| Auto-init on Start | YES - via `plugins` array in config |
| Installation | Add to `plugin` array in `opencode.json` |
| Grep Fallback | Must be in bootstrap instructions |

**Bootstrap File:** `.opencode/INSTALL.md`

### 5.3 Claude Code

| Aspect | Details |
|--------|---------|
| Extension System | `~/.claude/mcp.json` |
| Plugin Bootstrap | `leankg-bootstrap.md` in `.claude-plugin/` |
| Auto-init on Start | YES - MCP server auto-init |
| Installation | Manual MCP config or extension |
| Grep Fallback | Must be in bootstrap instructions |

**Bootstrap File:** `.claude-plugin/leankg-bootstrap.md`

### 5.4 Gemini CLI (Antigravity)

| Aspect | Details |
|--------|---------|
| Extension System | `~/.gemini/antigravity/mcp_config.json` |
| Plugin Bootstrap | `.google-antigravity/INSTALL.md` |
| Auto-init on Start | YES - MCP server auto-init |
| Installation | `gemini extensions install` |
| Grep Fallback | Must be in bootstrap instructions |

**Bootstrap File:** `.google-antigravity/INSTALL.md`

### 5.5 Kilo Code

| Aspect | Details |
|--------|---------|
| Extension System | `~/.config/kilo/kilo.json` |
| Plugin Bootstrap | `.kilo/INSTALL.md` |
| Auto-init on Start | YES - MCP server auto-init |
| Installation | MCP config or extension |
| Grep Fallback | Must be in bootstrap instructions |

**Bootstrap File:** `.kilo/INSTALL.md`

---

## 6. Common Bootstrap Template

### 6.1 Standardized LeanKG Bootstrap

All AI tools share the same core bootstrap content:

```markdown
# LeanKG - Lightweight Knowledge Graph

LeanKG is a lightweight knowledge graph for codebase understanding. 
It indexes code, builds dependency graphs, calculates impact radius, 
and exposes everything via MCP for AI tool integration.

## MCP Tools

| Tool | Purpose |
|------|---------|
| `mcp_status` | Check if LeanKG is initialized and ready |
| `mcp_init` | Initialize LeanKG for a project |
| `mcp_index` | Index codebase |
| `search_code` | Search code elements by name/type |
| `find_function` | Locate function definitions |
| `query_file` | Find files by name/pattern |
| `get_impact_radius` | Calculate blast radius of changes (N hops) |
| `get_dependencies` | Get direct imports of a file |
| `get_dependents` | Get files depending on target |
| `get_context` | Get AI-optimized context for a file |
| `get_call_graph` | Get function call chains |
| `find_large_functions` | Find oversized functions |
| `get_tested_by` | Get test coverage for a function/file |
| `get_doc_for_file` | Get documentation for a file |
| `get_traceability` | Get full traceability chain |
| `get_code_tree` | Get codebase structure |
| `get_doc_tree` | Get documentation tree |

## Workflow: LeanKG First, Grep Fallback

**MANDATORY: Use LeanKG First**

Before ANY codebase search/navigation, you MUST:

1. Check if LeanKG is available via `mcp_status`
2. If LeanKG is not initialized, run `mcp_init` first
3. Use the appropriate LeanKG tool for the task
4. **ONLY after LeanKG is exhausted (returns empty) may you fall back to grep/ripgrep**

| Instead of | Use LeanKG |
|------------|------------|
| grep/ripgrep for "where is X?" | `search_code` or `find_function` |
| glob + content search for tests | `get_tested_by` |
| Manual dependency tracing | `get_impact_radius` or `get_dependencies` |
| Reading entire files | `get_context` (token-optimized) |

## Quick Commands

```bash
# Index a codebase
cargo run -- init
cargo run -- index ./src

# Calculate impact radius
cargo run -- impact src/main.rs 3

# Start MCP server
cargo run -- serve
```
```

### 6.2 Tool-Specific Variations

| Tool | File | Variation |
|------|------|-----------|
| Cursor | `.cursor-plugin/leankg-bootstrap.md` | Uses Cursor plugin system |
| OpenCode | `.opencode/INSTALL.md` | Uses OpenCode plugin array |
| Claude Code | `.claude-plugin/leankg-bootstrap.md` | Uses Claude MCP config |
| Gemini CLI | `.google-antigravity/INSTALL.md` | Uses gemini extensions |
| Kilo Code | `.kilo/INSTALL.md` | Uses kilo.json config |

---

## 7. Auto-Init Behavior Details

### 7.1 First-Time Initialization Flow

```
1. MCP server starts
2. auto_init_if_needed() called
3. Check: .leankg or leankg.yaml exists?
   - YES: Skip to auto_index_if_needed()
   - NO: Continue
4. Check: Filesystem writable?
   - NO: Return error, server operates in uninitialized state
   - YES: Continue
5. Create .leankg/ directory
6. Create leankg.yaml with defaults
7. Initialize database
8. Index all source files (find_files_sync)
9. Resolve call edges
10. Index docs/ if exists
11. Server ready
```

### 7.2 Subsequent Starts (Incremental Index)

```
1. MCP server starts
2. auto_init_if_needed() called
3. .leankg exists: auto_index_if_needed()
4. Check: auto_index_on_start in config?
   - NO: Return (skip index)
   - YES: Continue
5. Check: leankg.db exists?
   - NO: Return (uninitialized state)
   - YES: Continue
6. Check: Git repo?
   - NO: Return (no auto-index for non-git)
   - YES: Continue
7. Get last commit time vs db modified time
8. If (last_commit <= db_modified + threshold): SKIP
9. Otherwise: Run incremental_index_sync()
10. If incremental fails: Fall back to full index
```

---

## 8. Edge Cases & Error Handling

### 8.1 Uninitialized State

When LeanKG is not initialized:
- Tools return error: "LeanKG not initialized..."
- Agent should call `mcp_init` or `mcp_index`
- No automatic self-initialization without user confirmation

### 8.2 Empty Results

When LeanKG returns empty:
- This is NOT an error
- Agent MUST fall back to grep/ripgrep
- This is the expected fallback pattern

### 8.3 Stale Index

When index is stale but auto-index disabled:
- Tools work with existing (potentially stale) data
- User can manually call `mcp_index`

### 8.4 Non-Git Repo

Auto-index skipped in non-git repos:
- Full index only via explicit `mcp_index` call

---

## 9. Implementation Checklist

### 9.1 Core Auto-Init (Already Implemented)
- [x] `auto_init_if_needed()` in `src/mcp/server.rs`
- [x] `auto_index_if_needed()` in `src/mcp/server.rs`
- [x] Configuration via `leankg.yaml`
- [x] Watch mode with `--watch` flag

### 9.2 Documentation Updates Required
- [ ] Update all bootstrap docs with grep fallback pattern
- [ ] Ensure AGENTS.md emphasizes LeanKG-first workflow
- [ ] Add grep fallback instructions to each tool's bootstrap

### 9.3 Verification
- [ ] Test auto-init in Cursor
- [ ] Test auto-init in OpenCode
- [ ] Test auto-init in Claude Code
- [ ] Test auto-init in Gemini CLI
- [ ] Test auto-init in Kilo Code
- [ ] Verify grep fallback works when LeanKG returns empty

---

## 10. Recommendations

### 10.1 For LeanKG Core
1. **Enhance auto-init feedback**: Show clearer progress during indexing
2. **Add status tool**: `mcp_status` should return indexed element count, last index time
3. **Smart fallback detection**: If all LeanKG searches return empty, suggest re-indexing

### 10.2 For AI Tool Integrations
1. **Standardize bootstrap content**: All tools share the same core bootstrap
2. **Add tool-specific instructions**: Installation steps per tool
3. **Document grep fallback clearly**: Every bootstrap must include fallback pattern

### 10.3 For User Experience
1. **First-use guidance**: When LeanKG not initialized, show init instructions
2. **Index progress**: Show "Indexing 1234/5000 files..." during auto-init
3. **Empty result guidance**: When results empty, show grep fallback command

---

## 11. References

- Auto-init implementation: `src/mcp/server.rs:125-298`
- Configuration schema: `src/config/project.rs:30-31`
- CLI watch mode: `src/cli/mod.rs:45`
- MCP tools: `src/mcp/tools.rs`
- Tool handler: `src/mcp/handler.rs`

---

*Document Version: 1.0*
*Last Updated: 2026-03-28*