# LeanKG 1-Year Roadmap (Refresh) — Q3 2026 → Q2 2027

**Version:** 2.0 · **Date:** 2026-08-28 · **Owner:** FreePeak
**Status:** Refresh of [`roadmap-2027.md`](roadmap-2027.md) v1.0 (2026-08-22) + [`roadmap-tracker.md`](roadmap-tracker.md)
**Companions:** [`prd.md`](prd.md) (SoT) · [`prd-enterprise.md`](prd-enterprise.md) · [`prd-task-tracker.md`](prd-task-tracker.md) · [`plan-remove-cozo-datalog-sql-migration.md`](plan-remove-cozo-datalog-sql-migration.md)

**Why this v2 exists:** v1 was written before the W8 SQL-first seam shipped, before v0.26.1, and before the open-PR triage done on 2026-08-28. This version adds: (a) the *now/this-month/next-month* operational grain v1 lacks; (b) re-anchored quarterly themes reflecting the W8 work; (c) explicit triage of all open PRs and live defects; (d) competitive/tech scout findings as of late August 2026; (e) a 12-month north-star scoreboard.

---

## 0. TL;DR — the 30,000-foot view

LeanKG's thesis: **deterministic, self-hostable, MCP-native code knowledge graph** — the credibility wedge for agent context that embedding-only incumbents (Cursor, Augment) cannot offer. The 12-month plan is one engineering rhythm (hackathon cycles of 5–7 days) that produces a sequence of trustworthy releases, with one major product bet per quarter:

| Quarter | Theme | One-line outcome |
|---|---|---|
| **Q3 2026** (now → Sep 30) | **Solid Foundation** | Postgres-only, no Datalog. 73 stable MCP tools. Sub-1s p95 on 100k-element repos. v0.27.0 release. |
| **Q4 2026** | **Team-Ready PLG** | `leankg connect` one-command setup; OAuth 2.1 + RBAC v1; audit log v1; hosted Team beta. v0.30.0. |
| **Q1 2027** | **Enterprise Procurement Pass** | SSO/SCIM/SOC 2 runway; Helm chart; provenance labels on every tool. v0.33.0. |
| **Q2 2027** | **Platform Play** | Public query API + MCP-gateway partner listings + air-gap tier. v0.36.0. |

Two products ship in this window: the **open-source binary** (continuous) and the **Team cloud** (Q4 beta → Q1 GA). Solo-maintainer constraint is the rate-limiter: every quarter must end with the repo *more* subagent-automatable, not less.

---

## 1. Scout: market & technology (Aug 2026)

### 1.1 Why this is the right window

- **MCP won the protocol war.** Linux Foundation governance; 97M+ monthly SDK downloads; every major agent/IDE (Claude Code, Cursor, Codex, Gemini CLI, JetBrains, Sourcegraph Cody) ships an MCP client. LeanKG is the only OSS Rust codebase-KG on the protocol.
- **Embedding-only stacks are hitting their ceiling.** Augment's Context Engine (cloud, closed) and Cursor's Merkle+embed are the de-facto references, but both fail call-chain queries and can't explain their answers. LeanKG's graph-first story is the credible response to a fast-growing "agent-trust crisis" (METR study: AI coding slowed complex tasks 19%; only 14% of orgs fully approve deployed agents).
- **The graph thesis is externally validated — and the local-binary tier is commoditized.** Codebase-Memory (arXiv 2603.27277, Mar 2026) proved tree-sitter code graphs answer agent queries at 10× fewer tokens than grep-exploration; its OSS implementation (codebase-memory-mcp, 40.9K★, MIT) plus a dozen peers now own the single-repo local-binary tier. What none of them have: multi-user shared server, Postgres ops, doc↔code traceability, persistent agent memory, hybrid embeddings. That's the layer LeanKG sells.
- **Procurement is catching up.** EU AI Act obligations apply Aug 2026; SOC 2 buyer surveys now ask for "explainable AI" by name. Deterministic graph answers with provenance are a marketable trust primitive, not a vanity feature.

### 1.2 Competitive landscape (scout-verified, Aug 2026)

**Strategic correction vs v1:** raw "structural code graph via MCP" is no longer differentiating — it exploded into a crowded OSS space in 2026. The category leader is **DeusData/codebase-memory-mcp** (MIT, ~40.9K★, Feb 2026): single static binary, 158 languages, Hybrid LSP type resolution, SQLite, 15 tools, sub-ms queries, full Linux kernel (28M LOC) in ~3 min — plus a peer-reviewed benchmark (arXiv 2603.27277): 83% answer quality vs 92% for a grep/read agent at **10× fewer tokens, 2.1× fewer tool calls**. That quality-per-token framing is now the yardstick buyers will apply to LeanKG.

| Competitor | Approach | LeanKG's position |
|---|---|---|
| **codebase-memory-mcp** (40.9K★, MIT) | Local AST graph, SQLite, 158 langs, Hybrid LSP, arXiv paper | **Surpass on**: multi-user server, per-project schema isolation, doc↔code traceability, embeddings hybrid, provenance labels. Match on: benchmark methodology (publish ours). |
| **Cursor** ($29B) | Merkle+embeddings on turbopuffer (1T+ vectors); added Instant Grep + agentic search — even they hybridize now | No MCP surface, no self-host, IDE-bound |
| **Augment Code** ($252M raised) | **Conceded the agent layer; unbundled Context Engine as MCP server** (GA Feb 2026). +70–80% agent quality claim; token pricing +40% fee | Validates "context-as-a-service over MCP" as a category; their pricing is our backlash wedge (flat, no toll) |
| **Sourcegraph** | Killed Cody's embeddings entirely (ops burden, >100K-repo wall); SCIP platform, $16K/yr floor | Their retreat from embeddings = our hybrid (graph + optional embeddings) argument |
| **Claude Code / Codex** | Dropped RAG for agentic grep ("works better") | Agentic grep fails on hub/impact/ranking queries — exactly where the graph camp wins (arXiv 19/31 languages) |
| **CodeScene** | "AI-readiness" pivot: CodeHealth MCP (fully local), 9.5 rule, AGENTS.md guidance files, agent-skills catalogue | Copy their agent-guidance distribution pattern; they don't do code-structure KG |
| **Cognee** ($7.5M, 12K★) | Agent-memory KG: remember/recall/forget/improve, hybrid graph+vector+BM25 RRF, **whole memory layer on single Postgres** | Closest conceptual neighbor; they're chat/docs-domain. Our code-structural + PG direction converges — validates PG choice |
| **GitNexus** (45k★) | PolyForm Noncommercial | License blocks commercial use; vacant slot is theirs to lose, not ours to fear |
| **Bito / Riftmap / Gortex / code-graph-mcp / code-cortex-mcp** | Typed graphs, cross-repo manifest edges, BM25+RRF, dead-code, CI gates | Confirms the commodity layer; none do multi-user server + business-context unification |

**Revised empty quadrant:** *multi-user self-hosted server × code graph unified with non-code knowledge (specs, FR/US IDs, ADRs, ownership) × provenance-labeled answers × Postgres-single-node ops × MCP-native with enterprise auth.* Single-repo local graph binaries are commodity; the durable product is the team/org context layer above them.

### 1.2b Spec-driven development is mainstream (Kiro GA)

Kiro (AWS) made requirements.md → design.md → tasks.md + dependency-wave execution standard, with Steering docs and "Powers" (on-demand context to avoid MCP context bloat). LeanKG's doc↔code traceability (`FR-*`/`US-*` resolution, `mcp_index_docs`, BusinessLogic annotations) is already the substrate for this — the roadmap should expose it as a first-class spec-driven workflow (see §6 H4), not leave it as scattered tools.

### 1.3 Tech-trend inputs to the roadmap

- **MCP auth is now a hard compliance bar, not a feature.** 2026-01 / 2026-07-28 spec revisions: OAuth 2.1 resource-server model is MUST for remote HTTP servers; RFC 9728 Protected Resource Metadata discovery; RFC 8707 Resource Indicators (audience binding, anti-confused-deputy); PKCE mandatory; **Dynamic Client Registration deprecated** in favor of Client ID Metadata Documents. **Enterprise-Managed Authorization ("zero-touch OAuth", ID-JAG) stable June 18 2026** — adopted by Okta, Claude, VS Code, Atlassian, Linear, Supabase. A 2026 scan found 33% of MCP servers had critical vulnerabilities; the "MCP gateway" pattern (per-tool scopes, per-agent identity, SIEM audit) is how enterprises contain the sprawl. F1 (Q4) must target this spec bar exactly — LeanKG's existing OAuth2 tokens need: PRM discovery endpoint, RFC 8707 audience validation, per-tool scopes (the `scopes` column exists but is always empty), and EMA/ID-JAG readiness.
- **Benchmark vocabulary is set.** arXiv 2603.27277 (Codebase-Memory, Mar 2026) established "answer quality vs token savings vs tool-call count vs grep-explorer baseline" as the reference frame. LeanKG must publish equivalent numbers on its own workload (docs/benchmark.md exists — extend to this format) or buyers will only see the competitor's.
- **Long-running task support** is on the MCP spec horizon — directly relevant to `add_documentation`/`temporal_query` (the async-job pattern we need anyway for remote PG).
- **Hybrid retrieval is the consensus bar; single-modality loses.** Academic consensus (RANGER, GRACE, RepoScope, SemanticForge) + benchmarks (CORE-Bench arXiv 2606.11864, CoREB) show graph + BM25 + vector with RRF beats any one modality on repo-level tasks. LeanKG has all three; the missing piece is the unified ranking function with provenance per node (US-SM-03/04).
- **Tree-sitter-only edges are now publicly criticized as a low ceiling** (name collisions, dynamic dispatch). The 2026 quality lever is a **hybrid type-resolution pass** — LeanKG already has `src/lsp/` (2,639 LOC: client, bridge, hybrid, type_registry) but it's budget-gated, not default. Making LSP-assisted resolution default for the top 5 languages (go/ts/py/rust/java) + persisting resolved-vs-heuristic confidence is the call-graph accuracy roadmap item.
- **rmcp 3.x is the Rust SDK to adopt.** Mature (v3.1.4, Aug 2026, ~22M downloads, 1,877 dependents), already implements the 2026-07-28 spec including **MCP Tasks (SEP-2663)** — which map exactly onto our long-running index/embed/document jobs (the same work the async-pool fix needs anyway). Also: stateless Streamable HTTP (SEP-2567) for hosted deployments, and `outputSchema` on every tool — only ~17% of the 18.8K listed MCP servers declare one, so it's still a differentiator, not table stakes.
- **Agent-memory standardization is happening OUTSIDE MCP core.** MCP maintainers declined the Memory Interchange Format SEP ("get provider adoption first"). Four mid-2026 specs converge on the same record envelope: `id, content, type, timestamp, source, metadata` + optional graph + soft-delete/pinning/supersedes (memorywire arXiv 2606.01138, AMP v1.1, UMP, AMCP). LeanKG's `knowledge_entries` + ontology concepts already fit this shape — align the schema + add export/import as first-class verbs = cheap interop insurance whichever standard settles.
- **Embedding economics moved to quantization.** voyage-code-4 (Aug 2026): +27.5% over code-3 on agentic code-retrieval, $0.12/1M tokens, Matryoshka 256–2048 dims with int8/binary output; binary-then-float rescoring recovers most quality at ~384× less storage. A 1024-dim int8 default + per-repo dimension knob is the 2026 sane choice (feeds H2 embeddings marketplace).
- **pgvector is safe through 2027.** pgvector 0.8/0.9 (halfvec, iterative scans, HNSW improvements) + managed-PG convergence (Neon — now Databricks/Lakebase after the ~$1B close — Supabase, Aiven) validate the architecture. Neon reports ~80% of new databases created by agents; CoW branching + scale-to-zero = ephemeral per-test/per-branch PG, which matches our test-fleet workflow.
- **Tool-surface minimalism is a counter-trend worth respecting.** CodeGraph (57k★) deliberately exposes ONE MCP tool; practitioners run several graph servers simultaneously. LeanKG's 76→~70 consolidation (W11) should trend toward fewer, richer verbs (`search(kind=)`, `get_traceability(mode=)`) — aligns with both the registry listing bar and context-bloat avoidance (Kiro "Powers" pattern).
- **Solo-maintainer × subagent velocity is a real strategic pattern.** Our hackathon-cycle cadence (5–7 days, 80+ PRs merged in 30 days, 71 releases in ~5 weeks) is a competitive advantage that compounds only if we keep the doc-SoT + parity-test + tool-contract discipline.

---

## 2. Where we are today (state as of 2026-08-28)

### 2.1 Repo health (live facts)

- **Version:** v0.26.1 (2026-08-22). 9 releases in 22 days. v0.19→v0.26 was a deliberate re-platforming arc (Cozo→PG).
- **Adoption:** 214★, 26 forks, 1 watcher, 20 open issues, 80+ PRs merged in 30 days. Niche but active.
- **Open PRs (4):**

  | PR | Title | State | Triage |
  |---|---|---|---|
  | **#246** | purge legacy-engine naming | CONFLICTING (1 file: `src/db/mod.rs`) | **Land this week.** Trivial rebase: keep main's wave-1b SQL upsert, take the branch's comment rewording. Brings 47-file remote-PG test hardening. |
  | **#237** | tsx/jsx indexing | MERGEABLE, CI not yet run | **Merge.** Small fix (22 LOC). 139-lang registry has no TSX spec; dead code in extractor already expects it. |
  | **#231** | ui-lite vis-network | MERGEABLE, CI green | **Close/shelve** — conflicts with the ui-v2/ SPA direction (it would revert the v2 bundle in `src/embed/`). |
  | **#43** | feat/csharp | OPEN with "being closed" comment that never executed | **Close** (the comment author is you). C# is tracked P2 PARTIAL elsewhere; this stale fork has no value. |

- **Uncommitted work on `main` (13 tracked files):** an *incomplete, partly regressive* duplicate of the d14530e commit inside PR #246. `tests/batch_delete_stress_tests.rs` has a `#[test]` mis-attribution syntax bug; `full_index_wipe_test.rs` regresses the conn-leak fix. **Action:** `git restore` the 13 files; commit the untracked `docs/reports/livetest-*.md` + `VALIDATION_REPORT.md`; delete `baseline-pre-cleanup.json`.

### 2.2 Engineering state

- **W8 SQL-first migration in progress.** P0 seam (`src/db/sql.rs`, 666 LOC) + P1 trait + wave-1a + wave-1b shipped. **Remaining: 223 `run_script` sites across 19 files** (39 in `src/db/mod.rs`, 108 in `src/graph/query.rs` alone — 7,656 LOC still emitting Datalog strings). `src/db/pg/translate.rs` (4,341 LOC hand-built Datalog→SQL translator, ~115 query shapes) is the compatibility tax: every query bug now has two possible layers. Pending waves: W2 (graph/query.rs reads) → W3 (writes/agg/vector/clustering) → W4 (embeddings/state/build/control) → W5 (auth+ontology) → W6 (mcp/handler, tracking_db, indexer) → W13 (mod.rs remainder) → **P3 deletion sweep** of `translate.rs` + `fake.rs` (1,535 LOC) + FakeBackend.
- **Scale is flag-guarded, not structurally fixed.** Mega-graph guards are band-aids: `is_mega_graph` cached flag, `MAX_BFS_VISITS=120`, frontier-local BFS, `LEANKG_MAX_CACHE_ELEMENTS` skip. Root cause: `run_script` is sync `postgres::Client` + `tokio::task::block_in_place` (backend.rs:1237) — every query blocks a worker thread; pool of 5 serializes under load. `leankg-worker` is a re-exec shim, not a real separate process. The async-pool + worker-process separation is a first-class roadmap item (§4.1, §6), not just a defect fix.
- **2 persistent live defects** (v0.26.1 validation, 2026-08-24, remote PG over WAN): `temporal_query` 300s timeout per-tool and `add_documentation` >320s hang. Both bound by the same root cause above. Fix shape proven by PR #215's async-pool work.
- **5 budget-bound operations** (correct but >300s over remote PG): `find_tunnels`, `get_cluster_skill`, `check_consistency`, `mcp_index_docs`, `index_prd`; `mcp_install` ~95s. These are the next speed targets after the two real defects.
- **Hackathon cadence:** C1–C4 banked, C5+ = W8. The hackathon cycle is *the* execution rhythm (7-day 24/7 loop: brainstorm → plan → implement → test → live-test on remote PG → fix → validate). Exit criterion per cycle: zero red gates + full tool sweep clean.
- **Tool count is unreliable across surfaces** (product audit): 76 registered in `src/mcp/tools.rs:7`, docs claim "~73", livetest reports say 79–83 with feature gates. W11 must fix the count *and* the surface: delete 2 soft-deprecated, merge the traceability quartet into `get_traceability(mode=)`, unify the 4-way search family under one `search(kind=)` with aliases, publish the semver'd tool contract.
- **Agent-memory is built but invisible** (product audit): `src/ontology/` (8 files), `src/conversation_indexer/`, `src/session/` with `session_recall` + `MEMORY_INDEX` shipped; **US-SM-01..06 all DONE** in the tracker (`docs/prd-task-tracker.md:417-422`); only US-SM-07 (retention/GC) pending. This is the headline differentiator vs codebase-memory-mcp / CodeGraph — yet it's absent from the README value prop. Marketing wedge + GC + a memory-recall demo in quickstart = cheap wins.
- **Onboarding is better than feared but has a hidden prerequisite:** README quickstart is honest (88s e2e smoke, weekly CI, `leankg connect claude-code`, `doctor --deep`) — but requires a local PG on :5433 ("fail if down", README.md:44) for a tool marketed as "lightweight." Zero-infra default (embedded/auto-provisioned PG) is a Q4 growth prerequisite.
- **No public docs site** — 66 files in `docs/` are internal working docs; public surface is repo markdown. PRD header stale (v3.7.1 vs actual v0.26.1).
- **PLG telemetry is local-only:** `leankg dashboard` (PLG-8) aggregates calls/tokens/tokens_saved per tool/day/project from `context_metrics` — but nothing is reported centrally, `tokens_saved` is modeled not measured, and there are no per-tool latency/failure columns despite timeouts being the top defect class.
- **Test infrastructure:** 2,449+ lib tests + 2,690+ (with --features embeddings) per the validation report. Parity test gate (`tests/pg_sql_wave1_test.rs`+`wave1b_test.rs`) ensures W8 conversions don't regress legacy semantics.
- **Distribution:** real release pipeline — `.github/workflows/release.yml` builds 4 targets (linux-x64, macos-arm64/x64, windows-x64) with embedded ui-v2, triggered by v* tags + release-please. Bins: `leankg`, `leankg-mcp`, `leankg-worker`. Docker slim image + embed-worker image. npm wrapper is at 0.26.1 (W12 in the tracker is stale — already synced; verify automation instead). Gaps: no artifact signing, no Homebrew/apt, no `leankg upgrade` with transactional PG migrations (migrations 002–006 exist, no downgrade path).
- **Auth/security baseline is stronger than the tracker implies:** OAuth2-style access tokens (`AccessTokenStore`, migration 004_auth) + a **hash-chained verifiable audit log already shipped** (migration 006_audit_log, `src/audit/mod.rs:147-196`). Missing: SSO/OIDC/SAML (zero hits), token `scopes` always empty (no authorization model), org-level isolation not enforced in query paths, thin audit coverage on mutating tools.
- **Docs surface:** `docs/` is rich but 30+ files; canonical SoT is `prd.md` (v3.8.7-harness-era-positioning). Tracker (`prd-task-tracker.md`) has 560 tracked items, 106 open.

### 2.3 P1/P2 backlog (from `prd-task-tracker.md`)

- **P1 (2 open):** `REL-ONRENDER-101` F3 prebuilt-image pull = `NOT_DONE`; everything else in Waves 0a–4 + Wave1b hard-delete is done.
- **P2 (~92 open, ordered):** US-SM-01 session offload (highest ROI) → US-SM-02 auto-recall (closes US-GE-05 self-improve loop) → US-SM-03/04 provenance+RRF hybrid → US-DOCJOIN-* polish → US-GE-02 planner DAG → US-GE-03 entity resolution → US-GE-04 cluster-first nav → US-SM-05/06 heat index/promote traces.
- **P3 (~13 backlog):** US-SM-07 retention/GC; US-GE-06 selective LLM pass-2; Track E 3D UI.

---

## 3. What to do NOW (this week, 2026-08-28 → 2026-09-03)

This is the actionable punch list — every item is a one-day task or less.

| # | Action | Owner | Evidence | Definition of done |
|---|--------|-------|----------|--------------------|
| 1 | `git restore` the 13 uncommitted tracked files on `main` (they're superseded by PR #246's d14530e). | you | `git status` | Working tree clean except untracked reports |
| 2 | Commit the untracked reports: `docs/reports/livetest-remote-pg-2026-08-22.md`, `docs/reports/livetest-v0261-validation-2026-08-24.md`, `VALIDATION_REPORT.md`. | you | git log | 3 commits on main, references in tracker §1 |
| 3 | Delete `baseline-pre-cleanup.json` (artifact, not source). | you | shell | gone |
| 4 | `gh pr close 43 --comment "closed as stale per prior comment; C# tracked P2 PARTIAL"` | you | gh | #43 closed |
| 5 | Rebase `fix/tsx-jsx-indexing` (PR #237) onto main; push; let CI run. | you | `gh pr checks 237` | CI green, ready-to-merge |
| 6 | Decide PR #231 (ui-lite) — recommendation: close with comment pointing to `ui-v2/` as canonical. | you (product call) | — | #231 closed OR a written decision in the PR |
| 7 | Rebase `feat/remove-cozo-datalog` (PR #246) onto main; resolve `src/db/mod.rs` in favor of main's wave-1b SQL upsert (keep the branch's comment rewording). Push; let CI run. | you | `gh pr checks 246` | CI green, ready-to-merge |
| 8 | Land #237 and #246 the same week; tag **v0.27.0** with combined changelog. | you | `git tag -a v0.27.0` | Release published |
| 9 | npm wrapper is already at 0.26.1 (tracker W12 is stale) — instead verify the release workflow auto-syncs npm on tag; close W12 if so. | you | release.yml | W12 closed or fixed |
| 10 | File two new issues: (a) `temporal_query` 300s timeout root cause; (b) `add_documentation` >320s hang root cause. Both already have a fix shape (server-side async pool from PR #215). | you | gh issue | Issues opened with reproduction commands |
| 11 | Update `docs/roadmap-tracker.md` §1 status: W8 (after #246 merge) → P0/P1 ✅, W2-W6 + P3 ⬜; W11, W12, W13 status. | you | diff | Tracker reflects reality |
| 12 | Start cycle 6: W2 (convert `graph/query.rs` reads to SqlParam seam). Subagent: `gh issue edit` to add W2 sub-tasks per the migration plan. | you + subagent | migration plan | W2 branch + parity test green |
| 13 | `kg_ontology_status` reports `nodes_missing_aliases: 14` while `domain_entity.counts` sums to 13 (probe 2) — fix the formula or backfill aliases (`FR-HEA-01`). | you | handler diff + unit test for the invariant | `nodes_missing_aliases <= sum(domain_entity_counts)` holds on the demo corpus |
| 14 | Add structured `search_code` fallback hint to empty/below-floor `semantic_search` / `kg_semantic_context` responses (`FR-HEA-02`). | you | handler diff + probe rerun | probe-4 scenario returns actionable hint |

**12 items / 1 week / 1 release.** This is the throughput rate the cycle has been hitting; the plan is to keep it.

---

## 4. This month (Sep 2026 — 30 days)

Theme: **finish the Postgres era cleanly and make the project trustworthy to adopt.**

### 4.1 Engineering work

- **Cycle 6 (W2):** convert `graph/query.rs` reads to SqlParam seam — the largest remaining `run_script` cluster. Parity test gate must stay green.
- **Cycle 7 (W3):** query writes + aggregations + vector + inventory/clustering conversions. The first wave that touches the embeddings pipeline's read path.
- **Cycle 8 (W4):** embeddings/state/build/control conversions. Watch for the same race conditions that bit wave-1b (tags JSONB bind).
- **v0.27.1 / v0.28.0:** ship cycles 6–7 mid-month, cycle 8 by Sep 30.
- **Tool-surface discipline (W11):** consolidation 76→~70 with a fixed, honest count (registry says 76, docs say 73, livetest says 79–83 — resolve to one number). Delete 2 soft-deprecated; merge traceability quartet into `get_traceability(mode=)`; unify the 4-way search family under one `search(kind=)` with aliases. Publish the semver'd tool contract **with `outputSchema` on every tool** (only ~17% of the MCP ecosystem does this — free differentiation). Precondition for F4 (registry listing).
- **Benchmark publication (new, from market scout):** run the arXiv 2603.27277 methodology (answer quality vs token count vs tool-call count vs grep-explorer baseline) on LeanKG's own workload; publish in `docs/benchmark.md`. Buyers will apply this frame whether we do or not.
- **Agent-memory marketing wedge (new, from product audit):** US-SM-01..06 are DONE but invisible in the README. Make persistent agent memory the headline differentiator vs codebase-memory-mcp/CodeGraph (neither has it); add a memory-recall demo to quickstart; ship US-SM-07 GC so memory doesn't rot.
- **Defect work (parallel, small):** `temporal_query` and `add_documentation` async-pool fix (see PR #215 for the pattern). `find_tunnels`, `get_cluster_skill`, `check_consistency` budget reductions.
- **Scale foundation (new, from architecture audit):** replace sync `block_in_place` + `postgres::Client` with async `tokio-postgres` + bb8 pool; make `leankg-worker` a genuine separate process (embed + index off the MCP server); push aggregation/pagination into SQL (recursive CTE impact radius with depth/fanout caps) instead of pulling rows to Rust. This is the structural fix behind the two live defects and the mega-graph guards.
- **CI hardening (E4):** CI today runs only `cargo test --lib` (ci.yml:51) — the ~2,042-test integration suite and the 31 `--features embeddings`-gated files are effectively untested in CI. Add a CI matrix with a local PG service container + `--features embeddings`; promote perf-gate from gate-only to trend-tracking.
- **Docs truth sweep (E5):** every generated doc/wiki/AGENTS.md says Postgres+pgvector only; `architecture.md` refreshed; `competitive-analysis.md` updated to reflect Aug 2026 landscape.

### 4.2 Product work

- **Pricing/positioning lock-in.** v1 roadmap said license decision lands in Q4 — accelerate the *core license* decision to Sep so the README can carry the right message before any Q4 launch.
- **Onboarding friction baseline.** Time-to-first-useful-query from a clean `docker compose up`. Target: <10 min for a 50k-LOC repo. Measure before fixing; ship the metric.
- **OSS adoption loop.** Publish a "what changed this month" digest post (one canonical URL) for the Sep release; cross-link from `prd-task-tracker.md` and the GitHub release notes.
- **License/bus-factor risk:** start a `good-first-issue` pipeline. Even one external contributor shipped in Q3 cuts the solo-maintainer tail risk.

### 4.3 Exit criteria for the month

- **Harness-era repositioning (new, from the 2026-08-30 value assessment):** the probe verdict — org-memory substrate = high value, search = mid value against harness-native Glob/Grep/LSP — lands as `US-HEA` / `FR-HEA-01..05` (PRD §3.31 / §5.36): alias-metric fix, semantic dead-end fallback hints, mega-scan banner, remote-PG latency strategy (shorter tool timeouts + documented local-PG / materialised-view options), and a README/docs positioning cutover to traceability / env conflicts / incidents / team map / cross-repo service graph.
- v0.28.0 released with `run_script` sites in `src/db/mod.rs` ≤ 25.
- 0 red gates on the 73-tool sweep against remote PG.
- `temporal_query` and `add_documentation` under 60s on remote PG (currently >300s).
- Stable tool API contract published in `docs/mcp-tool-contract.md`.
- CI runs the PG integration suite on every PR.

---

## 5. Next month (Oct 2026 — 30 days)

Theme: **P3 deletion sweep + tool API freeze + first Team-cloud prep.**

- **Cycle 9 (W5):** auth + ontology SQL-first conversions. Touches the AccessTokenStore (004_auth) — careful parity.
- **Cycle 10 (W6):** mcp/handler, tracking_db, indexer, doc_indexer, pack conversions.
- **Cycle 11 (W13 + P3 deletion sweep):** `src/db/mod.rs` remainder; **then delete** `src/db/pg/translate.rs` (4.3k LOC), `src/db/fake.rs` (1.4k), `mutability.rs`, `FakeBackend` trait. Tag v0.29.0 as **"Postgres-only, no Datalog, no FakeBackend"** — the *thesis* release.
- **PLG: `leankg connect`** (F4 precondition). Writes the MCP config block for Claude Code, Cursor, Codex, Gemini CLI. One command from install to first tool call.
- **Audit log v1 (F3 / ENT-1):** append-only `who/which-agent/which-tool/which-project/which-repo` ledger with SIEM-friendly JSON export. Schema ships in the v0.29.0 release even though the UI lands in Q1 — this is the enterprise procurement keystone, build the schema now.
- **Hosted Team beta signup page** (static, on the project website or GitHub Pages). Collect design-partner leads.
- **License + CLA infra:** choose core license (MIT/Apache/AGPL) and set up CLA tooling (cla-assistant or similar). Prereq for accepting external PRs at the rate we need.
- **Exit criteria:** v0.29.0; `grep -r Datalog src/` returns 0; `leankg connect` ships; audit-log schema in prod; ≥3 design-partner leads from the beta signup.

---

## 6. Q4 2026 (Nov + Dec) — "Team-Ready PLG"

Quarterly outcome: **Team cloud beta with paying users.** Code mostly ready from Q3; the work is productization + first sales motion.

- **Cycle 12 (F1):** adopt **rmcp 3.x** + the 2026-07-28 spec bar exactly: OAuth 2.1 resource-server model (MUST for remote HTTP), RFC 9728 Protected Resource Metadata discovery endpoint, RFC 8707 audience-bound tokens (anti-confused-deputy), PKCE, Client ID Metadata Documents (DCR is deprecated). Then **MCP Tasks (SEP-2663)** for background index/embed/document jobs — this is the same async-job substrate the `temporal_query`/`add_documentation` fixes need, so sequence it with the scale-foundation work, don't duplicate it.
- **Cycle 13 (F2):** RBAC v1 — enforce real scopes on MCP tool dispatch (the `scopes` column exists but is always empty today), admin/editor/reader roles scoped to projects/collections, org-tenant guard in schema resolution. Tool-level permission bundles ("Virtual bundles" pattern from gateway vendors).
- **Cycle 14 (F4):** official MCP registry listing. Note: registry is still **preview** as of Aug 2026 (~18.8K servers, ~17% of remotes dead) — listing is a credibility signal, not a distribution channel yet; also pursue the agent-guidance distribution surface (AGENTS.md rules, skills, hooks — the CodeScene/code-graph-mcp pattern).
- **Zero-infra default (new, from product audit):** PG on :5433 is a hidden prerequisite for a "lightweight" tool. Ship auto-provisioned embedded PG (or a documented one-command `leankg init --pg=auto` path) before any growth push — first-query failure is churn.
- **F5/F6:** Performance floor — p95 <150ms for top-10 tools at 100k-element repos. Benchmark methodology published in Sep (see §4.1); Q4 is about holding the line while the Team cloud adds load.
- **F7 (new this rev):** `leankg connect` v2 — interactive; onboards to hosted Team cloud (one command, OAuth flow, payment). The single funnel for the Team tier.
- **Memory-interop insurance (new, from tech scout):** align `knowledge_entries`/ontology record shape with the convergent memory envelope (id/content/type/timestamp/source/metadata + soft-delete/pinning/supersedes) and add export/import as first-class verbs — cheap hedge against the AMP/UMP/AMCP/memorywire standards race settling in 2027.
- **H1 (Q1 carry-forward):** start the Helm chart scaffolding in Q4 so Q1 is the procurement-grade deploy story, not a from-scratch.
- **PLG: usage dashboard GA** (already started in cycle 4 — extend from "what's used" to "what's worth keeping" — call this a feature, not a vanity metric).
- **Exit criteria:** v0.30.0; Team beta with 5+ active orgs; p95 target met; license + CLA done; hosted signup converts ≥1 design partner to paid pilot.

---

## 7. Q1 2027 (Jan + Feb + Mar) — "Enterprise Procurement Pass"

Quarterly outcome: **a mid-market+ buyer can pass procurement without sales friction.** Code mostly ready; the work is compliance + deployment + the trust primitive (provenance labels).

- **G1: SSO (SAML/OIDC)** + SCIM 2.0 provisioning. Okta + Entra ID + Google WS at minimum. Support **Enterprise-Managed Authorization (ID-JAG "zero-touch OAuth", stable June 2026)** — the IdP-grants-all-approved-servers pattern Okta/Claude/VS Code/Atlassian already adopted; this is the 2027 enterprise auth bar, not per-server OAuth consent screens.
- **G2: Audit log v2** — retention policies, tamper-evident hash chain, admin UI on top of the v1 schema.
- **G3: Deployment story** — Helm chart GA; Docker Compose prod profile; VPC/dedicated-tenant guide; data-residency pinning (EU/US).
- **G4: SOC 2 Type I → Type II runway** — controls automation; engage auditor when headcount triggers (likely Apr–May 2027).
- **G5: Multi-repo org topology GA** — cross-repo service graphs; monorepo scale (1M+ elements) verified.
- **G6: Provenance everywhere** — EXTRACTED / INFERRED / AMBIGUOUS labels surfaced in *every* tool response schema. This is the trust primitive that justifies the price.
- **G7 (new, from tech scout): call-graph accuracy v2** — make the existing `src/lsp/` hybrid layer (2,639 LOC, currently budget-gated) the default type-resolution pass for the top 5 languages (go/ts/py/rust/java); persist resolved-vs-heuristic `confidence` on every `calls` edge; report precision in `doctor --deep`. Tree-sitter-only edges are the publicly-criticized ceiling — this closes it for the languages that matter.
- **W14 (W2-W6 carry-overs):** the cycles that slipped from Q3 — close them; cycle 12+ runs the Q4 work.
- **PLG: Team GA + Ent pilot.** Convert at least 3 design partners to paid; start 1 enterprise pilot.
- **Exit criteria:** v0.33.0; SOC 2 Type I report in flight; ≥1 enterprise pilot signed; provenance labels in 100% of tool responses; ≥2k★ / ≥5k weekly installs.

---

## 8. Q2 2027 (Apr + May + Jun) — "Platform Play"

Quarterly outcome: **other agents build on LeanKG.** This is the wedge-expansion quarter.

- **H1: Public query API + webhooks** — REST/gRPC read API with API keys, usage analytics; Backstage plugin.
- **H2: Embeddings marketplace** — pluggable model registry (local ONNX default; BYO remote models). The per-model collections we already ship become a product surface.
- **H3: Bi-temporal code intelligence GA** — timeline queries, environment promotion (upcoming→staging→production) as first-class workflow. "What did this service look like when incident X happened" becomes a tool.
- **H4: Agent-native features v2** — personas/diaries/SKILL.md generation promoted; reflection-driven ranking biasing.
- **H5: Channel partnerships** — MCP-gateway vendors (MintMCP, Kong, Traefik Hub) list LeanKG as a governed server; DevEx/portal teams integration kit.
- **H6: Air-gap tier** — offline licensing, signed updates, zero-telemetry guarantee, NIST 800-171 alignment checklist. The wedge for gov/defense buyers.
- **Exit criteria:** v0.36.0; ≥8k★ / ≥25k weekly installs; ≥20 paid design partners; Backstage plugin adopted by ≥1 community; 1 air-gap pilot.

---

## 9. North-star scoreboard (12-month targets)

| Metric | Now (v0.26.1) | End Q3'26 | End Q4'26 | End Q1'27 | End Q2'27 |
|---|---:|---:|---:|---:|---:|
| GitHub stars | 214 | 500 | 2,000 | 4,000 | 8,000 |
| Weekly installs | ~hundreds | 1k | 5k | 15k | 25k |
| MCP tools (stable contract) | 76 registered (count unreliable: docs say 73, livetest 79–83) | ~70 semver'd, one honest count | ~70 + RBAC | ~70 + provenance | ~72 + API v1 |
| Datalog remnants in `src/` | 238 call sites (now 39) | 0 | 0 | 0 | 0 |
| `run_script` sites in `src/db/mod.rs` | 39 | ≤25 | 0 | 0 | 0 |
| CI gates | lib-only | lib + PG-integ | + e2e smoke | + perf gate | + security |
| p95 top-10 tools @ 100k elems (remote PG) | unmeasured | <500ms | <150ms | <100ms | <75ms |
| `temporal_query` p95 (remote PG) | >300s | <60s | <10s | <5s | <2s |
| External contributors (merged) | 0–1 | 3 | 5 | 8 | 12 |
| Design partners (paid) | 0 | 0 | 5 | 20 | 30 |
| SOC 2 | none | none | none | Type I | Type II runway |
| Versions shipped | 0.26.1 | 0.28 | 0.30 | 0.33 | 0.36 |

---

## 10. Risks & mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|:---:|:---:|---|
| W2-W6 SQL conversion regression | Medium | High | Parity harness gate each wave; small waves; promote PG-integ to CI *before* W2 starts (E4 → cycle 6) |
| Solo-maintainer bus factor | High | High | Subagent-parallel workflow is a real moat — keep investing in `docs/AGENTS.md`, `docs/workflow-opencode-agent.md`, parity tests as "agent-friendly surface"; good-first-issue pipeline from Sep; ≥3 external contributors in Q4 |
| **codebase-memory-mcp (40.9K★, MIT, arXiv paper) absorbs the OSS graph-MCP mindshare** | **High** | High | They are single-file SQLite, 15 tools, no multi-user server, no doc↔code traceability, no agent memory, no embeddings hybrid. LeanKG's moat is the *team/org layer above the local binary* — ship it (Q4 Team beta) rather than compete on stars for the commodity tier. Publish our own benchmark in their frame (Sep). |
| GitNexus license flip (to MIT/Apache) | Low | Medium | Less relevant now — the category leader already took the permissive-license slot. Our wedge is multi-user + Postgres + semantics depth. |
| MCP spec breaks our auth/transport | Medium | Medium | Track MCP spec weekly; ship minimal-impact upgrades in 7-day cycles; OAuth 2.1 in Q4 is the highest-risk bet. |
| pgvector ceiling at 1M+ vectors | Low | Medium | Iterative HNSW scans + halfvec in pgvector 0.8/0.9 already cover our 2027 needs; revisit Q3'27 if multi-tenant pushes past 5M |
| **Harness-native search out-competes LeanKG's semantic layer (2026-08-30 assessment: semantic probes 0/3, below confidence floor)** | High | High | Narrow the surface: lead with the org-memory substrate (traceability, env conflicts, incidents, team map, cross-repo service graph) — `FR-HEA-01..05`; fix semantic dead-ends (fallback hints); stop positioning against harness-native Glob/Grep/LSP. |
| EU AI Act compliance gap | Medium | Medium | G6 provenance labels (Q1) are the single biggest compliance asset; document data-residency + audit log in procurement packet |
| Metering-backlash contagion (Augment/CodeRabbit fallout) | Medium | High | Lock in flat published pricing in Q4 forever; commit publicly; codify in CLA. |
| Subagent-token-cost ceiling | Low | Medium | Project already documents 250KB Ox Alpha payload discipline in global CLAUDE.md — extend to cycle-level token budgets in `docs/cycles/HANDOFF.md`. |

---

## 11. Open questions (block Q4 decisions)

1. **Core license.** MIT vs Apache-2.0 vs AGPL. Decision needed by Sep 30 to land in v0.28 README. Recommend Apache-2.0 (matches the rest of the Rust ecosystem, plays with enterprise, no viral risk).
2. **Pricing for hosted Team.** $25/dev/mo is in v1; validate with 3 design-partner conversations in Oct. The market gives a strong wedge: Augment's token-based + 40% service fee (~$0.03–0.06/query) is precisely the metering backlash buyers complain about. Flat per-seat, no token toll, published forever — and say so in the README.
3. **SSO scope for Q1.** SAML only? OIDC only? Both? Recommend both — most enterprise buyers want both; SAML is the legacy floor.
4. **Air-gap demand.** H6 only matters if at least 2 design partners ask for it. Validate in Q1 buyer conversations.
5. **Backstage plugin scope.** Plugin itself is small; the harder question is which Backstage entities to model (Component, Resource, API, System). Defer to Q2 design sprint.

---

## 12. Process — how the roadmap updates

This document is refreshed **every 6 weeks** as part of the cycle retrospective. The refresh is owned by the same agent (or human) that closes the cycle. The single rule: **if reality diverges from the doc, the doc is wrong.** Update it within 48h of any quarterly milestone shift.

Related docs to keep in sync (in order of authority):
1. `docs/prd.md` — SoT for requirements (P0–P3 status)
2. `docs/prd-task-tracker.md` — machine-readable task state
3. `docs/roadmap-tracker.md` — cycle-level status
4. `docs/roadmap-2027.md` — v1 quarter-level narrative (now superseded by this v2)
5. **This doc (`roadmap-2027-v2.md`)** — operational, near-term, evidence-anchored

When this doc and `roadmap-2027.md` disagree, this doc wins for *operational* questions (now/this month/next month) and `roadmap-2027.md` wins for *strategic* questions (quarterly themes, pricing, market). At the next refresh, merge them into a single `roadmap.md` to remove the ambiguity.

---

*Last updated: 2026-08-28. Author: Claude (goal-mode planning session). For questions, see `docs/cycles/HANDOFF.md`.*
