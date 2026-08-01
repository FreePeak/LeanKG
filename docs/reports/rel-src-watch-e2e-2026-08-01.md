# REL-SRC-WATCH-01 — E2e: fake-gcs change → `watch` re-indexes

**Date:** 2026-08-01  
**Branch:** `prd/remote-source-live-closeouts`  
**PR:** PR-03 (remote-source-live-closeouts)  
**Tracker:** `docs/prd-task-tracker.md` task row 12 / §5.29 (FR-SRC-WATCH)

## Intent

Close REL-SRC-WATCH-01: prove `leankg watch --source gs://… --interval N`
(FR-SRC-WATCH-04) detects a NEW object in a fake-gcs bucket and re-indexes so the new
element becomes queryable, and that watch state persists (FR-SRC-WATCH-05).

## Test added

- **File:** `tests/sources_gcs_e2e_tests.rs`
- **Test:** `cli_watch_gcs_source_reindexes_on_change` (async, Docker-gated)
- **What it does:**
  1. Starts fake-gcs-server, uploads `main.go` (funcs `main`, `add`).
  2. Seeds the graph with the real CLI: `index --source gs://leankg-cli-watch-bucket`
     (also creates `<project>/.leankg` so `watch` can start).
  3. Spawns `watch --source gs://… --interval 1` with cwd = temp project, stdout piped
     to a collector thread; waits for the first `[watch] Indexed` marker
     (first poll: no persisted fingerprint → change detected → index).
  4. Waits 2s (state-persist quiesce), uploads `extra/util.go` (func `Extra`) —
     etag listing changes → fingerprint changes.
  5. Waits for the SECOND `[watch] Indexed` marker (timeout 45s), kills the watcher,
     then opens the DB and asserts `Extra` is a queryable `function` element.
  6. Asserts `.leankg/source_watch_state.json` exists and contains `fingerprint`.

## Commands & timing

```bash
cargo test --test sources_gcs_e2e_tests
# 5 passed; 0 failed; finished in 9.95s   (first run)
# 5 passed; 0 failed; finished in 9.10s   (re-run)
cargo test --test watcher_tests
# 12 passed; 0 failed; 1 ignored; finished in 0.01s
```

Watch-cycle runtime: first poll ≈ 1–2s, change upload + second poll ≈ 3–5s; total test
**≈ 8–10s** — well under the 2-minute bound.

## Assertions

| # | Assertion | Result |
|---|-----------|--------|
| 1 | First watch poll detects initial change and indexes (`[watch] Indexed` ×1) | **PASS** |
| 2 | Upload of new object → second poll re-indexes (`[watch] Indexed` ×2 within 45s) | **PASS** |
| 3 | `search_by_name_typed("Extra", Some("function"))` finds `Extra` after re-index | **PASS** |
| 4 | `.leankg/source_watch_state.json` written with a `fingerprint` (FR-SRC-WATCH-05) | **PASS** |

## Result

**PASS** — REL-SRC-WATCH-01 DONE. Evidence: `cli_watch_gcs_source_reindexes_on_change`,
commit `5c62b16d` (`test(watch): fake-gcs change re-indexes (REL-SRC-WATCH-01)`).
