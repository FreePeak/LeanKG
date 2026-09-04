# Root Cause Analysis: Expand graph flooded by `examples/` — hard to find LeanKG `src/`

**Date:** 2026-07-21  
**Project:** `/workspace` (LeanKG) via `leankg serve` ~15 290 elements  
**Symptom:** After expand, the canvas is dominated by `examples/` (user: “`.example/`”); `src/` (main LeanKG code) is hard to find.

---

## Issue Description

Opening the exploring graph for LeanKG (`?project=/workspace`) and expanding the root (or auto-expanding single-repo) paints a graph that looks like sample/demo code under `examples/`, not the product tree under `src/`.

---

## Evidence (live `/api/graph/expand-service`)

| Probe | Result |
|-------|--------|
| `expand-service?path=.&all=true&limit=500&offset=0` | **500** nodes; top prefixes: **examples 378**, benchmark 33, benches 30, **src 24** |
| `hasMore` on that response | **`false`** (despite ~15k elements in index) |
| Offset scan (50-row pages) | offsets **0–400** ≈ benches/benchmark/**examples**; **src dominates from offset ≈450–500** |
| `GET /api/search?q=src/main` | Hits `./examples/java-api-service/src/main` first (path contains `src`) |

Conclusion: **`src/` mostly starts after the first 500-node page.** With broken/`false` `hasMore`, the UI never offers a reliable path to page into `src/`.

---

## Problematic Code Chunks

### 1. Unordered full dump + hard limit (no path priority)

`get_elements_in_folder` root + `all_content=true` runs Cozo:

```text
*code_elements[…] :limit {limit} :offset {offset}
```

No `ORDER BY`, no prefer-`src`, no exclude-`examples`. Storage/key order places `benches` / `benchmark` / `examples` **before** `src`.

### 2. Single-repo root forces `all_content=true` (FR-MG-03)

`api_graph_expand_service` sets `all_content = true` when expanding `.` on a single-repo tree. That dumps **all nested symbols** (functions/properties under every example), not a structural folder overview.

### 3. Broken `hasMore` on root all-content (until worktree fix)

Live serve still reports `hasMore: false` on full pages. Pagination / “Load more” cannot surface the `src/` band at offset ≥ ~500.

### 4. Index includes `examples/`

`leankg.yaml` exclude list does **not** omit `examples/` / `benches/` / `benchmark/`. Demo trees are first-class in the graph.

### 5. UI filter defaults amplify noise

Default visible labels include Function/Method/Property, so hundreds of **example** symbols fill the canvas; the few `src/` nodes on page 1 are easy to miss.

---

## Logic Flow (current)

```text
UI expand root (all=true) or FR-MG-03 auto all_content
        │
        ▼
get_elements_in_folder(".", limit=500, offset=0, all_content=true)
        │
        ▼
Cozo unordered scan → first 500 rows ≈ benches + examples (+ tiny src tail)
        │
        ▼
hasMore=false (bug) → no Load more → user stuck in examples-dominated graph
```

---

## Root Causes (ranked)

| # | Cause | Impact |
|---|--------|--------|
| **RC-1** | Root expand is an **unordered, symbol-level dump** capped at 500 | First paint = `examples/`/`benches`, not `src/` |
| **RC-2** | **`hasMore` false** on full pages | Cannot page to `src/` (starts ~offset 450+) |
| **RC-3** | **FR-MG-03** forces `all_content` on single-repo root | Skips structural “folders first” overview |
| **RC-4** | **`examples/` indexed** and large | Competes with product code for page budget |
| **RC-5** | UI defaults show deep symbol types | Visual flood even when a few `src/` nodes exist |

*(User wording “`.example/`” maps to repo folder **`examples/`**, not a hidden `.example` dir.)*

---

## Fix Plan (proposed)

### P0 — Unblock finding `src/` (ship with current Load more work)

1. **Deploy `hasMore` fix + Load more (+200)** so offset ≥ 500 is reachable (already in `feature/ui-v2-service-expand`).
2. **Preferential ordering** for expand queries: sort by path priority  
   `src/` → `ui-v2/` / `ui/` → other → deprioritize `examples/`, `benches/`, `benchmark/`, `e2e/`, `target/`.
3. **Optional query flags:** `?prefer=src` or `?exclude_prefix=examples,benches,benchmark`.

### P1 — Correct first paint (structural expand)

4. **Default root expand = structural** (Directory/Folder/File at depth ≤ 1), then drill into `src/` (double-click / breadcrumb).  
   Keep `all=true` as explicit “load symbols under this path”.
5. **Change FR-MG-03:** do **not** auto-force `all_content` on `.` when the client sends pagination; or only auto-force for small graphs (&lt; N elements).
6. UI: **“Focus product code”** control that expands `./src` with `all=true` (or sets prefer/exclude).

### P2 — Index / product policy

7. Document optional indexer excludes for `examples/**`, `benches/**` (opt-in; demos still indexable).
8. Search: boost paths under `./src` over `./examples/**/src/**`.

### Acceptance criteria

- [ ] First expand page for `/workspace` shows **`src` (and top-level dirs)** prominently; examples not &gt;50% of nodes unless user opted in.
- [ ] `hasMore=true` when more than `limit` elements remain; Load more reaches majority-`src` pages.
- [ ] One-click / default path to explore `./src` without paging through examples.
- [ ] RCA + PRD story (e.g. US-UI2-12 / FR-UI2-14) tracked.

---

## Suggested Logging (when implementing)

- expand-service: log `relative_folder`, `all_content`, `limit`, `offset`, `returned`, `has_more`, **top-5 path prefixes**.
- UI: status line `prefixes: examples=N src=M` after expand.

---

## Sidebar note (follow-up)

`FileTreePanel` previously listed **File** nodes only (`buildTree` filtered `elementType === file`).  
Folders (`Directory` / `Folder`) never appeared, so users could not jump to `src/` from the left rail.

**Fix:** hierarchical **Folders & files** explorer (`buildExplorerTree`) — synthesizes parents from paths, sorts `src` before `examples`, double-click folder → `drillIntoPath`.

