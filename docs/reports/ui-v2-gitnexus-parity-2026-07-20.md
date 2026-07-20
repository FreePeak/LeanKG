# LeanKG UI v2 — GitNexus shell parity report

**Date:** 2026-07-20  
**Branch:** `feature/ui-v2`  
**Scope:** Phase 1 graph explorer shell only (no browser LLM agent)

## Commands

```bash
cd ui-v2 && npm test
# Vitest: 9 passed

# With leankg serve on :8080:
cd ui-v2 && E2E=1 npx playwright test
# Playwright: 6 passed
```

## Parity matrix (Phase 1 exploring shell)

| Capability | GitNexus proof | LeanKG UI v2 | Result |
|---|---|---|---|
| Connect to local serve | e2e/server-connect | e2e server-connect + connection-status | **Pass** |
| Load graph into exploring | graph load | topology → canvas attached | **Pass** |
| Force / Tree / Circles | e2e/tree-view | layout-* toggles | **Pass** |
| File tree + filters | filter-panel unit | filters-and-filetree e2e + Vitest defaults | **Pass** |
| Mega-graph skip + Load anyway | graph-load-decision unit | Vitest + `?skipGraph=1` e2e | **Pass** |
| Code panel on select | code-references unit | CodePanel wired to `/api/file` | **Pass** (unit/manual; e2e Sigma click deferred) |
| Header search | header tests | search-and-query e2e | **Pass** |
| Query FAB | query path | search-and-query e2e opens panel | **Pass** |
| URL `?project=` | url-restore | Vitest parseProjectParam + useUrlProject | **Pass** |
| Status / reconnect | heartbeat | status-bar e2e; poll index/status | **Pass** (no SSE heartbeat — N/A) |
| Agent chat / analyze / i18n / Processes Mermaid | GitNexus-only | — | **N/A** (Phase 2) |

## Notes

- Sigma requires `allowInvalidContainer` + sized `.sigma-container` (GitNexus/LeanKG shared pitfall).
- Vite `/api` proxy → `127.0.0.1:8080` required for e2e (no CORS on `leankg serve`).
- `/api/index/status` extended with `element_count` / `relationship_count` / `project_path` (cheap `count_*`) for mega-graph gating.
- Legacy `ui/` and `src/embed/` unchanged (no cutover).

## Known gaps vs GitNexus

- No LangChain agent / right-panel chat
- No analyze/upload / repo registry multi-repo delete
- Clusters list not yet a dedicated Processes-style Mermaid panel
- Code-panel e2e does not click Sigma canvas nodes (flaky coords)
