# FR-C08..C11 — Windows / package channel / SLSA / install targets — 2026-08-02

## Scope

FR-C08 (Windows build + smoke), FR-C09 (extra distribution channel:
Homebrew or npm), FR-C10 (release checksums; evaluate
SLSA/attestations), FR-C11 (expand agent install targets where
hooks/skills exist). All "Could Have" per original PRD Track C.

## Status: DEFERRED to P3 (documented, not implemented)

Per campaign plan (`docs/planning/2026-08-01-all-open-prd-campaign.md`,
Wave 2e): "Platform C … (Windows → P3)". No code changes in this batch.

## Per-item disposition

| ID | Requirement | Disposition | Evidence / reason |
|----|-------------|-------------|-------------------|
| FR-C08 | Windows build + smoke | **DEFERRED (P3)** | Rust + RocksDB/Cozo build matrix for Windows untested; smoke harness would need a Windows CI runner. No demand signal in tracker. |
| FR-C09 | Extra distribution channel (Homebrew or npm) | **PARTIAL / historical** | `US-14` npm-based installation wrapper shipped (`df0fec2`, PRD §3.11 evidence). Homebrew formula not published — P3. |
| FR-C10 | Release checksums; evaluate SLSA/attestations | **DEFERRED (P3)** | Release-please owns tags/version bumps; no checksum/SLSA pipeline exists. Security-signal item, P3. |
| FR-C11 | Expand agent install targets where hooks/skills exist | **DEFERRED (P3)** | `leankg install` auto-configures MCP for AI tools (CLI reference); hook/skill expansion is a packaging follow-up. |

## Tracker

- FR-C08..C11: DEFERRED → P3 (campaign Wave 3 `windows-smoke`
  PR-63 candidate)
