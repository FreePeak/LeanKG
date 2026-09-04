# DRAFT PRD — LeanKG as the Memory Backend for OMP (Oh My Pi)

**Status:** DRAFT — for review, not yet merged into `docs/prd.md`
**Date:** 2026-09-03
**Scope:** Integration PRD — wiring LeanKG into [OMP](https://github.com/can1357/oh-my-pi) so its knowledge graph serves as the agent's persistent codebase memory.
**Related:** §3.31 harness-era repositioning (`FR-HEA-01..05`), §3.32 session-memory audit (`US-SMA-*`), `analysis/tencentdb-agent-memory-vs-leankg-2026-07-31.md`

---

## 1. Summary

OMP's built-in memory subsystem (`memory.backend` in `~/.omp/agent/config.yml`) supports exactly four backends: `off | local | hindsight | mnemopi`. **There is no extension point to add a fifth backend** — `resolveMemoryBackend()` is a closed enum over compile-time implementations.

However, OMP exposes a first-class MCP layer (`~/.omp/agent/mcp.json`). LeanKG already ships a full MCP server (`mcp-http`) with 70+ knowledge-graph tools. The correct integration is therefore:

> **LeanKG as an always-available MCP server inside OMP** — the agent queries the pre-built knowledge graph on demand instead of re-reading files, making LeanKG the de-facto long-term codebase memory for every OMP session.

This PRD covers the initial enablement (done), the supporting ergonomics that make the model actually *reach for* the tools, and the follow-on work needed for per-project routing.

---

## 2. Background & Problem Statement

### 2.1 What was found (2026-09-03 investigation)

| Fact | Evidence |
|------|----------|
| OMP memory backends are a closed enum (`off/local/hindsight/mnemopi`) | `dist/types/memory-backend/resolve.d.ts`, `settings-schema.d.ts` (`memory.backend`) |
| OMP memory and MCP are **separate subsystems** — no MCP-backed memory bridge exists | `memory-backend/types.d.ts` (self-contained implementations, no MCP adapter) |
| User's `~/.omp/agent/mcp.json` already contained a `leankg` HTTP entry, but `enabled: false` | config inspection |
| LeanKG server running healthy: `leankg mcp-http --port 9699 --project <freepeak> --reuse --read-only` | `ps` + `GET :9699/health` → `{"status":"ok"}` |
| `tools/list` over `?project=<freepeak>` returns the full KG toolset; `mcp_status` → `initialized: true`, index populated (Postgres storage) | live JSON-RPC probes |
| The `?project=` query parameter **must match the server's host path** (here the polyrepo root `freepeak`, not `leankg`) — mismatches return "not initialized" | `AGENTS.md` MANDATORY section + live verification |

### 2.2 Problem

Without wiring, every OMP session re-discovers the codebase from scratch: grep-heavy navigation, repeated reads of the same files, no memory of impact radius, traceability, or prior insights captured in the graph. Meanwhile a fully indexed LeanKG instance sits idle on `:9699`.

### 2.3 Non-goal (explicitly rejected)

Replacing `memory.backend` with LeanKG. This would require a patch to OMP's closed backend enum or an out-of-tree fork. Revisit only if OMP gains a pluggable memory-backend API (tracked in Open Questions, §8).

---

## 3. Goals

| # | Goal |
|--:|------|
| G1 | Every new OMP session has LeanKG KG tools available without manual setup |
| G2 | The model reliably **prefers** LeanKG discovery tools over grep for codebase questions (tool-choice behavior, not just tool presence) |
| G3 | The LeanKG server's lifecycle is decoupled from OMP sessions (server stays up; OMP reconnects) |
| G4 | Project routing is correct and explicit: `?project=` always matches an initialized index |
| G5 | Zero token overhead when LeanKG is down — graceful, silent fallback to default tools |

## 4. Non-Goals

- NG1 — Modifying OMP source (no fork, no upstream PR required for v1)
- NG2 — Write-path integration (agent mutating the graph mid-session) — v1 is read-only (`--read-only` already enforced server-side)
- NG3 — Other agents/harnesses (OpenCode, Cursor) — LeanKG already has `.opencode.json` + plugin wiring; separate effort
- NG4 — Embeddings/semantic tools — orthogonal; governed by existing `--features embeddings` and embed-pipeline work

---

## 5. Proposed Changes

### 5.1 FR-OMP-01 — Enable LeanKG MCP server in OMP config *(DONE in draft)*

Flip the existing entry in `~/.omp/agent/mcp.json`:

```json
"leankg": {
  "type": "http",
  "url": "http://localhost:9699/mcp?project=/Users/linh.doan/work/harvey/freepeak",
  "enabled": true
}
```

**AC-OMP-01:**
1. `omp` (fresh session) lists LeanKG tools (`mcp_status`, `search_code`, `get_context`, `concept_search`, …) among available MCP tools.
2. `mcp_status` returns `initialized: true` for the configured project.
3. No other `mcp.json` entries regress (`be-knowledge-graph` still enabled; `glab`/`ktme` still disabled).

### 5.2 FR-OMP-02 — Usage-hint rule so the model prefers LeanKG

Tool presence ≠ tool usage. Ship a small rules/instructions file in the OMP config layer (equivalent of `instructions/leankg-tools.md` for OpenCode) telling the agent:

- Discover-first prefer-order: `concept_search` → `semantic_search` → `search_code` / `find_function` → connection verbs; never open with `query_graph`.
- Grep is the fallback, not the default, when health is OK.
- Health-gate behavior: if `mcp_status` fails, silently fall back to grep/glob/read (mirror `instructions/using-leankg/SKILL.md`).

**AC-OMP-02:**
1. In a fresh OMP session, a "where is X?" question triggers a LeanKG discovery tool before any grep in ≥ 4 of 5 trials (manual spot-check, aligned with `benchmarks/cross_tool/` methodology where feasible).
2. With the server stopped, sessions complete normally with no user-visible errors (graceful fallback).

### 5.3 FR-OMP-03 — Server lifecycle (auto-start / supervision)

The LeanKG HTTP server must not depend on someone remembering to start it. Options (decide in review):

| Option | Mechanism | Trade-off |
|--------|-----------|-----------|
| A (recommended) | `leankg` already runs `--reuse`; add launchd plist / OMP `ps` supervision for `mcp-http --port 9699` | One more daemon; `--reuse` makes reconnects cheap |
| B | Lazy-start wrapper: stdio server entry (`command: leankg mcp-stdio`) instead of HTTP | No daemon, but loses cross-tool sharing (`--reuse`) and the Docker path |
| C | Status quo (manual start) + health-gated skill | Zero ops, but silent absence when forgotten |

**AC-OMP-03:** After a reboot, an OMP session started within 60s has LeanKG tools available without manual intervention (Option A), or the chosen option's equivalent.

### 5.4 FR-OMP-04 — Per-project routing correctness

`?project=` must always reference an **initialized** index. For v1 the URL pins the polyrepo root (`freepeak`). Follow-up work:

1. Document the mapping table (repo → container/host project path) in the rule file from FR-OMP-02.
2. Evaluate whether OMP's per-project config (`.omp/mcp.json` in a repo) should override the global URL for work outside `freepeak` — note OMP supports project-level `mcp.json` with source precedence (user → project).

**AC-OMP-04:** Working in a repo **without** its own index yields a clear "not initialized" answer from `mcp_status` (not a hang), and the agent falls back to grep per the rule file.

### 5.5 FR-OMP-05 — Session-memory adjacency (forward-looking, P2)

Long-term, LeanKG's session-memory track (`US-SM-01/02`, `FR-SMA-01..04` — recall currently injects nothing) is the natural complement: once LeanKG has its own auto-recall/retain MCP surface, OMP sessions gain conversation-level memory alongside code-graph memory. This PRD does not implement it; it only records the dependency ordering: **FR-OMP-01..04 first, `US-SMA-*` second.**

---

## 6. Rollout Plan

| Phase | Content | Status |
|-------|---------|--------|
| 0 | Config enablement (FR-OMP-01) | **DONE** (2026-09-03; backup at `~/.omp/agent/mcp.json.bak.*`) |
| 1 | Rule file + health-gate instructions (FR-OMP-02) | Pending review |
| 2 | Lifecycle decision + implementation (FR-OMP-03) | Pending review |
| 3 | Per-project routing doc + `.omp/mcp.json` evaluation (FR-OMP-04) | Pending review |
| 4 | Revisit `memory.backend` pluggability if OMP ships an extension API | Backlog |

## 7. Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| ~76-tool MCP surface inflates context / confuses tool choice | Tools are on-demand (no injection cost until called); rule file pins the prefer-order; aligns with `FR-ZG-01` single-router direction |
| Server downtime stalls agent turns on MCP timeouts | Set explicit `timeout` on the server entry; rule file mandates silent fallback |
| `?project=` drift after server restart with different `--project` | FR-OMP-04 mapping doc + `mcp_status` precondition check in the rule file |
| Personal host paths leaking into commits | Rule file and mapping doc live in OMP config dir, not the LeanKG repo (mirrors `AGENTS.md` "never paste personal host paths" rule) |

## 8. Open Questions (for review)

1. **Lifecycle:** Option A, B, or C for FR-OMP-03?
2. **Rule placement:** OMP global config dir vs. a lean repo-level rule — which discovery path does OMP honor first?
3. **Should `be-knowledge-graph` and `leankg` coexist enabled**, or does the remote graph create tool-name ambiguity? (Observed: both enabled today, no name collisions, but worth a deliberate decision.)
4. Does this integration warrant a row in `docs/prd-task-tracker.md` (recommend: yes, as `US-OMP-01..04` under the P2 campaign) once this draft is approved?

---

*Draft ends. Reviewer notes welcome inline.*
