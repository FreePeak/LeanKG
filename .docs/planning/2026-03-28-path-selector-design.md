# Design: Dynamic Database Path Selection in Web UI

## Overview

The web UI will have a **persistent top bar** with a path selector that supports:
- **Text input** for typing/pasting paths
- **Browse button** for native file dialog (files + directories)
- **Drag & drop** onto the input field
- **GitHub URL** - clone repo, index it, display it

When a path is selected:
1. If it's a **GitHub URL** (e.g., `https://github.com/user/repo`) → clone, index, hot-swap
2. If it's a **directory** without `.leankg` → auto-index with progress overlay
3. If it has `.leankg` → hot-swap database, reload page
4. If it's a **file** → treat as database file directly

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ [Logo] LeanKG    [Path Input____________] [Browse]   [Home] │
└─────────────────────────────────────────────────────────────┘
```

**Components:**
- `TopBar` component (persistent, not page-specific)
- `PathInput` with drag-drop support + browse button
- `IndexProgress` overlay modal during indexing
- `AppState` extended to track current path and support hot-swap

---

## API Endpoint & State Management

### New Endpoint: `POST /api/path/switch`

```rust
// Request
{ "path": "/Users/foo/project" }

// Response
{ 
  "success": true,
  "is_directory": true,
  "has_database": false,
  "needs_indexing": true
}
```

### Flow When Path Changes

1. **Frontend** → `POST /api/path/switch { path }`
2. **Backend** checks:
   - If GitHub URL: clone repo, index it, return `{ is_github: true, needs_indexing: true }`
   - If directory: check if `.leankg` exists inside
   - If file: validate it's a valid database
3. **Backend responds** with status
4. **If `needs_indexing: true`**:
   - Frontend shows progress overlay
   - Backend triggers indexing via existing `index_codebase()`
   - Frontend polls `GET /api/index/status` for progress
5. **If `has_database: true`**:
   - Backend hot-swaps `AppState.db`
   - Frontend reloads page

### GitHub URL Support

**Supported URL formats:**
- `https://github.com/user/repo`
- `https://github.com/user/repo.git`
- `git@github.com:user/repo.git` (SSH)

**Flow:**
1. User pastes GitHub URL into path input
2. Frontend detects GitHub URL pattern, calls `POST /api/github/clone`
3. Backend clones repo to temp directory (e.g., `.leankg/clones/<repo-name>/`)
4. Backend starts indexing in background
5. Frontend polls `/api/index/status` for progress
6. When done, backend hot-swaps to cloned repo's database
7. Frontend reloads page

### State Stored in `AppState`

```rust
pub struct AppState {
    pub db_path: PathBuf,
    pub current_project_path: PathBuf,  // NEW
    db: RwLock<Option<CozoDb>>,
}
```

---

## Frontend Components

### TopBar (Persistent Header)

The top bar is rendered in `base_html()` function in `handlers.rs` and appears on all pages. It contains:
- LeanKG logo/title (left)
- Path input field (center, flexible width)
- Browse button (right of input)
- Home link (far right)

### PathInput Field

- Standard HTML `<input type="text">` styled to match UI
- Placeholder: "Enter path, GitHub URL, or drag & drop file/folder"
- On dragover: visual highlight (border change, background tint)
- On drop: extract path, submit to `/api/path/switch`
- On browse button click: open native file dialog with `directory` and `file` filters
- On submit (Enter key): detect if GitHub URL → call `/api/github/clone`, else call `/api/path/switch`

### Browse Button

- Triggers native file dialog via `window.showDirectoryPicker()` and `showOpenFilePicker()`
- Falls back to `<input type="file">` with `webkitdirectory` attribute for directory selection

### IndexProgress Overlay

Modal overlay shown during auto-indexing or GitHub clone:
- Semi-transparent backdrop
- Centered card with:
  - "Cloning repository..." title (for GitHub) OR "Indexing..." title (for local)
  - Progress bar (indeterminate initially, then percentage if available)
  - Current file being processed OR "Cloning: <repo-name>"
  - Cancel button (stops indexing/clone)

### Polling Endpoint: `GET /api/index/status`

```rust
// Response
{
  "is_indexing": true,
  "progress_percent": 45,
  "current_file": "src/main.rs",
  "total_files": 120,
  "indexed_files": 54
}
```

---

## Backend Changes

### New Handler: `api_switch_path`

```rust
pub async fn api_switch_path(
    State(state): State<AppState>,
    Json(req): Json<PathSwitchRequest>,
) -> impl IntoResponse
```

Logic:
1. Validate path exists
2. If directory:
   - Check if `path/.leankg` exists → `has_database = true`
   - If not → `needs_indexing = true`
3. If file:
   - Check if it's a valid cozo database → `has_database = true`
4. If `has_database`: hot-swap state
5. Return response

### New Handler: `api_index_status`

```rust
pub async fn api_index_status(State(state): State<AppState>) -> impl IntoResponse
```

Returns current indexing progress from shared state.

### New Handler: `api_github_clone`

```rust
pub async fn api_github_clone(
    State(state): State<AppState>,
    Json(req): Json<GitHubCloneRequest>,
) -> impl IntoResponse
```

**Request:**
```rust
{ "url": "https://github.com/user/repo" }
```

**Response:**
```rust
{
  "success": true,
  "clone_path": "/path/to/.leankg/clones/repo",
  "is_indexing": true
}
```

**Logic:**
1. Parse GitHub URL to extract owner/repo
2. Check if already cloned in `.leankg/clones/<repo>/`
3. If not, `git clone` to temp directory
4. Start indexing in background
5. Return clone path

### Hot-Swap Logic

```rust
impl AppState {
    pub async fn switch_path(&self, new_path: PathBuf) -> Result<PathSwitchResponse, Error> {
        // Validate path
        // Check if directory or file
        // Update db_path and current_project_path
        // Reinitialize db
    }
}
```

---

## Error Handling

| Scenario | Backend Response | Frontend Behavior |
|----------|----------------|-------------------|
| Path doesn't exist | `{ success: false, error: "Path not found" }` | Show error toast |
| Invalid database file | `{ success: false, error: "Not a valid database" }` | Show error toast |
| Invalid GitHub URL | `{ success: false, error: "Invalid GitHub URL" }` | Show error toast |
| GitHub clone failed | `{ success: false, error: "Clone failed: ..." }` | Show error toast |
| Indexing failed | `{ success: false, error: "Indexing failed: ..." }` | Show error, allow retry |
| Server error | `{ success: false, error: "Internal error" }` | Show error toast |

---

## File Changes

| File | Changes |
|------|---------|
| `src/web/mod.rs` | Add `current_project_path` to `AppState`, add new routes (`/api/path/switch`, `/api/index/status`, `/api/github/clone`) |
| `src/web/handlers.rs` | Add `api_switch_path`, `api_index_status`, `api_github_clone` handlers, modify `base_html` for top bar |
| `src/main.rs` | Update `start_server` to accept optional `db_path` override |

---

## UX Flow: Complete User Journey

1. User opens `http://localhost:8080`
2. Top bar shows current path (or empty/default if none)
3. User types `/Users/foo/newproject` OR drags folder onto input OR clicks Browse OR pastes GitHub URL
4. **If GitHub URL detected:**
   - Frontend calls `POST /api/github/clone`
   - Backend clones repo to `.leankg/clones/<repo>/`
   - Frontend shows "Cloning repository..." overlay
   - Backend starts indexing in background
   - Frontend polls `/api/index/status`
   - When done, frontend reloads page
5. **If local path:**
   - Frontend calls `POST /api/path/switch`
   - Backend checks path:
     - **If `.leankg` exists**: returns `{ needs_indexing: false, has_database: true }`
     - **If directory but no `.leankg`**: returns `{ needs_indexing: true }`
   - **Path has database**:
     - Backend hot-swaps db
     - Frontend reloads page
     - Page shows data from new database
   - **Needs indexing**:
     - Frontend shows progress overlay
     - Backend starts indexing in background
     - Frontend polls `/api/index/status`
     - When done, frontend reloads page
