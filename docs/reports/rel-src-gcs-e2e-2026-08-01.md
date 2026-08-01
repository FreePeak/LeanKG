# REL-SRC-01 — E2e: `index --source gs://` populates graph (fake-gcs)

**Date:** 2026-08-01  
**Branch:** `prd/remote-source-live-closeouts`  
**PR:** PR-03 (remote-source-live-closeouts)  
**Tracker:** `docs/prd-task-tracker.md` task row 6 / §5.28 (FR-SRC)

## Intent

Close REL-SRC-01: prove the CLI seam `leankg index --source gs://bucket` populates the
knowledge graph (file + function elements) using a `fsouza/fake-gcs-server` Docker
emulator, without real GCS credentials.

## Test added

- **File:** `tests/sources_gcs_e2e_tests.rs`
- **Test:** `cli_index_gcs_source_populates_graph` (async, Docker-gated; auto-skips when
  Docker is unavailable or `LEANKG_GCS_E2E=0`)
- **What it does:**
  1. Starts fake-gcs-server (one-shot Docker container on an ephemeral port,
     `STORAGE_EMULATOR_HOST` pointed at it — same helper infra as the existing tests).
  2. Uploads `main.go` (funcs `main`, `add`) and `lib/math.go` (func `Mul`).
  3. Runs the real CLI binary (`env!("CARGO_BIN_EXE_leankg")`) with
     `index --source gs://leankg-cli-index-bucket` in a temp project dir.
  4. Asserts CLI exit 0 + `Indexed … files` line.
  5. Opens `<project>/.leankg/leankg.db` and asserts `main`/`Mul` are findable as
     `function` elements and `main.go`/`math.go` are findable as `File` elements
     (the query_file/search_code surface).

## Supporting fix (minimal, test-driven)

The first run of the new test exposed a pre-existing defect: `search_by_name` /
`search_by_name_typed` could never match dotted names (i.e. every file) because
`escape_datalog` (src/graph/query.rs) doubled the backslashes that `regex::escape`
produces, while CozoDB passes backslashes through Datalog string literals verbatim.
`main`/`Mul` matched; `main.go`/`math.go` returned zero rows.

- **Fix:** `escape_datalog` no longer doubles `\` (verified against raw Datalog probes:
  `.*main\.go.*` matches, `.*main\\.go.*` does not).
- **Regression test:** `tests/graph_query_tests.rs::test_search_by_name_with_dotted_filename`.

## Commands & timing

```bash
cargo test --test sources_gcs_e2e_tests
# 5 passed; 0 failed; finished in 9.95s   (first run, incl. new tests)
# 5 passed; 0 failed; finished in 9.10s   (re-run)
cargo test --test graph_query_tests
# 7 passed; 0 failed; finished in 0.08s
cargo test --lib
# 744 passed; 0 failed; 3 ignored; finished in 2.37s   (CI gate, unchanged)
cargo clippy --all -- -D warnings   # clean
cargo fmt --all -- --check          # clean
```

Docker emulator boot ≈ 2s; full CLI index of 2 Go files ≈ 1s.

## Assertions

| # | Assertion | Result |
|---|-----------|--------|
| 1 | `index --source gs://…` exits 0 with `Indexed … files` | **PASS** |
| 2 | `search_by_name_typed("main", Some("function"))` finds `main` | **PASS** |
| 3 | `search_by_name_typed("Mul", Some("function"))` finds `Mul` | **PASS** |
| 4 | `search_by_name_typed("main.go", Some("File"))` finds the file element | **PASS** (after escape fix) |
| 5 | `search_by_name_typed("math.go", Some("File"))` finds the file element | **PASS** |
| 6 | All 4 pre-existing GCS e2e tests still pass (no regressions) | **PASS** |

## Result

**PASS** — REL-SRC-01 DONE. Evidence: `cli_index_gcs_source_populates_graph` +
`test_search_by_name_with_dotted_filename`, commit `119a249e`
(`test(sources): gcs e2e populates graph (REL-SRC-01)`).
