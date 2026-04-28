# LeanKG Massive Graph — Engineering Requirement Document

**Version:** 2.2
**Date:** 2026-04-15
**Branch:** `feature/massive-graph`
**Worktree:** `.worktrees/feature/massive-graph`
**Status:** Draft — Pending Review

**v2.2 Changes:**
- Deep dive root cause analysis: service node detection failures (Section 12)
- Dynamic NODE TYPES and EDGE TYPES sidebar (data-driven from graph)
- Service topology loaded on root when multi-repo detected
- Service node click handler for drill-down navigation
- Folder/Directory type equivalence in filters
- Expanded node type catalog (Directory, Struct, Module, Constructor, Property, Decorator)

**v2.1 Changes:**
- Added `GET /api/graph/stats` endpoint (full histogram: nodes_by_type, edges_by_type, nodes_by_depth, folders, services)
- Added adaptive loading strategy: small DB loads all, large DB loads by layers, multi-repo loads per-service
- Added `depth` parameter to `GET /api/graph/data` for layer-based loading
- Updated data flow to stats-driven decision model
- Added Phase 0 implementation tasks for stats API
- Updated test plan with stats API tests and caching verification

---

## 1. Feature Overview

**Goal:** Enable LeanKG's web UI to handle massive codebases (100K+ elements, 50K+ relationships) by implementing lazy, hierarchical graph loading with progressive drill-down from services → folders → files → functions. Both single-repo and multi-repo projects must work.

**Architecture:** Lazy-loading graph API with DB-level filtering. UI starts at service/folder level, expanding on click. CodeViewer sidebar shows source for file/function nodes. Never loads full DB at once.

**Tech Stack:** Rust (Axum) backend + Vite/React (sigma.js) frontend + CozoDB

---

## 2. Validated Codebase Audit

### 2.1 Rust Backend — API Endpoints (src/web/handlers.rs — 3587 lines)

**Every endpoint audited. Full DB load = calls `g.all_elements()` or `g.all_relationships()`.**

| Route | Handler | Line | DB Load | Notes |
|-------|---------|------|---------|-------|
| `GET /` | `root_handler` | — | None | Serves embedded Vite UI |
| `GET /api/elements` | `api_elements` | 2250 | **FULL** | Returns all CodeElements |
| `GET /api/relationships` | `api_relationships` | 2269 | **FULL** | Returns all Relationships |
| `GET/POST /api/annotations` | `api_annotations` / CRUD | 2289 | Partial | CRUD on BusinessLogic table |
| `GET /api/search` | `api_search` | 2405 | **FULL** | Loads all elements, then filters in Rust by q/element_type/file_path |
| `GET /api/graph/data` | `api_graph_data` | 2447 | **FULL** | Loads ALL elements + ALL relationships. Builds full graph. Deduplicates edges. Adds `service_nodes` for `service_calls` rels. |
| `GET /api/graph/services` | `api_service_graph` | — | **DB-level** | Queries only `service_calls` relationships. Builds ServiceGraph with nodes/edges/weights. **Good pattern to follow.** |
| `GET /api/graph/service-topology` | `api_service_topology` | 1666 | **No DB** | Pure filesystem: scans for `.git` folders (multi-repo), walks config files for `dns:///` patterns. 30s timeout. Returns `ServiceTopology { nodes, relationships }`. |
| `GET /api/graph/expand-service` | `api_graph_expand_service` | 1897 | **FULL** | Loads ALL elements + ALL relationships, then filters in Rust by folder path prefix with `depth <= 2`. Accepts `?path=` or `?service=`. |
| `GET /api/graph/expand-cluster` | `api_graph_expand_cluster` | 2105 | **FULL** | Same pattern as expand-service. Loads ALL then filters by folder prefix depth <= 2. Accepts `?path=` or `?cluster=`. |
| `GET /api/graph/subgraph` | `api_graph_subgraph` | 2639 | **FULL** | Loads ALL elements + ALL relationships. Builds adjacency map in memory. BFS from root within N hops. Params: `root`, `depth` (default 3), `types` filter. |
| `GET /api/graph/clusters` | `api_graph_clusters` | 2805 | **FULL** | Loads ALL elements + ALL relationships. Groups by parent directory. Creates `cluster:` prefix nodes. Builds inter-cluster edges. |
| `GET /api/graph/layout` | `api_graph_layout` | — | Partial | Server-side ForceAtlas2 layout via `LayoutEngine`. |
| `GET /api/export/graph` | `api_export_graph` | 2566 | **FULL** | Loads ALL, formats for export. |
| `POST /api/query` | `api_query` | — | Passthrough | Raw CozoDB Datalog query. User controls scope. |
| `POST /api/project/switch` | `api_switch_path` | — | None | Switches project, spawns background indexing thread. |
| `GET /api/index/status` | `api_index_status` | — | None | Returns indexing progress state. |
| `GET /api/file` | `api_get_file` | — | None | Reads file from filesystem. Security: canonicalizes path. |

**Critical finding: 7 of 10 graph endpoints load ALL data from the database.**

### 2.2 GraphEngine Methods (src/graph/query.rs — 1574 lines)

| Method | Line | DB-level? | Notes |
|--------|------|-----------|-------|
| `all_elements()` | — | Full scan | Returns every CodeElement row |
| `all_relationships()` | — | Full scan | Returns every Relationship row |
| `get_children(parent_qualified)` | **354** | **YES** | Queries with `parent_qualified = $pq`. **Already exists!** Returns direct children. |
| `get_elements_by_file(file_path)` | — | **YES** | Filters by file_path at DB level |
| `search_by_name(name)` | — | Partial | Regex search with lowercase |
| `search_by_name_typed(name, element_type, limit)` | — | **YES** | Typed search with limit |
| `search_by_type(element_type)` | — | **YES** | Filter by element_type |
| `get_service_graph(current_service)` | — | **YES** | Queries only `service_calls` relationships |
| `get_call_graph_bounded(source, max_depth, max_results)` | — | **YES** | Bounded BFS in Datalog |
| `get_dependencies(file_path)` | — | **YES** | With cache |
| `get_dependents(target)` | — | **YES** | With cache |
| `get_callers(function_name, file_scope)` | — | **YES** | Callers of a function |
| `get_annotation(element_qualified)` | — | **YES** | Single annotation lookup |

**Key discovery:** `get_children()` already exists but is marked `#[allow(dead_code)]` — it's never called by any handler. It queries by `parent_qualified` field which is populated during indexing for functions/methods nested inside classes/files.

**Graph modules** (src/graph/mod.rs): cache, clustering, context, layout, persistent_cache, query, traversal

### 2.3 Data Models (src/db/models.rs — 305 lines)

```rust
pub struct CodeElement {
    pub qualified_name: String,    // e.g., "src/main.rs::main"
    pub element_type: String,       // "file", "function", "class", "method", etc.
    pub name: String,
    pub file_path: String,
    pub line_start: u32,
    pub line_end: u32,
    pub language: String,
    pub parent_qualified: Option<String>,  // ✅ Used by get_children()
    pub cluster_id: Option<String>,
    pub cluster_label: Option<String>,
    pub metadata: serde_json::Value,
}

pub struct Relationship {
    pub source_qualified: String,
    pub target_qualified: String,
    pub rel_type: String,           // 23 types including "service_calls"
    pub confidence: f64,
    pub metadata: serde_json::Value,
}
```

**`RelationshipType` enum** has 23 variants: imports, calls, tested_by, references, documented_by, service_calls, contains, defines, declares, implements, extends, uses, embeds, annotates, depends_on, configures, triggers, produces, consumes, manages, owns, represents, routes.

### 2.4 Vite WebUI Audit

| Component | File | Status | Notes |
|-----------|------|--------|-------|
| App shell | `ui/src/App.tsx` | Working | Loads service-topology first, fallback clusters. Manages `expandedNodeId`, `overviewData`. Has `handleExpandNode` (for `cluster:`/`service:` prefix) and `handleBackToOverview`. |
| GraphViewer | `ui/src/components/GraphViewer.tsx` | Working | Sigma.js wrapper. Double-click expand for cluster/service nodes. Shows CodeViewer for non-cluster/service selected nodes. Has zoom controls and "Expand" button. |
| CodeViewer | `ui/src/components/CodeViewer.tsx` | Working | Resizable right sidebar with react-syntax-highlighter. Fetches from `/api/file?path=`. Highlights line range from node metadata (startLine/endLine). |
| useSigma hook | `ui/src/hooks/useSigma.ts` | Working | Full Sigma.js lifecycle: FA2 layout with auto-duration, noverlap post-processing, nodeReducer (search dimming, selection highlighting, service node label visibility), edgeReducer (type visibility, selection highlighting). Click/doubleclick/stage click handlers. |
| useGraphFilters | `ui/src/hooks/useGraphFilters.ts` | Working | Manages visibleLabels, visibleEdgeTypes, depthFilter, zoomLevel (clusters/modules/files/functions), effectiveLabels. Zoom level presets control which node types are visible. |
| graph-adapter | `ui/src/lib/graph-adapter.ts` | Working | `createSigmaGraph()` builds graphology Graph from API data. Uses CONTAINS/DEFINES edges for parent-child hierarchy positioning (BFS layout). Louvain community detection. Has filter/expand helpers. |
| constants | `ui/src/lib/constants.ts` | Working | NODE_COLORS, NODE_SIZES, EDGE_STYLES. FILTERABLE_LABELS = [Service, Folder, File, Class, Function, Method, Interface, Enum]. DEFAULT_VISIBLE_LABELS = ['Service']. |
| edge-bundling | `ui/src/lib/edge-bundling.ts` | Working | Edge bundling for visual clarity. |

---

## 3. Gap Analysis — What Must Change

### 3.1 Backend Gaps

| # | Gap | Current State | Impact | Priority |
|---|-----|---------------|--------|----------|
| B1 | **7 endpoints load full DB** | `api_graph_data`, `api_graph_expand_service`, `api_graph_expand_cluster`, `api_graph_subgraph`, `api_graph_clusters`, `api_search`, `api_export_graph` all call `g.all_elements()` + `g.all_relationships()` then filter in Rust | OOM on 100K+ DBs | P0 |
| B2 | **No single-repo detection** | `api_service_topology` only finds multi-repo projects (scans for `.git` subfolders). Single repos get empty topology. | Single repos show nothing | P0 |
| B3 | **No lazy children API** | `get_children()` exists at `query.rs:354` but is dead code — no handler calls it. No `/api/graph/children` endpoint exists. | No progressive drill-down | P0 |
| B4 | **No element_type filter on expand** | `api_graph_expand_service` and `api_graph_expand_cluster` filter by path depth but not by element_type | UI loads too much data | P1 |
| B5 | **No pagination** | Expand endpoints return all matches at once. No limit/offset | UI lag/crash on large dirs | P1 |
| B6 | **No directory nodes in DB** | Tree-sitter indexer creates `file`, `function`, `class`, `method` elements but NOT `directory` elements. No `contains` edges for dir→dir or dir→file. | Can't render folder hierarchy from DB | P0 |

### 3.2 Frontend Gaps

| # | Gap | Current State | Impact | Priority |
|---|-----|---------------|--------|----------|
| F1 | **Single-repo renders empty** | App.tsx loads service-topology → gets empty for single-repo → falls back to clusters (which loads full DB) | Single-repo broken | P0 |
| F2 | **No click-to-expand hierarchy** | Double-click on cluster/service dumps all depth-2 nodes at once. No folder→subfolder→file→function progression | No progressive drill-down | P0 |
| F3 | **Node type filter doesn't affect API** | Sidebar checkboxes only hide/show existing nodes in sigma. They don't filter what the expand API returns | Over-fetching | P1 |
| F4 | **No breadcrumb** | Only "Back to Overview" button. No path navigation | Poor UX for deep drill-down | P2 |
| F5 | **Global loading overlay** | Loading blocks entire UI. No per-node loading state | Bad UX during expand | P2 |
| F6 | **DEFAULT_VISIBLE_LABELS only Service** | `constants.ts` defaults to `['Service']` only. For single-repo, should also show Folder, File | Single-repo shows too little | P1 |

---

## 4. Requirements (Acceptance Criteria)

### AC-1: Single Repo vs Multi-Repo Detection
- **Given** a project directory is indexed
- **When** the web UI loads `/api/graph/service-topology`
- **Then** the API detects project type:
  - **Multi-repo**: ≥2 child directories with `.git/` → return service nodes as today
  - **Single-repo**: 0 or 1 `.git/` children → return one service node (project name) with `children_folders` populated from top-level directories
- **And** response includes `project_type: "single_repo" | "multi_repo"`

### AC-2: Default Node Rendering
- **Given** the graph loads
- **When** initial data arrives
- **Then** service nodes are always rendered
- **And** for single-repo: child folder nodes are also rendered at level 1
- **And** node type filter defaults to: Service ✓, Folder ✓, File ✓ (Function ✗, Class ✗)

### AC-3: Click-to-Expand Drill-Down
- **Given** a service or folder node is rendered
- **When** user clicks the node
- **Then** the API returns only direct children of that node (one level deeper)
- **And** children include: folders, files, functions (filtered by active node type checkboxes)
- **And** edges are returned for all returned nodes
- **And** loading is shown only for the expanding node

### AC-4: CodeViewer for File/Function Nodes
- **Given** a file or function node is clicked
- **When** the node is a file type → right sidebar shows the full file source
- **And** when function/class → sidebar highlights the function's line range
- **And** the sidebar is resizable and closeable
- **Note:** This already works. No changes needed.

### AC-5: Always Have Edges
- **Given** any level of the graph
- **When** nodes are rendered
- **Then** edges between visible nodes are always included

### AC-6: No Full DB Loading
- **Given** a database with 100K+ elements
- **When** any API call is made
- **Then** the query uses DB-level filtering (path prefix + element_type + LIMIT)
- **And** response time is < 2 seconds
- **And** memory usage stays < 200MB on the server

---

## 5. API Design

### 5.0 Adaptive Loading Strategy

The UI adapts its loading behavior based on DB size and project type:

| Condition | Strategy | Initial Load | Drill-Down |
|-----------|----------|-------------|------------|
| **Single repo, small** (< 2000 nodes) | Load all at once | `GET /api/graph/data` (full) | Expand on click via `/api/graph/children` |
| **Single repo, large** (>= 2000 nodes) | Load by depth layers | `GET /api/graph/data?depth=0,1` (Service/Folder only) | Lazy-load deeper layers via `/api/graph/children` |
| **Multi repo** | Per-service drill-down | `GET /api/graph/service-topology` | Per-service expand via `/api/graph/expand-service` |

**Decision flow:**
```
UI loads -> GET /api/graph/stats (fast histogram)
         -> if total_nodes < 2000 && single_repo:
              GET /api/graph/data (load all)
         -> elif total_nodes >= 2000 && single_repo:
              GET /api/graph/data?depth=0,1 (top layers only)
         -> elif multi_repo:
              GET /api/graph/service-topology (already fast)
```

### 5.1 `GET /api/graph/stats` (NEW)

```
Returns a full histogram of the DB. Cheap CozoDB COUNT aggregations.
Used by the UI to decide loading strategy before fetching any graph data.
Should be cached (TTL 60s) to avoid repeated counting.

Response:
{
  success: true,
  data: {
    project_type: "single_repo" | "multi_repo",
    total_nodes: 7542,
    total_edges: 12890,
    nodes_by_type: {
      "file": 200,
      "function": 5000,
      "class": 300,
      "method": 1800,
      "interface": 42,
      ...
    },
    edges_by_type: {
      "CALLS": 8000,
      "IMPORTS": 3000,
      "DEFINES": 1500,
      "CONTAINS": 390,
      ...
    },
    nodes_by_depth: {
      "0": 5,      // root/service level
      "1": 20,     // folder level
      "2": 200,    // file level
      "3": 3000,   // function/class level
      "4": 4317    // deeper
    },
    folders: [
      { "path": "src/", "nodes": 4000, "edges": 7000 },
      { "path": "ui/src/", "nodes": 800, "edges": 1200 },
      { "path": "config/", "nodes": 50, "edges": 30 },
      ...
    ],
    services: [
      { "name": "be-merchant", "nodes": 2000, "edges": 3500 },
      { "name": "be-autos", "nodes": 1500, "edges": 2800 },
      ...
    ]
  }
}

Implementation:
  - Single CozoDB aggregation query using GROUP BY on element_type, rel_type
  - Depth computed from file_path segment count (split by '/', count segments)
  - Folders: DISTINCT file_path prefixes with COUNT
  - Services: only populated for multi-repo (from service-topology logic)
  - Cache result in AppState with 60s TTL
```

### 5.2 `GET /api/graph/service-topology` (MODIFY)

```
Current: Scans filesystem for .git dirs + dns:/// patterns. No DB access.
         Returns ServiceTopology { nodes, relationships }
         Returns empty for single-repo (no .git subfolders).

New: Add single-repo detection.
     If < 2 .git children found, treat as single-repo.
     Query DB for DISTINCT top-level file_path prefixes (depth=1).
     Create folder nodes with "folder:" prefix.
     Add CONTAINS edges from service node to folder nodes.

Query params:
  - show_orphans: bool (default false)
  - depth: int (default 1) — for single-repo, how many folder levels to include

Response:
{
  success: true,
  data: {
    nodes: [...],
    relationships: [...],
    project_type: "single_repo" | "multi_repo"
  }
}
```

### 5.3 `GET /api/graph/children` (NEW)

```
Returns direct children of a node, filtered by type. DB-level query.

Query params:
  - parent: string (required) — qualified_name or path prefix of parent
  - element_types: string (optional) — comma-separated: "directory,file,function,class"
  - limit: int (default 200, max 500)
  - offset: int (default 0)

Implementation: Build on existing GraphEngine::get_children() at query.rs:354.
                Add element_type filter and LIMIT/OFFSET to the CozoDB query.

Response:
{
  success: true,
  data: {
    nodes: [...],
    relationships: [...],
    total_count: 1234,
    has_more: true
  }
}
```

### 5.4 `GET /api/graph/expand-node` (NEW — unified expand)

```
Generic expand that works for any node type. Replaces expand-service + expand-cluster.

Query params:
  - node_id: string (required) — the node's ID
  - node_type: string (required) — "service" | "directory" | "file" | "cluster"
  - element_types: string (optional) — filter children by type
  - depth: int (default 1) — how many levels to expand
  - limit: int (default 200, max 500)

Response: same as /api/graph/children
```

### 5.5 `GET /api/graph/data` (MODIFY — add depth filter)

```
Current: Loads ALL elements + ALL relationships. No filtering.

New: Accept optional depth parameter for layer-based loading.

Query params:
  - depth: string (optional) — comma-separated depth levels: "0,1" means load only
    root and first-level nodes. Depth computed from file_path segment count.

Response: same as current GraphData format.
```

### 5.6 `GET /api/file` (KEEP — already working)

No changes needed. Already serves file content with path security.

---

## 6. Database Query Strategy

### 6.1 Current Pattern (BROKEN for large DBs)

```rust
// handlers.rs:1928 — api_graph_expand_service
let (elements_result, relationships_result) = match state.get_graph_engine().await {
    Ok(g) => (g.all_elements(), g.all_relationships()),  // LOADS EVERYTHING
    ...
};
// Then filters in Rust:
let filtered_elements: Vec<_> = all_elements.iter()
    .filter(|e| e.file_path.starts_with(&prefix))
    .collect();
```

### 6.2 New Pattern (DB-level filtering)

**Approach 1: Extend existing `get_children()` (query.rs:354)**

The method already exists and does DB-level filtering by `parent_qualified`. Extend it:

```rust
pub fn get_children_filtered(
    &self,
    parent_path: &str,
    element_types: Option<&[String]>,
    limit: Option<usize>,
    offset: Option<usize>,
) -> Result<ChildrenResult, Box<dyn std::error::Error>> {
    // CozoDB query with path prefix + optional type filter + limit
    let type_filter = match element_types {
        Some(types) => format!("et in [{}]", types.iter().map(|t| format!("\"{}\"", t)).collect::<Vec<_>>().join(", ")),
        None => String::new(),
    };

    let query = format!(r#"
        ?[qn, et, name, fp, ls, le, lang, pq, cid, clbl, meta] :=
            *code_elements[qn, et, name, fp, ls, le, lang, pq, cid, clbl, meta],
            starts_with_str(fp, $prefix),
            {type_filter}
        :limit $limit
        :offset $offset
    "#);
    // ... execute and return
}
```

**Approach 2: Path-prefix query for directory listing**

```cozo
?[qn, et, name, fp] :=
    *code_elements[qn, et, name, fp, ls, le, lang, pq, cid, clbl, meta],
    starts_with_str(fp, $prefix),
    et = $type
```

**Note on CozoDB:** `starts_with_str` is available. If not, use Rust-side filtering with cursor-based iteration (read in chunks via `:limit` + `:offset`).

### 6.3 Directory Nodes — Missing Data Problem

Current indexer (`src/indexer/extractor.rs`) does NOT create `directory` elements. It creates `file`, `function`, `class`, `method`, `interface`, `enum` elements only.

**Two options:**
1. **Add directory indexing to extractor** — During indexing, emit `directory` elements for each unique folder path, with `contains` edges to child files/dirs. This is the proper solution.
2. **Synthesize directory nodes on-the-fly** — In the `/api/graph/children` handler, scan file_path values for the given prefix, extract unique subdirectories, create synthetic folder nodes. No DB schema change needed. **Recommended for V1 — faster to implement, no indexer changes.**

---

## 7. UI Component Changes

### 7.1 App.tsx Changes

| Current | New |
|---------|-----|
| Loads service-topology → fallback clusters | Load service-topology with `project_type` detection |
| `expandedNodeId` for one level | `navigationStack: string[]` for multi-level drill-down |
| Global loading overlay | Per-node loading indicator |
| Expand button for cluster/service only | Click any node to expand (service, folder, file) |
| Node type filter only hides/shows existing nodes | Node type filter also controls what children to fetch |
| DEFAULT_VISIBLE_LABELS = ['Service'] | For single-repo: ['Service', 'Folder', 'File'] |

### 7.2 GraphViewer.tsx Changes

| Current | New |
|---------|-----|
| Double-click only on service/cluster | Click on any node triggers expand or code view |
| No breadcrumb | Breadcrumb bar showing navigation path |
| CodeViewer only for non-expandable nodes | CodeViewer for file/function nodes, expand for service/folder nodes |

### 7.3 New Components

| Component | Purpose |
|-----------|---------|
| `Breadcrumb.tsx` | Navigation path: `leankg > src > graph > query.rs` |
| `NodeExpandIndicator.tsx` | "+" badge on nodes that have unloaded children |

### 7.4 State Management

```typescript
navigationStack: string[]          // ["service:leankg", "src/", "src/graph/"]
currentData: GraphData             // Current visible nodes + edges
overviewData: GraphData            // Service-level data for "back to overview"
expandedNodes: Set<string>         // Nodes that have been expanded (avoid re-fetch)
nodeChildrenCache: Map<string, GraphData>  // Cache expanded children per node
selectedNode: string | null        // For CodeViewer sidebar
nodeTypeFilter: string[]           // Active node types for API filtering
```

---

## 8. Data Flow

### 8.0 Initial Load — Stats-Driven Decision

```
UI -> GET /api/graph/stats
API -> CozoDB COUNT aggregations (fast: < 100ms even on 100K DB)
API -> Return histogram: total_nodes, total_edges, nodes_by_type, folders, services
API -> Cache result with 60s TTL
UI -> Decide loading strategy:
  - total_nodes < 2000 && single_repo → Strategy A (load all)
  - total_nodes >= 2000 && single_repo → Strategy B (depth layers)
  - multi_repo → Strategy C (per-service)
```

### 8.1 Initial Load — Single Repo, Small (Strategy A: < 2000 nodes)

```
UI -> (already has stats) → total_nodes < 2000, single_repo
UI -> GET /api/graph/data
API -> g.all_elements() + g.all_relationships() (safe: small DB)
API -> Return full GraphData
UI -> Render all nodes, set visibleLabels = [Service, Folder, File]
UI -> Subsequent clicks use /api/graph/children for targeted expand
```

### 8.2 Initial Load — Single Repo, Large (Strategy B: >= 2000 nodes)

```
UI -> (already has stats) → total_nodes >= 2000, single_repo
UI -> GET /api/graph/data?depth=0,1
API -> DB: filter elements where file_path segment count <= depth
API -> Return Service + Folder + top-level File nodes only
UI -> Render top-level layers, set visibleLabels = [Service, Folder]
UI -> User clicks folder "src/" → lazy-load via /api/graph/children
UI -> User clicks file "query.rs" → shows CodeViewer
```

### 8.3 Initial Load — Multi Repo (Strategy C)

```
UI -> (already has stats) → multi_repo detected
UI -> GET /api/graph/service-topology
API -> Scan filesystem: find .git dirs (example-user, example-order, example-product)
API -> Walk config files: find dns:/// patterns
API -> Build ServiceTopology with SERVICE_CALLS edges
API -> Response: { nodes, relationships, project_type: "multi_repo" }
UI -> Render service nodes + SERVICE_CALLS edges
UI -> User clicks service → per-service expand via /api/graph/expand-service
```

### 8.4 Click-to-Expand

```
User clicks folder node "src/graph/"
UI -> Show per-node loading spinner
UI -> GET /api/graph/children?parent=src/graph/&element_types=directory,file,function&limit=200
API -> GraphEngine::get_children_filtered("src/graph/", ["directory","file","function"], 200, 0)
API -> DB: CozoDB query with starts_with_str(fp, "src/graph/") + type filter + LIMIT
API -> Synthesize directory nodes from file_path prefixes
API -> Get relationships for returned elements
API -> Response: { nodes, relationships, total_count, has_more }
UI -> Merge children into current graph
UI -> Remove spinner
UI -> Layout new nodes
```

### 8.5 CodeViewer (already works)

```
User clicks file node "src/graph/query.rs"
UI -> Open CodeViewer sidebar
UI -> GET /api/file?path=src/graph/query.rs
API -> Read file from filesystem
API -> Response: { content, language, total_lines }
UI -> Render with syntax highlighting

User clicks function node "query.rs::GraphEngine::new"
UI -> GET /api/file?path=src/graph/query.rs
UI -> Highlight lines 8-22 (from node metadata startLine/endLine)
```

---

## 9. Implementation Plan

### Phase 0: Stats API + Adaptive Loading (P0 — new)

| Task | File | Description |
|------|------|-------------|
| 0.1 | `src/graph/query.rs` | Add `get_db_stats()` method — runs CozoDB COUNT aggregations: total nodes, total edges, nodes_by_type, edges_by_type, nodes_by_depth, top folders by node count |
| 0.2 | `src/web/handlers.rs` | Add `api_graph_stats` handler — calls `get_db_stats()`, detects project_type, returns full histogram. Cache with 60s TTL in AppState. |
| 0.3 | `src/web/mod.rs` | Register route `GET /api/graph/stats` |
| 0.4 | `src/graph/query.rs` | Add depth parameter to element queries — compute depth from file_path segment count, support `?depth=0,1` filter |
| 0.5 | `src/web/handlers.rs` | Modify `api_graph_data` to accept optional `depth` query param for layer-based loading |
| 0.6 | `ui/src/App.tsx` | Add stats fetch on load → decide strategy A/B/C → load graph data accordingly |
| 0.7 | `ui/src/hooks/useGraphFilters.ts` | Expose loading strategy state (all / layers / per-service) |
| 0.8 | `ui/src/lib/constants.ts` | Add `SMALL_DB_THRESHOLD = 2000` constant |

### Phase 1: Backend — DB-Level Filtering (P0)

| Task | File | Description |
|------|------|-------------|
| 1.1 | `src/graph/query.rs` | Add `get_children_filtered()` method extending existing `get_children()` at line 354. Add path-prefix query, element_type filter, LIMIT/OFFSET. |
| 1.2 | `src/graph/query.rs` | Add `get_top_level_directories()` method — returns distinct folder prefixes at depth N from file_path values. |
| 1.3 | `src/web/handlers.rs` | Modify `api_service_topology` (line 1666) to detect single-repo. Query DB for top-level dirs. Return `project_type` field. |
| 1.4 | `src/web/handlers.rs` | Add `api_graph_children` handler — uses `get_children_filtered()`. Synthesizes directory nodes from file_path prefixes. Returns paginated response. |
| 1.5 | `src/web/handlers.rs` | Add `api_graph_expand_node` handler — unified expand routing by node_type. |
| 1.6 | `src/web/mod.rs` | Register routes for `/api/graph/children` and `/api/graph/expand-node`. |

### Phase 2: Frontend — Stats-Driven Adaptive Loading (P0)

| Task | File | Description |
|------|------|-------------|
| 2.1 | `ui/src/App.tsx` | Replace `expandedNodeId` with `navigationStack`. Fetch `/api/graph/stats` first, then decide loading strategy. For small single-repo: load all. For large single-repo: load top layers. For multi-repo: load service topology. |
| 2.2 | `ui/src/components/GraphViewer.tsx` | Update click handling: service/folder → expand, file/function → CodeViewer. Per-node loading indicator. |
| 2.3 | `ui/src/components/Breadcrumb.tsx` | New file. Navigation path with click-to-navigate. |
| 2.4 | `ui/src/lib/graph-adapter.ts` | Update merge logic for incremental node addition from children API. |
| 2.5 | `ui/src/lib/constants.ts` | Change DEFAULT_VISIBLE_LABELS based on project type + DB size. For small single-repo: all types. For large single-repo: [Service, Folder]. For multi-repo: [Service]. |

### Phase 3: Polish (P1-P2)

| Task | Description |
|------|-------------|
| 3.1 | Node type filter integration — pass checkbox state to expand API as `element_types` param |
| 3.2 | "Load more" button for paginated results when `has_more` is true |
| 3.3 | Per-node loading spinner instead of global overlay |

---

## 10. Testing Plan

### 10.1 Test Projects

| Test Case | Path | Type | Why |
|-----------|------|------|-----|
| **Multi-repo** | `examples/` | multi_repo | Contains `example-user/`, `example-order/`, `example-product/` — 3 Go microservices with `dns:///` patterns in `config/config.go`. Service topology: `example-product → example-user`, `example-product → example-order`, `example-order → example-user`. |
| **Single-repo** | `.worktrees/feature/massive-graph/` | single_repo | The LeanKG codebase itself (Rust + TypeScript). 339 elements, 262 relationships. Has `src/`, `ui/`, `config/`, `docs/` etc. as top-level directories. One `.git` (worktree). |

### 10.2 Multi-repo Setup (examples/)

The `examples/` directory has 3 Go microservices with inter-service gRPC calls:

```
examples/
├── example-user/       (port 10001 grpc)
│   └── config/config.go     — no dns:/// (leaf service)
├── example-order/      (port 10002 grpc)
│   └── config/config.go     — dns:///example-user:10001
├── example-product/    (port 10003 grpc)
│   └── config/config.go     — dns:///example-user:10001 + dns:///example-order:10002
├── go-api-service/     (standalone, not part of multi-repo topology)
├── java-api-service/
├── kotlin-api-service/
└── ...
```

**Service topology expected:**
```
example-product ──SERVICE_CALLS──> example-user
example-product ──SERVICE_CALLS──> example-order
example-order   ──SERVICE_CALLS──> example-user
```

**Note:** `examples/` directories do NOT have `.git/` subfolders. For multi-repo detection testing, must `git init` in each service:
```bash
cd examples && \
  git init example-user && \
  git init example-order && \
  git init example-product
```

### 10.3 Single-repo Setup (.worktrees/feature/massive-graph/)

The worktree itself is the test case. It has:
```
.worktrees/feature/massive-graph/
├── .git           (worktree file, not directory)
├── src/           (Rust source — 1574 lines in query.rs alone)
├── ui/src/        (TypeScript/React frontend)
├── config/        (YAML configs)
├── docs/          (Documentation)
└── examples/      (Example projects)
```

Expected initial load: project_type = "single_repo", nodes = [service:leankg, folder:src, folder:ui, folder:config, folder:docs, folder:examples]

### 10.4 Backend Testing

```bash
cd .worktrees/feature/massive-graph

# Build
cargo build

# Test 0: Stats API
curl -s 'http://localhost:8080/api/graph/stats' | jq '.data'
# Expected: { project_type: "single_repo", total_nodes: ~339, total_edges: ~262,
#             nodes_by_type: { file: N, function: N, ... }, edges_by_type: {...},
#             nodes_by_depth: {"1": N, "2": N, ...}, folders: [...] }

# Test 1: Single-repo detection
curl -s 'http://localhost:8080/api/graph/service-topology' | jq '.data.project_type'
# Expected: "single_repo"

# Test 2: Children API — expand src/
curl -s 'http://localhost:8080/api/graph/children?parent=src/&element_types=directory,file&limit=50' | jq '.data.nodes | length'
# Expected: < 50 (paginated)

# Test 3: Children API — has_more
curl -s 'http://localhost:8080/api/graph/children?parent=src/&limit=5' | jq '.data.has_more'
# Expected: true (src/ has more than 5 children)

# Test 4: Graph data with depth filter
curl -s 'http://localhost:8080/api/graph/data?depth=0,1' | jq '.data.nodes | length'
# Expected: small number (only root + first level)

# Test 5: File content
curl -s 'http://localhost:8080/api/file?path=src/graph/query.rs' | jq '.data.language'
# Expected: "rust"

# Test 6: Stats API caching (call twice, second should be faster)
time curl -s 'http://localhost:8080/api/graph/stats' > /dev/null
time curl -s 'http://localhost:8080/api/graph/stats' > /dev/null
# Expected: second call significantly faster (cache hit)
```

### 10.5 Multi-repo Testing

```bash
# Setup: init git in example services
cd examples
git init example-user && git init example-order && git init example-product

# Switch project to examples/
curl -X POST 'http://localhost:8080/api/project/switch' \
  -H 'Content-Type: application/json' \
  -d '{"path": "/Users/linh.doan/work/harvey/freepeak/leankg/examples"}'

# Test: Multi-repo detection
curl -s 'http://localhost:8080/api/graph/service-topology' | jq '.data.project_type'
# Expected: "multi_repo"

# Test: Service topology has correct edges
curl -s 'http://localhost:8080/api/graph/service-topology' | jq '.data.relationships[].rel_type'
# Expected: "SERVICE_CALLS" (3 edges)

# Cleanup
rm -rf examples/example-user/.git examples/example-order/.git examples/example-product/.git
```

### 10.6 Frontend Testing

```bash
cd ui/ && npm run dev
# Open http://localhost:5173

# Verify stats-driven loading:
#   0. Network tab: first call = GET /api/graph/stats (< 100ms)
#   1. For small DB: second call = GET /api/graph/data (full load)
#   2. For large DB: second call = GET /api/graph/data?depth=0,1 (layers)
#   3. For multi-repo: second call = GET /api/graph/service-topology

# Verify single-repo:
#   1. Service node + folder nodes (src/, ui/, config/, docs/) render on load
#   2. Click "src/" → children load (subfolders + files)
#   3. Click "src/graph/" → more children load
#   4. Click "query.rs" → CodeViewer opens with Rust syntax highlighting
#   5. Click "GraphEngine::new" → CodeViewer highlights lines 8-22
#   6. Breadcrumb: leankg > src > graph > query.rs
#   7. No full-page loading after initial load
#   8. Node type checkboxes filter children on next expand

# Verify multi-repo:
#   1. Switch project to examples/
#   2. 3 service nodes render: example-user, example-order, example-product
#   3. SERVICE_CALLS edges between them
#   4. Click service → shows internal structure
```

---

## 11. Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Stats COUNT query slow on 100K+ DB | Low | Medium | Cache with 60s TTL. Use CozoDB aggregation (not Rust-side counting). If still slow, maintain running counts on insert. |
| CozoDB `starts_with_str` not efficient for prefix scans | Medium | High | Fall back to Rust-side filtering with cursor pagination (`:limit` + `:offset`) |
| Large directory (>1000 files) | High | Medium | LIMIT + pagination + "Load more" UI |
| Sigma.js performance with > 500 nodes | Medium | Medium | Virtual rendering, hide labels on zoom out, limit visible nodes |
| `parent_qualified` field not populated for all elements | High | High | Fallback to file_path prefix extraction for directory listing |
| Path normalization (relative vs absolute, `./` prefix) | High | Low | Normalize all paths at API boundary — strip `./` prefix consistently |
| No `.git` in examples/ for multi-repo testing | Certain | Low | `git init` in each service for testing; document in test plan |
| Stats cache stale after indexing | Medium | Low | Invalidate cache on `api_switch_path` and after indexing completes |
| Depth threshold (2000) may need tuning per project | Medium | Low | Make threshold configurable via leankg.yaml `web.stats_threshold` |

---

## 12. Root Cause Analysis: Service Node Detection & Display Issues

**Date:** 2026-04-15
**Investigator:** AI Agent Deep Dive

### 12.1 Problem Statement

Service nodes and SERVICE_CALLS edges are not displayed correctly in the WebUI. The right sidebar NODE TYPES list doesn't match the EDGE TYPES list. Clicking service nodes does nothing.

### 12.2 Root Causes Identified

#### RC-1: Services are never stored as CodeElement records (CRITICAL)

**File:** `src/indexer/microservice.rs:313-328`

The microservice extractor creates `Relationship` rows with `rel_type = "service_calls"`, where `source_qualified` and `target_qualified` hold service names like `"be-gateway"`. However, no corresponding `CodeElement` with `element_type: "service"` is ever created. This means:

- `all_elements()` never returns service nodes
- `get_children_filtered()` can't find services
- Search APIs can't locate services
- Only the specialized `get_service_graph()` and `extract_service_topology()` can surface them

**Impact:** Services are invisible to the standard graph query pipeline.

#### RC-2: Two disconnected service detection paths (CRITICAL)

**Files:** `src/web/handlers.rs:1609` vs `src/web/handlers.rs:1668`

Two completely separate APIs detect services differently:

| API | Method | Node ID Format | Data Source |
|-----|--------|---------------|-------------|
| `/api/graph/services` | `get_service_graph()` | `"be-gateway"` (plain) | CozoDB relationships |
| `/api/graph/service-topology` | `extract_service_topology()` | `"service:be-gateway"` (prefixed) | Filesystem scan |

The frontend `useSigma.ts:223` checks both patterns (`node.startsWith('service:') || data.nodeType === 'Service'`), but the main graph data endpoint (`/api/graph/children`) returns neither format.

#### RC-3: UI loads `/api/graph/children` at root — returns no services (HIGH)

**File:** `ui/src/App.tsx:83-85`

The initial load calls `loadChildren('')` which hits `/api/graph/children?parent=`. This endpoint queries `code_elements` via `get_children_filtered()` — since services don't exist as `CodeElement` records, only `Folder` and `File` nodes appear. No `Service` nodes are ever returned at root level.

**This is why service nodes are invisible.** The service topology data exists at `/api/graph/service-topology` but the UI never calls it on initial load.

#### RC-4: NODE TYPES sidebar is hardcoded (HIGH)

**File:** `ui/src/lib/constants.ts:37-46` (old `FILTERABLE_LABELS`)

The sidebar displayed a static list: `['Service', 'Folder', 'File', 'Class', 'Function', 'Method', 'Interface', 'Enum']`. This:
- Doesn't include `Struct`, `Directory`, `Module`, `Constructor`, `Property`
- Shows types that may not exist in the current graph
- Doesn't reflect the actual data returned by the API

#### RC-5: EDGE TYPES sidebar is hardcoded (MEDIUM)

**File:** `ui/src/lib/constants.ts:25-33`

The edge type sidebar shows `CONTAINS, DEFINES, IMPORTS, CALLS, SERVICE_CALLS, EXTENDS, IMPLEMENTS` regardless of what edges exist in the current graph view. Missing: `REFERENCES, DOCUMENTED_BY, TESTED_BY`.

#### RC-6: No click handler for Service nodes (MEDIUM)

**File:** `ui/src/App.tsx:87-98` (old code)

When clicking a service node, the `handleNodeClick` fell through without any case for `elementType === 'service'`. Clicking did nothing — no drill-down into the service's internals.

### 12.3 Fixes Applied

| Fix | Files Changed | Description |
|-----|--------------|-------------|
| **F1: Dynamic Node Types** | `constants.ts`, `App.tsx`, `useGraphFilters.ts` | Node types are now discovered from actual graph data via `useMemo`. Replaced static `FILTERABLE_LABELS` with `DEFAULT_NODE_TYPE_ORDER` + data-driven discovery. Added `Directory`, `Struct`, `Module`, `Constructor`, `Property`, `Decorator`. |
| **F2: Dynamic Edge Types** | `App.tsx` | Edge types are now discovered from actual relationships data. Added `REFERENCES`, `DOCUMENTED_BY`, `TESTED_BY` to EDGE_STYLES. |
| **F3: Service topology on root load** | `App.tsx` | `loadChildren('')` now first tries `/api/graph/service-topology`. If the project has multiple services (>1 node), it uses that data. Falls back to `/api/graph/children` for single-repo projects. |
| **F4: Service click handler** | `App.tsx` | Added `if (elementType === 'service' || nodeId.startsWith('service:'))` handler that navigates into the service's folder path. |
| **F5: Folder/Directory equivalence** | `graph-adapter.ts` | `filterGraphByLabels` now treats `Folder` and `Directory` as equivalent — toggling one toggles both. |
| **F6: Expanded color/size maps** | `constants.ts` | Added `Directory`, `Module`, `Constructor`, `Property`, `Decorator`, `Config` to both `NODE_COLORS` and `NODE_SIZES`. |

### 12.4 Architecture Diagram (After Fix)

```
Initial Load (root)
    │
    ├── Try /api/graph/service-topology
    │   ├── Multi-repo detected (>1 .git dirs)
    │   │   └── Return Service + Folder nodes with SERVICE_CALLS + CONTAINS edges
    │   └── Single-repo (fallback)
    │       └── Return Service(project) + Folder children with CONTAINS edges
    │
    └── If no services → /api/graph/children?parent=
        └── Return Folder + File + Function nodes from DB

User clicks Service node
    └── Navigate into service folder path → /api/graph/children?parent=<service-path>

User clicks Folder/Directory node
    └── Navigate deeper → /api/graph/children?parent=<path>

Sidebar: Node Types / Edge Types
    └── Discovered dynamically from current graph data
```

### 12.5 Remaining Issues (Future Work)

| Issue | Severity | Description |
|-------|----------|-------------|
| Services not stored as CodeElement | Critical | The indexer should create `CodeElement { element_type: "service", qualified_name: "service:be-gateway", ... }` when extracting microservice relationships. This would unify all queries. |
| `get_children_filtered` ignores services | High | Should also check `service_calls` relationships to find child services at a given path level |
| Clustering ignores service_calls | Medium | `clustering.rs` should include `service_calls` in community detection |
| Node ID format inconsistency | Medium | Standardize on `"service:<name>"` prefix format across all APIs |

---

*Last updated: 2026-04-15 — ERD v2.2 with Root Cause Analysis and fixes for service node detection & display.*
