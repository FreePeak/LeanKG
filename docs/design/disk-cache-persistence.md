# Disk-Persistent Cache Design for LeanKG

## Overview

This document describes the design for persisting LeanKG's query caches to disk using CozoDB, enabling cache survival across process restarts.

**Architecture**: Write-through L1/L2 cache
- **L1**: In-memory `TimedCache` (existing)
- **L2**: CozoDB-backed persistent storage (new)

## 1. Data Model

### CozoDB Table: `query_cache`

```cozo
:create query_cache {
    cache_key: String,
    value_json: String,
    created_at: Int,          -- Unix timestamp (seconds)
    ttl_seconds: Int,         -- TTL in seconds (0 = never expires)
    tool_name: String,        -- 'dependencies' | 'dependents' | 'orchestrate'
    project_path: String,     -- For multi-project isolation
    metadata: String          -- JSON for future extensibility
}
```

### Indexes

```cozo
:create query_cache::tool_name_index {ref: (tool_name), compressed: true}
:create query_cache::project_path_index {ref: (project_path), compressed: true}
:create query_cache::cache_key_index {ref: (cache_key), compressed: true, unique: true}
```

## 2. Cache Key Strategy

Cache keys encode the query context to ensure uniqueness:

| Cache Type | Key Format |
|------------|------------|
| Dependencies | `deps:{project_path}:{file_path}` |
| Dependents | `depn:{project_path}:{file_path}` |
| Orchestrate | `orch:{project_path}:{query_type}:{file}:{mode}` |

Key generation is deterministic, allowing consistent invalidation via prefix matching.

## 3. TTL Strategy

- **Stored**: `ttl_seconds` column in CozoDB (integer, seconds since epoch)
- **Expiration check**: On every `get()`, compare `created_at + ttl_seconds` vs current time
- **Default TTL**: 300 seconds (5 minutes), configurable
- **Never-expire**: `ttl_seconds = 0` indicates no expiration
- **Background cleanup**: Optional periodic sweep to delete expired entries

## 4. Integration Points

### GraphEngine (src/graph/query.rs)

```rust
// Modified constructor
impl GraphEngine {
    pub fn new(db: CozoDb) -> Self {
        let persistent_cache = PersistentCache::new(db.clone());
        let cache = QueryCache::with_persistence(300, 1000, persistent_cache);
        Self { db, cache: Arc::new(RwLock::new(cache)) }
    }
}
```

### QueryOrchestrator (src/orchestrator/mod.rs)

```rust
// Modified constructor
impl QueryOrchestrator {
    pub fn new(graph_engine: GraphEngine) -> Self {
        let persistent_cache = PersistentCache::new(graph_engine.db().clone());
        let cache = OrchestratorCache::with_persistence(300, 1000, persistent_cache);
        Self { graph_engine, cache: Arc::new(Mutex::new(cache)), intent_parser }
    }
}
```

### Cache Invalidation (src/watcher/mod.rs)

When file changes are detected via the watcher:
1. Invalidate in-memory cache for affected file
2. Invalidate CozoDB entries where `cache_key LIKE '{type}:{project}:{file}%'`

## 5. Schema Migration

The cache table is created if not exists (upsert pattern):

```rust
fn ensure_cache_table(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let check = r#"::relations"#;
    let existing: HashSet<String> = db.run_script(check, Default::default())?
        .rows.iter()
        .filter_map(|row| row.get(0).and_then(|v| v.as_str().map(String::from)))
        .collect();
    
    if !existing.contains("query_cache") {
        let create = r#":create query_cache {...}"#;
        db.run_script(create, Default::default())?;
    }
    Ok(())
}
```

## 6. Migration Path

### Phase 1: Non-Breaking Addition
1. Add `query_cache` table to schema.rs
2. Create `src/graph/persistent_cache.rs` module
3. Implement `PersistentCache<K,V>` wrapper

### Phase 2: Integration
4. Add `with_persistence()` constructors to `QueryCache` and `OrchestratorCache`
5. Modify `GraphEngine::new()` and `QueryOrchestrator::new()` to use persistent versions

### Phase 3: Invalidation Hooks
6. Connect watcher to cache invalidation
7. Add background cleanup task for expired entries

## 7. Error Handling

- **CozoDB unavailable**: Log warning, fall back to in-memory only
- **Serialization error**: Log error, skip cache entry
- **TTL expired**: Treat as cache miss, return None

## 8. Integration Points

### 8.1 GraphEngine Cache Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                      GraphEngine::get_dependencies()            │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
                    ┌───────────────────────┐
                    │   L1 Memory Cache     │
                    │  (TimedCache<String>) │
                    └───────────────────────┘
                                │
                     Cache Miss?│
                                ▼
                    ┌───────────────────────┐
                    │   L2 Persistent      │
                    │   (CozoDB query_cache) │
                    └───────────────────────┘
                                │
                     Cache Miss?│
                                ▼
                    ┌───────────────────────┐
                    │   Query CozoDB        │
                    │   (relationships)      │
                    └───────────────────────┘
```

**Write-through on result:**
1. Store in L1 memory cache
2. Async write to L2 CozoDB (non-blocking)

### 8.2 QueryOrchestrator Cache Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                   QueryOrchestrator::orchestrate()             │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
                    ┌───────────────────────┐
                    │   L1 Memory Cache    │
                    │  (OrchestratorCache)  │
                    └───────────────────────┘
                                │
                     Cache Miss?│
                                ▼
                    ┌───────────────────────┐
                    │   L2 Persistent      │
                    │   (CozoDB query_cache) │
                    └───────────────────────┘
                                │
                     Cache Miss?│
                                ▼
                    ┌───────────────────────┐
                    │   Execute Intent      │
                    │   (context/impact/etc) │
                    └───────────────────────┘
```

### 8.3 Cache Invalidation on File Changes

The watcher module (`src/watcher/`) detects file modifications and triggers re-indexing. This same hook invalidates affected cache entries:

```rust
// In watcher.rs - when a file changes:
async fn on_file_change(&self, path: &Path) {
    let file_path = path.to_string_lossy();
    
    // Invalidate GraphEngine cache for this file
    self.graph_engine.invalidate_file(&file_path);
    
    // Invalidate Orchestrator cache entries for this file
    self.orchestrator.invalidate_prefix(&file_path);
}
```

**Invalidation Strategy by Cache Type:**

| Cache | Invalidation Pattern |
|-------|---------------------|
| Dependencies | `deps:{project}:{file_path}%` |
| Dependents | `depn:{project}:{file_path}%` |
| Orchestrate | `orch:{project}:*:{file_path}:%` |

### 8.4 Module Exports

Add to `src/graph/mod.rs`:
```rust
pub mod persistent_cache;
pub use persistent_cache::PersistentCache;
```

## 9. Implementation Steps

### Step 1: Schema Addition (src/db/schema.rs)

Add `query_cache` table creation to `init_schema()`:

```rust
// Add after context_metrics table creation (~line 88)
if !existing_relations.contains("query_cache") {
    let create_query_cache = r#":create query_cache {
        cache_key: String,
        value_json: String,
        created_at: Int,
        ttl_seconds: Int,
        tool_name: String,
        project_path: String,
        metadata: String
    }"#;
    db.run_script(create_query_cache, Default::default())?;
    
    // Create indexes
    let idx = r#":create query_cache::cache_key_index {ref: (cache_key), compressed: true, unique: true}"#;
    db.run_script(idx, Default::default()).ok();
    
    let tool_idx = r#":create query_cache::tool_name_index {ref: (tool_name), compressed: true}"#;
    db.run_script(tool_idx, Default::default()).ok();
}
```

### Step 2: Create PersistentCache Module (src/graph/persistent_cache.rs)

New file with:
- `PersistentCache` struct wrapping CozoDB
- In-memory HashMap for L1
- `get()`, `insert()`, `invalidate()`, `invalidate_prefix()`
- TTL checking on reads
- Lazy loading from CozoDB

### Step 3: Extend QueryCache (src/graph/cache.rs)

Add:
- `persistent: Option<Arc<PersistentCache>>` field
- `with_persistence()` constructor
- Modified `get_dependencies()` to check L2 on L1 miss
- Modified `set_dependencies()` to write both L1 and L2
- Modified `invalidate_file()` to invalidate L2 as well

### Step 4: Extend OrchestratorCache (src/orchestrator/cache.rs)

Add:
- `persistent: Option<Arc<PersistentCache>>` field  
- `with_persistence()` constructor
- Modified `get()` to check L2 on L1 miss
- Modified `insert()` to write both L1 and L2
- Modified `invalidate_prefix()` to invalidate L2 as well

### Step 5: Update GraphEngine (src/graph/query.rs)

Modify `GraphEngine::new()`:
```rust
pub fn new(db: CozoDb) -> Self {
    let persistent_cache = Arc::new(PersistentCache::new(db.clone(), 300));
    let cache = QueryCache::with_persistence(300, 1000, persistent_cache);
    Self { db, cache: Arc::new(RwLock::new(cache)) }
}
```

### Step 6: Update QueryOrchestrator (src/orchestrator/mod.rs)

Modify `QueryOrchestrator::new()`:
```rust
pub fn new(graph_engine: GraphEngine) -> Self {
    let persistent_cache = Arc::new(PersistentCache::new(graph_engine.db().clone(), 300));
    let cache = OrchestratorCache::with_persistence(300, 1000, persistent_cache);
    Self { graph_engine, cache: Arc::new(Mutex::new(cache)), intent_parser }
}
```

### Step 7: Connect Watcher to Cache (src/watcher/mod.rs)

Add cache invalidation callback to watcher:
```rust
// When watcher detects file change:
graph_engine.cache.read().await.invalidate_file(&changed_path);
orchestrator_cache.lock().invalidate_prefix(&changed_path);
```

### Step 8: Add Background Cleanup (Optional)

Add periodic task to delete expired entries from CozoDB:
```rust
fn cleanup_expired(&self) -> Result<usize> {
    let query = r#":delete query_cache where (now() - created_at) > ttl_seconds"#;
    self.db.run_script(query, Default::default())?;
    Ok(0) // Return count
}
```

## 10. Test Plan

### 10.1 Unit Tests

**persistent_cache.rs tests:**
- `test_get_missing_returns_none` - cache miss on empty DB
- `test_insert_and_get` - basic put/get cycle
- `test_ttl_expiration` - entry expires after TTL
- `test_invalidate` - entry removed after invalidate
- `test_invalidate_prefix` - multiple entries removed
- `test_l1_l2_consistency` - memory and DB stay in sync

**cache.rs integration:**
- `test_query_cache_with_persistence` - QueryCache with L2
- `test_orchestrator_cache_with_persistence` - OrchestratorCache with L2

### 10.2 Integration Tests

**tests/persistent_cache_integration.rs:**
- `test_cache_survives_restart` - create cache, restart, verify entries exist
- `test_ttl_persists_across_restart` - expired entry not returned after restart
- `test_invalidation_persists` - invalidated entry stays invalid

### 10.3 Manual Verification

```bash
# 1. Start server, query some data (cache populates)
cargo run -- serve &
# Use leankg tools to populate cache

# 2. Kill server abruptly (no graceful shutdown)
pkill -9 leankg

# 3. Restart server, verify cache hits
cargo run -- serve &
# Query same data - should get cache hits
```

## 11. Order of Implementation

| Order | Step | Risk | Notes |
|-------|------|------|-------|
| 1 | Schema addition | Low | Additive, no breaking changes |
| 2 | PersistentCache module | Low | New file, isolated |
| 3 | QueryCache extension | Medium | Modified existing code |
| 4 | OrchestratorCache extension | Medium | Modified existing code |
| 5 | GraphEngine integration | Medium | Core component |
| 6 | QueryOrchestrator integration | Medium | Core component |
| 7 | Watcher invalidation | Low | Optional enhancement |
| 8 | Tests | Low | Can add anytime |

**Recommended**: Implement steps 1-6 first, verify cache persistence works, then add watcher invalidation and tests.

## 12. Rollback Plan

If issues arise:
1. Keep existing `QueryCache::new()` and `OrchestratorCache::new()` constructors
2. `GraphEngine::new()` can fall back to non-persistent cache if `query_cache` table creation fails
3. Feature flag to disable persistence via config

## 13. Implementation Checklist

- [ ] Add `query_cache` table schema to `src/db/schema.rs`
- [ ] Create `src/graph/persistent_cache.rs` with `PersistentCache<K,V>`
- [ ] Implement `get()`, `insert()`, `invalidate()`, `invalidate_prefix()` methods
- [ ] Add `with_persistence()` to `QueryCache` in `src/graph/cache.rs`
- [ ] Add `with_persistence()` to `OrchestratorCache` in `src/orchestrator/cache.rs`
- [ ] Update `GraphEngine::new()` to use persistent cache
- [ ] Update `QueryOrchestrator::new()` to use persistent cache
- [ ] Add cache invalidation to watcher module
- [ ] Add unit tests for persistent cache
- [ ] Add integration tests for cache persistence
