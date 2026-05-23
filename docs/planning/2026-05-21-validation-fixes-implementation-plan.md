# Validation Fixes Implementation Plan - 2026-05-21

## Overview

This plan addresses the validation failures found in `docs/analysis/feature-validation-report-2026-05-21.md` for latest fetched `origin/main` at commit `fe5df7b600aa83a65512f320113dbb01c7c50f61`.

Goal: restore a clean release validation path for LeanKG by fixing the release Rust test failure, frontend lint failures, UI E2E startup failure, and stale PRD status metadata.

Recommendation: implement these fixes in a dedicated branch or isolated worktree from latest `origin/main`, then re-run the full validation suite before merging.

## Problem Statement

The latest validation pass is blocked by three Major findings:

- `cargo test --release` fails in `tests/pipeline_integration_tests.rs::test_pipeline_project_structure`.
- `cd ui && npm run lint` fails with 22 errors and 2 warnings.
- `cd ui && npm run test:e2e` cannot start because Playwright waits for `http://localhost:8080`, but `npm run dev` does not satisfy that URL.

There are also hygiene and documentation issues:

- `ui/npm ci` reports one moderate audit vulnerability.
- `ui/npm run build` emits a large chunk warning.
- Rust tests emit unused-variable/import warnings.
- `docs/prd.md` still reports codebase version `0.11.1` while `Cargo.toml` reports `0.17.0`.
- PRD status for language extraction appears stale or too coarse, because current tests cover Dart extractor behavior and XML extraction while the PRD still marks some language items as parser-only partial.

## Scope

### In Scope

- Fix physical structure element type consistency for folder/directory nodes.
- Update or clarify tests so the expected graph schema matches the PRD and implementation contract.
- Clean UI lint violations in app code and tests.
- Fix Playwright web server configuration so UI E2E starts reliably.
- Update PRD metadata and validation status notes that are provably stale.
- Add targeted regression coverage where gaps caused the validation failure.
- Re-run release Rust, UI build/lint, and UI E2E validation.

### Out of Scope

- Implementing pending product features listed in the PRD, such as npm binary distribution, MCP Resources, cluster-level `SKILL.md` generation, MemPalace temporal graph features, or REST mutation endpoints.
- Large UI redesign or visual changes unrelated to lint/E2E correctness.
- Performance optimization for the Vite chunk-size warning unless it becomes a release requirement.

## Requirements And Acceptance Criteria

| ID | Requirement | Acceptance Criteria |
| --- | --- | --- |
| AC-1 | Full Rust release test suite is clean | `cargo test --release` passes without skipping `test_pipeline_project_structure`. |
| AC-2 | Folder/directory graph schema is explicit and stable | `generate_physical_structure` produces element types expected by downstream search, UI, and PRD. Tests cover the chosen casing/name. |
| AC-3 | UI lint is clean | `cd ui && npm run lint` exits 0. |
| AC-4 | UI production build remains clean | `cd ui && npm run build` exits 0. Existing large chunk warning is documented or addressed. |
| AC-5 | UI E2E starts and runs | `cd ui && npm run test:e2e` exits 0 locally, or failures are real test assertions rather than web server startup timeout. |
| AC-6 | PRD reflects current version and feature status | `docs/prd.md` version metadata matches `Cargo.toml`, and language/UI statuses match validated evidence. |
| AC-7 | Validation report can be regenerated | A follow-up validation report records the fixed commit, commands, and pass/fail evidence. |

## Design Decisions

### Folder Element Type Contract

Current failure:

- Test expects `element_type == "Folder"` for `src` and `src/utils`.
- Implementation in `src/indexer/mod.rs` creates folder elements as `element_type: "directory"`.
- PRD data model describes `directory` as a first-class graph element, while UI/graph docs and tests still refer to `Folder`.

Recommended contract:

- Keep persisted backend element type canonical as `directory`, matching the PRD data model and MemPalace folder-as-graph direction.
- Update tests and UI label mapping to treat `directory` as the backend type and `Folder` as a display label.
- Add a compatibility helper if existing UI/API consumers still send or expect `Folder`.

Rationale: using `directory` in storage avoids mixing UI labels with schema values. The UI can still display "Folder" without forcing backend casing.

## Implementation Work Packages

### WP-1: Restore Rust Release Test Pass

Files:

- `src/indexer/mod.rs`
- `tests/pipeline_integration_tests.rs`
- Any graph/UI adapter tests that assert folder element type strings

Steps:

1. Confirm all current usages of folder-like element type values: `directory`, `Folder`, `folder`, and `Service`.
2. Choose canonical backend type `directory` and document it in code comments only where it prevents future regressions.
3. Update `tests/pipeline_integration_tests.rs::test_pipeline_project_structure` to assert `directory` for structure nodes, unless a stronger compatibility reason requires changing implementation back to `Folder`.
4. Add a regression assertion that generated relationships still form the intended hierarchy: project to `src`, `src` to `src/utils`, `src` to `src/app.rs`, and `src/utils` to `src/utils/math.rs`.
5. Run targeted verification:
   - `cargo test --release test_pipeline_project_structure`
   - `cargo test --release --test pipeline_integration_tests`
6. Run full release verification:
   - `cargo test --release`

Risk:

- If existing persisted databases contain `Folder`, search/UI behavior may need a transitional normalization layer.
- If MCP clients consume raw `element_type`, changing expected type semantics should be documented in `docs/prd.md`.

### WP-2: Clean UI Lint Errors

Files:

- `ui/src/components/FileDetailPanel.tsx`
- `ui/src/components/CodeViewer.tsx`
- `ui/src/hooks/useSigma.ts`
- `ui/src/App.tsx`
- `ui/tests/click-behavior.spec.ts`
- `ui/tests/hierarchy.spec.ts`
- `ui/tests/service-navigation.spec.ts`

Steps:

1. Fix conditional hook order in `FileDetailPanel.tsx`.
   - Move all hooks before early returns.
   - Derive `fileNode`, `fileProps`, and `filePath` safely when selection is absent.
   - Keep returned UI `null` only after hooks have executed.
2. Fix `CodeViewer.tsx` set-state-in-effect warning.
   - Avoid synchronous `setContent(null)` inside the effect, or derive absent-file display state from `filePath`.
   - Keep async fetch cancellation behavior.
3. Replace broad `any` usage with concrete local types.
   - Prefer existing `KGNode`, `KGEdge`, Sigma event types, or narrow `Record<string, unknown>` wrappers.
   - For test-only window extensions, define explicit test interfaces instead of `any`.
4. Fix hook dependency warnings in `App.tsx`.
   - Stabilize callbacks with `useCallback` where needed.
   - Remove dependencies that are demonstrably unnecessary only when the closure is safe.
5. Remove unused test helper in `ui/tests/hierarchy.spec.ts` or wire it into assertions.
6. Run:
   - `cd ui && npm run lint`
   - `cd ui && npm run build`

Risk:

- Hook-order fixes can subtly change when file content is fetched. Use E2E and manual UI smoke testing to confirm selected file panels still render correctly.

### WP-3: Fix UI Playwright Startup

Files:

- `ui/playwright.config.ts`
- `ui/package.json`
- Possibly `ui/vite.config.ts`

Steps:

1. Align Playwright `webServer.url` with the Vite dev server.
   - Option A: change Playwright base URL to `http://localhost:5173` and keep `npm run dev`.
   - Option B: make the web server command run Vite on port `8080`, for example through the dev script or explicit config.
2. Prefer one source of truth for the test URL.
   - Use a stable `baseURL`.
   - Avoid a mismatch between `baseURL`, `use.baseURL`, and `webServer.url`.
3. Verify the app can reach API mocks used by tests.
   - If tests mock all API routes, Vite-only is sufficient.
   - If tests need the Rust backend, use a separate E2E profile and start `target/release/leankg serve`.
4. Run:
   - `cd ui && npm run test:e2e`
   - If browser dependencies are missing, run the project-approved Playwright install command and document it.

Recommended first implementation:

- Set Playwright to `http://localhost:5173`.
- Keep backend-dependent tests separate from mocked UI tests.

Risk:

- Some tests may accidentally depend on backend routes if route mocks do not cover all calls. Failures after startup should be treated as real test gaps, not config failures.

### WP-4: PRD And Report Alignment

Files:

- `docs/prd.md`
- `docs/analysis/feature-validation-report-2026-05-21.md` or a new follow-up report

Steps:

1. Update PRD codebase version from `0.11.1` to the current `Cargo.toml` version when validating the current release branch.
2. Clarify language extraction statuses:
   - Dart has extractor tests in `src/indexer/extractor.rs`; verify real extractor support before changing status from partial.
   - XML has extraction tests in `tests/xml_extraction_tests.rs`; distinguish generic XML, Android XML, and parser-only support.
   - Swift should remain partial unless extractor implementation and tests exist.
3. Clarify folder-as-graph terminology:
   - Backend element type: `directory`.
   - UI display label: `Folder`.
4. Add a short validation appendix or link to the follow-up report.

Risk:

- PRD changes should not overclaim features. Only mark DONE when implementation and repeatable tests exist.

### WP-5: Dependency And Warning Hygiene

Files:

- `ui/package-lock.json`
- Rust tests with unused imports/variables

Steps:

1. Run `cd ui && npm audit`.
2. Determine whether the moderate vulnerability affects production or dev-only dependencies.
3. If safe, run `npm audit fix` and review lockfile changes.
4. Clean Rust unused warnings in touched tests only, unless full warning cleanup is explicitly included in the sprint.
5. Re-run targeted commands after changes.

Risk:

- `npm audit fix` may upgrade transitive packages and change UI behavior. Keep this separate from lint/E2E fixes if the lockfile diff is large.

## Suggested Execution Order

1. Create isolated branch/worktree from latest `origin/main`.
2. WP-1: fix Rust release test failure first.
3. WP-2: fix UI lint errors.
4. WP-3: fix Playwright startup and run E2E.
5. WP-4: update PRD and produce a follow-up validation report.
6. WP-5: handle dependency audit and warnings if time remains.

This order restores the hard validation gates before documentation cleanup.

## Verification Plan

Run these commands from the repository root unless noted.

```bash
cargo build --release
cargo test --release test_pipeline_project_structure
cargo test --release --test pipeline_integration_tests
cargo test --release
cd ui && npm ci
cd ui && npm run lint
cd ui && npm run build
cd ui && npm run test:e2e
```

Additional verification:

- Confirm `docs/prd.md` version matches `Cargo.toml`.
- Confirm UI E2E startup no longer times out on `config.webServer`.
- Confirm final validation report marks `cargo test --release`, `npm run lint`, and `npm run test:e2e` with current evidence.

## Rollback Plan

- Rust schema/test fix: revert only the commit touching `src/indexer/mod.rs` and `tests/pipeline_integration_tests.rs`.
- UI lint/E2E fix: revert UI-only commit if tests regress.
- PRD/report update: revert documentation commit independently.

Keep these changes in separate commits so rollback is precise.

## Done Checklist

- [ ] `cargo build --release` passes.
- [ ] `cargo test --release` passes without skips.
- [ ] `cd ui && npm run lint` passes.
- [ ] `cd ui && npm run build` passes.
- [ ] `cd ui && npm run test:e2e` passes.
- [ ] `docs/prd.md` version and status claims are updated conservatively.
- [ ] Follow-up validation report is written with exact commit and command evidence.

