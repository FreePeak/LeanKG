# Auto-Index on DB Write Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add write tracking + lazy reindex to detect when external agents write directly to CozoDB and trigger reindex before next MCP tool call.

**Architecture:** WriteTracker (atomic dirty flag) + TrackingDb wrapper that intercepts `:put`/`:delete` operations. MCPServer checks dirty flag on each tool call and triggers incremental reindex if needed.

**Tech Stack:** Rust, Arc<AtomicBool>, RwLock, CozoDB

---

## Task 1: Add `auto_index_on_db_write` Config

**Files:**
- Modify: `src/config/project.rs:25-32`
- Modify: `src/config/project.rs:56-62`
- Modify: `leankg.yaml`

**Step 1: Add field to McpConfig struct**

In `src/config/project.rs`, add `auto_index_on_db_write: bool` to `McpConfig`:

```rust
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpConfig {
    pub enabled: bool,
    pub port: u16,
    pub auth_token: String,
    pub auto_index_on_start: bool,
    pub auto_index_threshold_minutes: u64,
    pub auto_index_on_db_write: bool,  // NEW
}
```

**Step 2: Add default value in ProjectConfig::default()**

In `src/config/project.rs`, add to the `mcp` default section:

```rust
mcp: McpConfig {
    enabled: true,
    port: 3000,
    auth_token: "".to_string(),
    auto_index_on_start: true,
    auto_index_threshold_minutes: 5,
    auto_index_on_db_write: true,  // NEW
},
```

**Step 3: Update leankg.yaml**

In `leankg.yaml`, add under `mcp:`:

```yaml
auto_index_on_db_write: true
```

**Step 4: Run tests**

Run: `cargo test test_default_config -v`
Expected: PASS (new field has default)

Run: `cargo test test_config_project_settings -v`
Expected: PASS

---

## Task 2: Create WriteTracker Struct

**Files:**
- Create: `src/mcp/tracker.rs`
- Modify: `src/mcp/mod.rs`
- Test: Inline tests in tracker.rs

**Step 1: Create WriteTracker**

Create `src/mcp/tracker.rs`:

```rust
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Instant;

/// Tracks whether CozoDB has been written to by external agents.
#[derive(Debug)]
pub struct WriteTracker {
    dirty: Arc<AtomicBool>,
    last_write: Arc<RwLock<Instant>>,
}

#[derive(Debug)]
struct RwLock<T> {
    inner: std::sync::RwLock<T>,
}

impl<T> RwLock<T> {
    fn new(val: T) -> Self {
        Self { inner: std::sync::RwLock::new(val) }
    }
    fn read(&self) -> std::sync::RwLockReadGuard<'_, T> {
        self.inner.read().unwrap()
    }
    fn write(&self) -> std::sync::RwLockWriteGuard<'_, T> {
        self.inner.write().unwrap()
    }
}

impl WriteTracker {
    pub fn new() -> Self {
        Self {
            dirty: Arc::new(AtomicBool::new(false)),
            last_write: Arc::new(RwLock::new(Instant::now())),
        }
    }

    /// Mark the tracker as dirty (external write detected)
    pub fn mark_dirty(&self) {
        self.dirty.store(true, Ordering::SeqCst);
        *self.last_write.write() = Instant::now();
    }

    /// Check if tracker is dirty
    pub fn is_dirty(&self) -> bool {
        self.dirty.load(Ordering::SeqCst)
    }

    /// Clear the dirty flag
    pub fn clear_dirty(&self) {
        self.dirty.store(false, Ordering::SeqCst);
    }

    /// Get last write time
    pub fn last_write_time(&self) -> Instant {
        *self.last_write.read()
    }
}

impl Default for WriteTracker {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_write_tracker_initial_state() {
        let tracker = WriteTracker::new();
        assert!(!tracker.is_dirty());
    }

    #[test]
    fn test_mark_dirty() {
        let tracker = WriteTracker::new();
        tracker.mark_dirty();
        assert!(tracker.is_dirty());
    }

    #[test]
    fn test_clear_dirty() {
        let tracker = WriteTracker::new();
        tracker.mark_dirty();
        tracker.clear_dirty();
        assert!(!tracker.is_dirty());
    }

    #[test]
    fn test_last_write_time() {
        let tracker = WriteTracker::new();
        let before = Instant::now();
        tracker.mark_dirty();
        let after = Instant::now();
        let last_write = tracker.last_write_time();
        assert!(last_write >= before && last_write <= after);
    }
}
```

**Step 2: Export from mod.rs**

Modify `src/mcp/mod.rs`:

```rust
pub mod auth;
pub mod handler;
pub mod server;
pub mod tools;
pub mod tracker;  // NEW
pub mod watcher;
```

**Step 3: Run tests**

Run: `cargo test tracker -v`
Expected: 4 PASS (test_write_tracker_initial_state, test_mark_dirty, test_clear_dirty, test_last_write_time)

---

## Task 3: Create TrackingDb Wrapper

**Files:**
- Create: `src/mcp/tracking_db.rs`
- Test: Inline tests in tracking_db.rs

**Step 1: Create TrackingDb**

Create `src/mcp/tracking_db.rs`:

```rust
use crate::db::schema::CozoDb;
use crate::mcp::tracker::WriteTracker;
use std::collections::BTreeMap;
use std::sync::Arc;

/// Wraps CozoDb to intercept write operations and track them.
pub struct TrackingDb {
    inner: CozoDb,
    tracker: Arc<WriteTracker>,
}

impl TrackingDb {
    pub fn new(inner: CozoDb, tracker: Arc<WriteTracker>) -> Self {
        Self { inner, tracker }
    }

    pub fn run_script(
        &self,
        script: &str,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<cozo::QueryResult, Box<dyn std::error::Error + Send + Sync>> {
        // Check if this is a write operation
        if is_write_operation(script) {
            self.tracker.mark_dirty();
        }
        self.inner.run_script(script, params)
    }

    pub fn into_inner(self) -> CozoDb {
        self.inner
    }
}

fn is_write_operation(script: &str) -> bool {
    let script_lower = script.to_lowercase();
    script_lower.contains(":put") || script_lower.contains(":delete")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_is_write_operation_put() {
        assert!(is_write_operation("?[id, name] <- [[$id, $name]] :put code_elements"));
        assert!(is_write_operation(":put code_elements { id, name }"));
    }

    #[test]
    fn test_is_write_operation_delete() {
        assert!(is_write_operation(":delete code_elements where id = $id"));
        assert!(is_write_operation("?[id] := *code_elements[id] :delete"));
    }

    #[test]
    fn test_is_write_operation_query() {
        assert!(!is_write_operation("?[id, name] := *code_elements[id, name]"));
        assert!(!is_write_operation(":schema code_elements"));
    }
}
```

**Step 2: Run tests**

Run: `cargo test tracking_db -v`
Expected: 5 PASS

---

## Task 4: Integrate WriteTracker into MCPServer

**Files:**
- Modify: `src/mcp/server.rs:17-50`
- Modify: `src/mcp/server.rs:404-441`
- Modify: `src/config/project.rs` (add getter for auto_index_on_db_write)

**Step 1: Add WriteTracker to MCPServer struct**

In `src/mcp/server.rs`, modify `MCPServer`:

```rust
use crate::mcp::tracker::WriteTracker;

pub struct MCPServer {
    auth_config: Arc<TokioRwLock<AuthConfig>>,
    db_path: Arc<RwLock<PathBuf>>,
    graph_engine: Arc<parking_lot::Mutex<Option<GraphEngine>>>,
    watch_path: Option<PathBuf>,
    write_tracker: Arc<WriteTracker>,  // NEW
}
```

**Step 2: Update MCPServer::new()**

```rust
pub fn new(db_path: std::path::PathBuf) -> Self {
    Self {
        auth_config: Arc::new(TokioRwLock::new(AuthConfig::default())),
        db_path: Arc::new(RwLock::new(db_path)),
        graph_engine: Arc::new(parking_lot::Mutex::new(None)),
        watch_path: None,
        write_tracker: Arc::new(WriteTracker::new()),  // NEW
    }
}
```

**Step 3: Update MCPServer::new_with_watch()**

```rust
pub fn new_with_watch(db_path: std::path::PathBuf, watch_path: std::path::PathBuf) -> Self {
    Self {
        auth_config: Arc::new(TokioRwLock::new(AuthConfig::default())),
        db_path: Arc::new(RwLock::new(db_path)),
        graph_engine: Arc::new(parking_lot::Mutex::new(None)),
        watch_path: Some(watch_path),
        write_tracker: Arc::new(WriteTracker::new()),  // NEW
    }
}
```

**Step 4: Update Clone impl**

```rust
impl Clone for MCPServer {
    fn clone(&self) -> Self {
        Self {
            auth_config: self.auth_config.clone(),
            db_path: self.db_path.clone(),
            graph_engine: self.graph_engine.clone(),
            watch_path: self.watch_path.clone(),
            write_tracker: self.write_tracker.clone(),  // NEW
        }
    }
}
```

**Step 5: Add dirty-check in execute_tool()**

In `src/mcp/server.rs`, modify `execute_tool()`:

```rust
async fn execute_tool(
    &self,
    tool_name: &str,
    arguments: serde_json::Map<String, serde_json::Value>,
) -> Result<serde_json::Value, String> {
    let project_root = self.find_project_root()?;
    
    // NEW: Check if we need to reindex due to external write
    if self.write_tracker.is_dirty() {
        let config = self.load_config(&project_root)?;
        if config.mcp.auto_index_on_db_write {
            tracing::info!("External write detected, triggering incremental reindex...");
            self.trigger_reindex().await?;
            self.write_tracker.clear_dirty();
        }
    }
    
    // ... rest of existing code
}
```

**Step 6: Add trigger_reindex() helper method**

Add after `auto_index_if_needed()`:

```rust
async fn trigger_reindex(&self) -> Result<(), String> {
    let project_root = self.find_project_root()?;
    let db = init_db(&self.get_db_path()).map_err(|e| format!("Database error: {}", e))?;
    let graph_engine = crate::graph::GraphEngine::new(db);
    let mut parser_manager = crate::indexer::ParserManager::new();
    parser_manager
        .init_parsers()
        .map_err(|e| format!("Parser init error: {}", e))?;
    
    let root_str = project_root.to_string_lossy().to_string();
    match crate::indexer::incremental_index_sync(&graph_engine, &mut parser_manager, &root_str).await {
        Ok(result) => {
            tracing::info!("Reindex triggered by external write: {} files processed", result.total_files_processed);
        }
        Err(e) => {
            tracing::warn!("Reindex failed: {}", e);
        }
    }
    
    {
        let mut guard = self.graph_engine.lock();
        *guard = None;
    }
    Ok(())
}
```

**Step 7: Add load_config helper**

```rust
fn load_config(&self, project_root: &std::path::Path) -> Result<crate::config::ProjectConfig, String> {
    let config_path = project_root.join(".leankg/leankg.yaml");
    if config_path.exists() {
        let content = std::fs::read_to_string(&config_path)
            .map_err(|e| format!("Failed to read config: {}", e))?;
        serde_yaml::from_str::<crate::config::ProjectConfig>(&content)
            .map_err(|e| format!("Failed to parse config: {}", e))
    } else {
        Ok(crate::config::ProjectConfig::default())
    }
}
```

**Step 8: Run tests**

Run: `cargo build`
Expected: PASS

Run: `cargo test mcp_server -v`
Expected: PASS

---

## Task 5: Commit and Final Verification

**Step 1: Run full test suite**

Run: `cargo test`
Expected: All tests PASS

**Step 2: Run clippy**

Run: `cargo clippy -- -D warnings`
Expected: No warnings

**Step 3: Commit**

```bash
git add -A && git commit -m "feat: add auto-index-on-db-write with WriteTracker"
```

---

## Dependencies
- Task 1 must complete before Task 4 (config needed)
- Task 2 must complete before Task 3 (WriteTracker needed)
- Task 3 must complete before Task 4 (TrackingDb needed)

## Files Changed Summary
| File | Action |
|------|--------|
| `src/config/project.rs` | Add `auto_index_on_db_write` field |
| `src/mcp/tracker.rs` | Create WriteTracker struct |
| `src/mcp/tracking_db.rs` | Create TrackingDb wrapper |
| `src/mcp/mod.rs` | Export new modules |
| `src/mcp/server.rs` | Integrate WriteTracker, dirty-check |
| `leankg.yaml` | Add config option |
