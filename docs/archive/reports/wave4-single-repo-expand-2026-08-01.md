# Wave 4: Single-repo expand — live evidence

**Date:** 2026-08-01
**Tracker:** `US-MG-02` (PARTIAL → **DONE**) / `FR-MG-03` (NOT_DONE → **DONE**)
**PRD:** §5.7 FR-MG-03 / §3.8 US-MG-02 (Wave 4)

## Summary

Single-repo projects are treated as a single service: `GET /api/graph/expand-service?path=.` loads the **entire** service tree — all folders, sub-folders, files, and functions — in **one API call**, without any `all=true` parameter (auto-enabled by `detect_single_repo`, `src/web/handlers.rs:2174`). Multi-repo layouts (nested `.git` dirs under root) do **not** get the auto-enable treatment: the root returns only root-level elements, and each nested service loads its own tree on service expansion.

## Setup

```bash
# Worktree build (release)
cd .worktrees/prd/wave4-single-repo-expand
cargo build --release        # 6m27s -> target/release/leankg 0.19.28
```

## Demo 1 — temp single-repo project (AC case: root double-click loads everything)

```bash
mkdir -p /tmp/opencode/wave4-demo/src/{api,util}
# src/main.rs (fn main, compute_total), src/api/orders.rs (Order, create_order, total_amount),
# src/util/helper.rs (double, greet)
leankg init          # writes leankg.yaml, detects src/ (rust)
time leankg index .  # 3 files, 17 elements, 20 relationships — 0.037s
leankg serve --project . --port 8091 &
curl -s "http://localhost:8091/api/graph/expand-service?path=." -o resp.json \
     -w "HTTP %{http_code} | total %{time_total}s | size %{size_download} bytes"
```

**Request (no `all=true` — forcing is automatic):**
```
GET /api/graph/expand-service?path=.
```

**Response:** `HTTP 200 | total 0.001450s | 5508 bytes`

```json
{
  "success": true,
  "data": {
    "nodes": 17, "relationships": 18,
    "hasMore": false,
    "filtered": { "tests_filtered": 0,
      "message": "Expanded service '.' with 17 elements and 18 relationships" }
  }
}
```

Full tree in one call (every folder, file, function — no multi-level drilling):

| type | name | filePath |
|------|------|----------|
| directory | . | . |
| directory | src | ./src |
| directory | api | ./src/api |
| File | orders.rs | ./src/api/orders.rs |
| class | Order | ./src/api/orders.rs |
| function | create_order | ./src/api/orders.rs |
| function | total_amount | ./src/api/orders.rs |
| File | main.rs | ./src/main.rs |
| function | compute_total | ./src/main.rs |
| function | main | ./src/main.rs |
| directory | util | ./src/util |
| File | helper.rs | ./src/util/helper.rs |
| function | double | ./src/util/helper.rs |
| function | greet | ./src/util/helper.rs |
| Project | wave4-demo | /private/tmp/opencode/wave4-demo |

## Demo 2 — this repo (worktree, real single-repo checkout)

```bash
cd .worktrees/prd/wave4-single-repo-expand
./target/release/leankg index ./src   # 180 files, 8423 elements, 50749 relationships
                                      # + docs: 141 documents / 2249 sections (~4.5 min total)
./target/release/leankg serve --project . --port 8092 &
curl -s "http://localhost:8092/api/graph/expand-service?path=." \
     -w "HTTP %{http_code} | total %{time_total}s | size %{size_download} bytes"
```

**Request (same shape — no `all=true`):**
```
GET /api/graph/expand-service?path=.
```

**Response:** `HTTP 200 | total 1.009081s (first call incl. cold DB open) | 263315 bytes`

| metric | value |
|--------|------:|
| nodes (page 1) | 500 |
| relationships (page 1) | 727 |
| hasMore | true (default page 500; full graph ~8.4k elements, ui-v2 load-more pages) |
| page 2 (`&offset=500&limit=500`) | 500 nodes in 0.754s |

Page-1 element types: `directory` 4, `File` 13, `class` 46, `function` 161, `property` 276 — nested `./src/*` content returned from the root, confirming the root is treated as the service.

## Multi-repo contrast (from integration tests)

| layout | root `path=.` without `all=true` | result |
|--------|----------------------------------|--------|
| single-repo (no nested `.git`) | auto `all_content` | full tree, one call |
| single-repo (only root `.git`) | auto `all_content` | full tree, one call |
| multi-repo (root `.git` + `svc-a/.git` + `svc-b/.git`) | no auto-enable | root-level elements only; `svc-a` expands its own tree |

## Regression coverage added

`tests/expand_service_single_repo_tests.rs` (real CLI + HTTP, 3 tests, all green):

| test | asserts |
|------|---------|
| `single_repo_root_expand_loads_entire_tree_in_one_call` | `path=.` without `all=true` returns `./src/main.rs`, `./src/util/helper.rs`, fns `main` + `helper` |
| `multi_repo_root_expand_does_not_dump_nested_services` | root expand excludes `./svc-a/src/lib.rs`; `svc-a` expansion still returns it |
| `single_repo_root_with_only_root_git_still_loads_entire_tree` | normal checkout (root `.git` only) behaves as single-repo |

Plus existing `fr_mg_03_tests` unit tests for `detect_single_repo` (3, green) and ui-v2 `normalizeExpandPath` tests (`test/unit/camera-fit-expand-path.test.ts`, `parity.test.ts` — 35/35 ui-v2 tests green).

## Gates

| gate | result |
|------|--------|
| `cargo fmt --all -- --check` | pass |
| `cargo clippy --all -- -D warnings` | pass |
| `cargo test --lib` | 744 passed / 3 ignored |
| `cargo test --bin leankg fr_mg_03` | 3 passed |
| `cargo test --test expand_service_single_repo_tests` | 3 passed |
| `cd ui-v2 && npm test` | 35 passed (7 files) |
| `cd ui-v2 && npx tsc -b` | pass |

## Tracker rows updated

- `US-MG-02` PARTIAL → **DONE**
- `FR-MG-03` NOT_DONE → **DONE**
- Waves table row 4 → **DONE** (evidence: this report)
- prd.md §1.1 status line: P1 Wave 4 DONE; P2 CURRENT next = `US-SM-01`
