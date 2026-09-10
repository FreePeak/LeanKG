# LeanKG PRD — Unified Product Document

**Version:** 4.6.0-go-full-parity
**Date:** 2026-09-10
**Status:** Active Development — **single source of truth** (this document + `docs/prd-task-tracker.md`; all historical documents preserved under [`docs/archive/`](archive/))
**Codebase Version:** 0.30.0 (Rust, maintenance) + Go engine W1 (`go/`, module `github.com/FreePeak/LeanKG/go`)
**Storage:** Rust line: dual-backend (SQLite default, PostgreSQL+pgvector opt-in). Go engine (W1): SQLite (WAL, FTS5, vector BLOBs); PG is W4.

---

## Changelog
### v4.6.0-go-full-parity — Go engine at full surface parity; Rust line removed (2026-09-10)

> **Trigger:** maintainer direction: "keep working until the Go code replaces 100% of the Rust code" — all waves landed, both engines live-verified, then the Rust tree deleted.

**Parity ledger (analysis §7 row-by-row):**

| Rust subsystem | LOC | Fate in Go | Status |
|---|---|---|---|
| db (Cozo+translator+sqlite) | 18.6k | `internal/store` — plain typed SQL, Backend interface, SQLite WAL + PostgreSQL/pgvector (schema-per-project, per-model vector tables + HNSW) | **DONE** (10/10 live-PG tests) |
| mcp transports + envelope | 16.4k | `internal/mcp` (official go-sdk, stdio+streamable HTTP) + `internal/rpc` (ConnectRPC gRPC/gRPC-Web/JSON) + `internal/rest` | **DONE** |
| graph/query traversal | 15.2k | `internal/graph` — Impact/ShortestPath/Callers/Callees/Context/Explain, wired as query actions | **DONE** (connection verbs; ontology-walk provenance = deferred, see below) |
| indexer extractor+call graph | 11k | `internal/index` + `internal/docindex` — regex extraction (7 langs + md), 3-signal detection, calls/contains, watch-triggered reconcile | **DONE** (regex ceiling documented; tree-sitter = DEFERRED, CGO-free constraint) |
| embeddings | 8.2k | `internal/embed` + `cmd/leankg-embed` — Provider port (OpenAI-compatible = API + llama.cpp sidecar shape), ModelStamp guards, NDJSON offsite, benchmarked | **DONE** (local ONNX runtime = DEFERRED for sidecar; sidecar lifecycle mgmt not in CLI) |
| web api+ui | 5.8k | `internal/web` (go:embed ui build, SPA fallback) + REST `/api/v1/*` | **DONE** |
| cli/connect/install | 2.2k+1.4k | `cmd/leankg` — serve/index/writer/doctor/connect/install for 6 clients, --register-cwd hooks | **DONE** |
| ontology | 4.0k | `internal/ontology` — concept catalog + element matching + kv persistence; reachable via `import{action:"ontology", path:<catalog.json>}` + `query{action:"ontology"}` over MCP/REST/CLI, POST /api/v1/ontology/match + GET /api/v1/ontology/matches | **PARTIAL — workflows/traceability + transport action DEFERRED** |
| session offload | 0.9k | `internal/session` — offload/recall bit-for-bit + checksums, canvas, lesson dedup; reachable via `import{action:"session", command:offload|lesson}` + `query{action:"session"}` (canvas/recall) over MCP/REST/CLI, POST /api/v1/session/read; core + mcp + rest tests pin the round-trip | **DONE** |
| compress | 3.5k | **DEFERRED** — `internal/compress` does not exist in Go; the Rust context-compression pipeline was not ported (its MCP verbs are outside the 3-tool surface) | **DEFERRED** |
| lsp bridge | 2.6k | DEFERRED (analysis §8 already deferred it) | **DEFERRED** |
| Android/Gradle/Maven extractors | ~9k | DEFERRED (analysis §8: mechanical, fixtures-first) | **DEFERRED** |
| benchmark harness | 4.5k | `go/benchmark/ab` — Go benchmarks + **executed A/B REPORT.md** (fresh Rust 0.30.0 build from pre-removal commit vs v4.6.0: index parity 0.10s/0.10s, L1 53ms/50ms, 12.7× smaller binary; impact marked NOT COMPARABLE — seed granularity differs (Rust=file, Go=element QN) and the two arms disagree on the same chained corpus; in-process Rust cells 'not measured') | **DONE** |
| audit/doctor/auth | ~3.3k | audit ledger (hash-chained, tamper-pinned) + RBAC middleware + doctor | **DONE** (doctor --deep fleet checks = PARTIAL) |
| npm wrapper + manifest + release pipeline | — | removed with Rust; Go release engineering: `go/cmd/leankg/VERSION` + `.github/workflows/release-go.yml` (4-target CGO_ENABLED=0 matrix → GitHub Release artifacts, manual `workflow_dispatch` until the maintainer opts into tag automation). npm wrapper intentionally not revived (no Node runtime in the Go engine) | **DONE** (manual-trigger release; auto-publish = maintainer choice) |

**Validation:** `go build ./... && go vet ./... && go test ./...` — 15 packages green. Live smoke both engines: SQLite (index→embed→L1/L2/L3→memory→MCP 3-tool registry→RPC) AND PostgreSQL :5433 (index→embed with vectors physically in `leankg_*` schema→L1/L3 pgvector→graph verbs→status backend=postgres). Benchmarks: IndexDir 100 files 0.73s, L1 137µs, L2 375µs, 1k×384 cosine scan 4.4ms (exact scan ceiling vs HNSW documented).

**Cutover state:** Rust source, Cargo config, cargo CI jobs, npm wrapper and manifest removed; Go CI job added; release engineering retargeted to `release-go.yml` (4-target CGO_ENABLED=0 matrix, manual `workflow_dispatch` with an explicit version input — tag automation is the maintainer's opt-in).

### v4.5.1-go-rewrite-w1 — Go engine W1 delivered: `go/` greenfield engine + leankg-embed (#368) + full-markdown memory (#369) (2026-09-10)

> **Trigger:** maintainer go-decision on the Rust→Go rewrite (#365 umbrella). This PR lands migration waves **W1 (core)** plus the two issue-scoped slices **#368 (embedding pipeline as an independent binary)** and **#369 (full-markdown memory)** — not full parity.

**Delivered** (`go/` module, ~7 packages, all `go test ./...` green + live-smoked):

| Slice | What landed |
|---|---|
| W1 store | SQLite WAL (`internal/store`): FTS5 L2 rung, float32-BLOB vectors + in-proc cosine (documented ceiling; sqlite-vec upgrade path), DB-resident `write_watermark` freshness (kills the C4 TOCTOU class), real PKs on `code_elements`/`relationships` (documented breaking fix) |
| W1 core | 3-tool envelope (`import`/`query`/`status`, legacy `set`/`get` as tool names rejected over the wire; action aliases live) + L0→L1→L2→L3 ladder with `retrieval{rung,reason}` + `freshness` on every answer |
| W1 indexer | regex extraction (go/rs/ts/tsx/js/jsx/py/md; documented ceiling until W2 tree-sitter), 3-signal change detection (size+mtime → SHA-256 confirm), calls/contains relationships |
| W1 transports | MCP stdio + streamable HTTP via official `modelcontextprotocol/go-sdk` v1.7.0; REST `/health`, `/api/v1/{status,query,import}` (stdlib net/http) |
| #368 | `cmd/leankg-embed` independent binary (`run`/`full`/`export`/`import`/`status`); shared `internal/embed` library; ModelStamp guard on EVERY vector writer — full+mismatch ⇒ clear+rebuild, **incremental+mismatch ⇒ hard fail with `leankg-embed full` directive** (flag-slip must not wipe a collection), first build stamps; NDJSON offsite export/import with dim-guard + resume; serving binary does zero inference (query-time embedding = HTTP client call, provider failure degrades to L2) |
| #369 | Full-markdown memory (`internal/memory`): `MEMORY.md`/`USER.md` bounded 2,200 bytes with **error-not-truncate** overflow + usage-% snapshot header; `topics/*.md` unbounded; Claude-Code file commands (`view/create/str_replace/insert/delete/rename`) with path-traversal + symlink-escape rejection; Hermes `add/replace/remove` with unique-substring semantics (ambiguous ⇒ error); FTS5 `index.db` re-indexed on every write; mnemopi JSONL banks adapter (wyhash64 byte-exact port of the Rust wyhash 0.5 crate, `retained_through_user_turn` cursor) |

**§9 decisions taken:** (1) tool rename `set`/`get` → `import`/`query` (breaking tool-name cutover; action aliases keep the verb namespace); (2) query-time embeddings provider-first — OpenAI-compatible API or llama.cpp sidecar share one HTTP shape; sidecar runtime itself is W6; (3) ui-v2 untouched (REST-compatible); (4) W1 scope = core + #368 + #369, NOT full parity. (5) project resolution: explicit `--project`/cwd first — roots/list stays available as a fallback but is not the only path (deprecated by SEP-2577 in the go-sdk); server-initiated roots/list lands when the HTTP surface needs remote resolution.

**Waves remaining:** W2 (tree-sitter indexer), W4 (pgvector), W5 (ConnectRPC + hindsight memory e2e), W6 (local model runtime), W7 (parity fixtures + cutover). Tracker rows: FR-GO-W1, FR-GO-EMBED, FR-GO-MEM.


### v4.5.0-go-rewrite-analysis — Deep Rust→Go feasibility study (2026-09-10)

> **Trigger:** user direction to evaluate replacing the Rust implementation with a Go engine: a lightweight agent-memory MCP server (just 3 tools — import/query/status), easy coding-tool integration, REST + RPC transports, layered storage (index → embedding), query ladder exact → fuzzy → semantic, SQLite default + PostgreSQL/pgvector, local (default) + API-provider embeddings, real writer/reader separation, and a comprehensive markdown analysis as the deliverable.

**Delivered:** [`docs/go-rewrite-analysis.md`](go-rewrite-analysis.md) — full-source feasibility study (5 parallel deep-dive passes over mcp/db/indexer+graph/embeddings+memory/ops-CI plus first-hand verification of every load-bearing claim). Contents: measured inventory (168k LOC across 323 files, 3 tools / 83 verbs / 114 CLI verbs / 176 MB binary / 662 MB self-index), root-caused cons (Datalog translator tax, Cozo single-writer + FFI-abort class #286, per-process freshness/L1 cache races, unwired BLAKE3 content hash, sqlite L2 fuzzy asymmetry, sync-PG + block_in_place, surface sprawl), an honest pros list, a shipped-vs-vision gap table (≈90% of the stated target is already shipped and live-verified), and a Go target architecture: plain-SQL dual store (modernc sqlite WAL + pgx/pgvector), DB-resident freshness watermarks replacing TTL caches, MCP + REST + ConnectRPC from one core, providers-first embeddings with llama.cpp-sidecar default, 7-wave migration plan, risks, and 4 open questions for the user.

**Status:** analysis only — no code changes landed. The Rust line (v0.30.0) stays released and in maintenance; the Go rewrite proceeds on user go/no-go and answers to §9 questions. Open questions: (1) sidecar vs in-process local ONNX, (2) rename `set`/`get` → `import`/`query` with aliases, (3) keep ui-v2 as-is, (4) timeline appetite (weeks core vs full parity).

### v4.4.3-dual-engine-verified — CI pipeline healed + Postgres path fixed + verified on both engines (2026-09-09)

> **Trigger:** user report that CI was failing on GitHub, then a dual-engine (sqlite + Postgres) verification sweep.

**CI pipeline healed (3 stacked semantic-release defects):** #336 idempotent bumps (release-PR merge re-entering create-pr treated already-at-target as a fatal throw); #337 event-race fix (merging any PR fired push+pull_request runs sharing one concurrency group — they cancelled each other; groups now per-event, release mode only for `release/*` head branches); #339 semantic-release now bumps the npm wrapper (auto release PRs pass the parity gate). Verified end-to-end: merge → auto release PR → CI green → merge → tag published automatically. **v0.28.0 (#335) and v0.28.1 (#338) shipped this way.**

**Storage platform:** #326 sqlite is the default and only CI-tested engine — Docker/Postgres triggers removed (Dockerfiles, compose files, docker-* scripts, Makefile docker targets, install.sh docker subcommand); PG service containers dropped from all workflows (also fixed the week-long perf-gate red: the scale harness still asserted the pre-#283 one-tool registry). Postgres remains an explicit `LEANKG_DB_ENGINE=postgres` opt-in and was re-verified live on pgvector/pg18.

**Postgres path fixed:** #342 restored `006_audit_log` to the PG MIGRATIONS array (dropped by the v4.4.0 sweep — fresh PG installs had no audit ledger: recording silently disabled, export/verify failing); #341 temporal_query uses `!regex_matches` (the `not` spelling parsed on sqlite but the PG translator has no top-level `not`); #343 integration suites updated to the 3-tool surface + bounded temporal_query API (they did not compile since #331 — CI runs `--lib` only, so this escaped CI).

**Stale-surface cleanup:** no_legacy_terms guard resized (cozo is the live engine; FORBIDDEN = datadog only), hidden dirs + vendor/ + nested node_modules skipped in its walk, Makefile dead docker targets removed, ui-v2/CLAUDE.md/SKILL.md Docker-era guidance rewritten, 10+ RocksDB comments modernized.

**Dual-engine live verification (same binary):** sqlite — get 7 hits, temporal_query 57,473 (bounded), audit chain 2 entries; PG — temporal_query 32ms/9, get green, audit chain intact (1 entry recorded + verified), migration 006 applies on fresh db; full PG integration suite 49/49 suites, 3,222 tests.

### v4.4.2-harden-and-validate — 8 PRs merged, live-validated (2026-09-08)

### v4.4.2-harden-and-validate — 8 PRs merged, live-validated (2026-09-08)

> **Trigger:** fanout review of the 8 open fix PRs (scouts caught 4 blockers pre-merge), then one-by-one merge with post-merge live validation on the sqlite server.

**Merged (each CI 6/6 green, reviewed):** #298 router L1-first (#290) — identifier queries answer from L1 exact before ANN; #301 embedder+rerank per-process cache (#292) — router latency 14-31s → 2.3s warm; #313 token budget post-truncation accounting (#300); #305 panic hook + durable crash log (#286 diagnostics); #312 sqlite audit ledger with review-blocker fixes (#309); #307 fuzzy ranking — test demotion, exact>substring, specificity (#293/#294) + hydration QN single-quote escaping (cozo 0.7.6 rejects \" in double-quoted strings); #304 vendor excludes (#291) — minified/embed assets skipped at collection; #311 stale-element sweep — incremental sync removes elements for files that dropped out of the collection set; #315 embeddings-feature test build green (1527/0) (#302); #314/#316 bench deltas.

**Live-validated on this repo (sqlite, `:9799`):** 578 files, 10,202 elements, 12,984 vectors (vendor-free); `create_hnsw_index` → rung=exact, real symbol; router latency 6.07s cold → 2.31s warm; audit ledger recording enabled.

**Still open (all closed since):** #308-followup + #309-followup + #300-followup landed in the v4.4.3 window; #302 closed via #315.

### v4.4.1-three-tools-live — live-tested SQLite server + Datalog repairs (2026-09-07, PR #284)

> **Trigger:** live 3-tool SQLite MCP server testing on this repo (index + embed + router queries on `:9799`) surfaced five Datalog/dispatch gaps between the PG-shaped code paths and raw Cozo.

**Fixes (PR #284, `fix/sqlite-l3-hydration`):**

| Bug | Fix |
|-----|-----|
| rmcp dispatch arm still resolved the one-tool envelope — `set`/`get`/`status` refused on stdio while JSON-RPC worked | single `resolve_3tool` resolver on both arms; legacy `leankg_context` envelope kept as back-compat; `action`/`verb` aliases |
| `import_relations` wrapped rows in an extra bracket layer → every import failed `Fixed rule head arity mismatch` | single bracket layer (`?[] <- [rows]`) |
| Cozo `:insert` rejects duplicate keys — full re-embeds replaying existing QNs died | upsert via `:put rel {cols}` (parity with PG `INSERT .. ON CONFLICT`); regression test `import_relations_upserts_duplicate_keys` |
| `fuzzy_find_elements` script had no relation reference (unparseable) + case-sensitive pattern | proper `:= *code_elements{...}` + lowercased pattern |
| short-positional rule application `*rel[col]` invalid on raw Cozo for multi-column relations | attribute form `*rel{col}` (doctor probes, embed counts) |
| L3 ANN hydration (`elements_by_qualified_names` / `find_element_by_key` / `find_element_by_name_col`) was PG-only | Datalog ports on `SqliteBackend` |
| `embed_control` sub-command (`on|off|status`) collided with the routing `action` key — `set {action:"embed"}` silently returned status | routing priority: control capabilities keep their payload `action`; friendly `embed` alias arms the builder |

**Open bugs filed:** #286 (server exit code 1, no panic trace, during concurrent embed+queries — suspected FFI abort), #287 (fuzzy pattern not regex-escaped), #288 (audit remaining short-positional rule applications).

**Live validation (this repo, sqlite):** index 581 files; full embed 9522/9522 vectors (`total_vectors` 9522, storage_engine sqlite); router ladder L1 exact / L2 fuzzy / L3 `hnsw+ontology-traverse` runs end-to-end with graceful below-confidence-floor degradation; ANN distance sanity 0.25 (relevant) vs 0.43 (random).

### v4.4.0-three-tools-dual-backend — 3-tool surface + SQLite dual-backend (2026-09-06)

> **Trigger:** user decision evolving the one-tool envelope into a **3-tool surface** — `set` (import repo / nested dir of repos), `get` (query with multiple layers via the L0–L3 ladder), `status` (health/inventory) — plus **dual-backend storage**: PostgreSQL *and* SQLite (vector-capable), with **SQLite as the session default**.

> **Design (D-2026-09-06-1):** the verb namespace survives as the *action* namespace inside each tool — `set {action}`, `get {action?}` (omitted = NL router), `status`. Legacy verb names remain valid as actions (zero-loss migration). Envelope resolution order is preserved: (tool, action) → effective capability resolved **before** read-only gate / write-lock / RBAC / audit.
>
> **Design (D-2026-09-06-2):** SQLite lands by resurrecting the pre-v0.20 CozoDB layer on the `storage-sqlite` engine (`cozo = 0.7.6`, feature-gated). All 216 Datalog call sites run natively on cozo-sqlite — zero translation — and HNSW vectors come from CozoDB's own `::hnsw` (cosine, dim 384). One SQLite file per project (`<project>/.leankg/leankg.db`) preserves per-project isolation. Selection: `LEANKG_DB_ENGINE=sqlite|postgres`, **default sqlite**; `LEANKG_PG_URL` opts back into PG.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-3T-01` | **P0** | Registry: exactly 3 tools (`set`/`get`/`status`); per-tool action namespaces; legacy verbs as actions (aliases) | **IN_PROGRESS** |
| 2 | `FR-3T-02` | **P0** | SQLite backend: cozo-sqlite engine, per-project `.leankg/leankg.db`, HNSW vectors, audit ledger, migrations | **IN_PROGRESS** |
| 3 | `FR-3T-03` | **P0** | Session default: SQLite engaged when `LEANKG_DB_ENGINE=sqlite` or `LEANKG_PG_URL` unset; `leankg migrate`/`index`/serve run on SQLite | **IN_PROGRESS** |
| 4 | `FR-3T-04` | **P1** | Live validation: this repo indexed into SQLite, 3-tool smoke (search/status/router), persistence across restart | **NOT_DONE** |

### v4.3.1-one-tool-envelope — Hard one-tool cutover (2026-09-05)

> **Trigger:** user decision superseding the T3 budget path — hard-delete every registered tool except `leankg_context`; all capabilities ride it as verbs. Envelope = `{verb: "<capability>", ...args}`; omitting `verb` uses the natural-language router. The verb namespace IS the legacy tool namespace (all docs/hints naming a tool remain valid as verb references). Envelope resolution happens **before** read-only gate / write-lock serialization / audit recording, so verb-scoped security decisions cannot be bypassed by hiding a write verb inside a read-named envelope (audit records the effective capability).

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-03` | **P0** | End-state enforced: registry = exactly 1 tool (`leankg_context`); `resolve_envelope` unwraps/hard-refuses; legacy names are gone, not deprecated (catalog error names the verb mechanism) | **DONE** |
| 2 | `FR-ZCP-12` | **P1** | T3 re-scoped: "≤ 12 + full opt-in" replaced by the CI-enforced one-tool invariant (`list_tools().len() == 1` + verb-catalog membership lint); T2 unchanged | folded |

### v4.3.0-one-tool-ladder — Single router + capability degradation ladder + first-run setup contract (2026-09-04)

> **Trigger:** user direction (2026-09-04): "turn LeanKG into 1 tool that fits every scenario — if there are no embedding vectors, use exact or fuzzy search," plus first-run setup requirements (auto init/index/embed choice, simple repo add). Two-scout ground-truth audit (search handlers + DB capabilities): a 4-tier retrieval ladder already exists inside separate tools — exact/regex (`search_code`, `translate.rs` regex/LIKE), ontology keyword (`safe_discover.rs:104-211`), pgvector ANN + rerank + traverse (`retrieval/pipeline.rs:272-293`), graph BFS — but only `semantic_search` degrades silently (`handler.rs:1918-2001`); `kg_semantic_context` hard-errors without vectors (`:3975-4036`); no tool exposes machine-readable capabilities; and the `QueryOrchestrator` (intent parser + hot-path cache, `orchestrator/mod.rs:26-46`) is **built but never registered as an MCP tool** (zero references under `src/mcp`) — FR-ZCP-03 has a seed implementation waiting for registration. Schema has zero FTS primitives (no tsvector/GIN/pg_trgm, `schema.sql`); pgvector supports exact-distance fallback (plain `ORDER BY vec <-> $1`, `translate.rs:2133-2168`).

> **Decision (D-2026-09-04-4):** one tool, many rungs. `leankg_context` (FR-ZCP-03) probes per-project capabilities once per request (< 10 ms: `state.has_any` limit-1 vector probe, `::relations` HNSW check, `index_inventory.total_vectors`) and routes each query down the best rung the data supports: **L3** vectors → ANN + rerank + traverse; **L2** no vectors → FTS/trigram fuzzy + ontology concepts; **L1** no index → exact identifier/regex + suggestions; **L0** cold → guidance + auto-index kick (FR-ZCP-02). Every response carries `retrieval: {rung, reason}` + `freshness`; the ladder never errors — capability loss degrades ranking, not availability. The attach side gets the same treatment (FR-ZCP-13): one auto/manual setup question, `leankg add` for coverage growth.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-03` | **P0** | Rewritten as the ladder router: capability probe + L0–L3 rungs + `retrieval` provenance block; registers the unregistered `orchestrate` parser | **NOT_DONE** |
| 2 | `FR-ZCP-13` | **P1** | First-run setup contract: one auto/manual question (init + index + embed vs manual) persisted in `.leankg/config.json`; `leankg add <path> [--embed]` one-command repo registration; embeddings are a preference, never a prerequisite | **NOT_DONE** |
| 3 | `FR-ZCP-05` | folded | Bridge tier spec: `pg_trgm` GIN + `text_pattern_ops` prefixes as the L2 fuzzy baseline before FTS lands | folded |

### v4.2.0-simplicity-first — Measured-simplicity contract for a young product (2026-09-04)

> **Trigger:** three-track research sprint (2026-09-04, three parallel scouts). **(1) Repo friction audit (file:line):** **76/73 MCP tools** exact-count CI-pinned (`src/mcp/tools.rs:1156-1160`, already pruned 87→76) vs the winning 1–2-tool norm (zg = 1 default + 6 full; context7 = 2; serena ≈ 48); **103 CLI verbs** (61 top-level + 42 nested, `src/cli/mod.rs`); **116 distinct `LEANKG_*` env-var names** (88 runtime in `src/`, 28 script-only, 1 docs-only; only ~5 first-run relevant); a **10-step / 8-decision** first-value walkthrough (README Get Started) vs Supabase's published "under 2 minutes"; error copy without remediation (`Unauthorized` — which env sets the token? `server.rs:3372-3374`; `Unknown tool` with no nearest-match hint, `handler.rs:299`); README claims "85+ tools" (README.md:170,179) vs the code-verified 76. **(2) Competitor mechanics (live-fetched URLs):** zg's 1-tool default + try-it tour + docs that match shipped behavior; context7's OAuth one-liner; Desktop Commander's fuzzy-match error feedback; gitleaks' zero-config default rules. **(3) Onboarding playbooks (URLs verified):** Supabase/Convex publish wall-clock TTFV numbers; Stripe's error objects carry `code` + `doc_url` (~200-code catalog); clig.dev: suggest the next command, never dead-end, <100 ms first feedback; Vercel's "zero configuration" is a script-verifiable per-framework claim; Stack Overflow 2025: 46% of developers actively distrust AI-tool accuracy — LeanKG's consumers are verification-hungry agents.

> **Decision (D-2026-09-04-3):** simplicity is a **measured contract, not a vibe** — every simplicity claim must be a number a CI job can verify (tool-count budget, TTFV wall-clock, error-catalog coverage, claim-to-script mapping). Tiered cheap-first: **T1** error/config/claim honesty (docs-and-strings cheap — ship immediately), **T2** published CI-timed TTFV (the number itself wins mindshare — zg publishes none), **T3** CI-pinned default-tool budget riding FR-ZCP-03's router. The audit's *unclaimed* hotspots (env-var surface, error-copy contract, docs split-brain, semantic-path default-off UX) are absorbed here; already-claimed ones stay in their FRs.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-12` | **P1** | Measured-simplicity contract, three tiers: T1 error catalog (stable code + cause + runnable fix + doc anchor, 100% CI-linted) + single copy-paste config block + claim hygiene; T2 CI-timed published TTFV ≤ 5 min; T3 CI-pinned default-tool budget (≤ 12 default, tier-tagged) riding FR-ZCP-03 | **NOT_DONE** |
| 2 | — | folded | §1.1 zg row extended; §2.6 new problem line (unclaimed friction, quantified); §3.4 AC additions; §4/§5/§6 anchored on audit numbers | folded |

### v4.1.1-omp-embed-ground-truth — OMP memory + zvec-grep embedding audits (2026-09-04)

> **Trigger:** two file:line audits (2026-09-04). **(1) OMP memory backend** — installed packages with full TS source (`@oh-my-pi/pi-coding-agent`, `@oh-my-pi/pi-mnemopi` v18.0.7): `memory.backend` is a **closed 4-value enum** (`off|local|hindsight|mnemopi`, settings-schema.ts:2932-2952, resolve.ts:16-25) with **no pluggable MCP/URL backend**; hindsight is the remote-HTTP precedent (`hindsight.apiUrl` → `POST /v1/default/banks/{bank_id}/memories` + `/recall` + `/reflect`, hindsight/client.ts:274-340). Mnemopi bank = `<basename(cwd)>-<wyhash36(abs cwd)>` ≤64 chars, derived from **cwd only, never git root** (stability contract #2412, mnemopi/config.ts:168-186); scoping `global|per-project|per-project-tagged` (default per-project; tagged = project write bank + [project, shared] recall); retain every N user turns (default 4) on `agent_end` with a `retained_through_user_turn` integer cursor — **not prefix-hash**; recall injected once on first turn as a `<memories>` block in developer instructions (state.ts:472-490, 914-922; injectionTokenLimit 5000). OMP's MCP client answers server-to-client `roots/list` with `file://<cwd>` (mcp/client.ts:59-64) — the standards-based cwd channel. **(2) zvec-grep v0.2.1 (main@d756cc7)**: every model catalog entry carries HF repo + **40-hex commit revision** (catalog.ts); the embedding schema `{provider, model, dimension, metric}` lives in the workspace manifest with a **hard mixed-model guard** (mismatch → `EMBEDDING_SCHEMA_CHANGE_REQUIRES_REBUILD`, service/zvec-grep.ts:1622+); positional chunk ids `sha256(fileId \0 chunkIndex)`; size+mtime fast-path → SHA-256 content-hash diff; per-hit query-time `fresh|possibly_stale`; hourly reconciliation; single-flight per root (JobScheduler + `{pid, hostname, instanceToken}` lease file).


### v4.1.0-portfolio-scale — Ground-truth storage audit + portfolio scale (2026-09-04)

> **Trigger:** the empty-glass → 1 repo → parent-of-3 → parent-of-100 stress test (2026-09-04 session) plus a file:line ground-truth audit of the storage/resolution model. Headline corrections to v4.0.0 assumptions: storage is **already one PG database with schema-per-project** (`leankg_p_<hex(canonical project root)>` via `project_identity_keys_in`, `src/db/backend.rs:2613-2675`; per-connection `search_path` pinning `:904-908`; all 6 migrations run **per schema** with per-schema ledgers, `src/db/pg/migrations.rs:36-57`/`:83-116`; HNSW per schema) — **not** DB-per-project; no one-DB+`project_id` consolidation is needed. The real 100-repo gaps: **no project registry** (project list is implicit — `.leankg` walks + `LEANKG_PROJECT_DIRS`, `src/mcp/server.rs:1062-1116`) and **zero cross-schema query capability** (only `current_schema()`-scoped UNION ALL, `translate.rs:3217-3224`). Further verified corrections: no `X-LeanKG-Project` header exists in code (v4.0.0 §3.1 wrongly listed it); unresolved projects **silently fall back to the server-default schema** (`server.rs:2987-2989`) — wrong-project data with no error; `LEANKG_AUTO_ATTACH` does not exist; `ensure_project_indexed` runs **awaited inline in the request path** (`server.rs:2579-2694` — blocking, errors swallowed, no freshness flag); the HTTP `initialize` handler returns a static result and never reads client params (`server.rs:3542-3552`); the watcher is single-project-per-process (`server.rs:1585-1596`/`:2015-2026`); recall/diary memory is **JSONL files under `<project>/.leankg/`**, not PG (only `knowledge_entries` is per-schema PG).

> **Decision (D-2026-09-04-1):** the repo is the unit of scope; **a portfolio (a directory of repos) is a scope, never a project**. Growth path: empty DB = registry (attach = one catalog INSERT); 1 repo = nearest-marker resolution (keep the `find_leankg_for_path` walk); parent-of-N cwd with no repo marker = **portfolio scope** with T0 manifest inventory (zero eager indexing, per-child freshness); parent-of-100 = hot-set cap + LRU detach-to-cold, one indexer slot, T0/T1/T2 tiers, cross-schema portfolio queries over the registry, federated portfolio memory. Project identity stays **canonical-path-derived**; any future re-key (e.g. git-remote) MUST follow the `schema_candidates_for_path` preferred+legacy adoption pattern (`backend.rs:2528-2540`, `:2928-2948`) — no dual-write, no row migration.

**Product actions this revision:**

| # | ID | Focus | Intent | Status |
|--:|----|-------|--------|--------|
| 1 | `FR-ZCP-11` | **P1** | Embedding correctness ported from zvec-grep: pinned model catalog (commit revision + query/document prefixes), model-stamped vectors + hard rebuild guard, chunker-version coupling, 3-signal change detection, per-file atomic replace + truncation accounting, watcher reconciliation, single-flight indexing | **IN_PROGRESS** — parts 1+2 DONE (#351: pinned revisions + per-collection stamp + embed-path guard; #353: query-side degrade guard in semantic_search + entry-point coverage in run/build_index_parallel; AC amended: stamp mismatch degrades to L2 with reason instead of erroring). 3-signal change detection: chunker_version coupled into content_hash_for (CHUNKER_VERSION folded into every hash — a bump invalidates all hashes → full re-embed; #355). part-2 compile fixes under the embeddings feature: #355) |
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
| 4 | `FR-ZCP-04` | **P1** | `leankg install --target` agent wiring incl. URL **without** `?project=` (merges `FR-ZG-04`) | **DONE** (opencode + omp writers, projectless URL contract — no ?project= by default; --register-cwd hook; six-client integration suite) |
| 5 | `FR-ZCP-05` | **P1** | Postgres FTS ranking + RRF fusion (merges `FR-ZG-02`) | **NOT_DONE** |
| 6 | `FR-ZCP-06` | **P1** | Freshness contract in every index-backed response (merges `FR-ZG-03`) | **DONE** (#347) |
| 7 | `FR-ZCP-07` | **P1** | OMP memory-backend adjacency: recall/retain MCP surface (`session_retain`, auto-recall injection) so LeanKG can act as harness memory alongside code-graph memory (extends `FR-SMA-04`) | **IN_PROGRESS** — slice 1 DONE (#357: mnemopi-compatible bank naming via wyhash36 + canonicalize, scoping matrix, session_retain/session_recall with retained_through_user_turn cursor, memory_get/update/forget/invalidate mirrors, file-backed JSONL banks; outstanding: hindsight-shaped HTTP API, OMP end-to-end injection AC) |
| 8 | `FR-ZCP-08` | **P2** | Cross-tool harness hardening (merges `FR-ZG-05`) | **DONE** (pinned 40-hex corpus SHAs + repos.lock.yaml, prompt_version + prompt SHA-256 per row, >=3-trials gate, judge-blind 0-6 rubric scorer `score.py` with shuffled labels, zg pitfalls checklist computed in the report: leakage/like-for-like/stochasticity/tool-access smoke) |

> **Doc restructure this revision:** all prior docs (66 entries: analyses, reports, plans, PRD v3.8.x history, design/ERD, benchmarks) moved to [`docs/archive/`](archive/). This document is the **one** comprehension document; [`docs/prd-task-tracker.md`](prd-task-tracker.md) is the **one** tracker (done / in-progress / todo). Section numbering below is fresh and self-contained.

---

## 1. Mission

**Stop Burning Tokens. Start Coding Lean.** LeanKG is the **persistent code-graph + org-memory substrate** for AI coding agents: semantic search + structural graph (impact, traceability, incidents) + session memory, exposed over MCP — with **zero configuration at the point of use**: an agent opens a repository and the memory is simply there.

**Positioning (2026-09-03, harness-era):** do not compete with harness-native Glob/Grep/LSP on raw search; zg (1.4k★) validates that flat hybrid retrieval is becoming commoditized. LeanKG's durable moat is the graph and the memory: impact radius, FR→workflow→code traceability, incidents, env conflicts, service graphs, cross-session lessons. Steal competitors' *discipline* (surface minimalism, freshness honesty, eval rigor, zero-config attach), not their *product*.

### 1.1 What we steal, and from whom

| Source | What they teach | LeanKG adoption |
|---|---|---|
| [zvec-grep](https://github.com/zvec-ai/zvec-grep) | One default MCP tool with intent-expressing params; `fresh`/`possibly_stale` on every response; `zg install --target`; `root`-based workspace addressing; every error carries a stable code + doc URL + runnable fix; try-it-yourself tour and a short numbered CLI (8 commands) whose docs verifiably match shipped behavior | `FR-ZCP-03..06`, `FR-ZCP-08`, `FR-ZCP-12` |
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
6. **Simplicity debt beyond config (this revision)** — what the friction audit quantified: **76/73 MCP tools** (exact-count CI-pinned, `src/mcp/tools.rs:1156-1160`) vs the 1–2-tool norm (zg=1, context7=2); **103 CLI verbs**; **116 `LEANKG_*` env-var names** with only ~5 first-run relevant; a 10-step/8-decision first-value path vs Supabase's published "under 2 minutes"; error copy that fails "what went wrong + how do I fix it" (`Unauthorized` `server.rs:3372-3374`; `Unknown tool` `handler.rs:299`); README claims ("85+ tools") diverging from the code-verified count. FR-ZCP-12 turns these into measured contracts.

---

## 3. Functional Requirements

### 3.1 Zero-Config Project Resolution (FR-ZCP-01/02/13) — **P0, this revision's core**

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

**FR-ZCP-13 — First-run setup contract + `leankg add` (Should Have, P1)**

- **One question, asked once.** The first time a user touches a repo with no `.leankg` config (install wizard, first `leankg` CLI call, or the router's L0 response), LeanKG asks exactly one question: **auto or manual?** Auto = init + index + embed (catalog default model) proceed unattended (index/embed always background); manual = `init` only, with `index`/`embed` as explicit commands. The choice persists in `.leankg/config.json` (`{"setup": "auto"|"manual", "embed": bool}`) and governs every later attach; `--auto`/`--manual` flags and `LEANKG_SETUP_MODE` override per invocation for scripts/CI. No silent re-prompting; `leankg setup --reset` re-asks.
- **Embeddings are a preference, not a prerequisite.** Choosing auto-with-embed on a non-`--features` build (or a machine without the model cache) stores the preference and serves L2/L1 results (the ladder, FR-ZCP-03) while `embed` is pending or unavailable — the answer changes what is *eventually* indexed, never whether queries work.
- **`leankg add <path> [--embed]`** — the one-command way to grow coverage: registers the repo (registry row once FR-ZCP-09 lands; `.leankg` init today), applies the persisted setup choice (or the flag), returns immediately with `mcp_status`-shaped per-project status. `leankg add .` inside a portfolio parent registers children without indexing them (T0 manifests, FR-ZCP-09). `leankg status` lists everything added with freshness + rung.
- **Zero dead ends**: `install --target` (FR-ZCP-04) prints the `add`/`index` commands in its output; the L0 router response names the exact next command; error strings carry runnable fixes (FR-ZCP-12 T1).
- AC: fresh machine + fresh repo → one auto/manual answer → queries work during indexing (`freshness: cold`, L0/L1) → embeddings arrive later with no further user action; `leankg add ../other-repo --embed` from an indexed repo returns < 2 s and other-repo appears in `leankg status` with its own schema; manual-mode users are never auto-indexed.

### 3.2 Default Toolset (FR-ZCP-03) — **P0**

- One default tool (`leankg_context` — router) whose parameters express intent (`semantic`, `lexical`, `impact`, `graph`, `files`). The router classifies intent **itself** — the existing `QueryOrchestrator` (`src/orchestrator/mod.rs:26-110`, never wired to MCP) covers only 5 file-centric intents (context/impact/dependencies/search/doc) and is demoted to one rung's executor, not the ladder's brain.
- Portfolio-aware (rides FR-ZCP-09): a query resolved to portfolio scope routes to a child when unambiguous, else answers from T0 manifests with per-child freshness — the router is the single surface for both project and portfolio answers.
- **Capability probe + degradation ladder (router = the ladder executor):** the rungs already exist as per-tool honest hints — `embeddings_index_available` gating (`handler.rs:4417-4421`, applied `:1934-1935`), `vectors_missing_hint` pointing at `search_code` (`:4800-4811`), low-confidence empty-page fallback with `rejected_reason` (`:4866-4886`) — but no single tool executes them server-side. The router consolidates them: it probes per-project capabilities in < 10 ms (`state.has_any` limit-1 probe `src/embeddings/state.rs:373-381`; HNSW presence via `::relations` `src/db/pg/translate.rs:3209-3227`; `index_inventory.total_vectors`) and runs the best rung the data supports:
  - **L3 — vector rung** (vectors present): pgvector ANN (`ORDER BY vec <-> $1`) + cross-encoder rerank + BFS traversal — today's `semantic_search` dual path.
  - **L2 — keyword rung** (no vectors): FTS + trigram/prefix fuzzy (FR-ZCP-05 bridge tier) fused with ontology concepts (`safe_discover.rs:104-211`) — never a bare `ILIKE` dead end.
  - **L1 — exact rung** (no/cold index): exact identifier + regex over existing graph remnants + nearest-match suggestions.
  - **L0 — cold rung** (nothing indexed): guidance + background index kick (FR-ZCP-02), `freshness: cold`, non-error.
  - Every response carries `retrieval: {rung, reason}` beside the `freshness` flag (FR-ZCP-06); capability loss downgrades **ranking, never availability** — the `kg_semantic_context` hard error today (`handler.rs:3975-4036`) is the pattern to delete.
- **Single source of recommendations:** tool-hint copy moves into the router — today `safe_discover.rs:105-113` still recommends the **pruned** `find_function`/`query_file` (claim rot exactly of the kind FR-ZCP-12 T1 lints), and `search_code`'s stale `recommended_tools` copy duplicates fallback logic. After FR-ZCP-03, exactly one component owns "what to try next"; every other tool references it.
- **Hard one-tool envelope (v4.3.1 end-state, DONE):** the registry exposes exactly one tool; every capability rides `{verb}` (legacy tool names are the verb namespace — existing hints stay valid); envelope unwrapped before the read-only gate, write-lock, and audit; unknown tool/verb hard-refuses with a catalog error naming the verb mechanism.
- AC: fresh-session probes resolve via the router with ≤1 tool call for intent + ≤1 follow-up for detail.
- AC (ladder): deleting a project's vectors and re-asking the same query returns L2-ranked results with `retrieval: {rung: "keyword"}` — never an error; with no index at all the response is L0 guidance + a started background index, still non-error.

### 3.3 Search Discipline (FR-ZCP-05/06) — **P1**

**FR-ZCP-05 — Postgres FTS + RRF fusion (Should Have, P1)**

- `tsvector` + GIN on `code_elements(name, qualified_name)` + `knowledge_entries(title, content)`; `websearch_to_tsquery`; RRF-fused with vector scores in `semantic_search`'s dual path (single parameterized `k`); substring/`ILIKE` only as exact escape hatch.
- **Bridge tier (no FTS required)**: `pg_trgm` GIN on `code_elements(name, qualified_name)` + b-tree `text_pattern_ops` prefixes give L2 fuzzy/prefix matching before FTS ships; trigram similarity powers "did you mean" suggestions; `websearch_to_tsquery` upgrades L2 to ranked FTS when FR-ZCP-05 lands — the ladder (FR-ZCP-03) targets the best tier available per schema.
- AC: lexical anchor queries rank real identifiers above noise; no F1 regression on the cross-tool suite.

**FR-ZCP-06 — Freshness contract (Should Have, P1)**

- Every index-backed response carries `freshness: fresh|possibly_stale|cold`; `cold` = attached but not yet indexed (FR-ZCP-02 state).
- Background reconciliation (watcher-maintained; burst-limit fix already in `src/mcp/watcher.rs`) flips the flag; **heavy work never shares the request transaction** (lesson of the pre-PG v3.8.4 LOCK-poison incident).
- AC: forced drift → next response says `possibly_stale` and self-heals without blocking the query.

### 3.4 Agent Onboarding (FR-ZCP-04) — **P1**

**Narrative.** Onboarding is one command per harness, writing that harness's own config format, with **no project path in the emitted URL** — FR-ZCP-01's contextual resolution (stdio process cwd; HTTP `roots/list`) makes `?project=` unnecessary in the happy path, and shipping it by default is the dead-end this FR exists to kill. Implementation extends the existing `connect` writers (`src/connect/`) with the two missing targets; `install --target` is the global-config surface, `connect <client>` stays as its alias.

**Command.** `leankg install --target opencode|claude|codex|cursor|omp [--remote URL] [--project PATH] [--register-cwd] [--remove]`

**Per-target config writers** (entry name `leankg`; merge-or-replace that key only, atomic tmp+rename write, never clobber siblings; parse errors abort with the file path — existing `connect` semantics):

| Target | File | Entry shape | Status |
|--------|------|-------------|--------|
| `claude-code` | `~/.claude.json` | `mcpServers.leankg` — stdio `{command,args}` (no `type` key); http `{type:"http",url}` | exists |
| `cursor` | `~/.cursor/mcp.json` | `mcpServers.leankg` — `{command,args}` | exists |
| `codex` | `~/.codex/config.toml` | `[mcp_servers.leankg]` table via `toml_edit` (comments/order preserved) | exists |
| `gemini` | `~/.gemini/settings.json` | `mcpServers.leankg` | exists |
| `opencode` | `~/.config/opencode/opencode.json` | `mcp.leankg` — `{type:"local",command:[…],enabled:true}` / `{type:"remote",url,enabled:true}` | **new writer** |
| `omp` | `~/.omp/agent/mcp.json` | `mcpServers.leankg` — `{type:"stdio",command,args,enabled:true}` / `{type:"http",url,enabled:true}` | **new writer** |

**URL contract.** Default stdio entry: `<current exe> mcp-stdio` — **no `--project` flag** (server resolves from process cwd, FR-ZCP-01 clause 1). `--remote URL` emits the bare `URL` (e.g. `http://localhost:9699/mcp`) — **no `?project=` suffix** (clause 2: server-initiated `roots/list` resolution, in review on PR #268). `--project PATH` remains the explicit escape hatch and is the only way a `--project`/`?project=` gets emitted. **Docker is the one documented exception:** the container cannot see host cwds, so `--docker` (or `--remote` to a Docker-hosted server) emits `?project=<container-mount>` and prints the mount table (`/workspace` = this repo; per-repo mounts per local `.dockerfile`) instead of guessing.

**`--register-cwd`.** Writes a per-client session-start hook that runs `leankg add <cwd>` (FR-ZCP-13 first-run setup contract, in review on PR #268) — real effect: attaches the project, persists setup mode, kicks the background indexer. It does **not** write a cwd→project table: the persistent session-registration table is FR-ZCP-01 clause 3, explicitly out of scope here. Clients with no hook mechanism get a printed note naming the manual command (zero dead ends).

**Zero dead ends.** `install` output always prints the next step: if the cwd project is unregistered, print `leankg add <cwd>`; always print "restart the client". The MCP `mcp_install` verb keeps its project-local behavior (`.mcp.json` + instructions) and gains the same project-less URL contract.

**Env hygiene (FR-ZCP-12 T1).** The `LEANKG_*` inventory is documented in one table, generated from source and CI-pinned — the table itself is the single source of truth for the count (a hand-typed total here would be exactly the unverifiable claim this AC polices; the last manual figure, "116 = 88+28+1", summed to 117 and matched no derivation). The happy path requires **zero** env vars beyond the one hard prerequisite (`LEANKG_PG_URL`). Every "zero-config"/"no-setup" sentence in README/docs names the script or CI job that executes it literally.

**Config-block parity.** `install`/`connect`/`mcp_install` emit **exactly one JSON (or TOML) block** per client, byte-identical to the docs snippet — snapshot-tested per target.

- AC: fresh clone → `leankg install --target omp` → open omp in a repo → tools work, correct project, zero manual URL edits; no emitted config contains `?project=` outside the documented Docker exception; re-run is idempotent (entry replaced, siblings byte-identical); `--remove` deletes only the `leankg` key; snapshot tests pin each target's exact block.

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

### 3.9 Measured Simplicity (FR-ZCP-12) — **P1, young-product adoption contract**

**Narrative.** LeanKG is young; adoption is won by products that are simple *and measurably* simple. The friction audit (2026-09-04) put numbers on the debt: **76/73 MCP tools** (exact-count CI-pinned, `src/mcp/tools.rs:1156-1160` — two pruning rounds already went 87→76, so the momentum exists), **103 CLI verbs** (`src/cli/mod.rs`), **116 `LEANKG_*` env names** (~5 first-run relevant), a **10-step / 8-decision** first-value walkthrough, error copy that fails the two-question rule (`Unauthorized`, `Unknown tool`), and a README saying "85+ tools" while the registry pins 76. Competitors set the bar: zg = 1 default tool (6 full) + freshness + install; context7 = 2 tools; Supabase publishes "under 2 minutes"; Stripe ships ~200 error codes each with `code` + `doc_url`; clig.dev: suggest the next command, never dead-end. Per the 2025 Stack Overflow survey, 46% of developers actively distrust AI-tool accuracy — LeanKG's consumers are verification-hungry agents that reward deterministic, low-friction surfaces.

**FR-ZCP-12 — Measured-simplicity contract (Should Have, P1)** — three tiers, ordered cheap-first:

- **T1 — Error & config honesty (cheap; ship first).** Every user-facing CLI + MCP error carries a **stable code, a human cause clause, and a runnable fix** naming the concrete command/flag/env var, plus a doc anchor — Stripe's `code`+`doc_url` model; clig.dev's "what went wrong + how do I fix it". Immediate victims from the audit: `Unauthorized` (say which env/flag sets the token), `Unknown tool` (suggest the nearest match + the `full` catalog hint), low-confidence empty pages (link the fallback tool). CI lints: error variants enumerated from source vs a catalog (100% coverage); fix-clause lint on every error string (allowlist shrinks, never grows). Config surface: one copy-paste JSON block per client (FR-ZCP-04), docs snippet byte-identical to generated (snapshot test); every "zero-config" claim maps to a named script/CI job that runs it literally (Vercel pattern) — unverified claims get deleted, not footnoted.
- **T2 — Published, CI-timed time-to-first-value.** The happy path (`install → leankg init → leankg mcp-http → one JSON config block → first useful query`) is measured in CI on a fresh cold environment and the number is **published** in README + quickstart. Target: **first useful MCP query ≤ 5 minutes** (embeddings stay out of the promise; Supabase's "under 2 minutes" and Convex's 7-step one-command path are the reference bar; zg publishes no TTFV number at all — publishing one wins mindshare). If measurement says worse, the PRD number changes, not the test.
- **T3 — CI-pinned one-tool invariant (v4.3.1 re-scope; the ≤ 12 budget + `full` opt-in is superseded by the hard cutover).** CI enforces `ToolRegistry::list_tools().len() == 1` and verb-catalog membership for every capability; the single tool description documents the verb mechanism. Audit/read-only/write-lock decisions resolve the effective capability from the envelope **before** any gate.
- AC-T1: 100% of error variants resolve to a catalog entry with doc anchor; every error string contains cause + runnable fix; docs config block == generated block (CI snapshot); zero unverifiable zero-config claims.
- AC-T2: CI times the cold happy path end-to-end; README publishes the measured number; a regression above 5 min fails the gate.
- AC-T3 (v4.3.1): registry length == 1 (CI-enforced); every legacy capability resolvable as a verb (catalog lint); unknown names refused with `LEANKG_ERROR_UNKNOWN_TOOL` naming the nearest verb; audit records the effective capability.


---

## 4. Architecture (HLD Summary)

| Layer | Today | Zero-config delta |
|---|---|---|
| Transport | axum HTTP MCP (`/mcp`) + stdio; Bearer + DB token store; `?project=` routing | Resolution layer **in front of** routing (FR-ZCP-01); `?project=` demoted to escape hatch |
| Storage | PostgreSQL + pgvector, **one database, schema-per-project** (`leankg_p_<hex(canonical root)>`; per-connection `search_path` pin, per-schema migrations + HNSW) | Registry table + portfolio scope + cross-schema portfolio queries (FR-ZCP-09); fleet reconciliation (FR-ZCP-10) |
| Indexing/embeddings | tree-sitter graph + optional embeddings (`--features embeddings`), incremental watcher — single-project-per-process, inline `ensure_project_indexed`; per-model collections exist but **no model stamp/guard** on the vectors | Lazy auto-attach + **background** first index (FR-ZCP-02); tiers T0/T1/T2 + one indexer slot + hot-set LRU (FR-ZCP-09); pinned catalog + model-stamped vectors + rebuild guard + single-flight (FR-ZCP-11) |
| Tools | Was 76/73 raw tools exact-count CI-pinned; v4.3.1 hard cutover → **1 tool** (`leankg_context`) with ~76 capabilities as verbs | One-tool envelope (FR-ZCP-03 end-state); published TTFV (FR-ZCP-12 T2); error code+fix catalog (FR-ZCP-12 T1, DONE); portfolio-scoped answers from T0 manifests |
| Memory | RecallStore = **JSONL files** under `<project>/.leankg/` (read path complete; write path dead — v3.8.8 audit); `knowledge_entries` per-schema PG | `session_retain` + auto-recall live (FR-ZCP-07, rides FR-SMA-01..04); portfolio memory federation (FR-ZCP-09) |

**Trust boundaries:** loopback-only HTTP by default; Bearer auth independent of embedding egress — but note (v4.5.0 correction): **remote embedding now exists** (`LEANKG_EMBED_PROVIDER=openai`, `src/embeddings/provider.rs` `OpenAiCompatibleProvider`, usable without the `embeddings` feature; catalog: Qwen3-Embedding-4B / jina-v3 / gemini-embedding-2|001). Setting it ships embedding input blobs (element names + doc lines, not raw source bodies — `text_blob.rs`) to a third-party API; it is an explicit opt-in and the stale "no remote embedding" claim is hereby corrected.

---

## 5. Milestones

| Milestone | Scope | Gate |
|---|---|---|
| **M1 — Zero-config attach** | FR-ZCP-01, FR-ZCP-02, FR-ZCP-13 | New repo, zero config → correct answers; no "not initialized" failures; one setup question (auto/manual) honored everywhere; `leankg add` returns instantly with status |
| **M2 — One-tool surface** | FR-ZCP-03 (+04) | Default set = 1 router tool; ladder degrades L3→L0 with `retrieval` provenance and zero hard errors; v3.8.5 probe suite passes; `install --target` writes project-less URLs |
| **M3 — Honest search** | FR-ZCP-05, FR-ZCP-06 | FTS ranking + freshness in every response; no F1 regression |
| **M4 — Harness memory** | FR-ZCP-07 (rides FR-SMA-01..04) | retain → recall round-trip works in OMP + OpenCode sessions; mnemopi-compatible bank/cursor contract verified against OMP session resume |
| **M5 — Defensible evidence** | FR-ZCP-08 | Pinned, ≥3-trial, judge-blind cross-tool report published |
| **M6 — Org-scale portfolio** | FR-ZCP-09, FR-ZCP-10 | 100-repo parent: registry rows ≠ indexed repos; portfolio queries answer from manifests; no eager indexing; `doctor --deep` reports fleet drift |
| **M7 — Embedding correctness** | FR-ZCP-11 | Model-stamp mismatch → explicit rebuild error, never mixed-model results; rehash-confirms-fresh; truncation accounting in `mcp_status` |
| **M8 — Measured simplicity** | FR-ZCP-12 (T1 immediately; T2/T3 after M1/M2) | Error catalog 100% + fix clauses CI-linted; README publishes the CI-timed TTFV; default-tool budget CI-enforced |

Order: M1 → M2 → M3 → M4 → M5 → M6 → M7 → M8, with M8's T1 tier (error/config honesty) pulled forward immediately — it is docs-and-strings-cheap and multiplies every later milestone's adoption. M1 and M2 are the adoption blockers; M3/M4 are quality gates; M5 is evidence; M6 is the org-scale moat; M7 hardens the embedding layer that M6's tiers depend on; M8 keeps the young product honest about its own surface. The router ladder (M2) and the setup contract (M1) are the "fits every scenario" story: any repo, any capability state, one tool, no dead ends.

---

## 6. Non-Functional Requirements

| Metric | Target |
|---|---|
| Project resolution overhead | < 5 ms per connection (cached) |
| First-query-on-unindexed-repo | Non-error response < 500 ms; background index per existing SLA |
| Router capability probe | < 10 ms per request (limit-1 probe + catalog reads; cached per connection between cwd changes) |
| Ladder degradation | Same query returns non-error results at every rung L0–L3; `retrieval: {rung, reason}` on 100% of index-backed responses; zero "not initialized"/no-vector hard errors in the default toolset |
| Default toolset surface | **1 tool** (`leankg_context`) — hard cutover, CI-enforced; capabilities ride the `{verb}` envelope |
| One-tool envelope (T3, v4.3.1) | `list_tools().len() == 1` CI-enforced; envelope resolved before RO-gate/write-lock/audit; unknown verb → catalog refusal with nearest-verb fix |
| Storage | PostgreSQL + pgvector only — one database, schema-per-project; the registry is the project SoT |
| MCP HTTP | loopback by default; Bearer + DB-backed access-token store |
| Portfolio attach (T0) | One registry INSERT + depth-limited manifest scan; zero eager indexing |
| Portfolio query (unindexed children) | Candidate repos + per-child freshness < 500 ms; fan-out capped (`max_repos_per_query`) |
| Model-stamp integrity | 100% of vector collections carry `{model_id, revision, dimensions, distance, provider}`; mismatch → explicit rebuild error on query/append, never mixed-model results |
| Embedding batch validation | 100% of embed/import batches validated (count, dimension, finiteness); truncations counted and surfaced via `mcp_status` |
| Single-flight indexing | ≤1 active index/embed job per project root; concurrent requests coalesce |
| Error contract (T1) | 100% of CLI+MCP error variants: stable code + cause clause + runnable fix + doc anchor; CI lints every string; allowlisted exceptions shrink, never grow |
| Time-to-first-value (T2) | Cold happy path (install → init → serve → one JSON config block → first useful query) ≤ 5 min, CI-timed on a fresh environment, number published in README |
| Claim hygiene (T1) | Every "zero-config"-class README/docs claim maps to a named script or CI job that executes it literally; claims without a passing script are deleted |
| Setup friction (FR-ZCP-13) | Exactly one auto/manual question per user, persisted; `leankg add` returns < 2 s; manual mode never auto-indexes |

## 7. Historical Record

All superseded material is preserved and linked, not deleted:

- **PRD history v2.0 → v3.8.9** (competitive analyses: Graphify, CBM, TencentDB, MemPalace, Codez, zvec-grep; harness-era repositioning; session-memory audits): [`archive/prd.md`](archive/prd.md)
- **Task-tracker history (560+ items, waves 0–4, release gates):** [`archive/prd-task-tracker.md`](archive/prd-task-tracker.md)
- **zvec-grep audit (2026-09-03):** [`archive/analysis/zvec-grep-vs-leankg-2026-09-03.md`](archive/analysis/zvec-grep-vs-leankg-2026-09-03.md)
- **OMP memory-integration draft (2026-09-03):** [`archive/planning/2026-09-03-leankg-omp-memory-integration-draft.md`](archive/planning/2026-09-03-leankg-omp-memory-integration-draft.md)
- **Design/ERD/architecture docs:** [`archive/design/`](archive/design/), [`archive/erd.md`](archive/erd.md), [`archive/architecture.md`](archive/architecture.md)
- **OMP memory-backend + zvec-grep embedding audits (2026-09-04, installed-source @ `node_modules/@oh-my-pi/*`, zvec-grep main@d756cc7):** findings folded into this PRD (§3.1, §3.5, §3.8); full citations inline
- **Simplicity research sprint (2026-09-04, three parallel scouts):** repo friction audit (file:line — 76/73 tools, 103 CLI verbs, 116 env names, 10-step walkthrough, error-copy gaps); competitor mechanics (zg, context7, serena, Desktop Commander, gitleaks — live-fetched URLs); onboarding playbooks (Supabase/Convex TTFV, Stripe error codes, clig.dev, Vercel, Stack Overflow 2025) → findings folded into §2.6, §3.9 (FR-ZCP-12), §5 M8, §6
- **One-tool ladder + setup-contract design (2026-09-04, two scouts):** retrieval-engine inventory (exact/regex, ontology keyword, pgvector ANN+rerank, graph BFS) with capability probes (`state.has_any`, `::relations`, `index_inventory`), the unregistered `orchestrate` parser, and the zero-FTS schema audit → folded into §3.1 (FR-ZCP-13), §3.2 (ladder), §3.3 (bridge tier)
- **Rust→Go rewrite feasibility study (2026-09-10):** [go-rewrite-analysis.md](go-rewrite-analysis.md) — 168k-LOC audit with pros/cons, shipped-vs-vision gap table (target ≈90% already live), Go target architecture (WAL sqlite + PG/pgvector, watermark freshness, MCP/REST/ConnectRPC from one core, provider-first embeddings), 7-wave migration plan, evidence index

*Last updated: 2026-09-10 (v4.6.0 — Go engine at full surface parity, Rust tree removed; deferred: ontology workflows, compress, LSP, Android extractors, local-ONNX runtime)*
