# Auto-Index on DB Write - Design Spec

**Date:** 2026-04-08
**Status:** APPROVED
**Feature:** Auto-detect and reindex when external agents write directly to CozoDB

---

## 1. Problem Statement

Current auto-index is git-commit-driven (`last_commit_time` vs `db_modified`). When external agents write directly to CozoDB (via SQL insert/update/delete), LeanKG doesn't detect the change and serve stale data.

**Scenario:**
1. External agent connects to LeanKG MCP
2. Agent executes raw SQL via some mechanism to insert business logic annotations
3. Next MCP tool call returns stale data (missing the annotations)

---

## 2. Core Concept: Write Tracker + Lazy Reindex

```
┌─────────────────┐         ┌──────────────────┐         ┌─────────────────┐
│  External Agent │──write──▶   CozoDB         │         │  MCP Tool Call  │
└─────────────────┘         └──────────────────┘         └────────┬────────┘
                                     │                            │
                    ┌────────────────┴────────────────┐            │
                    ▼                                     ▼            │
            ┌───────────────┐                    ┌─────────────┐     │
            │ Write Tracker │                    │  Lazy Check │◀────┘
            │ (dirty flag)  │                    │  "dirty?"   │
            └───────────────┘                    └─────────────┘
```

**Key insight:** We don't reindex immediately on write (blocking). We mark as dirty and reindex lazily on next MCP tool call.

---

## 3. Architecture

### 3.1 WriteTracker

```rust
pub struct WriteTracker {
    dirty: Arc<AtomicBool>,
    last_write: Arc<RwLock<Instant>>,
}

impl WriteTracker {
    pub fn new() -> Self;
    pub fn mark_dirty(&self);
    pub fn is_dirty(&self) -> bool;
    pub fn clear_dirty(&self);
    pub fn last_write_time(&self) -> Instant;
}
```

### 3.2 TrackingDb Wrapper

Wraps `CozoDb` to intercept write operations:

```rust
pub struct TrackingDb {
    inner: CozoDb,
    tracker: Arc<WriteTracker>,
}

impl TrackingDb {
    pub fn new(inner: CozoDb, tracker: Arc<WriteTracker>) -> Self;
    
    pub fn run_script(&self, script: &str, params: BTreeMap<String, JsonValue>) 
        -> Result<QueryResult, Error> {
        // If script contains :put or :delete, mark tracker dirty
        // Then execute the actual script
    }
}
```

**Detection logic:**
- Check if script contains `:put` or `:delete` (case-insensitive)
- If write operation detected → `tracker.mark_dirty()`
- Execute the actual CozoDB operation

### 3.3 MCPServer Integration

In `MCPServer::execute_tool()`:

```rust
async fn execute_tool(&self, tool_name: &str, arguments: serde_json::Map<String, serde_json::Value>) 
    -> Result<serde_json::Value, String> {
    
    // Check if index needs refresh (dirty flag set by external write)
    if self.write_tracker.is_dirty() && self.config.mcp.auto_index_on_db_write {
        tracing::info!("External write detected, triggering incremental reindex...");
        self.trigger_reindex().await?;
        self.write_tracker.clear_dirty();
    }
    
    // ... proceed with tool execution
}
```

---

## 4. Configuration

### 4.1 New Config Option

In `McpConfig`:

```rust
pub struct McpConfig {
    pub auto_index_on_start: bool,           // existing
    pub auto_index_threshold_minutes: u64,    // existing
    pub auto_index_on_db_write: bool,         // NEW: default true
}
```

### 4.2 leankg.yaml

```yaml
mcp:
  auto_index_on_start: true
  auto_index_threshold_minutes: 5
  auto_index_on_db_write: true    # NEW
```

---

## 5. Why This Approach (vs Alternatives)

| Approach | Problem |
|----------|---------|
| **File polling on db mtime** | Wastes CPU, ~5s latency from polling interval, race conditions |
| **DB triggers** | CozoSQLite doesn't support triggers reliably |
| **Write-through cache** | Complex, adds overhead to every read |
| **Write Tracker + Lazy Reindex** | Zero-latency at write time, deferred reindex only when needed |

**Key advantages:**
1. **Non-blocking writes:** dirty flag is atomic, no locks during write operations
2. **Lazy reindex:** reindex only when MCP tool actually called (not on every write)
3. **Zero overhead during reads:** dirty flag only affects write operations
4. **Configurable:** can disable via `auto_index_on_db_write: false`

---

## 6. Implementation Order

### Phase 1: Core Infrastructure
1. Create `WriteTracker` struct in `src/mcp/tracker.rs`
2. Create `TrackingDb` wrapper in `src/mcp/tracking_db.rs`
3. Add `auto_index_on_db_write` to `McpConfig`

### Phase 2: Integration
4. Integrate `WriteTracker` into `MCPServer`
5. Add dirty-check in `execute_tool()` before tool execution
6. Add `trigger_reindex()` method

### Phase 3: Testing
7. Write unit tests for `WriteTracker`
8. Write integration test for dirty-flag + reindex flow
9. Test with external agent writing to DB

---

## 7. File Changes

| File | Change |
|------|--------|
| `src/mcp/tracker.rs` | NEW: WriteTracker struct |
| `src/mcp/tracking_db.rs` | NEW: TrackingDb wrapper |
| `src/mcp/mod.rs` | Export new modules |
| `src/mcp/server.rs` | Integrate WriteTracker, dirty-check in execute_tool |
| `src/config/project.rs` | Add `auto_index_on_db_write` to McpConfig |
| `leankg.yaml` | Add `auto_index_on_db_write: true` default |
| `docs/design/hld-leankg.md` | Document Auto-Index on DB Write |

---

## 8. Acceptance Criteria

- [ ] WriteTracker dirty flag is set when external agent writes to CozoDB
- [ ] MCP tool call triggers reindex if dirty flag is set
- [ ] Reindex only happens once until next external write
- [ ] Config option `auto_index_on_db_write` can disable the feature
- [ ] No performance impact on reads (dirty flag is atomic)
- [ ] Existing auto-index-on-start behavior unchanged
