# LeanKG Feature Validation Report - 2026-05-21

## Scope

- Baseline: latest fetched `origin/main`
- Commit validated: `fe5df7b600aa83a65512f320113dbb01c7c50f61`
- Commit subject: `feat: add ontology semantic search layer for agentic queries (#50)`
- Validation worktree: `.worktree/qa-origin-main`
- PRD baseline: `docs/prd.md`, version `3.2-toon-format`
- Runtime: macOS, Rust `1.95.0`, Cargo `1.95.0`, Node `v26.0.0`, npm `11.12.1`

## Executive Summary

Recommendation: **Do not ship as fully validated yet.**

The latest `origin/main` builds successfully in release mode and most Rust feature suites pass, including MCP, CLI, indexing, docs, graph, compression, orchestration, load, and XML/Kotlin/Android tests. However, the full required release test command fails on a project-structure regression. Frontend production build passes, but UI lint and Playwright E2E validation fail. The PRD also still lists several Must/Should/Could items as pending or partial.

## Command Results

| Command | Result | Evidence |
| --- | --- | --- |
| `git fetch origin main` | PASS | Fetched `origin/main`; validated `fe5df7b` |
| `git worktree add .worktree/qa-origin-main origin/main` | PASS | Isolated detached worktree created to avoid local dirty state |
| `mcp_status(project="/Users/linh.doan/work/harvey/freepeak/leankg")` | PASS | LeanKG index initialized and populated |
| `cargo build --release` | PASS | Finished release profile in 3m31s |
| `cargo test --release` | FAIL | `tests/pipeline_integration_tests.rs::test_pipeline_project_structure` failed |
| `cargo test --release --tests -- --skip test_pipeline_project_structure` | PASS | Remaining Rust integration/test binaries passed; one watcher test ignored by design |
| `cd ui && npm ci` | PASS with audit warning | Dependencies installed; 1 moderate vulnerability reported |
| `cd ui && npm run build` | PASS with warning | Vite build completed; large JS chunk warning |
| `cd ui && npm run lint` | FAIL | 22 errors, 2 warnings |
| `cd ui && npm run test:e2e` | FAIL | Playwright timed out waiting for configured web server URL |

## PRD Feature Validation

| PRD Area | PRD Status | Validation Status | Notes |
| --- | --- | --- | --- |
| Core MVP, US-01 to US-13 and US-15 to US-18 | Mostly DONE | Mostly PASS | Rust indexing, docs, CLI, MCP, dependency graph, traceability, and auto-init/index paths are covered by passing suites after skipping the one known failing test. |
| US-14 npm-based installation | PENDING | NOT VALIDATED / GAP | PRD explicitly lists this Must Have feature as pending. |
| v2.0 enhancements, US-19 to US-27 | DONE | PASS | Search, call graph, docs, MCP docs indexing, injection safety, and signature/context behavior have passing Rust tests. |
| GitNexus, US-GN-01 to US-GN-06 and US-GN-09 | DONE | PASS | Impact confidence, detect_changes, global registry, clusters, review context, and wiki/export paths have passing coverage. |
| US-GN-07 cluster SKILL.md generation | PENDING | GAP | PRD explicitly lists pending. |
| US-GN-08 MCP Resources | PENDING | GAP | PRD explicitly lists pending. |
| AB testing, US-AB-01 to US-AB-05 | DONE | PASS | Benchmark parser, quality metrics, data-store tests, and report summary tests passed. |
| RTK compression, US-RTK-01 to US-RTK-10 | DONE | PASS | Compression unit and E2E suites passed. |
| Infrastructure, US-INF-01 to US-INF-10 | DONE | PARTIAL PASS | Hooks, metrics, API key, wiki/export, orchestrator coverage passed; UI/E2E issues remain open for web-facing validation. |
| Additional languages, US-LANG-01 to US-LANG-03 | PARTIAL | PARTIAL / DOC DRIFT RISK | PRD says Dart/Swift/XML are partial. Current tests include Dart extractor tests and XML extraction tests, so the PRD may be stale or the status needs finer wording. |
| Massive Graph, US-MG-01 to US-MG-05 | Mostly DONE, US-MG-02 partial | PARTIAL | UI build passes, but lint and E2E fail. PRD still lists FR-MG-03 pending. |
| TOON, US-TOON-01 | DONE | PASS | MCP responses observed through LeanKG tools use TOON envelope/format. |
| MemPalace-inspired, US-MP-01 to US-MP-08 / FR-MP-01 to FR-MP-26 | PENDING | GAP | PRD explicitly lists this section as pending. |
| Non-functional requirements | TBD | NOT SIGNED OFF | No current automated evidence for cold start, query latency, memory, indexing speed, or detect_changes SLA in this pass. |

## Open Bugs / Findings

### Major: release Rust suite fails on project structure element type

- Failing command: `cargo test --release`
- Failing test: `tests/pipeline_integration_tests.rs::test_pipeline_project_structure`
- Assertion: expected a structure element with `element_type == "Folder"` and `qualified_name == "src"`
- Current implementation evidence: `src/indexer/mod.rs::generate_physical_structure` creates folder nodes with `element_type: "directory"` instead of `"Folder"`
- PRD impact: affects project/folder graph modeling and the folder-as-graph direction in the PRD. This blocks a clean full-suite release validation.

### Major: UI lint is not clean

- Failing command: `cd ui && npm run lint`
- Result: 22 errors, 2 warnings
- Notable issues:
  - `ui/src/components/FileDetailPanel.tsx:72`: conditional hook call
  - `ui/src/components/CodeViewer.tsx:60`: synchronous setState inside effect
  - Multiple `@typescript-eslint/no-explicit-any` errors in UI components and tests
  - Hook dependency warnings in `ui/src/App.tsx`

### Major: UI Playwright E2E cannot start

- Failing command: `cd ui && npm run test:e2e`
- Result: timed out waiting 120000ms for `config.webServer`
- Evidence: `ui/playwright.config.ts` waits for `http://localhost:8080`, but the web server command is `npm run dev`. The dev server did not satisfy that URL in the timeout window.
- PRD impact: blocks automated validation for graph UI interactions, service expansion, and filter behavior.

### Minor: dependency and build hygiene warnings

- `cd ui && npm ci` reported 1 moderate npm audit vulnerability.
- `cd ui && npm run build` passed but emitted a large chunk warning for `dist/assets/index-*.js` over 500 kB.
- Rust tests emitted multiple unused-variable/import warnings.

## PRD Gaps Still Listed As Open

The PRD itself says these features are not complete:

- US-14: npm-based installation without Rust
- US-GN-07: cluster-level `SKILL.md` generation
- US-GN-08: MCP Resources for overview context
- US-LANG-01 to US-LANG-03: Dart, Swift, XML extraction marked partial
- US-MG-02 / FR-MG-03: single-repo root expansion still partial/pending
- FR-MP-01 to FR-MP-26: MemPalace-inspired temporal/layered/context/tunnel/directory features pending
- REST API completion: auth wiring and mutation endpoints still noted as pending
- NFRs: cold start, indexing speed, query latency, memory, detect_changes SLA, and enhanced context size are still `TBD`

## Notes

- The main checkout was dirty before validation, so all execution was done in `.worktree/qa-origin-main` against detached `origin/main`.
- Test execution modified `leankg.yaml` inside the validation worktree; no source changes were made to the validated branch.
- `docs/prd.md` still says codebase version `0.11.1`, while `Cargo.toml` on `origin/main` is `0.17.0`. The PRD version metadata should be updated.

