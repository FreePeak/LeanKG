# LeanKG → Go Rewrite: Deep Codebase Analysis & Target Design

**Date:** 2026-09-10
**Codebase version analyzed:** v0.30.0 (Cargo.toml) / PRD v4.4.3
**Method:** full-source exploration (5 parallel deep-dive passes over `src/mcp`, `src/db`, `src/indexer`+`src/graph`, `src/embeddings`+`src/memory`, CLI/ops/CI) plus first-hand verification of every load-bearing claim. Evidence anchors are `file:line` references; the small set of claims marked `[INFERENCE]` could not be verified from source and should be proven before build.

---

## 1. Executive summary

LeanKG is a 323-file, 168,121-line Rust workspace that is **much closer to the stated target than expected**: the 3-tool MCP surface (`set`/`get`/`status`), the exact→fuzzy→semantic query ladder (L1→L2→L3 + L0 cold), SQLite-default storage with PostgreSQL+pgvector opt-in, local-ONNX-default **and** OpenAI-compatible API-provider embeddings, model stamping with hard rebuild guards, mnemopi-compatible agent-memory banks, a freshness contract on every response, and config writers for 6 coding clients — all already shipped and live-verified on both engines.

The real problems are **architectural, not feature gaps**:

1. **Three query representations for one data model.** Rust methods author Datalog-IR strings → run through either the CozoDB runtime (sqlite) or a hand-written 4,389-LOC Datalog→SQL translator (Postgres) — plus a 1,890-LOC in-memory Datalog interpreter used only by tests. The repo's own W8 migration plan calls this ~5,700 lines of dead weight (`docs/archive/plan-remove-cozo-datalog-sql-migration.md`).
2. **Writer/reader separation is nominal.** `leankg-mcp` and `leankg-worker` are 10–13-line re-exec wrappers around the same monolith; separation is a `--read-only` flag. CozoDB owns the SQLite file single-process (no WAL control), freshness lives in per-process in-memory caches, and concurrent embed+query hits an **FFI abort class** (`#286`) that bypasses Rust panic hooks entirely.
3. **Weight.** A 176 MB release binary (measured: 176,115,392 bytes; ONNX + 43 tree-sitter grammars), 4–5-minute release builds, 5 files over 5.7k LOC, 114 CLI verbs, 83 MCP verbs, 116 env-var names.

**Verdict:** a Go rewrite is **feasible and lower-risk than a typical rewrite**, because (a) every query is already a single-relation scan + equality filter with exactly one ANN shape (measured, `docs/archive/plan-migrate-cozo-to-postgres-pgvector.md` §2.1) — so Datalog dies without regret, (b) the target surface (3 tools, ladder, dual engine, providers) is already specified and live-tested, and (c) the Go ecosystem now covers every pillar: official MCP Go SDK, official tree-sitter bindings, pgvector-go, pure-Go SQLite. The rewrite's center of gravity is the indexer (tree-sitter extraction, ~27k LOC) and local-ONNX embeddings; both have sound strategies below. A working core (MCP 3-tool + exact/fuzzy ladder + SQLite, no embeddings) is ~3–4 weeks of focused work; full parity ~3–4 months.

---

## 2. The codebase today — inventory

### 2.1 Scale

| Metric | Value | Evidence |
|---|---|---|
| Rust source files | 323 | `git ls-files '*.rs'` |
| Total Rust LOC (src + tests) | 168,121 | `wc -l` over tracked `.rs` |
| LOC by subsystem | indexer 27,237 · db 18,613 · mcp 16,397 · graph 15,215 · embeddings 8,177 · web 5,763 · benchmark 4,468 · ontology 3,972 · compress 3,516 · lsp 2,639 · retrieval 2,363 · cli 2,222 · top-level files 10,076 (main.rs 7,630) | per-directory `wc` |
| Binaries | 3 — `leankg`, `leankg-mcp` (13 LOC re-exec, RO forced), `leankg-worker` (10 LOC re-exec) | Cargo.toml:18-25, `src/bin/` |
| MCP tool surface | **3 tools** (`set`/`get`/`status`), CI-pinned | `src/mcp/tools.rs:76-78`, `docs/mcp-tool-contract.md` |
| MCP verb catalog (actions) | **83** entries | counted over `verb_catalog()` body, `src/mcp/tools.rs:272-364` |
| CLI verbs | **114** (74 top-level + 40 nested in 8 enums) | `src/cli/mod.rs:64-1023`, `:35-1353` |
| Env vars | 116 documented names (CI-pinned table mandated) | `docs/prd.md:297,436` |
| Tests | 100 test files in `tests/` (96 `.rs` + 4 `.sh`); ≥162 `src/` files carry inline tests; documented full PG suite = 49/49 suites, 3,222 tests | ScoutOps; `docs/prd.md:25` |
| CI workflows | 6 (ci, semantic-release, release, quickstart, leankg-update dogfood, perf-gate) + 1 composite action | `.github/workflows/` |
| Release binary | **176,115,392 bytes** (176 MB) | measured `~/.cache/cargo-target/leankg-target/release/leankg` |
| This repo's own SQLite index | **662 MB** (`.leankg/leankg.db`; 578 files, ~10.2k elements, ~12.9k vectors per PRD live-validation) | measured; `docs/prd.md:35` |

### 2.2 How a request flows today (verified end-to-end)

```
MCP stdio (rmcp 1.4.0)  ─┐
MCP HTTP  (custom axum   ├─► resolve_3tool (envelope unwrap BEFORE any gate)
  dispatcher :9699,      │        │
  POST JSON-RPC + SSE,   │        ▼
  session id + server-   │   read-only gate ─► RBAC (Admin/Contributor/Viewer)
  initiated roots/list)  │        ▼
                         │   semaphore(100) + timeout watchdog (30s; floors 120s/300s)
REST  web UI  :8080  ────┤        ▼
REST  API     :8081  ────┤   dispatch cache (reads) ─► write_lock (tokio Mutex) ─►
                         │   project routing (nearest .leankg walk, auto-attach,
                         │   background first-index)
                         │        ▼
                         │   ToolHandler::execute_tool — big match, ~83 verbs
                         │        ▼
                         │   freshness stamp (30s TTL cache + WriteTracker dirty)
                         │        ▼
                         │   fire-and-forget hash-chained audit record
                         ▼
                DbBackend::run_script(datalog_string)
                   ├── CozoDB 0.7.6 storage-sqlite  (default; native Datalog + ::hnsw)
                   └── PostgresBackend → Datalog→SQL translator (4,389 LOC)
                       + hand-rolled sync pool (default 5) + block_in_place
```

Evidence: gate order `src/mcp/server.rs:4715-4743` (envelope before gates), RO gate `:3592-3598`, WRITE_TOOLS `:45-72`, write_lock `:292`, semaphore/timeout `:79-142`, freshness `:878-908`, audit `src/audit/mod.rs`, transports `server.rs:2279-2348` (rmcp stdio) and `:2811-2816`/`:4418-4453` (axum HTTP).

### 2.3 The storage layer is three engines pretending to be one

| | SQLite (default) | PostgreSQL (opt-in) |
|---|---|---|
| Engine | **CozoDB 0.7.6** on `storage-sqlite` — parses its own Datalog dialect | plain Postgres via **sync** `postgres 0.19` client + hand-rolled Condvar pool |
| Query authoring | native Datalog | same Datalog, translated at runtime |
| Vectors | Cozo `::hnsw` (cosine, dim 384, ef_construction 20, m 50) | pgvector `vector(384)` + HNSW `m=16, ef_construction=200` (`src/db/pg/schema.sql:362-369`) |
| Isolation | one file per project `<project>/.leankg/leankg.db` | one database, **schema per project** `leankg_p_<hex(canonical_root)>` (`src/db/backend.rs:2795-2812`), per-schema migration ledgers (7 steps) |
| Fuzzy (L2) | **lowercased `LIKE` only — no trigram, no FTS** (`fuzzy_find_elements` sqlite path; PR #284 fix) | `pg_trgm` similarity + ILIKE degrade (migration 007) |
| Migration DDL | Rust string literals `:create …` | embedded `schema.sql` + `migrations/*.sql` |

The translator (`src/db/pg/translate.rs`) maps ~115 Datalog shapes with hard-coded column catalogs for arity polymorphism (11/12/13-column `code_elements` — "the single most brittle spot in the translator", translate.rs:514,923-937). `:create`/`:replace` are no-ops on PG; unknown relations mis-map columns; cross-relation reads are unsupported; `run_raw_query` is explicitly out of scope for translation (translate.rs:9-11).

**Key measured fact** (2026-08-04 audit, `docs/archive/plan-migrate-cozo-to-postgres-pgvector.md` §2.1): *all queries are single-relation scans + equality filters; no `:group`/`:join`/recursion in query strings; group/join happen in Rust; exactly ONE ANN query shape.* Graph algorithms (shortest path, impact radius) are Rust code, not Datalog. **This is why the storage layer can be replaced with plain SQL.**

223 `run_script` Datalog sites remain across 19 files (108 in `src/graph/query.rs`; `docs/archive/roadmap-2027-v2.md:92`). The W8 "SQL-first" seam (`src/db/sql.rs`, 666 LOC — `SqlParam`/`SqlRow`, COPY, pgvector text binding) is landed; only wave 1 of 6 site-conversion waves is done.

### 2.4 The query ladder already implements exact → fuzzy → semantic

`src/mcp/router.rs` (1,215 LOC) — `get` with no `action` = the NL router:

| Rung | Condition | Implementation |
|---|---|---|
| L0 cold | no elements | guidance response + background index kick (never errors) |
| L1 exact | in-ladder fallback on zero results | `find_element`/`find_elements_by_name_exact`/regex (query.rs:171, 2997, 3023); L1-first for identifier queries (#290) |
| L2 keyword | elements, no vectors | PG: pg_trgm + ontology concept discovery; **sqlite: LIKE-substring + ontology only** |
| L3 vector | vectors present | embed query → ANN (adaptive_k 50→300, ef=max(k·2,50)) → filters → cross-encoder rerank (ANN-order fallback) → ontology traversal |

Every response carries `retrieval: {rung, reason}` + `freshness: fresh|possibly_stale|cold`. Capability probe is 3 cheap limit-1 reads, <10 ms target, cached (router.rs:81-95, 265-291). Measured router latency on this repo: 6.07 s cold → 2.31 s warm (`docs/prd.md:35`).

### 2.5 Embeddings: local ONNX default **and** API providers already exist

- **Catalog** (`src/embeddings/registry.rs:66-124`): 5 pinned entries — `bge-small-en-v1.5` (384-d, **local default**, revision `onnx:bge-small-en-v1.5@main`) + 4 **OpenAI-compatible API models**: `Qwen3-Embedding-4B` (2560-d), `jina-embeddings-v3` (1024-d), `gemini-embedding-2` / `gemini-embedding-001` (3072-d each).
- **Provider trait** (`src/embeddings/provider.rs:60-64`): `FakeEmbedProvider`, `LocalOnnxProvider` (fastembed 4.9.1 / ORT, feature-gated), **`OpenAiCompatibleProvider`** (in-process reqwest client, `provider.rs:137` — usable **without** the `embeddings` feature). `LEANKG_EMBED_PROVIDER=local|openai`.
- **Correctness machinery** (FR-ZCP-11): per-model collections (`embedding_vectors_<model>` / `embedding_state_<model>`), `ModelStamp {model_id, revision, dimensions, distance, provider}` persisted in `emb_stamp_*` with a hard rebuild guard on write and query-side degrade to L2 on mismatch; `CHUNKER_VERSION` folded into SHA-256 content hashes (a bump invalidates everything → full re-embed).
- **Reranker:** bge-reranker-v2-m3 (~600 MB ONNX), falls back to ANN order on load failure.
- **Offsite batch** (`embed --dry-run` → NDJSON → `scripts/embed_batch.py` on GPU → `embed --import` with dim-guard + drift-verify + resume) — language-agnostic, ports unchanged.
- **Gaps:** revisions are coarse strings (`@main`, `api:2026-01`), not the 40-hex commit pins the PRD mandates; per-file atomic replace + truncation accounting + 3-signal change detection are specced but **not implemented** (§7, con C9).

### 2.6 Agent memory already has the right shape (and is file-based by design)

- **Banks** (`src/memory/`): name = `sanitize(basename(cwd) + "-" + base36(wyhash(abs_cwd, 0)))` ≤64 chars, **cwd-only, never git root**; scope matrix global / per-project / per-project-tagged. Storage = append-only JSONL per bank under `<project>/.leankg/memory/`; retain honors the `retained_through_user_turn` integer cursor; recall = token-overlap scored, zero-match entries never surface; get/update/forget/invalidate = rewrite-bank. `src/memory/mod.rs:6` states the sqlite/PG split deliberately does not apply.
- **Session offload** (`src/session/mod.rs`, 884 LOC): bulky tool payloads → `.leankg/sessions/<id>/refs/<node_id>.md`, compact canvas stays in context; `session_recall` restores bit-for-bit; lessons index with SHA-256 dedup.
- Known limits: O(n) full-file scans, constant importance (0.65), lexical-only recall (no embeddings).

### 2.7 Writer/reader separation today: a flag, not a boundary

- `leankg-mcp` = re-exec `leankg --read-only` (`src/bin/leankg_mcp.rs`); `leankg-worker` = re-exec with worker-only CLI. Same handlers, same process model.
- Cozo owns the SQLite file single-process: two processes cannot share a RocksDB data dir, and the code relies on RO opens "legitimately co-existing" with a writer handle (`src/db/sqlite_backend.rs:130-142`). **No `PRAGMA journal_mode=WAL` / `busy_timeout` exists anywhere in `src/`** (grep-verified) — Cozo gives no control.
- Freshness + dirty-tracking are **per-process in-memory** (30s TTL `parking_lot` HashMap; `WriteTracker` AtomicBool) — invisible cross-process. The L1 cache layer adds nine moka caches (60s TTL, 10k entries) with hand-rolled invalidation.
- Concurrency defect class: **#286** — sqlite live server died exit-1, no panic trace, during concurrent embed+query; suspected cozo C++/ONNX FFI `abort()` which bypasses Rust panic hooks by construction (`src/main.rs:120-146`, mitigation-only comment). Plus an open **TOCTOU race** in L1 invalidation: `invalidate_l1_caches_public` drains engines under the lock, then awaits each engine's invalidate *after* releasing it (`src/mcp/server.rs:3551-3563`) — a concurrent reader can re-insert an engine built on pre-write state in between. A generation-counter fix is pending.
- Write serialization inside one process: `write_lock` (tokio Mutex) + priority write bus (`ToolWrite` dequeues ahead of `EmbedWrite`, `src/db/write_bus.rs:11-15`) + PG advisory locks + `embed.lock` pid file for single-flight.

### 2.8 Transports today: MCP JSON-RPC + REST, no RPC

- MCP: rmcp stdio + custom axum HTTP dispatcher (JSON-RPC POST, SSE stream, `Mcp-Session-Id`, server-initiated `roots/list`; responses TOON-wrapped).
- REST: web-UI server `:8080` (~40 routes: elements/annotations/teams/graph views/export; `src/web/mod.rs:428-516`) + API server `:8081` (`/api/v1|v2` status/search, auth register/login/token/org; `src/api/mod.rs:123-155`).
- **No gRPC/RPC surface** — grep for tonic/prost/grpc finds only the microservice *extractor* (it mines `grpc.NewClient(...)` calls out of indexed Go code, `src/indexer/microservice.rs:110-150`).

---

## 3. Gap analysis: the stated vision vs. shipped reality

| Vision requirement | Status today | Verdict |
|---|---|---|
| Lightweight setup memory for agents | zero-env-var SQLite happy path, `leankg add` <2s, connect writers for 6 clients, TTFV 88 s | ✅ shipped (weight is in the binary, §7 C6) |
| Easy integration into coding tools | MCP stdio + HTTP, 6 client config writers, cwd-based project resolution | ✅ shipped (FR-ZCP-01/04) |
| **3 simple tools: import, query, status** | 3-tool surface shipped as `set`/`get`/`status`; `get` w/o action = NL router | ✅ shipped — Go design renames surface to `import`/`query`/`status`, keeps old names as aliases |
| Data stored in layers: index → embedding | `code_elements`/`relationships` (+inventory, knowledge) then `embedding_state_<model>`/`embedding_vectors_<model>`/`emb_stamp_<model>` | ✅ already layered |
| Query: exact → fuzzy → semantic | L1 exact → L2 fuzzy → L3 semantic (+L0), provenance block on every response | ✅ shipped; **fuzzy ranking is PG-only** (sqlite L2 = LIKE substring) — fix in Go |
| Memory for long-running / big-context agents | mnemopi-compatible banks + cursor resume + session offload + recall | 🟡 slice 1 done; hindsight-shaped HTTP API + injection AC outstanding (FR-ZCP-07) |
| SQLite (default) + Postgres with vectors | both engines live-verified same binary | ✅ shipped |
| Embedding: local (default) + API providers | local ONNX default + 4 OpenAI-compatible API models, stamping, offsite batch | ✅ shipped (pins too coarse) |
| Writer and reader separated | re-exec wrappers + `--read-only` flag; per-process caches; single-writer Cozo file; FFI abort class under concurrency | ❌ **the genuine gap** — redesigned in §6.6 |

**Bottom line:** the rewrite is not "build the product in Go"; it is "rebuild the engine under a proven product spec, and fix the one architectural promise (read/write separation) the current storage engine structurally cannot keep."

---

## 4. Pros of the current Rust implementation

Being fair to what exists — these are real strengths a rewrite must preserve:

1. **The product spec is de-risked.** 3-tool envelope resolution with gates-before-dispatch, ladder degradation with provenance, freshness vocabulary, model stamps, bank naming/cursor semantics — all implemented, unit-tested, and live-verified on both engines. The Go build copies a *tested specification*, not a guess.
2. **Performance where it matters, measured.** HNSW top-50 (10k×384): **4 ms**, recall@50 = 1.0000; bulk embed COPY 7,695–9,579 vectors/s; 371k-function codebase ≈ 47 s (~11× faster than Cozo); router 2.31 s warm; e2e quickstart smoke 88 s vs 300 s budget (docs/archive/analysis/pg-phase0-spike.md, pg-migration-report.md, README).
3. **Engineering discipline is unusually strong.** 3,222-test documented suite; CI gates for formatting, clippy `-D warnings`, all-targets compilation (catches integration-test rot), tool-contract drift, claim hygiene, perf regression vs `benchmarks/baseline.json`; error catalog with stable codes + runnable fixes; hash-chained append-only audit ledger.
4. **Dual-engine honesty.** Both SQLite and Postgres paths are actually run and verified (v4.4.3 dual-engine sweep), not aspirational.
5. **Single static binary, zero-config default.** No Docker, no Postgres, no env vars for the happy path — the right product instinct, worth carrying into Go.
6. **Ops surface:** `doctor --deep` (8 checks, exit 0/1/2, JSON output), pack/export (deterministic snapshots), self-update, weekly TTFV gate, dogfooding workflow that indexes its own source.
7. **Rust-specific payoffs being given up:** memory safety without GC, the fastest cold query latencies of the three rungs' implementations, and mature crates (rmcp, tree-sitter grammars, fastembed/ort) that required real integration work to reach today's stability.

---

## 5. Cons — pain points, root-caused

Each con names the root cause; the Go design in §6 either deletes it or isolates it.

| # | Con | Root cause | Evidence |
|---|-----|-----------|----------|
| C1 | **Datalog/translator tax** — 4,389-LOC translator + 1,890-LOC test interpreter + hard-coded column catalogs + arity runtime probing + 223 string-built script sites; `run_raw_query` untranslatable | queries authored in a niche query language over an embedded DB | translate.rs; fake.rs; roadmap-2027-v2.md:92; W8 plan |
| C2 | **Cozo single-writer file ownership** — no WAL control, multi-process sharing unsupported, RO/writer co-existence is best-effort | engine choice (CozoDB), not code | sqlite_backend.rs:130-142; write_bus.rs:3-9 |
| C3 | **FFI abort class (#286)** — concurrent embed+query kills the process with no trace; unfixable at root while cozo-C++/ONNX share the process | FFI `abort()` bypasses panic hooks | main.rs:120-146; PRD v4.4.1 open-bug table |
| C4 | **Per-process cache/state** — freshness TTL cache, WriteTracker, L1 moka ×9, dispatch cache; invalidation is hand-ordered and has shipped bugs (#350 multi-project) and an open TOCTOU race (drain-then-await) | state lives in memory, not in the DB | server.rs:878-908, 3551-3563, 3795-3808 |
| C5 | **God files / high coupling** — main.rs 7,630, query.rs 7,894, extractor.rs 7,765, server.rs 6,465, handler.rs 5,754; 3 lock families in one dispatcher struct | monolith accretion | LOC table §2.1; ScoutMcp §6 |
| C6 | **Weight & build friction** — 176 MB binary; 4–5 min release builds (CI dev-profile all-targets 4m58s); `embeddings` feature build drift (errors the plain build misses — 3 caught in #355); wasm32 carve-outs; 4-target native release matrix (no cross toolchain); 43 grammar crates | ONNX + grammars + feature matrix in one binary | Cargo.toml; measured sizes; memory of #346/#355 |
| C7 | **Surface sprawl** — 114 CLI verbs (PRD audit said 103 — drift), 83 MCP verbs behind 3 tools, 116 env names, install.sh 1,788 LOC duplicating `connect/` writers, npm wrapper with hardcoded stale fallback v0.17.9 | feature accretion vs the measured-simplicity contract (FR-ZCP-12) | ScoutOps §1, §7; tracker |
| C8 | **Doc/config drift** — README says "1 tool, ~77 capabilities" vs CI-generated 3-tool contract; PRD header v0.28.1 vs Cargo 0.30.0; tracker rows stale vs code (e.g. FR-ZCP-04) | hand-synced surfaces | README vs docs/mcp-tool-contract.md; Cargo.toml:3 |
| C9 | **Incremental indexing incomplete** — BLAKE3 content-hash machinery exists but is **unwired**; 3-signal (size+mtime → hash confirm) **not implemented**; primary signal is `git diff` with dependent-file expansion + mega-skip heuristics; embed staleness (SHA-256) is the only wired hash | FR-ZCP-11 shipped parts 1–3, not the fast path | content_hash.rs:15-19; indexer scout §2; tracker M7 row |
| C10 | **Ladder asymmetry on the default engine** — sqlite L2 has no fuzzy *ranking* (LIKE substring; no FTS5 anywhere in the repo); pg_trgm is PG-only; ANN distance semantics diverge between engines (Cozo cosine vs pgvector `<->` L2 — same order for normalized vectors, different raw values) | per-engine capabilities drifted | indexer scout §6; translate.rs:13-17 |
| C11 | **Sync PG client + `block_in_place`** — hand-rolled pool of 5 with Condvar; sync scans inside async escape the timeout watchdog (fixed by floors, but the design remains) | translator phase chose sync `postgres 0.19` | backend.rs:506+; server.rs:102-142 |
| C12 | **Cozo project risk** — 0.7.6 is the last release; maintenance concerns documented in the repo's own migration plan; niche Datalog skill for contributors | external dependency | plan-migrate-cozo-to-postgres-pgvector.md §1 |

---

## 6. Target architecture (Go)

One product, three transports, one SQL core, two processes when you want them.

### 6.1 Repository & binary layout

```
leankg-go/
├── cmd/leankg/main.go          # ONE binary: serve | writer | index | embed | connect | doctor
├── internal/
│   ├── core/                   # service layer BOTH transports call (Import/Query/Status)
│   ├── mcp/                    # 3-tool surface via official go-sdk (stdio + streamable HTTP)
│   ├── rest/                   # net/http REST API (stdlib ServeMux; unify :8080/:8081)
│   ├── rpc/                    # ConnectRPC handlers (gRPC + gRPC-Web + JSON from one proto)
│   ├── ladder/                 # exact → fuzzy → semantic + capability probe + provenance
│   ├── store/                  # Store interface: sqlite | postgres
│   │   ├── sqlite/             # modernc.org/sqlite, WAL, FTS5, migrations (go:embed)
│   │   ├── postgres/           # pgx/v5 + pgvector-go, schema-per-project
│   │   └── vector/             # VectorIndex: inproc-cosine | sqlite-vec | pgvector
│   ├── index/                  # tree-sitter extraction (go-tree-sitter), lang registry (data), call graph
│   ├── embed/                  # Provider: openai-compatible | local | offsite-import; ModelStamp
│   ├── memory/                 # JSONL banks (port as-is) + hindsight-shaped HTTP API
│   └── audit/                  # hash-chained ledger (channel batcher)
```

The registry, NodeKinds, ontology YAML schema, stamp format, bank naming, and the envelope mechanics are **pure data / small logic — they port mechanically** (the Rust sources even isolate them for this: `lang/registry.rs` is a data table; `memory/bank.rs` is 165 LOC; `resolve_3tool` is a pure function).

### 6.2 Tool surface: `import`, `query`, `status`

Keep the proven envelope; rename the tool names (old names become aliases for one minor release per the deprecation policy in `docs/mcp-tool-contract.md`):

| Tool | Today | Go |
|---|---|---|
| `import` | `set` — actions: `index` (default), `incremental`, `attach`, `index_docs`, knowledge/ontology mutators, `install` | same action namespace; legacy verbs valid as actions |
| `query` | `get` — any read verb; **no action = NL router** down the ladder | same; router is the default and the headline |
| `status` | `mcp_status` — health/inventory/freshness/backend | same |

Tool count stays CI-pinned (`list == 3`), preserving the one measured-simplicity invariant that survived v4.3.1→v4.4.0.

### 6.3 Storage: plain SQL, two engines, layers preserved

**Delete Datalog entirely.** The Go port writes typed SQL against one `Store` interface — this is the W8 plan's end-state (`docs/archive/plan-remove-cozo-datalog-sql-migration.md` §3: `SqlParam/SqlRow` + `query/execute/transaction/copy_import`), realized natively instead of by waves. The 13 Datalog capabilities the Rust code leans on (storage scout §7a) do not need porting: real PKs replace arity probing; `DELETE … WHERE` replaces read-head+`:rm` scripts; `NOT EXISTS` replaces negation-as-fact; the multi-rule `count(DISTINCT)` special case becomes one SQL query.

Schema (per project: one SQLite file / one PG schema `leankg_p_<hex>`):

```sql
-- LAYER 1: index
code_elements(qualified_name TEXT PRIMARY KEY, element_type, name, file_path,
              line_start, line_end, language, parent_qualified,
              cluster_id, cluster_label, metadata JSON, env, ontology_layer)
relationships(id INTEGER PRIMARY KEY, source_qualified, target_qualified,
              rel_type, confidence, metadata JSON, env)          -- real PK: kills rm-then-put
code_elements_fts(...)                                               -- sqlite: FTS5 trigram tokenizer
index_inventory(...); index_hashes(path PK, blake3_hash, ...)        -- wire the hash at last
knowledge_entries(...); audit_log(...); migrations(...)
-- LAYER 2: embedding (per model; identical shape to today)
embedding_state_<model>(qualified_name PK, content_hash, state, embedded_at)
embedding_vectors_<model>(qualified_name PK, vec)                    -- BLOB (sqlite) / vector(N) (pg)
emb_stamp_<model>(model_key PK, stamp JSON)                          -- {model_id, revision,
                                                                     --  dimensions, distance, provider}
```

Engine choices:

- **SQLite (default):** `modernc.org/sqlite` — pure Go, no CGO, keeps `CGO_ENABLED=0` cross-compilation; FTS5 included. Set `PRAGMA journal_mode=WAL; busy_timeout=5000; synchronous=NORMAL` — **the concrete fix for C2**: WAL gives concurrent readers + single writer without blocking, which Cozo never exposed. Vectors stored as `BLOB` of float32 and scored by an in-process cosine scan. `ponytail:` ceiling — brute-force cosine is O(n·d) (~5 ms at 50k×384 measured class; fine ≤ ~200k vectors/project); upgrade path = `sqlite-vec` loadable extension, which requires a CGO driver build tag (`mattn/go-sqlite3`), kept behind the `VectorIndex` interface from day one.
- **PostgreSQL (opt-in):** `pgx/v5` + `pgxpool` (replaces the sync client, the Condvar pool, and every `block_in_place` — C11) + `pgvector/pgvector-go` (`RegisterTypes` on `AfterConnect`; binds exactly like today's `$n::text::vector`). HNSW `vector_cosine_ops` — **pick cosine explicitly and normalize embeddings**, ending the C10 distance divergence.
- **Schema naming:** re-derive `leankg_p_<hex(canonical_root)>` **or** ship a one-time re-key. Byte-identical reproduction requires replicating Rust's legacy `DefaultHasher` fallback, which has no Go stdlib equivalent `[INFERENCE]` — prefer the re-key path (below) over emulation.
- **Migrations:** `go:embed` SQL files + ledger table, one tx per step (the current per-schema model carries over unchanged).

**Data migration splits by engine — PG users get continuity, SQLite users re-index.**

- **PostgreSQL: reuse the existing 16-table DDL as-is.** `src/db/pg/schema.sql` is the portable asset — plain SQL, plus `pgvector` HNSW (`m=16, ef_construction=200`) and `pg_trgm` indexes. A Go writer can adopt the same tables and indexes, so **existing PG installs need no data migration** (only the additions for real PKs/dedup, applied as a normal migration). This asymmetry is the argument for PG-first wave ordering for server users.
- **SQLite: re-index, do not convert.** Existing `.leankg/leankg.db` files are Cozo-format — the Cozo storage engine owns the file layout, so plain SQLite drivers cannot read its tables `[INFERENCE: verify by opening one live file with the sqlite3 CLI before scheduling]`. Re-indexing is cheap (small repo « 2 min; 371k functions ≈ 47 s measured) and the indexer is the source of truth, so a fresh index avoids every format-compatibility landmine. This also **supersedes the in-flight W8 Datalog-removal plan** (`docs/archive/plan-remove-cozo-datalog-sql-migration.md`): W8's end-state (typed `SqlParam`/`SqlRow` seam, translator deletion, FakeBackend deletion) is exactly this document's §6.3, reached natively in Go instead of through 6 conversion waves in Rust. If the Go rewrite is not approved, W8 remains the correct Rust-side path — the two are alternatives, not complements.

### 6.4 Query ladder: exact → fuzzy → semantic, now symmetric on both engines

Port `router.rs` (probe → select rung → execute → `retrieval{rung, reason}` + freshness) nearly 1:1 — it is 1,215 LOC of pure decision logic and ports mechanically. Engine rungs:

| Rung | SQLite (Go) | PostgreSQL (Go) |
|---|---|---|
| L1 exact | parameterized SQL (`qualified_name`, `name`, prefix, batched IN) | same |
| L2 fuzzy | **FTS5 with trigram tokenizer** (fixes today's default-engine gap, C10) + ontology concept discovery | `pg_trgm` similarity (+ optional tsvector/RRF per FR-ZCP-05 remainder) |
| L3 semantic | `VectorIndex` (inproc cosine → sqlite-vec) | pgvector HNSW |
| L0 cold | guidance + background index kick | same |

Rerank: API provider or ANN-order fallback (the Rust code already treats reranker absence as a supported degrade — reuse that posture; do not port the 600 MB ONNX reranker into the default path).

### 6.5 Embeddings: providers first, local model behind an interface

The `EmbedProvider` trait (name, dimensions, embed_batch) ports as a Go interface with the same three impls:

1. **`OpenAiCompatible`** — port of `OpenAiCompatibleProvider` (one HTTP call; already proven; covers Qwen3/Jina/Gemini/OpenAI/Voyage-compatible endpoints). Ship first.
2. **`OffsiteImport`** — `embed --dry-run` / `embed --import` NDJSON flow ports unchanged (`scripts/embed_batch.py` is Python and stays).
3. **`LocalOnnx` (default experience)** — the hard 10%. Options, in recommended order:
   - **llama.cpp `llama-server` sidecar** (~10 MB binary + ~25 MB GGUF embedding model, MIT): writer process supervises it, embeds over HTTP; zero Go FFI; local by construction; ships as an optional download.
   - **`yalue/onnxruntime_go`** (CGO) + HuggingFace tokenizers via CGO (`daulet/tokenizers`): closest port of fastembed, but you own session/thread config (the Rust code documents the `intra_threads=available_parallelism()` oversubscription trap at models.rs:145) and Go has no mature pure-Go equivalent of HF `tokenizers` `[INFERENCE: evaluate daulet/tokenizers maintenance before committing]`.
   - **Keep the Rust embed worker as a transitional sidecar** via the existing import path if 1–2 stall.

**ModelStamp ports with a fix:** pin real 40-hex revisions (replace `@main` / `api:2026-01`), keep the hard-rebuild guard and query-side L2 degrade — this is small, tested logic (stamp.rs is 276 LOC).

**Security constraint — embedding egress (first-class, not a footnote).** "API-provider embedding" ships indexer output off-machine: the text blob is `qualified_name + doc first line + synthesized signature` (no raw source bodies — `text_blob.rs`), but for a *code* knowledge graph that is still a material egress decision. The Rust implementation today has no redaction or warning on this path, and the same repo already scopes secret-redaction for memory writes (FR-SMA-06). The Go design MUST: (a) default to the local provider; (b) require an explicit opt-in env/flag for any remote provider; (c) state plainly in `status` and `doctor` output which provider is active and that index content leaves the machine; (d) redact obvious secret patterns from blobs before egress. Corrections to the record: `docs/prd.md` §4 previously asserted "LeanKG has no remote embedding" — factually wrong since `OpenAiCompatibleProvider` shipped; fixed in the same change as this document.

### 6.6 Writer/reader separation (the headline fix)

The requirement: writers and readers must not impact each other. Design — two roles, one binary, one database:

```
leankg writer  --project /repo          # owns: watcher, indexer, embedder, all RW handles
leankg serve   --read-only [transports] # owns: MCP stdio/HTTP + REST + ConnectRPC, RO handles only
leankg serve                            # dev default: single process, internal writer pool
```

- **SQLite:** WAL mode makes this real — the writer commits continuously while readers query without blocking (and vice versa); readers open `mode=ro`. The #286 FFI-abort class disappears because cozo-C++ is gone; the L1-invalidation TOCTOU (C4) disappears because **freshness stops being process memory**: the writer bumps a `write_watermark(seq, at)` row per commit; readers compare `index_inventory.last_seq` vs the watermark to compute `fresh|possibly_stale` on every response. No TTL caches, no invalidation ordering, no cross-process blindness — the whole #350/cache-race bug class is structurally impossible.
- **PostgreSQL:** reader pool (`default_transaction_read_only`) + writer pool (single-flight via advisory locks, as today). Readers never take write locks; embed bulk-load uses COPY in the writer only.
- **Priorities:** the write-bus seam (`ToolWrite` before `EmbedWrite`) carries over as one ordered channel in the writer process.
- **Memory banks stay JSONL files** (deliberate: per-project, harness-resumable, DB-independent) — the writer appends, readers mmap-read; both processes agree on the bank name (deterministic from cwd).

This satisfies the letter and the spirit of the requirement at every deployment size: laptop users get one process with internal separation (WAL + channel priorities); shared-server users get real process isolation for free.

### 6.7 Transports: MCP + REST + RPC from one core

- **MCP:** official `modelcontextprotocol/go-sdk` ("maintained in collaboration with Google") — stdio + streamable HTTP. The 3-tool envelope resolves in `internal/core` before any gate (preserving the security property that a read-named envelope cannot smuggle a write verb).
- **REST:** stdlib `net/http` ServeMux (Go ≥1.22 routing patterns are sufficient; no framework). Unify `:8080`/`:8081` into one API surface with `/health`, `/api/v1/status|search|query`, auth endpoints; the existing ui-v2 keeps working untouched (it already talks plain REST).
- **RPC:** `connectrpc.com/connect-go` — one protobuf service serves gRPC **and** gRPC-Web **and** plain JSON from the same handlers, zero extra runtime:

```proto
service LeanKG {
  rpc Import(ImportRequest) returns (ImportResponse);   // index/incremental/attach/docs/knowledge
  rpc Query(QueryRequest)   returns (QueryResponse);    // ladder answer + retrieval provenance
  rpc Status(StatusRequest) returns (StatusResponse);   // freshness/inventory/backend
}
```

All three transports are thin adapters over `internal/core` — the same guarantee the Rust 3-tool envelope achieved transport-agnostically.

### 6.8 Memory for long-running agents

Port `src/memory/` (637 LOC) as-is — bank naming, scope matrix, cursor-resume retain, zero-match-filtered recall — and replace the O(n) file scans with an in-memory index rebuilt at load (cheap in Go). Add the outstanding FR-ZCP-07 remainder: the hindsight-shaped HTTP memory API (`POST /banks/{bank}/memories` + `/recall`) so harnesses can use LeanKG as `memory.backend` evidence — it is a thin REST adapter over the same bank store. Vector-scored recall can ride the L3 rung later (embed memory blobs with the same provider interface) without changing the on-disk format.

---

## 7. What gets deleted / ported / redesigned

| Rust today | LOC | Fate in Go |
|---|---|---|
| `db/pg/translate.rs` | 4,389 | **deleted** — no Datalog, no translation |
| `db/fake.rs` (test interpreter) | 1,890 | **deleted** — tests run against real SQLite (tmpfile) |
| `db/sqlite_backend.rs` (Cozo) | 2,726 | **replaced** by `store/sqlite` (~800 LOC typed SQL + migrations) |
| Datalog-IR authoring across 223 sites | ~5,700 called dead weight by W8 audit | **deleted** — typed SQL methods |
| `graph/query.rs` (73 methods over Datalog) | 7,894 | **redesigned** — SQL service layer + pure-Go graph algorithms (~2–3k LOC) |
| `mcp/handler.rs` big verb match | 5,754 | **table-driven dispatch** (~1–2k) |
| `main.rs` giant match | 7,630 | **cmd/ + cobra-style table** (~1k) |
| `mcp/server.rs` gate pipeline | 6,465 | middleware chain (~1.5k); lock-soup → plain mutexes + channels |
| indexer core (extractor + registry + call graph) | ~11,000 | **ported** (registry = data; extractor visitor mechanical; call graph port) |
| Android/Gradle/Maven/XML specialist extractors | ~9,000 | **deferrable** — regex-based, mechanical; parity fixtures first |
| embeddings build pipeline | ~6,000 | **ported slim** (providers, stamp, offsite; ONNX last) |
| memory + session | ~1,500 | **ported** + in-memory index |
| web/api/auth/audit/doctor/pack/dashboard | ~7,500 | **ported** (REST in stdlib; `go:embed` for UI; audit/doctor small) |
| **Estimated Go total** | | **~55–75k LOC** (Rust 168k incl. tests) |

---

## 8. Migration strategy & sequencing

**Strategy: greenfield Go repo, strangler cutover, re-index data.** The Rust release (v0.30.x) stays shipped and supported during the overlap; the Go binary replaces it when parity fixtures pass.

| Wave | Deliverable | Depends on | Est. |
|---|---|---|---|
| W0 | Contract fixtures: pin current 3-tool request/response JSON, ladder behavior per rung, stamp/bank formats as golden files from the live Rust server | — | 2–3 d |
| W1 | `internal/core` + MCP 3-tool (`import`/`query`/`status`) + SQLite store (WAL, migrations) + ladder L1/L2 (FTS5) + freshness watermark — **no embeddings** | W0 | 1.5–2 wk |
| W2 | Indexer: go-tree-sitter, 17 core languages, registry data port, call graph, watcher, writer process; re-index this repo as the smoke | W1 | 3–5 wk |
| W3 | Vectors + providers: BLOB cosine (inproc), ModelStamp, OpenAI-compatible provider, offsite import | W2 | 1–2 wk |
| W4 | PostgreSQL backend (pgx + pgvector, schema-per-project, re-key path) | W3 | 1–2 wk |
| W5 | REST (stdlib, unified :8080) + ConnectRPC service + auth/audit/doctor port + memory banks + hindsight HTTP API | W1 (parallelizable from W3) | 2–3 wk |
| W6 | Local embeddings: llama.cpp sidecar first; onnxruntime_go as the follow-up if wanted | W3 | 1–2 wk (sidecar) / 3–4 wk (ORT) |
| W7 | Parity hardening vs W0 fixtures, perf gate, install.sh/npm retarget, docs cutover | all | 1–2 wk |

**Core usable state (MCP + exact/fuzzy + SQLite, no vectors): ~3–4 weeks. Full parity: ~3–4 months** of focused single-maintainer work. The tree-sitter port (W2) and local embeddings (W6) are the long poles; both have bounded fallbacks (regex extraction degrades gracefully today and would in Go; the sidecar removes the ONNX cliff).

**Deliberately deferred:** the 25 `lang-extras` grammars (default-off in Rust anyway for slim builds), the ~4.5k LOC Android specialist extractors (defer or port mechanically with fixtures), LSP bridge, benchmark harness internals, the Rust `connect` target set (re-target to Go binary).

---

## 9. Risks & open questions

| Risk | Mitigation |
|---|---|
| tree-sitter via CGO: 43-grammar build matrix, cross-compile cost | core 17 languages first; `CGO_ENABLED=0` until a grammar is enabled; grammars are C and compile everywhere clang/gcc exists; WASM-grammar path exists but parses 5–10× slower (avoid) |
| Local embeddings in pure Go do not exist; tokenizers are CGO | sidecar default; ORT path optional; API providers work day 1; offsite flow unchanged |
| Schema-name byte-identity for existing PG installs (`DefaultHasher` has no Go stdlib equivalent) `[INFERENCE]` | re-key with `schema_candidates_for_path`-style adoption (precedent exists, backend.rs:3204-3248) or accept re-index |
| Parity drift in ladder ranking (FTS5 vs pg_trgm produce different orders) | W0 golden fixtures + per-rung precision spot-checks; provenance block makes rung visible, never silent |
| Behavior change: real PKs on `relationships` change duplicate semantics | deliberate improvement (kills the rm-then-put dance, query.rs:2414-2440); document as breaking fix |
| Two codebases during transition drift | Rust is in maintenance mode (no new features); all product work lands in Go; W0 fixtures are the contract |
| Rewrite freezes feature work ~1 quarter | explicit user tradeoff; M6/M7 items (registry, 3-signal) land **in Go** rather than Rust — they were specced but unbuilt (C9), so nothing is lost |
| Rewrite must replace the Datalog layer with native SQL (no Go CozoDB binding) — the largest single cost/risk item | **Measured, not assumed:** the current tree contains **zero Datalog recursion**. Verified 2026-09-10: (a) no self/mutually-referencing rule exists (every `<-` in a Datalog string is the literal-row write form, e.g. `?[cols] <- [[$a,...]] :put …`); (b) the translator has no `WITH RECURSIVE`; (c) all transitive traversal — `shortest_path` (query.rs:4920, 120-visit cap), impact radius (traversal.rs:14-46), NL-query neighborhood (nl_query.rs:442) — is **Rust-side BFS** over indexed per-hop lookups (`get_relationships_involving_elements_fast`, query.rs:1262), not Datalog. The recursive class is empty, so the port needs **no recursive CTEs**; the remaining shapes are scans/filters/aggregates/negation/`DELETE…WHERE`, each 1:1 to SQL. The W8 plan's risk line ("recursive/transitive graph queries have no trivial SQL form") is precautionary and does not apply to shipped code. Caveat: `run_raw_query` (MCP/web pass-throughs) accepts arbitrary user Datalog and is fenced/out of scope — it is dropped or replaced by a documented SQL surface, a deliberate API change. |
| Per-query-shape classification for the port (the count that sizes the work) | W4 §2.1 measured ~115 distinct shapes / 216–278 `run_script` sites; no fresh audit exists post-sqlite-revival — the Go port replaces sites with a much smaller set of typed `Store` methods (est. 40–60), so shape count shrinks regardless. |

**Open questions for the user:**
1. Local embeddings: sidecar (llama.cpp) acceptable as the default "local model", or is in-process ONNX mandatory?
2. Rename `set/get` → `import/query` with aliases, or keep current names?
3. Keep ui-v2 as-is (it already speaks REST) or fold the dashboard into the Go binary later?
4. Timeline appetite: ship core in weeks (W1) vs wait for full parity?

---

## 10. Recommendation

**Proceed with the Go rewrite as a greenfield engine under the existing product spec** — but treat it as an engine replacement, not a product rebuild:

1. The product decisions (3 tools, ladder, layers, dual engine, providers, memory banks, freshness) are **already made and validated** — freeze them as the W0 contract and stop relitigating them during the port.
2. Kill Datalog by writing plain SQL from day one; the repo's own measurements prove the query surface is trivial SQL (single-relation scans + one ANN shape).
3. Make **WAL-mode SQLite + DB-resident freshness watermarks** the foundation of real writer/reader separation — it deletes three bug classes at once (Cozo single-writer, per-process caches, FFI aborts).
4. Ship providers before local ONNX; sidecar the local model; keep the offsite batch flow.
5. Keep Rust released and in maintenance until W7 parity gates pass; cut over install/npm surfaces once, per the existing deprecation policy.

**The honest counter-case (when NOT to rewrite):** if the actual pain were only "delete the translator," the in-progress W8 SQL-first plan already removes ~5.7k LOC of it in-place without a language change. The rewrite is justified by what the user is optimizing for — **long-term maintainability in a language they want to work in** — plus the structural read/write separation that the current engine cannot deliver. That is a legitimate product reason, and this document treats it as such rather than as a taste preference.

---

## Appendix A — Evidence index (primary claims → source)

| Claim | Anchor |
|---|---|
| 3-tool registry CI-pinned; envelope resolution order | src/mcp/tools.rs:76-78, 117-240; docs/mcp-tool-contract.md |
| Verb catalog size (83) | src/mcp/tools.rs:272-364 (counted) |
| Gate order (envelope before gates), RO gate, RBAC | src/mcp/server.rs:4715-4743, 3592-3598; src/mcp/auth.rs:106-152 |
| Semaphore/timeout, floors | src/mcp/server.rs:79-142 |
| Freshness 30s TTL + tracker | src/mcp/server.rs:151, 878-908; src/mcp/tracker.rs |
| L1 cache invalidate TOCTOU | src/mcp/server.rs:3551-3563 |
| Priority write bus | src/db/write_bus.rs:1-29 |
| RO/writer co-existence comments; no multi-process RocksDB | src/db/sqlite_backend.rs:130-142, 1880-1887 |
| No WAL/busy_timeout pragma | grep `journal_mode|WAL|busy_timeout` over src/ — comments only |
| Cozo engine selection; RocksDB variant | src/db/sqlite_backend.rs:279-293; Cargo.toml:73 |
| Translator size/behavior; no-ops; arity catalogs | src/db/pg/translate.rs:34-56, 208-210, 514, 923-937, 3068-3090 |
| One-ANN-shape / single-relation scans measurement | docs/archive/plan-migrate-cozo-to-postgres-pgvector.md §2.1, §2.4 |
| W8 SQL-first seam + remaining sites | src/db/sql.rs; docs/archive/plan-remove-cozo-datalog-sql-migration.md; roadmap-2027-v2.md:92 |
| PG HNSW params; schema-per-project; per-schema ledgers | src/db/pg/schema.sql:362-369; src/db/backend.rs:2795-2812; src/db/pg/migrations.rs:33-62 |
| Ladder rungs + probes + provenance | src/mcp/router.rs:29-38, 81-95, 253-291, 293-301 |
| sqlite L2 = LIKE only; pg_trgm PG-only | PR #284 notes (docs/prd.md:47-51); indexer scout §6; migration 007 |
| Embedding catalog + OpenAI-compatible provider | src/embeddings/registry.rs:66-124; src/embeddings/provider.rs:1-13, 137-233 |
| ModelStamp + guards + chunker version | src/embeddings/stamp.rs; build.rs:819,1123; text_blob.rs:336-368 |
| Offsite flow | src/embeddings/offsite.rs:216, 394; scripts/embed_batch.py |
| Bank naming + scopes + cursor retain + zero-match recall | src/memory/bank.rs:24-47, 63+; src/memory/store.rs |
| Session offload + recall index | src/session/mod.rs:3-26, 159-171 |
| BLAKE3 unwired; 3-signal absent; git-diff incremental | src/indexer/content_hash.rs:15-19, 28; indexer scout §2 |
| Language registry: 142 rows, 17 core + 25 extras, 43 grammars | src/indexer/lang/registry.rs:140; Cargo.toml:28-52 |
| 67 canonical rel types; element types free strings | src/db/models.rs:31-100, 239 |
| REST surfaces :8080 / :8081 | src/web/mod.rs:428-516; src/api/mod.rs:118-158 |
| No gRPC | grep tonic/prost/grpc — extractor regexes only |
| Binaries are re-exec wrappers | src/bin/leankg_mcp.rs; src/bin/leankg_worker.rs; src/cli/reexec.rs |
| #286 FFI abort + panic-hook mitigation | src/main.rs:120-146; docs/prd.md:55 |
| CI gates (all-targets, contract drift, perf, TTFV) | .github/workflows/ci.yml, perf-gate.yml, quickstart.yml |
| CLI verb counts; test inventory; workflow list | ScoutOps §1, §3, §4, §8 |
| Measured perf numbers | docs/archive/analysis/pg-phase0-spike.md:53-56; pg-migration-report.md:86; pg-perf-large-codebase.md:56-61; docs/prd.md:35, 72-73 |
| Binary 176 MB; index 662 MB | measured on this machine (2026-09-10) |
| Official MCP Go SDK / tree-sitter bindings / pgvector-go / sqlite-vec | github.com/modelcontextprotocol/go-sdk; github.com/tree-sitter/go-tree-sitter; github.com/pgvector/pgvector-go; github.com/asg017/sqlite-vec (fetched 2026-09-10) |

## Appendix B —LOC per subsystem (measured)

| Subsystem | LOC | | Subsystem | LOC |
|---|---|---|---|---|
| src/indexer | 27,237 | | src/retrieval | 2,363 |
| src/db | 18,613 | | src/cli | 2,222 |
| src/mcp | 16,397 | | src/doc_indexer | 1,856 |
| src/graph | 15,215 | | src/doctor | 1,724 |
| src/embeddings | 8,177 | | src/connect | 1,398 |
| src/web | 5,763 | | src/sources | 1,306 |
| src/benchmark | 4,468 | | src/session | 884 |
| src/ontology | 3,972 | | src/api + audit + auth + setup | ~3,300 |
| src/compress | 3,516 | | src/lsp | 2,639 |
| src (top-level, incl. main.rs 7,630) | 10,076 | | **Total** | **168,121** |

---

*Prepared 2026-09-10. Every `file:line` anchor was read from the v0.30.0 working tree during this session; `[INFERENCE]` marks the two claims needing pre-build verification (Go `DefaultHasher` absence; Cozo-file opacity to plain SQLite drivers) plus tokenizer-library maturity.*
