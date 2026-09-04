# Hackathon R1b — Prioritized Implementation Backlog

**Date:** 2026-08-22 · **Branch:** `feature/hackathon` · **Worktree:** `.worktrees/hackathon`
**Inputs:** [`prd-enterprise.md`](../prd-enterprise.md) · [`roadmap-2027.md`](../roadmap-2027.md) · [`roadmap-tracker.md`](../roadmap-tracker.md) §1/§6 · `AGENTS.md`

## Constraints (binding)

- Rust workspace; **Postgres-only**; remote PG via `.env` (`LEANKG_PG_URL`) — **never** a Docker Postgres.
- Patterns live in `src/cli/`, `src/mcp/`, `src/web/`, `src/db/pg/migrations.rs` (+ `src/db/pg/migrations/`).
- `origin/main` is squash-PR-only; all work lands via PR from worktree branches.
- TDD mandatory: failing test first, then implementation (`AGENTS.md` / tracker §2).
- Gates per change: `cargo build --release && cargo test --lib && make lint && cargo fmt --all -- --check`.

## Effort scale

| Size | Meaning |
|---|---|
| S | ≤ 1 focused day incl. tests |
| M | 1–3 days |
| L | > 3 days or cross-cutting risk |

## Order summary (value ÷ effort)

| # | ID | Title | Source | Effort | FIRST |
|---|----|-------|--------|--------|-------|
| 1 | H1 | `leankg connect` client-config generator | PLG-1 | S | ★ |
| 2 | H2 | ENT-1 audit-log foundation | ENT-1/W13/R3 | L | ★ |
| 3 | H3 | npm wrapper version-sync automation | W12/PLG-6 | S | ★ |
| 4 | H4 | Provenance labels surfaced in all graph responses | ENT-9/G6 | M | |
| 5 | H5 | Quickstart < 5 min timed smoke test | PLG-7/F6 | S–M | |
| 6 | H6 | Tool consolidation round 2 (76→~70) | W11/CORE-2/E2 | M | |
| 7 | H7 | Stable-tool-contract doc + CI guard | PLG-5/E2 | M | |
| 8 | H8 | CI benchmark regression gate (p95 ±20%) | CORE-6/F6 | M | |
| 9 | H9 | `leankg doctor --deep` self-diagnosis | innovation | M | |
| 10 | H10 | Usage dashboard from context_metrics | PLG-8/F3 | M–L | |
| 11 | H11 | `leankg export --markdown` snapshot docs | innovation | S–M | |
| 12 | H12 | README quickstart refresh + timing badges | innovation/F6 | S | |

---

## ★ Do first (top 3)

### H1 — `leankg connect claude-code|cursor|codex|gemini [--remove]` (PLG-1)

**Description**
One command writes the correct MCP client config for the four dominant agent clients. Idempotent: re-running merges rather than duplicates; `--remove` cleanly deletes only the LeanKG entry. Turns "read 4 docs and hand-edit JSON/TOML" into a single zero-config step.

**Why**
FR-PLG-1 (P0), roadmap F4 "one-command setup"; the startup segment's #1 need is zero-config onboarding (PRD §1.1). Directly feeds the "quickstart < 5 min" promise (PLG-7) and registry listing story (PLG-2).

**Implementation sketch**
- New `src/cli/connect.rs` registered in `src/cli/mod.rs`; core logic in `src/connect/mod.rs` with one writer module per client: `claude_code.rs` (`~/.claude.json` → `mcpServers.leankg`), `cursor.rs` (`~/.cursor/mcp.json`), `codex.rs` (`~/.codex/config.toml` → `[mcp_servers.leankg]`), `gemini.rs` (`~/.gemini/settings.json` → `mcpServers`).
- Config entry: stdio transport default — `command = <current_exe>`, `args = ["mcp-stdio", "--watch", "--project", <cwd>]`; optional `--remote http://host:9699` writes HTTP URL variant.
- Idempotent merge via JSON-preserving edit (serde_json::Value walk, preserve sibling keys); TOML via `toml_edit` to keep comments; write temp file + atomic rename; never touch non-LeanKG entries.

**TDD plan (test-first)**
1. Red: unit tests in each writer module using `TempDir` as faked HOME — assert exact emitted config shape per client (JSON parse / toml parse assertions).
2. Red: idempotency test — run connect twice, output byte-equal (or key-set-equal) and no duplicate `leankg` key.
3. Red: `--remove` test — pre-seed config with other servers present, remove leaves them intact, exit 0 when absent.
4. Green: implement writers; e2e CLI test `tests/cli_connect_tests.rs` invoking clap command with env-overridden home dir.

- **Effort:** S
- **Risk:** Low — client config formats drift over time (mitigate: golden fixtures + version note in `--help`).
- **Dependencies:** None. Unblocks PLG-7 quickstart measurement and README quickstart rewrite (H12).
- **Acceptance criteria:**
  - All 4 clients configured by one command on a clean machine; re-run is a no-op; `--remove` restores prior state exactly.
  - Unit + e2e tests green in CI; documented in README quickstart section.

---

### H2 — ENT-1 audit-log foundation (append-only who/agent/tool/project ledger)

**Description**
Append-only ledger of every MCP and REST call: actor, agent-client, tool, project, args-hash, result-status, timestamp. JSON-lines export plus tamper-evident SHA-256 hash chain; write overhead budget < 2 ms. The procurement keystone every enterprise/security reviewer asks for first.

**Why**
FR-ENT-1 (P0 ACs: JSON-lines export; admin-queryable; <2 ms overhead; hash chain). Tracker W13 explicitly starts here; hackathon round R3 is scoped to it. Roadmap F3 calls it "the enterprise procurement keystone"; prerequisite for ENT-7 SIEM export and SOC2 evidence (ENT-10).

**Implementation sketch**
- Migration `src/db/pg/migrations/00XX_audit_log.sql`: table `audit_log(id BIGSERIAL PK, ts TIMESTAMPTZ NOT NULL DEFAULT now(), actor TEXT NOT NULL DEFAULT 'local', agent_client TEXT, tool TEXT NOT NULL, project TEXT, args_hash TEXT NOT NULL, result_status TEXT NOT NULL, prev_hash TEXT, entry_hash TEXT NOT NULL)`; append-only enforced with `REVOKE UPDATE, DELETE` + BEFORE UPDATE/DELETE trigger raising exception.
- New `src/audit/mod.rs`: `AuditRecorder` (fire-and-forget via bounded `tokio::sync::mpsc` + batched INSERT so the hot path stays < 2 ms), chain builder (canonical JSON of record fields → SHA-256, prev_hash linked), `verify_chain()` scanner, JSONL exporter.
- Hooks: dispatch wrapper in `src/mcp/handler.rs` and Axum middleware in `src/web/handlers.rs` (hash args with SHA-256 — never log raw args, NFR-2).
- CLI: `leankg audit export --since --until --format jsonl --out FILE` and `leankg audit verify` in `src/cli/audit.rs`.

**TDD plan (test-first)**
1. Red: unit test chain math — N synthetic records verify; flipping any byte in any exported line makes `verify` fail naming the broken sequence number.
2. Red: integration test `tests/pg_audit_log_tests.rs` (live PG from `.env`) — insert 100 events through recorder, append-only trigger rejects UPDATE/DELETE, exporter emits exactly 100 well-formed JSONL lines with required fields.
3. Red: overhead test — bench harness records per-call added latency; assert p50 added < 2 ms.
4. Green: implement migration, recorder, hooks, CLI.

- **Effort:** L
- **Risk:** Medium — hot-path latency if recording is synchronous (mitigate: async channel + batching, drop-oldest policy under backpressure with counter metric); migration ordering on existing DBs (use standard migrations.rs flow).
- **Dependencies:** None hard. Feeds ENT-7 SIEM drain and ENT-10 SOC2 later; benefits from H9-style diagnostics but not blocked.
- **Acceptance criteria:**
  - Every MCP tool call and mutating REST call produces exactly one audit row with all 7 mandated fields.
  - `audit export --format jsonl` round-trips; `audit verify` detects any tampered line; append-only trigger blocks mutations.
  - Measured write overhead < 2 ms p95 in benchmark run committed under `tests/benchmark/`.
  - Integration suite green against remote PG; no Docker.

---

### H3 — npm wrapper version-sync automation (W12 / PLG-6)

**Description**
Release workflow auto-bumps `npm/leankg/package.json` to the crate version and publishes on tag. Ends the current 9-minor drift (npm 0.17.9 vs crate 0.26.0) that makes the npm install path look abandoned. Cheap automation, immediate distribution credibility.

**Why**
Tracker W12 pending "quick win"; FR-PLG-6 (P0 AC: npm version == crate version on every release); roadmap E2. Broken npm path directly contradicts the PLG wedge.

**Implementation sketch**
- `scripts/sync-npm-version.sh`: read `version` from root `Cargo.toml`, validate semver, write into `npm/leankg/package.json` (`npm version $V --no-git-tag-version`), `git diff --exit-code` guard.
- Extend `.github/workflows/release.yml`: after crate publish/tag step — run sync script, commit bump (`chore(npm): sync vX.Y.Z`), then conditional `npm publish` in `npm/leankg` gated on `NPM_TOKEN` secret presence; fail job loudly if versions diverge post-step.
- Add divergence guard to `ci.yml`: tiny step failing main builds when `Cargo.toml` ≠ `package.json` versions, so drift can never silently return.

**TDD plan (test-first)**
1. Red: shell test harness (bats or plain bash asserts) for sync script: fixture Cargo.toml+package.json → correct rewrite; mismatched input exits nonzero; no-op when equal.
2. Workflow validation: `actionlint` step in CI on all workflows; dry-run mode of release job (`workflow_dispatch` with publish disabled) exercised once before enabling real publish.
3. Green: wire into release.yml + ci.yml guard.

- **Effort:** S
- **Risk:** Low — npm publish credentials/2FA (gate on secret presence, document token setup; keep manual fallback documented).
- **Dependencies:** None.
- **Acceptance criteria:**
  - Next tagged release publishes matching npm version; `npm view leankg version` == crate version.
  - CI fails on any future version divergence; actionlint green on modified workflows.

---

## Remaining backlog (value order)

### H4 — Provenance labels surfaced in ALL graph responses (ENT-9)

**Description**
Every edge in every tool output carries `confidence_label ∈ {EXTRACTED, INFERRED, AMBIGUOUS}` — today only some responses (e.g., `query_graph`) do. Sweep serializers so agents can always calibrate trust. This label discipline is LeanKG's marketable differentiator vs black-box retrieval engines.

**Why**
FR-ENT-9 (P0 AC: "Every edge in tool output carries confidence_label"); roadmap G6 "provenance everywhere" and positioning pillar "deterministic, auditable".

**Implementation sketch**
- Audit edge serialization paths: `src/graph/query.rs` result structs, `src/mcp/handler.rs` response builders, `src/web/handlers.rs` REST graph endpoints.
- Introduce single `RelationshipOut { ..., confidence_label }` serializer used everywhere; default `EXTRACTED` for extractor-created edges, `INFERRED` for derived hops, `AMBIGUOUS` where confidence < threshold already computed.
- Backfill: SQL migration not needed if label computed at read time; otherwise `ALTER TABLE relationships ADD COLUMN confidence_label TEXT DEFAULT 'EXTRACTED'` in `src/db/pg/migrations/`.

**TDD plan (test-first)**
1. Red: contract test iterating the registry of graph-returning MCP tools against a seeded fixture project — assert every relationship object has `confidence_label` with valid enum value (currently fails for at least one tool).
2. Red: unit tests on `RelationshipOut` defaults per edge origin.
3. Green: refactor serializers to the shared struct until contract test passes.

- **Effort:** M
- **Risk:** Medium — touching shared response shapes can break clients (coordinate with H7 contract doc; additive field only).
- **Dependencies:** None; should land before or with H7 so the published contract includes the field.
- **Acceptance criteria:** Contract test proves 100% of graph-returning tools emit `confidence_label`; no removed fields; lib + matrix suites green.

---

### H5 — Quickstart < 5 min timed smoke test (PLG-7)

**Description**
CI-timed smoke: init + index a ~10k-element fixture repo + first successful query, wall-clock measured, median across runs must be < 300 s. Converts the marketing claim into an enforced regression gate and produces a shareable timing artifact.

**Why**
FR-PLG-7 (P0 AC: "Median < 5 min in CI-timed smoke test"); roadmap F6 credibility metrics.

**Implementation sketch**
- Fixture generator `tests/fixtures/gen_quickstart_repo.rs` (deterministic synthetic tree ≈10k elements).
- Integration test `tests/quickstart_timed_smoke.rs` (#[ignore]-tagged; dedicated CI job running release binary): time `init` → `index` → one `search_code` query via stdio JSON-RPC; emit `quickstart-timing.json` artifact (per-phase ms, total, commit sha).
- Threshold assertion in test + separate eval function so the "<5min median" logic is unit-testable without running the full index.

**TDD plan (test-first)**
1. Red: unit test timing evaluator — medians over sample arrays; boundary 300_000 ms exactly; malformed artifact rejected.
2. Red: fixture determinism test — generated tree yields identical element count twice.
3. Green: implement generator + timed test; wire nightly/scheduled CI job uploading artifact.

- **Effort:** S–M
- **Risk:** Medium — runner noise causes flaky red (median-of-N + generous margin; quarantine rule per roadmap E4).
- **Dependencies:** Release build speed; benefits from H12 badge publishing.
- **Acceptance criteria:** Scheduled CI job green with median total < 300 s; timing artifact attached to run; evaluator unit-tested.

---

### H6 — Tool consolidation round 2: 76→~70 (W11 / CORE-2)

**Description**
Remove thin-wrapper tools: delete `get_graph_report` (thin wrapper whose side effect writes `GRAPH_REPORT.md`; move file-write behind remaining surface or CLI), fold orchestrate's cached-intent path into the cache layer it wraps, fold `search_by_requirement` into the traceability quartet (`get_traceability`, `get_feature_flow`, `search_by_requirement` consumers documented). Each removal updates the matrix test and tool reference doc in the same PR.

**Why**
Tracker W11 pending; FR-CORE-2 (P0: matrix passes, contract doc updated); roadmap E2 tool-surface discipline — smaller surface = smaller audit/contract burden (synergy with H7/H2).

**Implementation sketch**
- Per candidate: mark deprecated in registry metadata (H7 field), remove handler arm in `src/mcp/handler.rs`, drop definition in `src/mcp/tools.rs`.
- Update `tests/redundant_tools_matrix.rs`: add names to `REMOVED_TOOLS`, adjust expected count assert (76→70 target).
- Update `instructions/leankg-tools.md` + AGENTS.md prefer-order tables; preserve `GRAPH_REPORT.md` side effect via existing report-writing code path retained behind kept tools/CLI.

**TDD plan (test-first)**
1. Red: extend matrix test expecting new count + absence list (fails while tools still exist).
2. Red: deprecation-alias test — calling a removed name returns structured "removed in vX.Y, use Y" error (grace period per PLG-5 policy).
3. Green: delete handlers/tools until matrix green; docs updated same commit.

- **Effort:** M
- **Risk:** High-ish — removals break agent muscle memory and docs in the wild (deprecation error messages + 2-minor notice window mitigate).
- **Dependencies:** H7 ideally lands first (registry metadata + policy), but can proceed in parallel with manual bookkeeping.
- **Acceptance criteria:** `redundant_tools_matrix` green asserting ≤70 active tools; every removed name answers with actionable deprecation error; leankg-tools.md diff matches removed set.

---

### H7 — Stable-tool-contract doc + CI guard (PLG-5)

**Description**
Publish `docs/tool-contract.md`: semver'd tool registry (name, schema digest, since-version, stability), deprecation policy (2-minor notice). CI guard snapshots the tool registry to `tools-contract.json`; any unregistered breaking change (tool removed / required param added / schema digest changed without minor bump) fails the build.

**Why**
FR-PLG-5 (P0 AC: contract doc published; CI fails on unregistered breaking change); roadmap E2; protects the 97%-agent audience that breaks silently when tools shift.

**Implementation sketch**
- Registry introspection: derive a canonical manifest from `src/mcp/tools.rs` definitions (serde → sorted JSON: name, inputSchema digest via SHA-256, description).
- Committed baseline `docs/tool-contract.json`; new test `tests/tool_contract_guard.rs` diffs manifest vs baseline with explicit override marker (`"breaking": true` + version bump note in PR template).
- Generate human doc `docs/tool-contract.md` from baseline via `xtask`-style bin or `cargo run --release -- tools contract-gen`.

**TDD plan (test-first)**
1. Red: unit tests on manifest generation — deterministic ordering, digest stability, pretty-printed diff on mismatch.
2. Red: guard test with mutated fixture baseline fails listing exact drift (added/removed/changed).
3. Green: generate real baseline, wire guard into ci.yml.

- **Effort:** M
- **Risk:** Low-Medium — guard friction on legitimate changes (escape hatch = intentional baseline update reviewed in PR).
- **Dependencies:** Benefits H6; independent otherwise.
- **Acceptance criteria:** Contract doc published; deliberately breaking a tool schema locally turns CI red with precise message; baseline update path documented.

---

### H8 — CI benchmark regression gate: fail PR if p95 regresses >20% (CORE-6)

**Description**
Store p95 baselines for top-10 tools as committed JSON; CI job reruns the unified benchmark on PRs touching hot paths and fails when any tool's p95 exceeds baseline × 1.2. Makes the performance floor enforceable instead of aspirational.

**Why**
FR-CORE-6 (P1 AC: "benchmark-unified report in repo; regression gate ±20%"); roadmap F6 p95<150ms @100k elements; supports "fast enough for startups" positioning.

**Implementation sketch**
- Build on existing `tests/benchmark/` harness; new comparator bin/script `scripts/bench-gate.py` (or Rust bin): reads `baseline.json` vs fresh `result.json`, emits markdown table + exit code.
- Baseline file `benchmarks/baseline-p95.json` (tool → p95_ms, machine class note, commit).
- CI job keyed on `paths:` filter (`src/graph/**`, `src/db/**`, `src/mcp/**`); scheduled job on main refreshes baseline via approved PR.

**TDD plan (test-first)**
1. Red: unit tests for comparator — exactly +20% boundary passes, +20.1% fails; missing tool in results fails; new tool ignored with warning; malformed JSON rejected.
2. Golden fixtures for both outcomes.
3. Green: wire job; run once to seed honest baseline.

- **Effort:** M
- **Risk:** Medium — shared-runner variance (pin runner class, warm-up rounds, median-of-3; flake quarantine per E4).
- **Dependencies:** Benchmark harness stability; pairs with H5 infra patterns.
- **Acceptance criteria:** Synthetic +25% regression injected locally makes gate red; clean PR green; baseline refresh procedure documented in file header.

---

### H9 — `leankg doctor --deep` self-diagnosis

**Description**
One command that interrogates the deployment: PG reachability/latency via `LEANKG_PG_URL`, migration state (applied vs pending files), index freshness (max indexed_at vs git HEAD), pool config sanity, embeddings/vector state. Human + `--json` output; non-zero exit codes make it CI/support-script friendly.

**Why**
Innovation fitting "self-hostable, enterprise-ready": cuts support tickets, gives ENT-5 deploy-kit users a first-line health check, complements ENT-1 auditability with operational trust. No external APM needed (NFR-friendly).

**Implementation sketch**
- Extend `src/cli/doctor.rs`: check modules under `src/doctor/` — `pg_reachability` (SELECT 1 + rtt ms + TLS status), `migrations` (diff applied ledger vs `src/db/pg/migrations/*.sql`), `index_freshness` (max(elements.indexed_at) vs newest tracked-file mtime/git sha), `pool_config` (pool size vs server max_connections), `embeddings_state` (vector rows vs element count when feature enabled).
- Output: severity-tagged findings (OK/WARN/FAIL) + remediation hint per finding; `--json` machine mode.

**TDD plan (test-first)**
1. Red: unit tests per checker with stubbed inputs — verdict mapping, JSON schema shape, exit-code aggregation rules.
2. Red: integration test against live PG asserting healthy-env run returns all OK and exit 0; a deliberately wrong `LEANKG_PG_URL` fails pg_reachability with FAIL + exit≠0.
3. Green: implement checks.

- **Effort:** M
- **Risk:** Low — false WARNs on exotic setups (every WARN must carry a remediation hint and be suppressible).
- **Dependencies:** None.
- **Acceptance criteria:** Healthy remote-PG environment scores all-green; each failure class demonstrably detected (unit-proven); `--json` validated by schema test; documented in AGENTS.md CLI table.

---

### H10 — Usage dashboard from context_metrics (PLG-8)

**Description**
Per-project and per-user views over the existing `context_metrics` ledger: tokens saved, queries/day trend, top tools — served via REST endpoints and a UI v2 panel, plus CSV export. Monetization proof: shows the "no token toll" savings claim with the customer's own data.

**Why**
FR-PLG-8 (P1 ACs: per-user + per-project views; CSV export); roadmap F3; Team-tier deliverable ($25/dev/mo includes usage dashboard). Data already exists (`context_metrics`: tokens_saved, savings_percent, tool_name, timestamp, project_path).

**Implementation sketch**
- Aggregation queries in db layer (SQL via the seam; if W8 seam waves haven't landed this file's slice yet, follow the prevailing pattern in `src/db/pg/`): daily rollup by tool/project, totals, top-N.
- REST: `GET /api/usage/summary?project=&user=&from=&to=` + `?format=csv` in `src/web/handlers.rs`.
- UI v2 panel card in embedded dashboard (follow existing ui-v2 component conventions).

**TDD plan (test-first)**
1. Red: aggregation correctness tests on seeded fixture rows (known sums/trends/top-N, timezone boundary case).
2. Red: CSV formatter tests (header row, quoting, empty result set).
3. Red: REST handler tests (auth-free local mode; param validation 400s).
4. Green: implement queries/endpoints/panel.

- **Effort:** M–L
- **Risk:** Medium — rollup query cost grows with ledger size (add index `(project_path, timestamp)` in migration; cap range windows); per-user identity only meaningful after ENT-1/PLG-3 actors exist (ship per-project first, per-user column ready).
- **Dependencies:** Reads existing data; richer per-user views after H2. Index migration follows migrations.rs flow.
- **Acceptance criteria:** Dashboard renders savings + trends from live data; CSV downloads open correctly in spreadsheet apps (fixture-verified); rollups match independently computed SQL results in tests.

---

### H11 — `leankg export --markdown` git-committable graph docs

**Description**
Render the graph snapshot as reviewable Markdown — clusters, god nodes, per-file dependency tables, provenance counts — suitable for committing to the target repo so humans browse architecture in PRs. Reuses snapshot machinery; deterministic output makes diffs meaningful in code review.

**Why**
Innovation on positioning: "the code-intelligence layer other agents build on" (H-track) + auditability ethos; gives staff engineers (persona P2) a zero-UI artifact and doubles as documentation CI.

**Implementation sketch**
- CLI subcommand in `src/cli/mod.rs` → `src/export/markdown.rs`: consumes graph snapshot structures (same source as `export_graph_snapshot`), templates per section (cluster summary table, top-degree nodes with `confidence_label` tallies, file→file edges).
- Flags: `--out PATH` (default `GRAPH.md`), `--path PREFIX` scoping, `--max-nodes` truncation banner parity with HTML export.

**TDD plan (test-first)**
1. Red: golden-file test — small fixture graph renders byte-stable expected markdown (idempotency: two runs identical).
2. Red: scoping/truncation banner tests.
3. Green: implement renderer.

- **Effort:** S–M
- **Risk:** Low — large graphs produce huge files (truncation + scope flags default sane).
- **Dependencies:** None.
- **Acceptance criteria:** Deterministic render on this repo; committed sample in docs; truncation banner appears beyond node budget.

---

### H12 — README quickstart refresh + timing badges

**Description**
Rewrite README quickstart to the true 3-command path (`connect` → `index` → ask your agent), Postgres-only truth, and embed live timing badges fed by the H5 quickstart artifact. First impression = conversion; stale badges ("Rust 1.75+") currently contradict reality.

**Why**
Supports FR-PLG-1/PLG-2/PLG-7 funnel and roadmap E5 "docs truth sweep"; startup segment buys via README in minutes.

**Implementation sketch**
- README sections: 30-second pitch, Prereqs (Rust ≥1.85, remote PG via `.env`), Quickstart (H1 command featured), timing badge block (static shields.io badge values updated by CI job reading `quickstart-timing.json`), link-out to `docs/tool-contract.md` (H7).
- Small CI step (in H5's job) commits badge JSON / opens PR when median changes >10%.

**TDD plan (test-first)**
1. Red: link/claim checker script `scripts/readme_truth_check.sh` — asserts commands mentioned exist in `--help` output and referenced doc paths exist (fails today on stale bits).
2. Green: fix README until checker passes; badge step wired to H5 artifacts.

- **Effort:** S
- **Risk:** Low.
- **Dependencies:** H1 (featured command), H5 (timings), H7 (doc link) — order last in the wave.
- **Acceptance criteria:** Truth checker green; quickstart copy matches actual CLI; badge renders with current measured median.

---

## Sequencing note for hackathon rounds (maps to tracker §6)

- **R3** ← H2 (ENT-1). **R4** ← H1 (PLG-1). **R5** brainstorm picks ← H4/H9/H8.
- Parallel-safe fan-out set: {H1, H3, H9} then {H4, H5, H7}; H6 after H7; H10/H11/H12 fill remaining capacity.
- Every item lands as squash PR on `feature/hackathon` with TDD evidence noted in HACKATHON.md log.
