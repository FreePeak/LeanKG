# LeanKG PRD — Unified Product Document

**Version:** 4.1.1-omp-embed-ground-truth
**Date:** 2026-09-04 (v4.1.1 audit revision; v4.1.0 same day)
**Status:** Active Development — **single source of truth** (this document + `docs/prd-task-tracker.md`; all historical documents preserved under [`docs/archive/`](archive/))
**Codebase Version:** 0.26.1
**Storage:** PostgreSQL + pgvector only (`LEANKG_PG_URL`)

---

## Changelog

### v4.1.1-omp-embed-ground-truth — OMP memory + zvec-grep embedding audits (2026-09-04)

> **Trigger:** two file:line audits (2026-09-04). **(1) OMP memory backend** — installed packages with full TS source (`@oh-my-pi/pi-coding-agent`, `@oh-my-pi/pi-mnemopi` v18.0.7): `memory.backend` is a **closed 4-value enum** (`off|local|hindsight|mnemopi`, settings-schema.ts:2932-2952, resolve.ts:16-25) with **no pluggable MCP/URL backend**; hindsight is the remote-HTTP precedent (`hindsight.apiUrl` → `POST /v1/default/banks/{bank_id}/memories` + `/recall` + `/reflect`, hindsight/client.ts:274-340). Mnemopi bank = `<basename(cwd)>-<wyhash36(abs cwd)>` ≤64 chars, derived from **cwd only, never git root** (stability contract #2412, mnemopi/config.ts:168-186); scoping `global|per-project|per-project-tagged` (default per-project; tagged = project write bank + [project, shared] recall); retain every N user turns (default 4) on `agent_end` with a `retained_through_user_turn` integer cursor — **not prefix-hash**; recall injected once on first turn as a `<memories>` block in developer instructions (state.ts:472-490, 914-922; injectionTokenLimit 5000). OMP's MCP client answers server-to-client `roots/list` with `file://<cwd>` (mcp/client.ts:59-64) — the standards-based cwd channel. **(2) zvec-grep v0.2.1 (main@d756cc7)**: every model catalog entry carries HF repo + **40-hex commit revision** (catalog.ts); the embedding schema `{provider, model, dimension, metric}` lives in the workspace manifest with a **hard mixed-model guard** (mismatch → `EMBEDDING_SCHEMA_CHANGE_REQUIRES_REBUILD`, service/zvec-grep.ts:1622+); positional chunk ids `sha256(fileId \0 chunkIndex)`; size+mtime fast-path → SHA-256 content-hash diff; per-hit query-time `fresh|possibly_stale`; hourly reconciliation; single-flight per root (JobScheduler + `{pid, hostname, instanceToken}` lease file).


### v4.1.0-portfolio-scale — Ground-truth storage audit + portfolio scale (2026-09-04)

> **Trigger:** the empty-glass → 1 repo → parent-of-3 → parent-of-100 stress test (2026-09-04 session) plus a file:line ground-truth audit of the storage/resolution model. Headline corrections to v4.0.0 assumptions: storage is **already one PG database with schema-per-project** (`leankg_p_<hex(canonical project root)>` via `project_identity_keys_in`, `src/db/backend.rs:2613-2675`; per-connection `search_path` pinning `:904-908`; all 6 migrations run **per schema** with per-schema ledgers, `src/db/pg/migrations.rs:36-57`/`:83-116`; HNSW per schema) — **not** DB-per-project; no one-DB+`project_id` consolidation is needed. The real 100-repo gaps: **no project registry** (project list is implicit — `.leankg` walks + `LEANKG_PROJECT_DIRS`, `src/mcp/server.rs:1062-1116`) and **zero cross-schema query capability** (only `current_schema()`-scoped UNION ALL, `translate.rs:3217-3224`). Further verified corrections: no `X-LeanKG-Project` header exists in code (v4.0.0 §3.1 wrongly listed it); unresolved projects **silently fall back to the server-default schema** (`server.rs:2987-2989`) — wrong-project data with no error; `LEANKG_AUTO_ATTACH` does not exist; `ensure_project_indexed` runs **awaited inline in the request path** (`server.rs:2579-2694` — blocking, errors swallowed, no freshness flag); the HTTP `initialize` handler returns a static result and never reads client params (`server.rs:3542-3552`); the watcher is single-project-per-process (`server.rs:1585-1596`/`:2015-2026`); recall/diary memory is **JSONL files under `<project>/.leankg/`**, not PG (only `knowledge_entries` is per-schema PG).

> **Decision (D-2026-09-04-1):** the repo is the unit of scope; **a portfolio (a directory of repos) is a scope, never a project**. Growth path: empty DB = registry (attach = one catalog INSERT); 1 repo = nearest-marker resolution (keep the `find_leankg_for_path` walk); parent-of-N cwd with no repo marker = **portfolio scope** with T0 manifest inventory (zero eager indexing, per-child freshness); parent-of-100 = hot-set cap + LRU detach-to-cold, one indexer slot, T0/T1/T2 tiers, cross-schema portfolio queries over the registry, federated portfolio memory. Project identity stays **canonical-path-derived**; any future re-key (e.g. git-remote) MUST follow the `schema_candidates_for_path` preferred+legacy adoption pattern (`backend.rs:2528-2540`, `:2928-2948`) — no dual-write, no row migration.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-11` | **P1** | Embedding correctness ported from zvec-grep: pinned model catalog (commit revision + query/document prefixes), model-stamped vectors + hard rebuild guard, chunker-version coupling, 3-signal change detection, per-file atomic replace + truncation accounting, watcher reconciliation, single-flight indexing | **NOT_DONE** |
| 2 | `FR-ZCP-01` | **P0** | §3.1 HTTP resolution corrected: server-initiated MCP `roots/list` (answers `file://<cwd>`) replaces the dropped `clientInfo.workingDirectory` proposal — no harness sends the latter (OMP initialize params verified) | folded |
| 3 | `FR-ZCP-07` | **P1** | §3.5 rewritten on installed-source ground truth: mnemopi-compatible bank naming/scoping/`retained_through_user_turn` cursor/recall contract + hindsight-shaped HTTP memory API as upstream-evidence artifact | folded |

**v4.1.0's action #3 said "`initialize` must read `workingDirectory`" — superseded:** no harness transmits `workingDirectory` on initialize; the standards-based cwd channel is the server-to-client `roots/list` request.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-09` | **P1** | Project registry + portfolio scope (T0 manifest inventory, per-child freshness) + cross-schema portfolio queries + federated portfolio memory; one indexer slot, hot-set cap, LRU detach-to-cold | **NOT_DONE** |
| 2 | `FR-ZCP-10` | **P2** | Per-schema migration fleet reconciliation + `doctor --deep` drift check across all project schemas | **NOT_DONE** |
| 3 | — | correction | §2/§3.1/§3.2/§4 rewritten on verified file:line ground truth (escape hatch = `?project=` only; silent fallback killed in FR-ZCP-02; background-only indexing; `initialize` must read `workingDirectory`) | folded |

**Portfolio stages this revision answers:**

| Stage | Behavior |
|---|---|
| Empty DB | Registry, not an error: first connection with auto-attach inserts one catalog row keyed by canonical project root, `freshness: cold`, serves empty results, background index |
| 1 repo | Nearest repo marker (`.leankg`/`.git`) wins; existing pipeline unchanged |
| Parent of 3 | cwd has no repo marker → portfolio scope: depth-limited manifest scan (seconds, no tree-sitter); cross-repo questions answered from manifests + `service_calls` + env configs; full graph per child only on first touching query |
| Parent of 100 | Never eager-index: attach = one row; hot-set cap (~8 fully-indexed children) + LRU detach-to-cold (archive, never destroy); single indexer slot; ambiguous portfolio queries degrade to candidate repos + per-child freshness, never block |

### v4.0.0-zero-config-projects — Unified doc set + zero-config project resolution (2026-09-03)

> **Trigger:** Two 2026-09-03 sources: the [zvec-grep audit](archive/analysis/zvec-grep-vs-leankg-2026-09-03.md) (search-layer discipline: 1 default tool, freshness contract, one-command install) and the [OMP memory-integration draft](archive/planning/2026-09-03-leankg-omp-memory-integration-draft.md) (LeanKG wired into OMP via MCP; per-project `?project=` routing is the top ergonomic failure). Cross-checked against the mnemopi / Mnemosyne memory backend (oh-my-pi): **banks derived automatically from the working directory** — `per-project` scoping derives a project bank from the cwd basename + stable hash of the absolute path; `per-project-tagged` adds a shared global bank. No URL parameters, no path pinning, no init step in the agent's workflow.

> **Decision (D-2026-09-03-1):** Kill the explicit `?project=` contract. LeanKG resolves the project **from the request context automatically** — exactly how mnemopi derives banks and how zg accepts a bare workspace `root`. The agent never passes a project path; the server maps connection → project via (1) the OMP/OpenCode harness cwd (stdio: process cwd; HTTP: registered harness sessions), (2) a client-declared working directory on the MCP `initialize` handshake, (3) first-touch auto-attach of the nearest `.leankg`/repo root with **lazy indexing** on first query, (4) explicit URL `?project=` retained only as an escape hatch for remote/multi-tenant deployments.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-01` | **P0** | Contextual project resolution: connection→project mapping (cwd / initialize workingDirectory / registered session), zero URL params | **NOT_DONE** |
| 2 | `FR-ZCP-02` | **P0** | Lazy auto-attach + auto-index: first query in an unindexed repo attaches and indexes in background; queries serve stale-or-empty with freshness flag instead of failing "not initialized" | **NOT_DONE** |
| 3 | `FR-ZCP-03` | **P0** | Default toolset: one intent-expressing router tool; full catalog behind `full` opt-in (merges `FR-ZG-01`) | **NOT_DONE** |
| 4 | `FR-ZCP-04` | **P1** | `leankg install --target` agent wiring incl. URL **without** `?project=` (merges `FR-ZG-04`) | **NOT_DONE** |
| 5 | `FR-ZCP-05` | **P1** | Postgres FTS ranking + RRF fusion (merges `FR-ZG-02`) | **NOT_DONE** |
| 6 | `FR-ZCP-06` | **P1** | Freshness contract in every index-backed response (merges `FR-ZG-03`) | **NOT_DONE** |
| 7 | `FR-ZCP-07` | **P1** | OMP memory-backend adjacency: recall/retain MCP surface (`session_retain`, auto-recall injection) so LeanKG can act as harness memory alongside code-graph memory (extends `FR-SMA-04`) | **NOT_DONE** |
| 8 | `FR-ZCP-08` | **P2** | Cross-tool harness hardening (merges `FR-ZG-05`) | **NOT_DONE** |

> **Doc restructure this revision:** all prior docs (66 entries: analyses, reports, plans, PRD v3.8.x history, design/ERD, benchmarks) moved to [`docs/archive/`](archive/). This document is the **one** comprehension document; [`docs/prd-task-tracker.md`](prd-task-tracker.md) is the **one** tracker (done / in-progress / todo). Section numbering below is fresh and self-contained.

---

## 1. Mission

**Stop Burning Tokens. Start Coding Lean.** LeanKG is the **persistent code-graph + org-memory substrate** for AI coding agents: semantic search + structural graph (impact, traceability, incidents) + session memory, exposed over MCP — with **zero configuration at the point of use**: an agent opens a repository and the memory is simply there.

**Positioning (2026-09-03, harness-era):** do not compete with harness-native Glob/Grep/LSP on raw search; zg (1.4k★) validates that flat hybrid retrieval is becoming commoditized. LeanKG's durable moat is the graph and the memory: impact radius, FR→workflow→code traceability, incidents, env conflicts, service graphs, cross-session lessons. Steal competitors' *discipline* (surface minimalism, freshness honesty, eval rigor, zero-config attach), not their *product*.

### 1.1 What we steal, and from whom

| Source | What they teach | LeanKG adoption |
|---|---|---|
| [zvec-grep](https://github.com/zvec-ai/zvec-grep) | One default MCP tool with intent-expressing params; `fresh`/`possibly_stale` on every response; `zg install --target`; `root`-based workspace addressing | `FR-ZCP-03..06`, `FR-ZCP-08` |
| [OMP mnemopi + Hindsight backends](https://github.com/can1357/oh-my-pi) (installed-source audit, 2026-09-04) | Closed `memory.backend` enum (`off\|local\|hindsight\|mnemopi`, no pluggable MCP backend); hindsight = remote HTTP banks (`POST /banks/{id}/memories` + `/recall`, `hindsight.apiUrl`); mnemopi banks = `<basename(cwd)>-<wyhash36(cwd)>` ≤64 chars, **cwd only, never git root** (stability contract #2412); 3-mode scoping (`per-project` default; tagged = project write bank + [project, shared] recall); retain every N user turns (default 4) with a `retained_through_user_turn` cursor — **not prefix-hash**; recall injected once on first turn as a `<memories>` block (injectionTokenLimit 5000); OMP's MCP client answers `roots/list` with `file://<cwd>` | `FR-ZCP-01` (roots/list channel), `FR-ZCP-07` (bank/scoping/cursor/injection contract) |
| Harness-native primitives | Glob/Grep/LSP win raw search; don't fight them | positioning §1 |

### 1.2 Explicit non-goals

- Competing on raw file-chunk search speed with harness-native tools or zg.
- Becoming a chat-persona memory (Mem0/Tencent style); code-graph memory + org memory only.
- Forking OMP's closed `memory.backend` enum — integration is via MCP, never a fork (OMP draft §3).
- Multimodal/PDF/image ingest; managed-rg reimplementation; desktop GUI.

---

## 2. Problem Statement

1. **Context blindness** — agents re-read the same files every session; no memory of impact radius, traceability, or prior insights.
2. **Configuration friction (this revision's P0)** — LeanKG today requires the agent (or its config) to pass an explicit `?project=` path that must match an **initialized** index (`src/mcp/server.rs:636` `resolve_project_db_path`; `find_leankg_for_path` at `:588`). Mismatch → "not initialized" → the agent gives up and greps. Every harness integration draft spends its hardest section on this. mnemopi and zg both demonstrate the alternative: **the working directory is the identity.** Ground truth (2026-09-04 audit) makes it worse: an unrecognized project does **not** fail — it **silently falls back to the server-default schema** (`server.rs:2987-2989`), serving wrong-project data with no error; `LEANKG_AUTO_ATTACH` does not exist, so a repo without `.leankg` is never attached; and resolution re-walks the filesystem on every request with no connection cache.
3. **First-use latency** — `leankg index ./src` takes minutes on large repos; requiring it before first query is a dead-end for lazy adoption.
4. **Tool sprawl** — ~76 MCP tools inflate agent triage; v3.8.5 live audit found 50% failing.
5. **No freshness honesty** — responses carry no staleness signal; agents cannot distinguish current from drifted data.

---

## 3. Functional Requirements

### 3.1 Zero-Config Project Resolution (FR-ZCP-01/02) — **P0, this revision's core**

**Narrative.** Like mnemopi's banks and zg's `root`: the project is derived from context, never typed by the user. The connection IS the scope.

**FR-ZCP-01 — Contextual project resolution (Must Have, P0)**

- Resolution order (first match wins):
  0. **Nearest repo marker wins**: walk up from the request cwd to the nearest `.leankg`/repo root (existing `find_leankg_for_path`, `server.rs:588-605`); that root IS the project. A cwd **outside** any repo marker (a container/workspace parent) resolves to the **portfolio scope**, never to a project (FR-ZCP-09).
  1. **stdio MCP**: process cwd → clause 0.
  2. **HTTP MCP**: harness-registered session mapping (see below) → server-initiated **`roots/list`** (standard MCP server-to-client request — OMP's client answers `file://<cwd>`, `pi-coding-agent/src/mcp/client.ts:59-64` and `manager.ts:#getRoots`; OpenCode does the same; LeanKG MUST ask once at initialize and re-ask on cwd-change capability) → legacy `?project=` (compat, deprecated) → loopback client IP + recent attach table. ~~clientInfo.workingDirectory~~ — **dropped in v4.1.1**: no harness sends it (OMP's initialize params carry only `protocolVersion`/`capabilities`/`clientInfo.name`, client.ts:99-105); `roots/list` is the standards-based equivalent.
- Project identity = **canonical project root** (existing `project_identity_keys_in`, `src/db/backend.rs:2613-2675`; schema `leankg_p_<hex>`, `:2543-2560`) — never a raw cwd hash, so opening a repo from any subdirectory, or after opening its parent portfolio, reuses the same schema. A future re-key (e.g. git-remote identity) MUST use the `schema_candidates_for_path` preferred+legacy adoption pattern (`:2528-2540`, `:2928-2948`) — no dual-write, no row migration.
- Harness session mapping: server keeps a registration table (`cwd → project`) populated by (a) `leankg install --target` writing per-repo MCP config that includes a one-time `register` call, or (b) OMP/OpenCode `session_start` hook calling `leankg_register(cwd)`. **The user never edits URLs.**
- Resolution is **cached per connection** (today there is none — every request re-walks the FS, `:637-661`); invalidation on cwd-change notification (stdio) or re-initialize.
- AC: a fresh agent session with zero config in a new repo gets correct KG answers for that repo; a second repo in the same server gets its own scope; a repo opened after its parent portfolio was opened reuses the existing schema (no re-index); no URL editing anywhere in the flow.

**FR-ZCP-02 — Lazy auto-attach + background first index (Must Have, P0)**

- First query against an unindexed repo: attach immediately (= one registry row once FR-ZCP-09 lands; today a de-facto `.leankg` init), answer from what exists (empty/stale + `freshness: cold`), and kick off **background** indexing (existing watcher + incremental indexer; see FR-ZCP-06).
- **Kill the silent fallback**: an unresolved project MUST error with "unknown project" (or auto-attach per `LEANKG_AUTO_ATTACH`), never route to the server-default schema (`server.rs:2987-2989` does this today — wrong-project data, no error).
- **Never block a query on indexing**: today `ensure_project_indexed` runs awaited inline in the request (`server.rs:2579-2694`, errors swallowed, no flag); move it fully background and surface state via `freshness`.
- Never fail a query with "not initialized" — degrade gracefully (`freshness: cold|possibly_stale|fresh`) and serve zero-verbosity results rather than errors.
- Indexing status surfaces via `mcp_status` and the router tool's preamble (no polling tool needed by default).
- Respect `LEANKG_AUTO_ATTACH=0` opt-out (indexing nothing by default in read-only/shared deployments); default ON for local single-user. This flag does not exist yet — it is introduced by this FR.
- Watcher lifecycle: the watcher is single-project-per-process today (`server.rs:1585-1596`/`:2015-2026`); multi-project attach requires per-project watcher tasks bounded by the same one-indexer-slot budget as FR-ZCP-09.
- AC: `rm -rf .leankg && query "where is auth handled?"` → immediate non-error response + background index completes within existing SLA; second query hits the graph; a query naming a never-seen repo never returns another repo's data.

### 3.2 Default Toolset (FR-ZCP-03) — **P0**

- One default tool (`leankg_context` — router) whose parameters express intent (`semantic`, `lexical`, `impact`, `graph`, `files`); reuses `orchestrate`'s parser + hot-path cache.
- Portfolio-aware (rides FR-ZCP-09): a query resolved to portfolio scope routes to a child when unambiguous, else answers from T0 manifests with per-child freshness — the router is the single surface for both project and portfolio answers.
- Full catalog behind `full` opt-in (CLI flag / env / config); default-set session passes the v3.8.5 probe suite with zero tool-selection errors.
- AC: fresh-session probes resolve via the router with ≤1 tool call for intent + ≤1 follow-up for detail.

### 3.3 Search Discipline (FR-ZCP-05/06) — **P1**

**FR-ZCP-05 — Postgres FTS + RRF fusion (Should Have, P1)**

- `tsvector` + GIN on `code_elements(name, qualified_name)` + `knowledge_entries(title, content)`; `websearch_to_tsquery`; RRF-fused with vector scores in `semantic_search`'s dual path (single parameterized `k`); substring/`ILIKE` only as exact escape hatch.
- AC: lexical anchor queries rank real identifiers above noise; no F1 regression on the cross-tool suite.

**FR-ZCP-06 — Freshness contract (Should Have, P1)**

- Every index-backed response carries `freshness: fresh|possibly_stale|cold`; `cold` = attached but not yet indexed (FR-ZCP-02 state).
- Background reconciliation (watcher-maintained; burst-limit fix already in `src/mcp/watcher.rs`) flips the flag; **heavy work never shares the request transaction** (lesson of the pre-PG v3.8.4 LOCK-poison incident).
- AC: forced drift → next response says `possibly_stale` and self-heals without blocking the query.

### 3.4 Agent Onboarding (FR-ZCP-04) — **P1**

- `leankg install --target opencode|claude|codex|cursor|omp`: idempotent MCP-config writer — **URL contains no `?project=`** (FR-ZCP-01 makes it unnecessary); Docker deployments emit container-mount guidance; optional `--register-cwd` writes the session-registration hook.
- AC: install in a repo → fresh agent session → tools work, correct project, zero manual URL edits.

### 3.5 Memory-Backend Adjacency (FR-ZCP-07) — **P1**

LeanKG as harness memory **via MCP** (no fork of OMP's closed `memory.backend` enum — verified v4.1.1: the enum `off|local|hindsight|mnemopi` is a closed switch, `pi-coding-agent/src/memory-backend/resolve.ts:16-25`, with no URL/adapter setting). Dual integration target:

1. **Mimic the mnemopi MCP surface** so LeanKG can stand in for `mnemopi mcp` (stdio, 22 tools, per-request `bank` arg falling back to `MNEMOPI_MCP_BANK`, `pi-mnemopi/src/mcp-tools.ts:284-395, 425-427`). LeanKG already serves MCP over HTTP; it exposes a compatible subset and honors the same conventions:
   - **Bank naming**: `sanitize(basename(cwd)) + "-" + wyhash36(abs cwd)`, ≤64 chars, `[A-Za-z0-9_-]` (`mnemopi/config.ts:176-186, 253-263`) — **derived from cwd only, never the git root** (upstream bug #2412: git-root resolution fragmented banks when a `.git` appears/disappears). LeanKG's canonical-root project identity (FR-ZCP-01) is the stable superset; the bank alias is computed for compatibility.
   - **Scoping matrix** (`computeMnemopiBankScope`, `mnemopi/config.ts:128-161`): `global` (write+read shared) / `per-project` default (write+read project) / `per-project-tagged` (write project, read [project, shared] merged + deduped). Cross-project recall is never implicit. Store `cwd` in memory metadata — it is load-bearing for OMP's legacy-bank rescue scan.
   - **Retain contract**: incremental transcript text in `[role: user]\n…\n[user:end]` framing (only user/assistant plain-text turns), one row per batch with `source="coding-agent-transcript"`, importance 0.65, metadata `{session_id, source_id: "<sessionId>-<ms>", message_count, retained_through_user_turn, cwd}` — LeanKG MUST persist and honor the **`retained_through_user_turn` integer cursor** (idempotency: re-retain with the same cursor is a no-op; sessions resume without re-retaining). Retain cadence belongs to the harness (default every 4 user turns on `agent_end`); LeanKG never re-frames or re-chunks what it is told to retain.
   - **Recall/injection contract**: ranked `{id, content, source, timestamp, score}` list — OMP renders it as the `<memories>` block appended to developer instructions on the first turn, capped by `recallLimit` (8) and `injectionTokenLimit` (5000 tokens), query composed from the prompt + last 3 turns truncated to 4000 chars (`mnemopi/state.ts:472-490, 914-922`). LeanKG's `session_retain` (FR-SMA-04) + `get_overview_context(recall=true)` + ranked-lesson read path (FR-SMA-01..03) are the implementation; the AC below is the mnemopi-parity gate.
   - Tool names LeanKG exposes for stand-in use: `session_retain`, `session_recall` (mnemopi-shaped args: `query`, `limit`, `bank`), plus id-stable `memory_get`/`memory_update`/`memory_forget`/`memory_invalidate` mirrors of `mnemopi_get/update/forget/invalidate`.
2. **Hindsight-shaped HTTP memory API** (evidence base for an upstream OMP proposal): hindsight proves the harness can use a **remote HTTP memory** (`hindsight.apiUrl` → `POST /v1/default/banks/{bank_id}/memories` + `/memories/recall` + `/reflect`, `hindsight/client.ts:274-340`; real `project:<name>` retain/recall tags in `per-project-tagged`, `hindsight/bank.ts:30, 95-103`). LeanKG exposes `POST /api/v1/memory/{bank}/retain|recall|reflect` on its existing axum server with mnemopi-identical payload semantics; with that artifact, propose upstream `memory.backend: "mcp"` + URL setting (the `MemoryBackend` interface is already backend-agnostic and non-throwing, `memory-backend/types.ts:80-166`) — upstream PR, not a fork (§1.2 non-goal).

- AC (mnemopi parity): an OMP session configured with LeanKG's memory endpoint retains at the same cadence boundaries and recalls the lesson on the next session's first turn inside a `<memories>`-equivalent envelope, with the turn cursor preventing duplicate retention after resume; deleting the project bank never leaves the harness's cursor claiming rows that no longer exist (cursor + rows agree).
- AC (regression): retain → new session → recall injects the lesson (currently injects nothing — the v3.8.8 audit finding).

### 3.6 Benchmark Rigor (FR-ZCP-08) — **P2**

- Harden `benchmarks/cross_tool/` (existing 7-repo WITH/WITHOUT harness): pinned repo SHAs + prompt versions, ≥3 trials/arm with variance, judge-blind scorer; adopt the zg pitfalls checklist (leakage / like-for-like / stochasticity / tool-access smoke).

### 3.7 Portfolio Scale (FR-ZCP-09/10) — **P1/P2, org-scale moat**

**Narrative.** The empty glass → 1 repo → parent of 3 → parent of 100 progression (2026-09-04 stress test). Storage is already one PG database with schema-per-project — the missing pieces are a registry, a portfolio scope, and cross-schema reads.

**FR-ZCP-09 — Project registry + portfolio scope + cross-schema queries (Should Have, P1)**

- **Registry table** (`public.leankg_projects`): one row per attached project (canonical root, schema name, freshness tier, last-indexed, indexer state). Attach = INSERT; detach = archive. Today the project list is implicit (`.leankg` walks + `LEANKG_PROJECT_DIRS`, `server.rs:1062-1116`) — the registry becomes the project SoT.
- **Portfolio scope**: a cwd with no repo marker resolves to the portfolio, never a project. Portfolio behavior: depth-limited manifest scan (T0 — seconds, no tree-sitter), per-child freshness from the registry, cross-repo questions answered from manifests + `service_calls` + env configs. Zero eager indexing of children; a full index is strictly earned by the first touching query.
- **Index tiers**: T0 manifest inventory / T1 declarations-only shallow graph / T2 full graph + embeddings. Budget: **one background indexer slot** (same bounded-jobs discipline as the repo build rules), queue ordered by recency; hot-set cap (~8 fully-indexed children) with **LRU detach-to-cold** — detach archives graph data and keeps the registry row; an earned index is never silently destroyed.
- **Cross-schema portfolio queries**: schema-qualified UNION ALL / dynamic SQL over the registry on a catalog connection (no cross-schema capability exists today — only `current_schema()`-scoped UNION ALL, `translate.rs:3217-3224`). Cap fan-out (`max_repos_per_query`); ambiguous portfolio queries degrade to candidate repos + per-child freshness, never block.
- **Memory federation**: per-repo recall/diary banks (JSONL files under each `<project>/.leankg/` — file-based, verified) + one portfolio bank; recall merges both, writes never mix scopes (mirrors mnemopi `per-project-tagged`; folds in FR-SMA-05's git-common-dir worktree sharing).
- **Search-path hardening**: unqualified tables currently fall through to `public` (cross-tenant leak vector if DDL is ever shared); portfolio/catalog connections must qualify schemas explicitly.

- AC: attach 100 repos → registry has 100 rows, zero indexing started; one touching query indexes exactly one child in the background; a portfolio query returns candidates with per-child freshness < 500 ms; detach keeps cold data restorable.

**FR-ZCP-10 — Migration fleet reconciliation (Could Have, P2)**

- All 6 migrations run **per project-schema** with per-schema ledgers (`src/db/pg/migrations.rs:36-57`, `:83-116`) — nothing checks fleet-wide drift. Add a reconciliation pass + `doctor --deep` check: every registered schema at the latest migration version, per-schema HNSW/collection state consistent (`reconcile_vector_dim` semantics, `migrations.rs:124-159`), orphan schemas (registry row without schema or vice versa) reported.
- AC: `doctor --deep --format json` lists per-schema migration versions and flags any drift; a drifted schema is repairable with one command.

### 3.8 Embedding Correctness (FR-ZCP-11) — **P1, ported from zvec-grep**

**Narrative.** zvec-grep (v0.2.1, main@d756cc7) runs local embedding models while keeping vector data **provably correct** — the property LeanKG's embed pipeline currently lacks a guard for: nothing records which model produced a given vector set, so a model upgrade silently poisons the HNSW space. Port zg's machinery onto LeanKG's existing per-model collections (`src/embeddings/registry.rs:4` — collections already split per model; `state.rs:1-21` — content-hash staleness already exists).

**FR-ZCP-11 — Local-embedding vector correctness (Should Have, P1)**

- **Pinned model catalog** (zg: every entry = HF repo + 40-hex commit `revision` + dims + dtype + pooling + normalize + query/document prefixes, `src/engine/models/catalog.ts`): LeanKG's `EmbeddingModelEntry` gains `revision` (commit pin, not semver) and `query_prefix`/`document_prefix` (E5/nomic-style models need paired prefixes; one-sided prefixing silently degrades recall). Same reference ⇒ same vectors for a given release. Unknown id = hard error (today's `set_embed_model` already refuses unknown ids — keep).
- **Model schema stored with the vectors + hard mixed-model guard** (zg: `{provider, model, dimension, metric}` in the workspace manifest; mismatch → `EMBEDDING_SCHEMA_CHANGE_REQUIRES_REBUILD`, `service/zvec-grep.ts:1622+`; dimension-only checks are insufficient): persist the full stamp `{model_id, revision, dimensions, distance, provider}` per schema (a per-schema `leankg_meta` row beside the collections) and **refuse query/append on mismatch** with an explicit `leankg embed --rebuild --model <id>` remediation hint. Current `set_embed_model` runtime switch persists only `model_id` (`.leankg/embed-model.json`) — extend to validate the stored stamp before any embed/search against that collection.
- **Positional chunk keys, chunker-version coupling** (zg: `makeEntityId = sha256(fileId + "\0" + chunkIndex)` — content-independent keys, so content edits change vectors, never keys; chunker change is treated like model change: explicit rebuild): LeanKG's `embedding_state` is keyed by `qualified_name` (already positional/stable — keep); add a `chunker_version` to the model stamp; re-extraction schema change ⇒ mark collection `requires_rebuild` like a model change, never mix chunk generations under one ANN index.
- **Three-signal file change detection** (zg: size+mtime fast-path skip → SHA-256 content-hash confirm; per-hit query-time `fresh|possibly_stale` = `indexedTime >= mtime` OR rehash matches): the indexer's staleness marking (`mark_stale_if_changed`, `state.rs:218+`) gains the hash-confirm fallback so git-checkout mtime churn does not spuriously re-embed, and touches do not fake freshness (rides FR-ZCP-06's per-response flag).
- **Per-file atomic replace + truncation accounting + batch validation** (zg: mark dirty → delete-by-file → batch upsert → single optimize; `truncated_fragment_count` per file surfaced in status; every embedding batch validated for count/dimension/finiteness at the model boundary): `embed` processes per file; over-long inputs are counted and surfaced via `mcp_status` instead of silently truncated; imported vectors (`embed --import`) are validated against the active model's dims before entering the collection.
- **Watcher-miss insurance** (zg: hourly full reconciliation + sleep/wake resume checks + watcher-burst compaction with forced full reconcile beyond a path budget; `.gitignore` edit triggers directory rescan): schedule a periodic reconcile probe on the FR-ZCP-02 background indexer; watcher-burst handling reuses the existing burst-limit fix (`src/mcp/watcher.rs`).
- **Single-flight indexing** (zg: `JobScheduler.activeByRoot` coalescing + cross-process `{pid, hostname, instanceToken}` lease file with heartbeat/stale-PID adoption): one index/embed job per project root; concurrent MCP queries attach to the running job instead of duplicating it (FR-ZCP-02's background indexer + FR-ZCP-09's one-indexer-slot budget).
- AC: flipping the model stamp in a project's DB without re-embedding → next `semantic_search` errors with the rebuild hint (never mixed-model results); `touch`-ing a file without content change → rehash confirms fresh, zero re-embed; truncation counter appears in `mcp_status` after indexing a file with an over-budget chunk; two concurrent `embed` runs on one project → one runs, one coalesces.


---

## 4. Architecture (HLD Summary)

| Layer | Today | Zero-config delta |
|---|---|---|
| Transport | axum HTTP MCP (`/mcp`) + stdio; Bearer + DB token store; `?project=` routing | Resolution layer **in front of** routing (FR-ZCP-01); `?project=` demoted to escape hatch |
| Storage | PostgreSQL + pgvector, **one database, schema-per-project** (`leankg_p_<hex(canonical root)>`; per-connection `search_path` pin, per-schema migrations + HNSW) | Registry table + portfolio scope + cross-schema portfolio queries (FR-ZCP-09); fleet reconciliation (FR-ZCP-10) |
| Indexing/embeddings | tree-sitter graph + optional embeddings (`--features embeddings`), incremental watcher — single-project-per-process, inline `ensure_project_indexed`; per-model collections exist but **no model stamp/guard** on the vectors | Lazy auto-attach + **background** first index (FR-ZCP-02); tiers T0/T1/T2 + one indexer slot + hot-set LRU (FR-ZCP-09); pinned catalog + model-stamped vectors + rebuild guard + single-flight (FR-ZCP-11) |
| Tools | ~76 tools, `orchestrate` router exists | Default toolset = 1 router (FR-ZCP-03); portfolio-scoped answers from T0 manifests |
| Memory | RecallStore = **JSONL files** under `<project>/.leankg/` (read path complete; write path dead — v3.8.8 audit); `knowledge_entries` per-schema PG | `session_retain` + auto-recall live (FR-ZCP-07, rides FR-SMA-01..04); portfolio memory federation (FR-ZCP-09) |

**Trust boundaries unchanged:** loopback-only HTTP by default; Bearer auth independent of any remote embedding (LeanKG has no remote embedding).

---

## 5. Milestones

| Milestone | Scope | Gate |
|---|---|---|
| **M1 — Zero-config attach** | FR-ZCP-01, FR-ZCP-02 | New repo, zero config → correct answers; no "not initialized" failures |
| **M2 — One-tool surface** | FR-ZCP-03 (+04) | Default set = 1 tool; v3.8.5 probe suite passes; `install --target` writes project-less URLs |
| **M3 — Honest search** | FR-ZCP-05, FR-ZCP-06 | FTS ranking + freshness in every response; no F1 regression |
| **M4 — Harness memory** | FR-ZCP-07 (rides FR-SMA-01..04) | retain → recall round-trip works in OMP + OpenCode sessions; mnemopi-compatible bank/cursor contract verified against OMP session resume |
| **M5 — Defensible evidence** | FR-ZCP-08 | Pinned, ≥3-trial, judge-blind cross-tool report published |
| **M6 — Org-scale portfolio** | FR-ZCP-09, FR-ZCP-10 | 100-repo parent: registry rows ≠ indexed repos; portfolio queries answer from manifests; no eager indexing; `doctor --deep` reports fleet drift |
| **M7 — Embedding correctness** | FR-ZCP-11 | Model-stamp mismatch → explicit rebuild error, never mixed-model results; rehash-confirms-fresh; truncation accounting in `mcp_status` |

Order: M1 → M2 → M3 → M4 → M5 → M6 → M7. M1 and M2 are the adoption blockers; M3/M4 are quality gates; M5 is evidence; M6 is the org-scale moat; M7 hardens the embedding layer that M6's tiers depend on.

---

## 6. Non-Functional Requirements

| Metric | Target |
|---|---|
| Project resolution overhead | < 5 ms per connection (cached) |
| First-query-on-unindexed-repo | Non-error response < 500 ms; background index per existing SLA |
| Default toolset surface | ≤ 1 tool in `agent` toolset; full catalog behind explicit opt-in |
| Freshness honesty | 100% of index-backed responses carry `freshness` |
| Storage | PostgreSQL + pgvector only — one database, schema-per-project; the registry is the project SoT |
| MCP HTTP | loopback by default; Bearer + DB-backed access-token store |
| Portfolio attach (T0) | One registry INSERT + depth-limited manifest scan; zero eager indexing |
| Portfolio query (unindexed children) | Candidate repos + per-child freshness < 500 ms; fan-out capped (`max_repos_per_query`) |
| Model-stamp integrity | 100% of vector collections carry `{model_id, revision, dimensions, distance, provider}`; mismatch → explicit rebuild error on query/append, never mixed-model results |
| Embedding batch validation | 100% of embed/import batches validated (count, dimension, finiteness); truncations counted and surfaced via `mcp_status` |
| Single-flight indexing | ≤1 active index/embed job per project root; concurrent requests coalesce |

## 7. Historical Record

All superseded material is preserved and linked, not deleted:

- **PRD history v2.0 → v3.8.9** (competitive analyses: Graphify, CBM, TencentDB, MemPalace, Codez, zvec-grep; harness-era repositioning; session-memory audits): [`archive/prd.md`](archive/prd.md)
- **Task-tracker history (560+ items, waves 0–4, release gates):** [`archive/prd-task-tracker.md`](archive/prd-task-tracker.md)
- **zvec-grep audit (2026-09-03):** [`archive/analysis/zvec-grep-vs-leankg-2026-09-03.md`](archive/analysis/zvec-grep-vs-leankg-2026-09-03.md)
- **OMP memory-integration draft (2026-09-03):** [`archive/planning/2026-09-03-leankg-omp-memory-integration-draft.md`](archive/planning/2026-09-03-leankg-omp-memory-integration-draft.md)
- **Design/ERD/architecture docs:** [`archive/design/`](archive/design/), [`archive/erd.md`](archive/erd.md), [`archive/architecture.md`](archive/architecture.md)
- **OMP memory-backend + zvec-grep embedding audits (2026-09-04, installed-source @ `node_modules/@oh-my-pi/*`, zvec-grep main@d756cc7):** findings folded into this PRD (§3.1, §3.5, §3.8); full citations inline

*Last updated: 2026-09-04 (v4.1.1 — OMP memory-backend audit (closed enum; mnemopi bank/cursor/injection contract; roots/list cwd channel) + zvec-grep embedding-correctness audit → FR-ZCP-11; FR-ZCP-01/07 rewritten on installed-source evidence)*
