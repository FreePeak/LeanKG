# Tracker: context propagation and interface redesign

**Plan:** [plan-context-and-interfaces-refactor.md](plan-context-and-interfaces-refactor.md) · **Last updated:** 2026-11-13 · **Base:** 6b4b741e

## How to use

- One row per PR.
- The PR that lands an item flips its own row to `done` and fills the PR link column in the same diff.
- Status values are exactly `todo`, `in progress`, `in PR`, `done`, `dropped`.
- A PR that creates, moves or deletes a file also updates the ontology code_refs.
- When the programme is done, the plan and this file move to `docs/archive/`.

These IDs may be mirrored into `prd-task-tracker.md` when the owner adopts the plan.

## P0: defect fixes

| ID | PR | Size | Depends | Status | PR link | Notes |
|---|---|---|---|---|---|---|
| CX-01 | `fix(memory): redact before indexing and recall` | S | - | todo | - | - |
| CX-02 | `fix(auth): classify retain, ontology match and auto-config as writes; gate auto-config` | S-M | - | todo | - | - |
| CX-03 | `fix(auth): never return token hashes; revoking needs the owner or an admin` | S | - | todo | - | - |
| CX-04 | `fix(cmd): open every verb's store through one resolver` | M | - | todo | - | blocks CX-18 |
| CX-05 | `fix(rest/auto): refuse before side effects; run jobs on the server context` | S-M | - | todo | - | - |
| CX-06 | `fix(cmd): validate serve first, surface listener errors, await shutdown` | M | - | todo | - | - |
| CX-07 | `fix(cmd): remove the data races on telemetry sinks and the capture recorder` | S | - | todo | - | - |
| CX-08 | `fix(leankg-embed): cancellation is a failure; keep flags after the positional; stop the sidecar on error` | S-M | - | todo | - | - |
| CX-09 | `fix(telemetry): report writer panics; Nop.SetLevel honours its contract` | S | - | todo | - | - |
| CX-10 | `test(index): pin markdown ownership between index and docindex`, plus a fix PR only if it fails | S-M | - | todo | - | - |
| CX-11 | `fix(store): one rare-token bound on both engines` | S | - | todo | - | - |
| CX-12 | `fix(auth): uniform login failure and a login throttle` | S | - | todo | - | - |

## P1: guardrails and contract shape

| ID | PR | Size | Depends | Status | PR link | Notes |
|---|---|---|---|---|---|---|
| CX-13 | `test(archtest): ratchet context roots and ctx-less I/O at today's baseline` | S | - | in PR | pending | internal/archtest ratchet at baseline; AGENTS.md ctx conventions |
| CX-14 | `refactor(store): declare role interfaces; Backend composes them; add Mode` | S-M | - | todo | - | - |
| CX-15 | `refactor(store): the keyword arm is part of the search contract` | M | CX-14 | todo | - | - |
| CX-16 | `fix(store): filter vector and hybrid search before LIMIT` | M | CX-15 | todo | - | behaviour change (PG filters before LIMIT) |
| CX-17 | `refactor(store): engine-neutral Maintain op with one read-only refusal` | M | CX-14 | todo | - | - |
| CX-18 | `refactor(store): OpenBackend takes OpenOptions; AppliedMigrations is a method` | S-M | CX-04 | todo | - | - |

## P2: ctx through the store, bottom-up

| ID | PR | Size | Depends | Status | PR link | Notes |
|---|---|---|---|---|---|---|
| CX-19 | `refactor(store): ctx for Migrate, KV, Inventory, Projects and Summaries` | M | CX-13, CX-14 | todo | - | - |
| CX-20 | `refactor(store): ctx for ElementReader and GraphReader` | M | CX-19 | todo | - | - |
| CX-21 | `refactor(store): ctx for Searcher` | M | CX-16, CX-20 | todo | - | - |
| CX-22 | `refactor(store): ctx for IndexWriter` | M | CX-20 | todo | - | - |
| CX-23 | `refactor(store): ctx for Vectors and EmbedRuns` | M-L | CX-19 | todo | - | split at the role boundary if over 1000 lines |
| CX-24 | `refactor(store): ctx for Maintainer` | S-M | CX-17 | todo | - | - |
| CX-25 | `refactor(store): ctx for Tokens, Identity and Audit` | M | CX-19 | todo | - | - |
| CX-26 | `refactor(store): ctx for Knowledge and Metrics` | M | CX-19 | todo | - | - |
| CX-27 | `refactor(store): delete pgCtx` | S | CX-19..26 | todo | - | - |

## P3: root contexts and TODO burn-down

| ID | PR | Size | Depends | Status | PR link | Notes |
|---|---|---|---|---|---|---|
| CX-28 | `refactor(cmd): one root signal context; index and serve take ctx` | M | - | todo | - | goes first |
| CX-29 | `refactor(cmd): every verb takes ctx and returns an error` | M-L | - | todo | - | - |
| CX-30 | `refactor(core,graph,mcp): thread ctx through the rungs, graph and status` | M | - | todo | - | - |
| CX-31 | `refactor(rest,rpc,web,dashboard,auth): use the request context` | M | - | todo | - | - |
| CX-32 | `refactor(index,lsp,langs,tstree): cancellable extraction and LSP` | M | - | todo | - | - |
| CX-33 | `refactor(embed): ctx-aware HTTP and run loop` | M | - | todo | - | - |
| CX-34 | `refactor(memory): one ctx-first API; delete the ctx-less wrappers` | M | - | todo | - | - |
| CX-35 | `refactor(doctor,maintain): ctx-first probes, checks and passes` | M | - | todo | - | - |
| CX-36 | `refactor(sessionlink,telemetry): ctx-first adapters and ledger internals` | M | - | todo | - | - |

## P4: interface redesign beyond ctx

| ID | PR | Size | Depends | Status | PR link | Notes |
|---|---|---|---|---|---|---|
| CX-37 | `refactor: narrow store dependencies in graph, annot, githistory, summarize, export and metrics` | S each | - | todo | - | - |
| CX-38 | `refactor: auth, maintain, embed and orgknowledge take store roles` | M | - | todo | - | - |
| CX-39 | `refactor(core): remove Engine.Store()` | M | - | todo | - | - |
| CX-40 | `refactor(embed): an Identity value with one encoding, resolved once` | M | - | todo | - | - |
| CX-41 | `refactor(embed,core,memory): Provider replaces QueryEmbedder and Vectorizer; env read only in cmd` | M-L | - | todo | - | - |
| CX-42 | `test(core): golden responses for every query and import action` | M | - | todo | - | must merge before CX-43..45 |
| CX-43 | `refactor(core): action handler registry` | M | - | todo | - | - |
| CX-44 | `refactor(core): typed action args; command is the sub-command key` | M-L | - | todo | - | - |
| CX-45 | `refactor(core): Options at New, Close, once-only LSP, surfaced store errors` | M | - | todo | - | - |
| CX-46 | `refactor(httpx): one JSON and error writer; one error-code table` | M | - | todo | - | - |
| CX-47 | `refactor(rest,rpc,web,mcp): one query decoder and one budget in core` | M | - | todo | - | - |
| CX-48 | `refactor(web): status and graph views delegate to core and graph` | M | - | todo | - | - |
| CX-49 | `refactor(auth): per-route role requirement; role in ctx` | M | - | todo | - | - |
| CX-50 | `refactor(sessionlink): split discovery and reading; parse once per Link` | M | - | todo | - | - |

## P5: duplication

| ID | PR | Size | Depends | Status | PR link | Notes |
|---|---|---|---|---|---|---|
| CX-51 | `refactor(store): share scanners, the migration loop and the sweep rules between engines` | M | - | todo | - | - |
| CX-52 | `refactor(store): IndexWriter.ReplaceFile unit of work` | S-M | - | todo | - | only if decision D6 says yes after the CX-22 timing |

## Decisions

| # | Decision | Recommendation | Status |
|---|---|---|---|
| D1 | Web API is unauthenticated on the `--ui` listener; `/api/file` serves any file under the project root | Gate `/api/file` behind the bearer token when the gate is on; ungated only on a loopback bind | open |
| D2 | Rare-token bound: `/5000` (SQLite) or `/500` (PG and comments) | `/500` on both engines, after measuring SQLite recall before and after | open |
| D3 | Login throttle | In-memory per-account backoff, in the same PR as the uniform error (CX-12) | open |
| D4 | Where the token budget applies | In core, for every transport; MCP default unchanged | open |
| D5 | `index --hook` time budget | 10 s by default, overridable by flag or environment | open |
| D6 | Per-file unit of work (`ReplaceFile`) | Defer to P5; do it only if the CX-22 timing shows per-file commits dominate | open |
| D7 | Web API answers HTTP 200 on every error | Move to the shared error table in CX-46 with the matching `ui-v2` change; otherwise keep and document | open |
| D8 | golangci-lint (`noctx`, `contextcheck`) | Not now; revisit after P3 | open |
| D9 | Graph BFS caps | Node budget (default 5000) with `truncated: true` and effective depth in the response, in CX-30 | open |
| D10 | Deterministic embedding provider is selectable in production | Keep behind explicit `LEANKG_EMBED_PROVIDER=deterministic` and flag it in `status` | open |

## Metrics

| Metric | Baseline (6b4b741e) | Target | Current |
|---|---|---|---|
| functions taking ctx | 253 of 2899 | all I/O and long-running functions | see archtest |
| store.Backend methods taking ctx | 0 of 106 | all except Close/Path/Engine/Mode | 0 of 106 |
| pgCtx uses in internal/store | package-level var, used by every PG statement | 0 | present |
| context.Background() in non-main non-test code | 32 | 0 | see internal/archtest budget map |
| context.TODO() | 0 | 0 | see internal/archtest budget map |
| store capability type assertions in consumers | 7 | 0 | 7 |
| non-Context database/sql calls on the SQLite side | 153 | 0 | 153 |

Once CX-13 lands, `internal/archtest` budgets are the machine-checked source of truth for the Background/TODO and ctx-less I/O counts.

## Definition of done

- [ ] `TestStoreRolesTakeContext` passes with the allowlist `{Close, Path, Engine, Mode}`; `rg pgCtx internal` is empty.
- [ ] Non-Context `database/sql` calls on the SQLite side go from 153 to 0.
- [ ] `exec.Command(`, `http.NewRequest(` and timeout-less `http.Client` values under `internal/` are 0.
- [ ] Non-test `context.TODO()` is 0; `context.Background()` outside `package main` is 0 (down from 32), with detached work on `WithoutCancel`; every ratchet budget is at its floor.
- [ ] Store capability type assertions are 0 (down from 7); `FTSBackend` and the hybrid interfaces are deleted.
- [ ] `Backend` composes the roles in 3.1 with no role above 18 methods; `Engine.Store()` is deleted; `store.Backend` is referenced only in `cmd/`, `core.New` and `internal/store`.
- [ ] `internal/memory` has 0 `Ctx`-suffixed methods; `writeJSON`/`writeErr` exist only in `internal/httpx`.
- [ ] On SIGTERM, `serve` shuts its servers down before closing the stores (unit test on `runServers`); a cancelled ctx stops `leankg index`, `leankg-embed` and an MCP query within one file or batch with a non-zero exit; every P0 test passes.
- [ ] `make go-build && make go-test && make go-vet && make dual-engine` pass; `docker ps --filter name=leankg-pg-500mb` lists nothing; `go test ./internal/ontology ./internal/mcp ./internal/archtest` passes; AGENTS.md and CLAUDE.md carry rule 3.3 and the section 4 conventions.
