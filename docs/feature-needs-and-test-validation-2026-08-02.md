# LeanKG Feature Needs + Agent Test-Validation — Research Findings (2026-08-02)

**Status:** Research draft (internet + local state merge). Not a PRD change.
**Scope:** (1) What features the 2026 code-KG market needs, mapped to LeanKG current state. (2) How an agent can prove ALL tests pass (regression + new feature) before declaring done.
**Live result (2026-08-02):** Full-suite probe surfaced a real product bug in `src/db/schema.rs` — `ensure_canonical_relationships` dropped only 2 of 3 indices before CozoDB `:replace`, failing `test_init_db_repairs_legacy_code_elements_after_recorded_migration` (`cannot replace relation relationships since it has indices`). Fixed (drop+recreate all 3). See Part B.6.

Sources: internet research (competitor matrix + agent-test-validation), `docs/prd.md`, `docs/test-coverage-status.md`, `docs/feature-testing-progress.md`, `docs/roadmap.md`, `docs/workflow-opencode-agent.md`, live-test reports `docs/reports/*-2026-08-02.md`, CI workflow.

---

## Part A — Feature needs (what the market wants in 2026)

### A.1 Competitor feature matrix (condensed)

| Tool | Core model | Feature highlights |
|------|-----------|--------------------|
| Sourcegraph | SCIP precise symbol graph, exhaustive search | `count:all` deterministic search, cross-repo go-to-def/refs, Deep Search (agentic NL w/ citations), Cody, Batch Changes |
| GitLab Orbit | SDLC+code property graph (ClickHouse CDC) | Cross-project blast radius, vulnerability lineage, MCP+`get_graph_schema`/`query_graph`, Orbit Local (DuckDB) |
| GitHub Copilot | RAG + Copilot Memory + CodeQL | Custom KBs (≤100 repos), repo-fact memory w/ citations, `#codebase` hybrid search, agent mode MCP |
| DeepWiki | Precomputed code graph → generated wiki | Per-module pages, line-level citations, Fast (sub-second) vs Deep Research (multi-hop) Q&A, MCP |
| Greptile | Graph + swarm agents per PR | Full-context PR review, impact beyond diff, TREX writes+runs tests per PR, "Fix with Agent" |
| Graphify | Python + NetworkX + Leiden, 36 grammars | Multi-modal graph (code+docs+PDF+meetings), `graph.html`/`GRAPH_REPORT.md`, 20+ agent slash commands |
| codebase-memory-mcp | Pure C, tree-sitter, SQLite | Linux kernel 28M LOC in ~3min, sub-ms queries, hybrid LSP type resolve, 14 MCP tools |
| GitNexus | TS + LadybugDB (Kuzu), Leiden | Confidence-scored edges (tier 1.0→0.4), incremental schema-version guard, PDG/taint, hooks+skills |
| code-graph-mcp | Rust, tree-sitter, sqlite-vec | BM25+vector RRF + call-graph CTE + HTTP route tracing, token-aware L0-L3 compression |
| CodeScene | Git activity + Code Health (behavioral) | Hotspots × health → Tech Debt Friction, bus factor, refactoring targets |
| Joern/CPG | AST+CFG+PDG merged property graph | Graph-traversal taint/dataflow vuln discovery, DSL queries |

### A.2 Top 10 must-have features (2026 consensus)

1. **Graph + vector hybrid** (deterministic AST edges + embeddings + BM25, fused via RRF) — embeddings alone can't answer "who calls X".
2. **One-call blast-radius / impact with confidence scoring** — precomputed, severity-graded.
3. **Test-coverage awareness + test-impact analysis** (`tested_by` edges, `diff_impact`, which-tests-break).
4. **Freshness / staleness as first-class contract** — expose "how stale is this node/edge" to the agent; stale indexes silently grounding agents in phantom code is the #1 cited failure.
5. **Incremental indexing with schema-stability guarantees** — per-file content-hash gating, versioned schema, sub-ms to <10ms queries.
6. **Cross-repo search + declared cross-repo edges** (go.mod, Dockerfile FROM, package.json, HTTP/gRPC contracts).
7. **MCP server as the standard agent surface** — every tool ships MCP in 2026.
8. **Agent-oriented output** — token-bounded, citation-grounded, deterministic; single-call answers not N grep+read.
9. **Doc ↔ code traceability + generated architecture docs**.
10. **Behavioral/org intelligence** — tech-debt friction, bus factor, ownership, change coupling.

### A.3 LeanKG current state vs these needs

| Need | LeanKG state | Gap |
|------|--------------|-----|
| Graph + vector hybrid | RRF hybrid retrieval, HNSW embeddings, cross-encoder rerank (`kg_semantic_context`) | **Done** — strong |
| Impact w/ confidence | `get_impact_radius` severity+confidence, EXTRACTED/INFERRED/AMBIGUOUS | Mostly done; numeric per-edge confidence + confidence floors for traversal = niche to push |
| Test-impact edges | `tested_by` edges, `get_tested_by` | **Gap**: no `diff_impact`/which-tests-break command, no risk-ranked coverage gaps, no test-selection loop |
| Freshness/staleness | Content-hash incremental index, watcher, ontology watch, embed resume | **Gap**: no exposed "stale since commit X" metadata for agents |
| Incremental + schema-stable | Incremental index, `INCREMENTAL_SCHEMA_VERSION`-style guards (in watcher/indexer) | Mostly done |
| Cross-repo edges | Multi-repo registry, service graph, HTTP route extractor, service_calls | Partial: contract-drift detection absent |
| MCP surface | **85 MCP tools**, JSON-RPC, resources, hooks | Done (tool bloat risk — redundancy matrix exists) |
| Agent output | TOON/RTK compression, token budgets, `_token_budget` envelope | Done |
| Doc↔code traceability | `documented_by`, `references`, PRD indexing, docjoin symbol upgrade | Done |
| Org intelligence | Conversation mining, sessions, team map, god nodes | Partial |

### A.4 Highest-ROI feature opportunities (from research, mapped to LeanKG)

1. **Test-impact analysis as MCP tools** — `diff_impact(changed_files) → {tested_by affected, suggested test commands, coverage gaps}`. Market validated (Chisel, ast-impact-mapper, infigraph, recon, codescope all ship it in 2026); LeanKG has `tested_by` + call graph + `find_dead_code` — the primitives exist. **Niche moat: polyglot** (most are JS/TS-heavy).
2. **Freshness-typed graph** — expose staleness metadata (`indexed_at`, `valid_from`, `source_commit`) per node/edge as queryable fields; stale-aware confidence budget. Underserved niche.
3. **Numeric confidence edges** — promote `confidence_label` to numeric `confidence` + `reason` per edge, queryable. Only GitNexus does this.
4. **Cross-repo contract drift** — canonical route identity + cross-repo provider/consumer edges, drift detection on index.
5. **Self-eval harness** — publish a peer-reviewable eval (90-task style) to normalize the tool surface; also feeds Part B.
6. **PDG/taint optional layer** — opt-in `--pdg` Program Dependence Graph (TAINT_PATH edges) — expensive, defensive moat.

### A.5 What NOT to build (research-backed)

- LLM-extracted KGs (arXiv 2601.08773: 0.688 file success vs 0.902 deterministic; slower, less reliable). Stay deterministic-parser + agent-annotated.
- Full agent harness/runtime (US-GF-17 already scoped: install/hooks only).
- 36-language race before typed-resolve depth (PRD explicit non-goal).
- Cypher-only queryability as primary surface (graph-only weak at semantic recall).

---

## Part B — How an agent validates ALL test cases (regression + new feature)

### B.1 Current state (verified)

| Layer | Status |
|-------|--------|
| Unit tests (`--lib`) | **791 passed, 0 failed** (verified 2026-08-02, release) |
| Integration tests | **72 test files, ~20k LOC**; NOT run in CI (CI only runs `cargo test --lib`) |
| MCP tool coverage | 85/85 tools referenced by name in tests (redundancy matrix gates drift) |
| Test tooling | cargo test only — **no nextest, no llvm-cov/tarpaulin, no insta, no proptest, no cargo-mutants** |
| Live validation | `docs/planning/2026-08-02-live-test-plan.md` → per-feature evidence reports (good pattern, manual) |
| Hooks | PreToolUse leanKG routing hook exists; **no Stop-hook test gate** |

### B.2 The validation loop (what the agent must do, concretely)

**Gate principle: full suite is the default for "done". Targeted selection is only for iteration speed.**

1. **Baseline first** — before touching code, run full suite green. If already red, triage pre-existing failures before your change (never attribute old failures to your diff).
2. **Per-edit fast loop** — `cargo check` + targeted test for the changed area after every edit.
3. **Feature test first** (red-green) — write the failing test for the new feature before/with the code; a test written after implementation tends to be tautological.
4. **Full suite at gate** — run everything release-mode. For LeanKG:
   - `cargo test --release --lib` (unit) — fast, ~1.6s
   - `cargo test --release --test <feature_file>` — targeted integration
   - `cargo test --release` — full gate (all 72 integration files + lib)
   - `cargo test --release --features embeddings` — when embeddings touched
5. **Classify every failure** (evidence, not vibe):
   - **A env/infra** — timeout, OOM, port/RocksDB lock contention, network fetch → retry (bounded), skip documented
   - **B test bug** — weak assertion, order dependence, shared-state leak → fix test code
   - **C product bug** — deterministic + correlates with your change → fix source, file bug
   - Isolate: re-run failing test alone. Passed alone + intermittent = flaky; reproducible = real.
6. **Never mask** — no bumping retries to hide a flake; quarantine only via `#[ignore]` + tracking issue + owner + expiry.
7. **Live evidence for HTTP/MCP features** — one server, one RocksDB path; record command + trimmed output per probe (see `docs/planning/2026-08-02-live-test-plan.md` §3 evidence format).
8. **Report with evidence trail** — commands run, exit codes, pass/fail counts, live-report files. Don't assert "tests pass" — show the output.

### B.3 CI gap (highest-ROI fix)

`ci.yml` runs **only `cargo test --lib`** — the 72 integration test files (which is where regressions actually break: MCP tool behavior, graph queries, CozoDB schema, extractors) never run in CI. An agent's local full-suite pass is the only guard today.

**Fix:** add a `Test Suite (integration)` job running `cargo test --release --test '*'` (plus embeddings variant), gated on main + PRs. This closes the gap between what the agent proves locally and what CI enforces.

### B.4 Tooling roadmap (ROI-ranked)

| # | Tool | Why | Effort |
|---|------|-----|--------|
| 1 | **cargo-nextest** | ~3x faster parallel runner, per-test isolation (RocksDB/port collisions become non-issue), `--retries 2 --flaky-result fail`, JUnit, partition sharding. `.config/nextest.toml` profiles: `ci` (fast deterministic) vs `ci-extended` (embeddings/e2e/stress) | Low (config + install) |
| 2 | **cargo-llvm-cov** gate | Source-based coverage, `--fail-under-lines 80` CI gate; verifies new tests actually reach new code | Low-mid |
| 3 | **insta snapshots** | Golden JSON for graph-structured output (MCP responses, `get_graph_report`, exports, REST JSON). Directly matches LeanKG domain. `INSTA_UPDATE` CI-detect mode | Low-mid |
| 4 | **mcp-covenant / tooltest** | Contract test the `:9699` MCP registry — snapshot `tools/list`, fail CI on breaking schema (tool rename, new required arg). Agents depend on this interface | Low |
| 5 | **cargo-mutants** | Mutation testing — proves tests *check behavior*, not just reach code. Incremental on PRs, full on main | Mid |
| 6 | **proptest** | Property-based + shrinking for parsers, query builders, round-trip serialization | Mid |

### B.5 The single highest-ROI verification control

A **Stop hook** that runs the fast gate and blocks "done" on failure — ~15 lines of bash, zero tokens:

```json
// .claude/settings.local.json → hooks.Stop
{
  "matcher": "",
  "hooks": [{
    "type": "command",
    "command": "scripts/verify-gate.sh"
  }]
}
```

```bash
#!/usr/bin/env bash
# scripts/verify-gate.sh — blocks "done" unless tests pass
set -o pipefail
cargo test --release --lib >/tmp/verify-gate.log 2>&1 || {
  echo "FAIL: unit tests not green — see /tmp/verify-gate.log"
  exit 2   # Stop hook: nonzero blocks stopping, forces continuation
}
echo "OK: unit tests green"
exit 0
```

(`exit 2` in a Stop hook prevents the agent from stopping while tests fail; check `stop_hook_active` field to avoid infinite block.)

### B.6 Full-suite probe → real bug fixed (2026-08-02)

Running the full release suite (not just `--lib`) surfaced a genuine product bug that CI's `--lib`-only gate would never catch:

- **Failure:** `tests/integration.rs` `test_init_db_repairs_legacy_code_elements_after_recorded_migration` — `init_db` panicked with CozoDB `cannot replace relation relationships since it has indices`.
- **Root cause:** `ensure_canonical_relationships` (`src/db/schema.rs`) dropped only `rel_type_index` + `target_qualified_index` before `:replace`, but `init_db`'s existing-relations branch also creates `source_qualified_index`. The survivor index made CozoDB 0.7.6 refuse the `:replace`.
- **Fix:** drop all 3 indices before `:replace`, recreate all 3 after. (The parallel `ensure_canonical_code_elements` already dropped all 4 it creates — `relationships` was the inconsistent one.)
- **Evidence:** isolated test green (1 passed); full `integration` binary green (31 passed, was 30/1); `--lib` still 791/0.
- **Lesson:** a legacy-schema repair path is exactly the kind of code a `--lib`-only CI never exercises but real installs hit. This is why the full-suite gate (§B.3) matters.

---

## Part C — Recommended next steps

0. **Land the schema-repair fix** (§B.6): `src/db/schema.rs` relationships index-drop — **DONE 2026-08-02** (uncommitted, `docs/` note in this file). Commit as `fix(db): drop all relationships indices before canonical :replace`.
1. **Close CI gap** (§B.3): integration tests in CI. Highest confidence, low effort, removes the "agent says green but CI never ran these" hole.
2. **Ship `diff_impact` MCP** (§A.4.1): test-impact analysis using existing `tested_by` + call graph. Feature + dogfood (LeanKG's own validation).
3. **Add Stop-hook gate** (§B.5): 15-line bash, forces the agent to prove tests before done.
4. **Adopt nextest** (§B.4.1) then **insta** for graph JSON.
5. **Consider freshness-typed edges** (§A.4.2) as the differentiated feature for 0.20.x.

---

## Sources

Internet research (2026-08-02, general-purpose agents; files `/tmp/kg-research.md`, `/tmp/agent-test-research.md`):
- Competitor matrix: Sourcegraph docs/blog, GitLab Orbit docs, DeepWiki, Greptile, Sourcebot, CodeScene, Joern/CPG, GitNexus, codebase-memory-mcp, code-graph-mcp, Graphify, Chisel, infigraph, ast-impact-mapper, recon, codescope, Gortex
- Test validation: nexte.st, insta.rs, mutants.rs, martinfowler.com test pyramid, Azure Test Impact Analysis, OpenAI Codex docs, Cognition Devin testing docs, Claude Code Stop-hook patterns (prove_it, verification loops), Microsoft Rust Training coverage guide
- Primary anchors preferred over vendor blogs where available.

Local: `docs/prd.md` (§1.1, §5.27 FR-TEST-ED), `docs/test-coverage-status.md` (2026-07-18), `docs/feature-testing-progress.md`, `docs/roadmap.md`, `docs/workflow-opencode-agent.md`, `docs/planning/2026-08-02-live-test-plan.md`, `docs/reports/*-2026-08-02.md`, `.github/workflows/ci.yml`.
