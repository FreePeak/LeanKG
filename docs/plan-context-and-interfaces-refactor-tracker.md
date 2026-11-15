# Tracker: context propagation and interface redesign

**Plan:** [plan-context-and-interfaces-refactor.md](plan-context-and-interfaces-refactor.md) · **Last updated:** 2026-11-15 · **Base:** 6b4b741e

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
| CX-01 | `fix(memory): redact before indexing and recall` | S | - | done | - | - |
| CX-02 | `fix(auth): classify retain, ontology match and auto-config as writes; gate auto-config` | S-M | - | done | - | - |
| CX-03 | `fix(auth): never return token hashes; revoking needs the owner or an admin` | S | - | done | - | - |
| CX-04 | `fix(cmd): open every verb's store through one resolver` | M | - | done | - | blocks CX-18. Production code opens stores only through openStoreWith (guard test); test fixtures open through openFixtureStore (storefactory_test.go), so `rg 'store\.Open\(' cmd` is empty. The PostgreSQL subcase of TestWriterAndServeOpenTheSameStore SKIPs without LEANKG_TEST_PG_URL; not run against the live fixture in the wave-2 integration. |
| CX-05 | `fix(rest/auto): refuse before side effects; run jobs on the server context` | S-M | - | done | - | serve owns the Jobs (NewJobs, WithJobs, Stop on return); embed debounce is a channel-driven test |
| CX-06 | `fix(cmd): validate serve first, surface listener errors, await shutdown` | M | - | done | - | flags and listen syntax are checked before the store opens; `runServers` (cmd/leankg/helpers.go) serves serve and dashboard, returns the first listener error and awaits every `Shutdown`. Exposure refusal needs the store's auth state, so it still runs after the store opens (DB file created first). Maintenance fleet is not joined at shutdown (P3 maintain item). |
| CX-07 | `fix(cmd): remove the data races on telemetry sinks and the capture recorder` | S | - | done | - | local commit, no PR. `-race` green on `./cmd/...`, `./internal/rest/...`, `./internal/telemetry/...`; live `serve --rest` under 50 loops + 4 SIGHUP reloads: base 2 DATA RACE reports, fix 0; all 3000 responses 200 each run |
| CX-08 | `fix(leankg-embed): cancellation is a failure; keep flags after the positional; stop the sidecar on error` | S-M | - | done | - | - |
| CX-09 | `fix(telemetry): report writer panics; Nop.SetLevel honours its contract` | S | - | done | - | - |
| CX-10 | `test(index): pin markdown ownership between index and docindex`, plus a fix PR only if it fails | S-M | - | done | - | - |
| CX-11 | `fix(store): one rare-token bound on both engines` | S | - | done | local branch `refactor/cx-16-filter-before-limit` (not pushed) | one nameArmBound (elements/500) on both engines; D2 asks for SQLite recall before and after, not measured yet (needs the labelled sweep with the real embedder) |
| CX-12 | `fix(auth): uniform login failure and a login throttle` | S | - | done | - | REST login shares the MCP authenticate check: one generic error, dummy hash for unknown accounts, 5 failures lock 60 s (AGENTS.md) |

## P1: guardrails and contract shape

| ID | PR | Size | Depends | Status | PR link | Notes |
|---|---|---|---|---|---|---|
| CX-13 | `test(archtest): ratchet context roots and ctx-less I/O at today's baseline` | S | - | done | #584 | internal/archtest ratchet at baseline; AGENTS.md ctx conventions |
| CX-14 | `refactor(store): declare role interfaces; Backend composes them; add Mode` | S-M | - | done | local, not pushed | roles.go; Mode on Backend; 9 uncalled methods dropped from the interface |
| CX-15 | `refactor(store): the keyword arm is part of the search contract` | M | CX-14 | done | local branch `refactor/cx-15-findfuzzy` (not pushed) | FindFuzzy returns the typed Arm; FTSBackend deleted; PG SearchKnowledgeFTS folded into KnowledgeEntriesSearch; SQLite knowledge_fts index with triggers (migration 19) |
| CX-16 | `fix(store): filter vector and hybrid search before LIMIT` | M | CX-15 | done | local branch `refactor/cx-16-filter-before-limit` (not pushed) | ElementFilter, VectorQuery, HybridQuery; Hybrid returns the keyword arm; PG filter in WHERE with hnsw.iterative_scan; SQLite FTS5 and name arms filter in SQL; L3 reason lists arms of returned hits |
| CX-17 | `refactor(store): engine-neutral Maintain op with one read-only refusal` | M | CX-14 | done | local, not pushed | sixth kind `Upgrade` runs first and is fatal; ErrReadOnly on every Maintainer write; SQLite live output byte-identical to CX-14; live PostgreSQL run deferred to the integration gate |
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
| CX-28 | `refactor(cmd): one root signal context; index and serve take ctx` | M | - | done | local branch `refactor/cx-28-root-ctx` (not pushed) | root ctx from signal.NotifyContext in main (a second Ctrl-C kills a stuck run); index, serve and setup take ctx; runIndex stops between files with the ctx error. `index --hook` budget: `--hook-budget` or `LEANKG_HOOK_BUDGET`, default 10 s, expiry prints one line and exits 0. Live (3000 files): SIGINT base killed at 0.001 s with status -2; new exits 130 at 0.028 s, store opens; hook 1.037 s exit 0; full index counts equal base. cmd/ is package main, outside the ratchet, so no budget number changes. |
| CX-29 | `refactor(cmd): every verb takes ctx and returns an error` | M-L | - | done | local branch `refactor/cx-29-verbs-ctx` (not pushed) | every cmd verb is `func(ctx, args) error` in one `verbs` map; main's `runVerb` maps the error once via `internal/cliexit` (0, 1, 2 usage or bad flag, 130 cancelled, a verb-set code for doctor, update and run). Per-verb `NotifyContext` removed (6: 5 in cmd/leankg, 1 in leankg-embed; the 2 roots remain); SIGHUP in telemetry kept. `context.Background()` in cmd: 36 to 2 (the two main roots). `os.Exit` in cmd: 4 calls, all in the two main functions; `log.Fatal`: 0. Live: 14 success verbs and 4 failure paths match the CX-28 binary on stdout and exit code; SIGINT during a 3000-file index exits 130 on both. Evidence: scratchpad CX-29.txt. |
| CX-30 | `refactor(core,graph,mcp): thread ctx through the rungs, graph and status` | M | - | done | local branch `refactor/cx-30-core-graph-mcp-ctx` (not pushed) | rungs L1-L3 and ladder check ctx.Err per rung (cancelled embed is ctx.Err, not a degrade); graph verbs take ctx, BFS checks ctx per node; D9 NodeBudget 5000 with depth and truncated:true (path reports truncated, not unreachable); embed probe bounded by caller ctx, outside the mutex, a cancelled probe is not cached; core Background budget 2 to 0 (archtest entry removed). Live (5200 callers + 4 tiers, depth 5): base 26001 hits unbounded; new 5000 hits, truncated:true, depth 5. 20 queries aborted at 2 ms: base 6.0 s server CPU, new 0.06 s. callers and callees are one hop, still unbounded (see report). |
| CX-31 | `refactor(rest,rpc,web,dashboard,auth): use the request context` | M | - | in progress | local branch `refactor/cx-31-http-ctx` (not pushed) | `internal/httpx.Guard` answers a request whose context is already done with 499 before any handler work (rest, web, dashboard, multi-project router); REST and dashboard map a cancelled call to 499 instead of 400/500; `routeByProject` lost its stored ctx (per-request `r.Context()`); dashboard transcript reads stop per session. Not done: store, index, core ladder, memory and embed work below the transports still ignores ctx (CX-19..27, CX-30, CX-32..34), so a client abort does not stop a running import or query yet. Live (20k elements, 1.7 s import, abort at 0.3 s): base and new both finish the store writes after the client leaves (last write 1.35 s / 1.40 s after abort); multi-project second request works on both. astgrep tests time out under load and fail the same way on base. |
| CX-32 | `refactor(index,lsp,langs,tstree): cancellable extraction and LSP` | M | - | done | - | local commit only; `SupportedFiles` (index/walk.go) keeps its two Background roots for CX-35 (doctor checkFunc ctx) |
| CX-33 | `refactor(embed): ctx-aware HTTP and run loop` | M | - | done | local branch `refactor/cx-33-embed-ctx` (not pushed) | client timeout on every embed client (`LEANKG_EMBED_REQUEST_TIMEOUT_SECS`, default 120 s); a timeout is `ErrEndpointTimeout` and stops the run at once; probe and health poll take ctx (`http.NewRequestWithContext`); sidecar spawn is `CommandContext` with group SIGTERM and WaitDelay; `Shutdown` bounds the post-SIGKILL wait. Ratchet: `internal/embed` entry removed from ioBudget (was exec.Command 1, http.NewRequest 1, client-without-Timeout 2). Live: base hung 20 s and was killed; new returned in 5.04 s, exit 1, status failed; SIGINT mid-run exit 1 in 0.02 s, status cancelled. Open: the lazy `fingerprint` probe in `internal/embed/env.go` still runs without ctx (3 s client timeout), because `Provider.Revision()` and `TextBudget()` take no ctx; it needs the Provider interface to take ctx (outside CX-33's file scope), so the row stays in progress. |
| CX-34 | `refactor(memory): one ctx-first API; delete the ctx-less wrappers` | M | - | done | local branch `refactor/cx-34-memory-ctx` (not pushed) | 10 XxxCtx renamed to Xxx, 10 ctx-less wrappers deleted; every write checks ctx before touching the file or index, RecallFiltered and firstTurn check ctx; memory context.Background() 11 -> 0 (entry leaves archtest map), sql-no-ctx 32 -> 28. Deviation resolved at integration: the 20 s dense-arm bound is removed; the embed client timeout (CX-33) bounds a hung endpoint, and TestDenseArmReturnsByEmbedClientTimeout proves recall returns at 1 s. Live: REST responses identical base vs new (timestamps and ids normalised); aborted 2500-row and 15000-row retains: base wrote every batch in full, new wrote 14 of 15 in full and wrote nothing for the 0.04 s abort; file and index agree everywhere; raw secret rows in memory_fts 0 on both. Evidence: scratchpad/evidence/CX-34.txt |
| CX-35 | `refactor(doctor,maintain): ctx-first probes, checks and passes` | M | - | done | - | ctx-first code done and verified; serve and writer now join the fleet at shutdown (joinFleet, bounded by shutdownTimeout; cmd/leankg/fleet_join_test.go). No embed, ports, git or LSP probe exists in doctor; only the store and fleet probes are bounded. |
| CX-36 | `refactor(sessionlink,telemetry): ctx-first adapters and ledger internals` | M | - | done | local branch `refactor/cx-36-sessionlink-telemetry-ctx` (not pushed) | ctx-first adapters (Transcript takes ctx; the dashboard keeps its per-session ctx.Err check) and WithoutCancel rollback tails; telemetry Background 8 to 0 (archtest entry removed); opencode sql-no-ctx 4 to 0 (entry removed). |

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
| CX-46 | `refactor(httpx): one JSON and error writer; one error-code table` | M | - | done | - | one `internal/httpx` table (`StatusFor`) and writers (`WriteJSON`, `WriteError`, `WriteMessage`). Eleven copies deleted in seven files: rest (writeJSON, writeErr), rest/auto (writeAutoJSON), dashboard (writeJSON, writeErr), web (writeEnvelope, writeEnvelopeStatus), federation (writeJSON, writeError), auth (writeJSON), cmd/leankg (writeProjectError). The auth gate denial is a JSON body. Not moved: the MCP `{"error":"unauthorized"}` in mcp/server.go, pinned by its gate test. Live: 8 error and 6 valid scenarios, base vs new, all transports agree on status and message for the same bad input (CX-46 evidence, session-local). |

| CX-47 | `refactor(rest,rpc,web,mcp): one query decoder and one budget in core` | M | CX-46 | in progress | - | `core.DecodeQuery` (REST, MCP, RPC) and `core.DecodeQueryParams` (web flat params) in core/decode_query.go; a wrong-type or fractional field answers LEANKG_ERROR_INVALID_PARAM 400 on every transport (REST and web previously differed, web ignored it). Former decode sites: rest/rest.go POST /api/v1/query, rpc/service.go Query and MemoryRead, web/api.go query, mcp/server.go handleQuery. Not done: the token budget is still MCP-only (decision D4), so the same request does not yet return the same body on all four transports, and the four-transport valid-body parity test is not written. |

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
| D5 | `index --hook` time budget | 10 s by default, overridable by flag or environment | decided in CX-28: `--hook-budget` or `LEANKG_HOOK_BUDGET`; covers the size gate, index and embed; expiry prints one line and exits 0 (the plan named neither the flag, the variable nor the expiry status) |
| D6 | Per-file unit of work (`ReplaceFile`) | Defer to P5; do it only if the CX-22 timing shows per-file commits dominate | open |
| D7 | Web API answers HTTP 200 on every error | Move to the shared error table in CX-46 with the matching `ui-v2` change; otherwise keep and document | decided: real statuses on the web API, no per-route flag (ui-v2 fetchJson throws on non-2xx and reads `error`) |
| D8 | golangci-lint (`noctx`, `contextcheck`) | Not now; revisit after P3 | open |
| D9 | Graph BFS caps | Node budget (default 5000) with `truncated: true` and effective depth in the response, in CX-30 | decided in CX-30: `graph.NodeBudget` = 5000 on Impact and ShortestPath; impact and path responses carry `depth`, and `truncated` only when the budget stopped the walk; path reports truncated instead of reachable:false |
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
