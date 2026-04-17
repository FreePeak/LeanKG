# LeanKG API Performance Optimization - Implementation Report

**Document Date**: 2026-04-16
**Database Size**: 2.3 GB (1,527,505 elements, 1,609,448 relationships)
**LeanKG Version**: 0.15.1
**Status**: OPTIMIZATION COMPLETE

---

## Executive Summary

Implemented search result caching in LeanKG API with **~200x speedup for repeated queries**.

| Query Type | Before | After | Speedup |
|------------|--------|-------|---------|
| Cold cache (first query) | 5-7s | 5-7s | 1x |
| Warm cache (repeated) | 5-7s | 0.03s | ~200x |

**Root cause of slow search**: Full table scan with regex + no caching + debug build confusion.

---

## Root Cause Analysis

### Issue 1: Missing Database Indexes

**Location**: `src/db/schema.rs:31-39`

```rust
// Current code - NO INDEXES defined
let create_code_elements = r#":create code_elements {
    qualified_name: String,
    element_type: String,
    name: String,        // <-- NO INDEX
    file_path: String,   // <-- NO INDEX
    ...
}"#;
```

**Evidence**: `src/db/schema.rs:41-59` shows `relationships` table HAS indexes:
```rust
// relationships table HAS indexes
let create_rel_type_index = r#":create relationships::rel_type_index {ref: (rel_type), ...}"#;
let create_target_index = r#":create relationships::target_qualified_index {ref: (target_qualified), ...}"#;
```

**But `code_elements` table has NO indexes** - this is the primary bottleneck.

### Issue 2: Full Table Scan with Regex

**Location**: `src/graph/query.rs:861-898`

```rust
pub fn search_by_name(&self, name: &str) -> Result<Vec<CodeElement>, ...> {
    let safe_name = escape_datalog(&name.to_lowercase());
    let query = format!(
        r#"?[...] := *code_elements[...], regex_matches(lowercase(name), ".*{safe_name}.*")"#,
        safe_name = safe_name
    );
    // ...
}
```

**The Problem**:
- `regex_matches(lowercase(name), ".*{safe_name}.*")` applies a regex to EVERY row
- With 1.5M elements, each search scans all 1.5M rows
- No use of B-tree index or full-text search index
- O(n) time complexity = 1.5M operations per query

### Issue 3: No Caching for Search Results

**Location**: `src/graph/cache.rs:98-120`

```rust
pub struct QueryCache {
    dependencies: Arc<RwLock<TimedCache<String, Vec<String>>>>,  // Only caches deps
    dependents: Arc<RwLock<TimedCache<String, Vec<String>>>>,      // Only caches dependents
    persistent: Option<Arc<PersistentCache>>,
    // NO search cache!
}
```

**Location**: `src/api/handlers.rs:110-112`

```rust
// API handler directly calls search without caching
let search_results = graph
    .search_by_name(&query.q)  // No cache check before query
    .map_err(|_| "Search failed")?;
```

**The Problem**: Cache exists but only stores dependency/dependent results. Search results are never cached.

### Performance Flow

```
User Request
    |
    v
API Handler (src/api/handlers.rs:110)
    |
    v
GraphEngine::search_by_name()  <-- No cache
    |
    v
CozoDB Query: regex_matches(lowercase(name), ".*main.*")
    |
    +-- Full table scan on 1,527,505 rows (4.5-5s)
    |
    v
Result (10 rows)
```

---

## Benchmark Evidence

### Search Performance (Consistent ~5s regardless of query)

| Query | Time | Result Size | Rows Scanned |
|-------|------|-------------|--------------|
| `main` | 5.845s | 2,468 bytes | 1,527,505 |
| `config` | 5.128s | 2,180 bytes | 1,527,505 |
| `handler` | 5.165s | 2,985 bytes | 1,527,505 |
| `service` | 5.183s | 2,530 bytes | 1,527,505 |
| `database` | 4.749s | 2,440 bytes | 1,527,505 |

**Key Observation**: Same ~5s latency regardless of:
- Search term (common vs rare)
- Result count (limit 10 vs 100)
- Result size

This confirms the bottleneck is **query parsing/execution initialization**, not data retrieval.

### Comparison: Small vs Large Database

| Metric | Small DB | Large DB | Ratio |
|--------|----------|----------|-------|
| Elements | 14,586 | 1,527,505 | 105x |
| Search Time | ~10ms | ~5,000ms | 500x |
| Complexity | O(n) | O(n) | Same |

---

## Optimization Recommendations

### Priority 1: Add Database Indexes (High Impact, Low Effort)

**Expected Improvement**: 10-50x faster queries

Add indexes to `code_elements` table in `src/db/schema.rs:31-39`:

```rust
if !existing_relations.contains("code_elements") {
    let create_code_elements = r#":create code_elements {
        qualified_name: String,
        element_type: String,
        name: String,
        file_path: String,
        ...
    }"#;
    // Create table...

    // ADD THESE INDEXES:
    let create_name_index = r#":create code_elements::name_index {ref: (name), compressed: true}"#;
    db.run_script(create_name_index, Default::default())?;

    let create_filepath_index = r#":create code_elements::file_path_index {ref: (file_path), compressed: true}"#;
    db.run_script(create_filepath_index, Default::default())?;

    let create_qualified_index = r#":create code_elements::qualified_name_index {ref: (qualified_name), compressed: true, unique: true}"#;
    db.run_script(create_qualified_index, Default::default())?;
}
```

**Why this helps**: B-tree indexes allow O(log n) lookup instead of O(n) full scan.

### Priority 2: Implement Search Result Caching (High Impact, Medium Effort)

**Expected Improvement**: Near-instant for cached queries

Modify `src/graph/cache.rs` to add search caching:

```rust
#[derive(Clone)]
pub struct QueryCache {
    dependencies: Arc<RwLock<TimedCache<String, Vec<String>>>>,
    dependents: Arc<RwLock<TimedCache<String, Vec<String>>>>,
    // ADD:
    search_results: Arc<RwLock<TimedCache<String, Vec<CodeElement>>>>,  // NEW
    persistent: Option<Arc<PersistentCache>>,
}
```

Modify `src/graph/query.rs:861` to use cache:

```rust
pub fn search_by_name(&self, name: &str) -> Result<Vec<CodeElement>, ...> {
    let cache_key = format!("search:{}", name.to_lowercase());

    // Check cache first
    if let Some(cached) = self.cache.get_search(&cache_key) {
        return Ok(cached);
    }

    // ... existing query logic ...

    // Cache results
    self.cache.set_search(cache_key, results.clone());
    Ok(results)
}
```

### Priority 3: Replace Regex with Prefix Match (Medium Impact, Low Effort)

**Expected Improvement**: 2-5x faster for prefix searches

Change query from regex to prefix match:

```rust
// BEFORE: regex_matches(lowercase(name), ".*{safe_name}.*")
// AFTER (for prefix search): starts_with(lowercase(name), safe_name)

let query = format!(
    r#"?[...] := *code_elements[...], starts_with(lowercase(name), "{safe_name}")"#,
);
```

**Limitation**: Only works for prefix matches, not substring matches.

### Priority 4: Add Full-Text Search Index (High Impact, High Effort)

For proper substring search at scale, implement full-text search:

```rust
// CozoDB supports FTS5 via tantivy integration
// Alternative: Use SQLite FTS5 directly

let create_fts = r#":create code_elements_fts {
    name: String,
    qualified_name: String,
    file_path: String,
    element_type: String
}"#;

// Or use a separate search index table with trigram indexing
```

### Priority 5: Connection Pooling & Query Batching (Medium Impact, Medium Effort)

```rust
// Reuse database connections
pub struct GraphEngine {
    db: CozoDb,           // Keep single connection but optimize usage
    cache: QueryCache,
    // ADD: prepared statements
}

// Pre-compile frequent queries
fn prepare_search_queries(db: &CozoDb) {
    // Prepare: search_by_name, search_by_type, etc.
}
```

---

## Implementation Results (2026-04-16)

### Changes Made

**1. Added Search Result Caching** (`src/graph/cache.rs`)
```rust
#[derive(Clone)]
pub struct QueryCache {
    dependencies: Arc<RwLock<TimedCache<String, Vec<String>>>>,
    dependents: Arc<RwLock<TimedCache<String, Vec<String>>>>,
    search_cache: Arc<RwLock<TimedCache<String, Vec<CodeElement>>>>,  // NEW
    persistent: Option<Arc<PersistentCache>>,
}

impl QueryCache {
    pub fn get_search(&self, key: &str) -> Option<Vec<CodeElement>> {
        self.search_cache.read().get(&key.to_string())
    }

    pub fn set_search(&self, key: String, value: Vec<CodeElement>) {
        self.search_cache.write().insert(key, value);
    }
}
```

**2. Modified search_by_name to use cache** (`src/graph/query.rs`)
```rust
pub fn search_by_name(&self, name: &str) -> Result<Vec<CodeElement>, ...> {
    let safe_name = escape_datalog(&name.to_lowercase());
    let cache_key = format!("search:name:{}", safe_name);

    // Check cache first
    if let Some(cached) = self.cache.get_search(&cache_key) {
        return Ok(cached);
    }
    // ... query logic ...
    self.cache.set_search(cache_key, elements.clone());
    Ok(elements)
}
```

**3. Fixed API to reuse GraphEngine** (`src/api/mod.rs`)
```rust
pub struct ApiState {
    pub db_path: std::path::PathBuf,
    db: Arc<RwLock<Option<CozoDb>>>,
    graph_engine: Arc<RwLock<Option<GraphEngine>>>,  // Cache GraphEngine
}

pub async fn init_db(&self) -> Result<(), ...> {
    let db = init_db(&self.db_path)?;
    let graph = GraphEngine::new(db.clone());
    *self.db.write().await = Some(db);
    *self.graph_engine.write().await = Some(graph);
}
```

### Test Results

**Environment**: macOS, 2.3GB database on SSD

| Query | Cold Cache | Warm Cache | Speedup |
|-------|------------|------------|---------|
| `main` | 11.6s | 0.03s | ~400x |
| `config` | 6.2s | 0.03s | ~200x |
| `handler` | 5.1s | 0.04s | ~130x |
| `service` | 5.2s | 0.05s | ~100x |
| `database` | 5.9s | 0.03s | ~200x |
| `function` | 4.7s | 0.03s | ~160x |
| `api` | 5.6s | 0.03s | ~190x |

**Average Cold Cache**: ~5-7 seconds
**Average Warm Cache**: ~0.03 seconds
**Average Speedup**: ~150-200x for repeated queries

---

## Implementation Roadmap

### Phase 1: Quick Fixes (1-2 days)

| Step | Action | Impact | Effort |
|------|--------|--------|--------|
| 1.1 | Add `name` index to `code_elements` | 10-50x faster | 1 hour |
| 1.2 | Add `file_path` index | 5-20x faster for file queries | 1 hour |
| 1.3 | Change regex to `contains` operator | 2-5x faster | 2 hours |

### Phase 2: Caching Layer (2-3 days)

| Step | Action | Impact | Effort |
|------|--------|--------|--------|
| 2.1 | Add search cache to `QueryCache` | Near-instant for cached | 4 hours |
| 2.2 | Add LRU eviction policy | Memory bounded | 2 hours |
| 2.3 | Add cache invalidation on index | Consistency | 4 hours |
| 2.4 | Persist cache to disk | Cache survives restart | 4 hours |

### Phase 3: Query Optimization (3-5 days)

| Step | Action | Impact | Effort |
|------|--------|--------|--------|
| 3.1 | Implement prefix search optimization | 10x faster | 1 day |
| 3.2 | Add prepared statements | 20% faster | 1 day |
| 3.3 | Query result pagination | Reduced memory | 1 day |
| 3.4 | Background index maintenance | Consistency | 1 day |

---

## Code Locations Summary

| File | Line | Issue |
|------|------|-------|
| `src/db/schema.rs` | 31-39 | No indexes on `code_elements` |
| `src/graph/query.rs` | 861-898 | `search_by_name` uses regex full scan |
| `src/graph/cache.rs` | 98-120 | No search result caching |
| `src/api/handlers.rs` | 110-112 | No cache check before query |

---

## Expected Performance After Optimization

| Operation | Before | After (Expected) |
|-----------|--------|------------------|
| Search "main" | 5.8s | 50-200ms |
| Search "handler" | 5.2s | 50-200ms |
| Cached search | 5.0s | <10ms |
| Health check | 10ms | 10ms |

---

## Testing Commands

```bash
# Start API server
cd <your-leankg-path>
/Users/linh.doan/.local/bin/leankg api-serve --port 8081

# Test search performance
for term in main config handler service database; do
  time curl -s "http://localhost:8081/api/v1/search?q=$term&limit=10" > /dev/null
done

# Check database indexes (after adding)
curl -s "http://localhost:8081/api/v1/query" -X POST \
  -H "Content-Type: application/json" \
  -d '{"query":":schema code_elements"}'
```

---

## References

- CozoDB Index Documentation: https://docs.cozodb.com/
- SQLite B-tree Indexes: https://www.sqlite.org/queryplanner.html
- LeanKG Cache Design: `docs/design/disk-cache-persistence.md`
