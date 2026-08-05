# Workflow: Large-Scale Engine Migration via Fan-Out Subagents in a Git Worktree

**Status:** DRAFT v0.1 — derived from the LeanKG CozoDB → PostgreSQL + pgvector migration (2026-08-04 → 2026-08-05). 10 commits ahead of origin on `worktree-leankg-pg-migration`.

**Author:** Hermes (this session), synthesizing the actual Claude Code session JSONL `5d294459-37eb-46dd-8c7f-e6a492f1409d.jsonl` (2091 lines, 660 assistant turns, 326 tool calls, 14 subagent fan-outs, 11 TaskCreate, 41 user prompts).

**Audience:** Future migrations of similar scope (Rust/Go/Python service replacing an embedded engine with a client-server database, 2-3 week effort, 8-10 distinct phases).

---

## When to use this workflow

Use this workflow when **all** of these are true:

| Condition | LeanKG case |
|-----------|-------------|
| Migration touches **8+ distinct phases** with inter-dependencies | Phases 0–9 (spike → abstraction → schema → translator → vector → modules → regression → cleanup → perf) |
| A single agent loop would take **>1 working day** | Actual: ~12 hours of wall-clock with 14 subagents vs estimated 2-3 days serial |
| The plan document has **discrete per-phase exit criteria** | Each phase had "Exit:" bullet + measurable tests |
| The codebase has a **non-trivial abstraction surface to maintain during the migration** (trait, interface, schema version) | The `DbBackend` trait was kept intact through Phases 1-7 so other code could compile unchanged |
| **Production runtime is live and MUST NOT be touched** | Docker containers `leankg-leankg-1` and `leankg-enterprise-cozoserver-1` were running 24/7; only side dev containers allowed |
| The user is willing to **explicitly waive the "no commits without explicit user request" rule** for this goal | User said "Create PR for me after done everything" + "Keep working on this goal" |

**Do NOT use** for: 1-day bug fixes, single-file refactors, migrations where the engine boundary is clean and a `sed` + `go fix` suffices.

---

## Top-level shape

```
ONE worktree (one branch, one DB container)
  │
  ├── ONE parent agent (Claude Code session, owns the goal, runs the kanban)
  │      │
  │      ├── Research agent (read-only)              ─→ produces 1 doc (query inventory)
  │      │
  │      └── N worker subagents (one per phase)      ─→ each owns 1 phase end-to-end
  │             │                                        │
  │             │   fan-out pattern:                     │
  │             │   1. parent writes detailed prompt    │
  │             │   2. subagent does TDD on phase       │
  │             │   3. subagent commits + reports       │
  │             │   4. parent updates kanban + plan doc │
  │             │                                        │
  │             └─ examples of phase fan-outs: ──────────┘
  │                   Phase 0 spike, Phase 1 trait, Phase 2 schema,
  │                   Phase 3 translator, Phase 4 vector, Phase 5 modules,
  │                   Phase 5.5 regression, Phase 6 server, Phase 7 embed,
  │                   (Phase 8 cleanup + Phase 9 perf often sequential)
  │
  └── Single dev Postgres container (shared by all subagents)
```

The ONE worktree / ONE container constraint is non-negotiable. **All** subagents commit to the **same** branch on the same worktree. They don't isolate. If they did, every merge would risk corrupting the trait surface. The safety comes from **strict scoping in the prompt** ("only edit `/this/path`"), **shared Postgres container with scratch schemas**, and **the parent agent's commit discipline**.

---

## Step 1 — Up-front (BEFORE the first subagent)

### 1.1 Write the plan doc with exit criteria

The plan doc is the **single source of truth**. Every phase needs:

- Numbered sub-tasks (T1.1, T1.2, ...)
- An **Exit** bullet with measurable outcome (test count, perf threshold, files deleted)
- Cross-references to upstream phases ("see §2.2 schema", "see §8.1 examples")

LeanKG plan doc was 437 lines + 9-section structure. Without this scaffolding the subagents will invent different shapes for the same data model.

### 1.2 Establish the worktree + dev DB BEFORE the first subagent

```bash
# 1. Create worktree (do NOT use git checkout -b in main)
git worktree add .claude/worktrees/<feature>/<name> -b <branch> main

# 2. Create the dev DB container with a unique name (don't clash with prod)
docker run -d --name <service>-pg-phase0 -p 5433:5432 \
  -e POSTGRES_PASSWORD=postgres pgvector/pgvector:pg18

# 3. Write a per-subagent "STRICT RULES" preamble you'll append to every prompt
```

### 1.3 The per-subagent preamble (use VERBATIM in every fan-out prompt)

```text
STRICT RULES:
- Edit ONLY: <worktree path>. Never touch the main repo or any sibling worktree.
- Production containers <names> are LIVE — never stop/restart/modify them.
  Use only the dev container <name> for your verification.
- ALWAYS: cargo build --release / cargo test --release / cargo check.
  Never bare cargo build/test/run.
- Pre-commit hook = fmt + clippy --all-targets --all-features -- -D warnings.
  Keep code clean or commits will fail.
- NO AI attribution in commits or PRs (no Co-Authored-By, no AI footnotes).
- Commit cadence: one logical commit per T-task group, conventional commits
  (feat:, fix:, docs:, chore:, test:).
- If you find a bug outside your phase's scope: document it in
  docs/analysis/<your-phase>-findings.md, do NOT fix it.
- Push to origin after each commit. Parent agent pulls + verifies.
- When done, write a 1-page report (what shipped, what didn't, what's blocked).
```

This preamble **prevents the most common fan-out failure mode**: a subagent "fixing" out-of-scope work and stepping on the next phase's design.

---

## Step 2 — Fan-out pattern for each phase

### 2.1 The parent agent's prompt for a worker subagent

Template (illustrative with LeanKG actual prompts):

```text
You are a <role> working in a git worktree at <path> (the ONLY directory you may edit).

Project: <name>. We're migrating <old engine> → <new engine>.

Your job: <Phase N from plan doc>.

TDD: <how to apply TDD here — write test first, watch fail, then implement>.

STRICT RULES: <the preamble from §1.3>

STATE (verify before starting):
- <what's already done in upstream phases>
- <what files exist that you must read>
- <what NOT to touch>

YOUR DELIVERABLES:
1. <list of files to create/modify>
2. <list of test files to add with pass criteria>
3. <git commit(s) with conventional-commit messages>
4. <1-page report at docs/analysis/<phase>-report.md>
5. Push to origin/<branch>
```

### 2.2 What makes a good fan-out prompt

A good fan-out prompt is **self-sufficient**: the subagent should not need to read its parent's chat history to start. Specifically:

| Element | Why |
|---------|-----|
| Exact worktree path | Subagent must know what to `cd` into |
| Phase exit criteria copied from plan doc | Subagent can't infer these |
| Names of upstream agents' artifacts (files they wrote) | Subagent must read those before implementing |
| Explicit "what NOT to touch" list | Prevents scope creep |
| Concrete commit message examples ("feat(pg): ...") | Forces consistency |
| "STATE (verify before starting)" section | Subagent checks rather than assumes |
| "1-page report at <path>" | Forces structured handoff back to parent |

LeanKG fan-outs averaged 1200-1500 chars in the `prompt` field. Shorter = subagent guesses. Longer = subagent gets lost.

### 2.3 What to do when a subagent fails mid-phase

LeanKG had one case (Phase 3 SQL translator) where the first attempt died mid-refactor. The parent agent's response was:

```bash
# 1. Inspect current state in worktree
git log --oneline -8
git status --short
ls src/db/pg/
grep 'cozo' Cargo.toml

# 2. Find the partially-done file + read its current state
wc -l src/db/backend.rs
grep -rn 'cozo' src/db/ | head -10

# 3. Fan out a new subagent with the EXPLICIT HANDOFF context:
#    "Phase 3 was started but died. Here is exactly where it stopped.
#     src/db/backend.rs is COMPLETE [or PARTIAL]. Your job is to FINISH
#     this — read what's there, pick up from <last commit>, do NOT redesign."
```

**Critical rule: never have two subagents work the same phase concurrently.** If the first dies, the second reads the partial state and continues — does NOT restart.

### 2.4 Recovery subagent pattern

In LeanKG the recovery subagent (subagent #5, "Finish Phase 1 Arc threading") had this in its prompt:

```text
STATE (verify first):
- src/db/backend.rs is COMPLETE: `trait DbBackend: Send + Sync { ... }`,
  `SharedDb = Arc<dyn DbBackend>`, `CozoBackend` shim, `PostgresBackend` stub,
  `init_db/init_db_readonly/init_db_pg/init_cozo` factories, `resolve_engine()`.
  9 unit tests. DO NOT redesign it.
- src/db/schema.rs: has `pub fn run_script_cozo`, ... The types `NamedRows`
  and `ScriptMutability` are IMPORTED from `cozo` — check whether schema.rs
  re-exports them pub; if not, re-export them.

If something is missing from the STATE description, verify by reading the
file and proceed. Do not invent new structures.
```

This "verify first, don't redesign" preamble is essential for recovery agents.

---

## Step 3 — Parent agent's continuous duties

While subagents work, the parent agent does:

### 3.1 Kanban maintenance (every 30 min or on each subagent completion)

The kanban is **derived** from the plan doc, not maintained separately. LeanKG's kanban (`docs/pg-migration-kanban.md`) had:
- "Last updated" timestamp
- Source-of-truth pointer to plan doc §9
- "Note on staleness" explaining the parent JSONL stops but subagents continue
- Status legend (✅ done / 🚧 in progress / ⛔ blocked / ⬜ pending / ⚠️ at risk)
- "Current focus" section (what's running RIGHT NOW)
- Backlog (pending)
- Done (history)

**Hard rule:** if the kanban says "Phase X in progress" but `git log` shows Phase X already done, **the kanban is wrong, not git**. Fix the kanban.

### 3.2 Commit discipline

The parent agent never commits. Subagents commit. The parent agent:
1. Watches for new commits via `git log @{u}..HEAD` (must show 0 commits)
2. When `git log @{u}..HEAD` shows N>0, run `git push origin <branch>`
3. Update the kanban with the new commit hashes
4. Update the plan doc §9 progress tracker (the user explicitly asked: "Update to my plan document too")

### 3.3 Steering on user questions

The user asked many steering questions during LeanKG migration. Each was a chance to redirect:

| User question | Parent agent's response |
|---------------|------------------------|
| "why not fanout agents to implement this in paralel?" | Already was — show them the 14 subagents dispatched, ask which phases they want re-prioritized |
| "why phase 3 running so slow?" | Inspect subagent JSONL, check what tool calls it was making, identify if it was stuck on `cargo build` or actually thinking |
| "phase 6 completed?" | **Triangulate**: git log + plan doc §9 + kanban + subagent JSONL. Never answer from one source |
| "can phase 9 working paralel with phase 8?" | YES — they only need a working binary, not committed code. Set up a parallel worktree |
| "what is the ccurrent status of this goal" | Read-only kanban dump with commit hashes + uncommitted work |
| "do we still need the git worktree phase 9 in my locally now?" | Check if the parallel worktree has uncommitted work; if not, `git worktree remove --force + git branch -D` |

### 3.4 Anti-stale-JSONL defense

The LeanKG Claude session JSONL stopped being appended at 2026-08-04T14:46:46Z while subagents continued working. This is **normal** — parent JSONL is the parent's transcript, not the workers'. **Never claim "no progress" based on parent JSONL being quiet.** Always cross-check `git log` + subagent JSONL + plan doc + kanban.

---

## Step 4 — Subagent lifecycle

### 4.1 Fan-out taxonomy (LeanKG actual)

| # | Type | When | Typical duration |
|---|------|------|------------------|
| 1 | Worker (long-lived) | Standard phase work | 30 min – 4 h |
| 2 | Recovery worker | Phase partially done, previous agent died | 30 min – 2 h |
| 3 | Researcher (read-only) | Pre-phase: catalog queries, count files | 5 – 30 min |
| 4 | QA / regression | After implementation, sweep all features | 1 – 3 h |
| 5 | Reporter / finisher | Write docs, regression report | 30 min – 1 h |

### 4.2 When to fan out vs continue in parent

| Scenario | Choice | Rationale |
|----------|--------|-----------|
| "Phase X is independent of Y and Z" | Fan out (parallel) | Wall-clock win |
| "Phase X is the next sequential dep" | Wait for X to land, fan out X | Sequential by necessity |
| "Phase X is 80% done, Y depends on it" | Single worker finishes X, then fan out Y | Don't duplicate work |
| "I need a query inventory before designing the schema" | Fan out a researcher first | Plan must precede code |

### 4.3 Spawn budget

LeanKG had 14 subagents over ~12 hours. Each subagent costs:
- ~2-5 min for context load (reading plan doc, reading upstream phase artifacts)
- 1-4 hours of actual work
- ~500 KB JSONL output

Rule of thumb: **don't fan out more than 3-5 subagents in parallel** on the same worktree — they contend on the same Postgres container + same Rust compiler cache + same `git push` lock. The LeanKG user explicitly asked "why not fanout?" mid-stream and the answer was "we already are — see the dispatched agents."

---

## Step 5 — Verification between phases

Between every phase, the parent agent must verify:

```bash
# 1. Did the subagent commit?
git log --oneline @{u}..HEAD
# Expected: N>0 new commits with conventional-commit messages

# 2. Did the binary still build?
cd <worktree>
cargo build --release 2>&1 | tail -5
# Expected: "Finished `release` profile"

# 3. Did the test suite stay green?
cargo test --release --lib 2>&1 | grep 'test result'
# Expected: N tests passed

# 4. Did the subagent touch out-of-scope files?
git diff --stat @{u}..HEAD
# Review: only files in this phase's expected set

# 5. Does the plan doc §9 progress tracker need updating?
grep -E "^\| [0-9]" docs/plan-migrate-cozo-to-postgres-pgvector.md
# Update the "✅" / "🚧" status column to match reality

# 6. Push to origin
git push origin <branch>
```

If any of these fail, **don't proceed to the next phase fan-out**. Either fix the subagent's work in place, or fan out a recovery worker with the explicit handoff context.

---

## Step 6 — Phase 9 perf verification (the special case)

Phase 9 (perf verification on a large real codebase) has unique properties:

| Property | How LeanKG handled it |
|----------|----------------------|
| Takes 2-4 hours wall-clock per run (index 721k elements) | Run in foreground, NOT a fan-out. Or accept the long wait |
| Generates 5-10 GB of intermediate data | Use a scratch schema (`leankg_pg9`) on the same dev container |
| **Doesn't need committed code** to run — works against the dirty tree | Can start in parallel with Phase 8 cleanup (T8.4) |
| Buggy at scale (HNSW recall drift, missing GIN indexes) | Expect 2-3 missing indexes; plan for 2-4 hours of index-add + ANALYZE |
| Binary needs `--features embeddings` | `cargo build --release --features embeddings` adds 1-2 min but is required for T9.2 |
| Production binary may lack embeddings by default | **Document this in the Phase 9 report** as a v0.20.x follow-up |

LeanKG's Phase 9 actually completed in the **same dirty worktree as T8.4** — the subagent wrote `docs/analysis/pg-perf-large-codebase.md` while T8.4 was mid-flight. This works because:
- Phase 9 perf scripts only need a `leankg` binary
- The T8.4 cleanup was making the binary better, not breaking it
- Both workers shared the same Postgres container on different scratch schemas

---

## Step 7 — Releasing v0.20.x

LeanKG user explicitly asked about this mid-stream:

```
> "Check the current ci, what if i bump my self the new version 0.20.x does
>  the github action will increase from that ?"
> "what if after this goal achive how can i release version 0.20?"
```

The LeanKG workflow here was:

1. **Use `release-please`** (already configured in `.github/workflows/`) — do NOT manually bump `Cargo.toml`. Plan doc §8.5 explains:
   > "Push → release-please opens a release PR bumping to `0.20.0` (minor, via `feat:` commits). Do not manually bump `Cargo.toml`; it is ignored."
2. Conventional commits are the trigger: every `feat:` since the last release becomes a changelog entry.
3. The user explicitly set the scope: "**0.20.0** is **MINOR** because all the migration changes are additive (Postgres backend becomes the default; cozo shim stays as migration path until v0.21)."
4. **For breaking MAJOR (if removing cozo shim itself at v0.21)**: release-please never auto-majors (config `bump-minor-pre-major: true`). Manual procedure:
   - Bump `manifest.json` `"."` to target major
   - Push → CI releases from that base

**Anti-pattern:** manually editing `Cargo.toml` `version = "..."` field. The release-please config ignores it; you create a divergence that breaks the next release.

---

## Step 8 — Common pitfalls (LeanKG lessons)

### 8.1 The "ship cozo as the only engine" mistake

LeanKG kept `cozo` as a Cargo dep through Phases 0-7 so the migration shim could still validate against it. Removing cozo before Phase 8 meant **no way to A/B test** the new PG backend. The discipline:

- Keep both engines present + routable via `LEANKG_DB_ENGINE=postgres|cozo`
- Default to PG (the new path) but keep cozo as escape hatch
- Remove cozo ONLY at Phase 8, in a single "T8.4 cozo deletion" commit

### 8.2 The "commit but no push" bug

Subagents committed but sometimes forgot `git push`. Result: parent agent's `git log @{u}..HEAD` was empty but the local commits were there. **Fix:** the per-subagent preamble must include "push to origin after each commit", and the parent agent must verify the push happened (`git log origin/<branch>..HEAD` should be empty).

### 8.3 The "release binary missing --features embeddings" gotcha

`Cargo.toml` had `default = []` for the `embeddings` feature. A plain `cargo build --release` produced a binary with no `leankg embed` subcommand and no vector search. Subagent discovered this when `semantic_search` returned "no vectors". **Fix:** either flip `embeddings` to a default feature, or always build with `--features embeddings` for dev/release.

### 8.4 The "EEXIST on existing .leankg" bug

`leankg index` calls `create_dir` not `create_dir_all`, so re-indexing any tree with an existing `.leankg` dir failed with `EEXIST`. Discovered only at Phase 9 scale (had to APFS-clone workspace-be to bypass). **Fix in v0.20.x:** change `create_dir` → `create_dir_all` or check existence first.

### 8.5 The "DBI container cache lock" contention

Two concurrent `cargo build --release` runs in sibling worktrees both blocked on `~/.cargo/.package-cache` lock. The shared cache is fine; the contention shows up as `Blocking waiting for file lock on package cache` messages. Not a bug, just slow. **Fix:** set `CARGO_BUILD_JOBS=1` in `~/.cargo/config.toml` (already done in LeanKG) and accept the slower sequential builds.

### 8.6 The "stale parent JSONL" confusion

The parent Claude Code session JSONL stopped being appended while subagents kept working. Multiple times the parent agent (this session) reported "no progress" because parent JSONL was quiet, when actually git log + subagent JSONL showed otherwise. **Fix:** never use parent JSONL alone as a source of progress. Always cross-check `git log -1`, plan doc §9, and subagent JSONL tail.

---

## Step 9 — Done criteria

The migration is done when **all** of these are true:

- [ ] All phases ✅ in plan doc §9
- [ ] All sub-tasks (T<N>.<M>) ✅ in plan doc §4
- [ ] No out-of-scope fixes pending (`docs/analysis/*-findings.md` reviewed)
- [ ] All commits pushed to origin/<branch>
- [ ] `cargo test --release` green
- [ ] `cargo clippy --all-targets --all-features -- -D warnings` green
- [ ] `cargo fmt --all -- --check` green
- [ ] `docs/analysis/pg-regression-report.md` exists with PASS/DIFF/FAIL per tool
- [ ] `docs/analysis/pg-perf-large-codebase.md` exists with go/no-go verdict
- [ ] Release PR opened by release-please
- [ ] User has explicitly approved the PR

---

## Appendix A — Tooling

### A.1 The kanban file format (LeanKG convention)

```markdown
# LeanKG: Postgres Migration Kanban

**Last updated:** <ISO timestamp>
**Source of truth:** docs/plan-migrate-cozo-to-postgres-pgvector.md §9
**Worktree:** .claude/worktrees/leankg-pg-migration (branch worktree-leankg-pg-migration)

**Note on staleness:** <explain parent JSONL stops but work continues>

## Status legend
✅ done | 🚧 in progress | ⛔ blocked | ⬜ pending | ⚠️ at risk

## Current focus
- <one or two lines: what's running RIGHT NOW>

## Backlog (pending)
| # | Phase | Status | Notes |
|---|-------|--------|-------|

## Done (history)
| # | Phase | Status | Evidence |
|---|-------|--------|----------|

## Watch-outs
- ⚠️ <caveats the parent agent should remember>
```

### A.2 The per-phase report format (1 page)

```markdown
# Phase <N> Report: <Title>

**Status:** ✅ / 🚧 / ⛔
**Subagent:** <id>
**Branch:** worktree-leankg-pg-migration
**Commits:** <SHAs>

## What shipped
- <file>
- <file>

## Test coverage
- <test>: <pass count> / <total>
- <test>: ...

## Known issues / hand-offs to next phase
1. <bug or gap>
2. <bug or gap>

## Recommended follow-ups (not in this phase's scope)
- ...
```

### A.3 The status-script pattern (cron-friendly)

LeanKG had a `~/.hermes/scripts/leankg-pg-status.py` script that:
- Parses plan doc §9 dynamically (NOT hardcoded list)
- Includes regression test for the parser (`tests/test_phase_parser.py`)
- Returns 4-tuples: `(num, name, status, latest)`
- Has a fallback hardcoded list if the doc is unreachable
- Posts to Telegram via cron every 10 min

Critical pitfall avoided: the regex must extract the leading number from `"N. Name"` cells, NOT require the whole cell to be digits. Test it against a real plan-doc fragment with at least one row.

---

## Appendix B — Anti-patterns (do not do these)

| Anti-pattern | What happens | Fix |
|--------------|--------------|-----|
| Two subagents working the same phase concurrently | Merge conflict + design divergence | Serialize via parent agent dispatch order |
| Subagent commits and doesn't push | Parent thinks no progress | Preamble includes "push after each commit"; parent verifies with `git log origin/<branch>..HEAD` |
| Parent agent edits source files directly | Bypasses the per-subagent safety net | Parent agent is the dispatcher only, not the implementer |
| One subagent owns multiple phases | Can't be recovered if it dies; loses the per-phase atomicity | One phase = one subagent = one commit set |
| Fan-out to a subagent for a 30-min task | Tool overhead exceeds the work | Only fan out phases that take >1 h |
| Reading the parent JSONL to assess progress | Always stale; subagent work is in subagent JSONL | Cross-check git log + plan doc + subagent JSONL + kanban |
| Using `git checkout -b` in main repo | Pollutes the main checkout with uncommitted phase work | Use `git worktree add .claude/worktrees/<feature>/<name>` |
| Targeting production containers for testing | Downtime for the user | Per-subagent preamble bans this in explicit terms |
| Bumping `Cargo.toml` `version` manually | Diverges from release-please; breaks next release | Let release-please do it; never touch the version field |

---

## Appendix C — Checklist (copy/paste for next migration)

```markdown
## Pre-flight
- [ ] Plan doc written with numbered phases, T-tasks, exit criteria per phase
- [ ] Worktree created via `git worktree add`, not `git checkout -b`
- [ ] Dev DB container running with unique name + non-default port
- [ ] Per-subagent "STRICT RULES" preamble written, will append to every fan-out
- [ ] Kanban file created at docs/<goal>-kanban.md with placeholder status
- [ ] User has explicitly waived "no commits without explicit user request" for this goal

## For each phase
- [ ] Parent: write fan-out prompt (1200+ chars, self-sufficient)
- [ ] Parent: dispatch subagent (TaskCreate with description)
- [ ] Subagent: read plan doc + upstream artifacts
- [ ] Subagent: TDD (write test → fail → implement → green)
- [ ] Subagent: commit with conventional-commit message
- [ ] Subagent: push to origin
- [ ] Subagent: write 1-page report at docs/analysis/<phase>-report.md
- [ ] Parent: verify git log + cargo build --release + cargo test --release
- [ ] Parent: update kanban + plan doc §9
- [ ] Parent: if subagent died, fan out recovery worker with handoff context

## Done
- [ ] All phases ✅ in plan doc §9
- [ ] All commits pushed
- [ ] All test suites green
- [ ] Regression report exists
- [ ] Perf report exists (if Phase 9 in scope)
- [ ] Release-please opened the release PR
- [ ] User approved
```

---

## Appendix D — Provenance

This workflow document was derived from:
- **Primary source**: `5d294459-37eb-46dd-8c7f-e6a492f1409d.jsonl` (the active Claude Code session, 2091 lines, Aug 4 08:02 → Aug 5 07:41)
- **Secondary sources**: 14 subagent JSONLs (under `/private/tmp/claude-502/.../tasks/`), plan doc `docs/plan-migrate-cozo-to-postgres-pgvector.md` (437 lines, 9 sections), kanban file `docs/pg-migration-kanban.md`
- **Outcome as of writing**: Phases 0-7 ✅ committed (10 commits ahead of origin), Phase 8 🚧 in flight, Phase 9 mostly done in same worktree (15 KB report exists)

The actual user steering patterns observed:
- 41 real user prompts
- 9 model switches (deepseek-v4-flash variants, deepseek-v4-flash-max, clinepass)
- 1 effort-level change (ultracode = xhigh + dynamic workflow orchestration)
- 11 TaskCreate + 23 TaskUpdate calls
- 14 Agent (subagent) fan-outs
- 8 SendMessage calls (cross-session communication)
- 1 EnterWorktree + 1 TaskStop

The user's intervention points were: **start**, **continue**, **status checks**, **scope adjustments**, **escalation to parallel work**, **release planning**.
