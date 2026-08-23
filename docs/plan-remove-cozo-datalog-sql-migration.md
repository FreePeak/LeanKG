# Plan: Remove Cozo/Datalog — All Queries Become Plain PostgreSQL

> **Adopted (Hackathon C5 / W8):** this plan was authored in the dormant WIP
> (`wip/cozo-sql-seam-backup` @ `dd8018fa`, worktree
> `/Users/linh.doan/orca/workspaces/cozo-removal`) and is now the working plan
> on `feature/hackathon`. Waves are executed here; deviations from the
> original text are logged in §6 "W8 execution log" below.

Worktree: `/Users/linh.doan/orca/workspaces/cozo-removal` (branch `feat/remove-cozo-datalog`, base `541ff626`).
Status legend: `[ ]` todo, `[-]` in progress, `[x]` done.

## 1. Problem

Storage already lives in Postgres (Phase 8), but every query is authored as a
Datalog script string, passed to `DbBackend::run_script()`, parsed and
translated to SQL at runtime by `src/db/pg/translate.rs` (~4,300 lines). The
in-memory test fake re-implements a Datalog interpreter (`src/db/fake.rs`,
~1,400 lines). This keeps ~5,700 lines of dead-weight translator machinery,
a legacy value model (`DataValue`/`NamedRows`, cozo-shaped), and injection-prone
string-built scripts.

## 2. Measured surface (2026-08-21 audit)

| Area | Sites | Files |
|------|-------|-------|
| graph engine | 105 | `src/graph/query.rs` (+ nl_query, inventory 4, clustering 3) |
| db helpers | 46 | `src/db/mod.rs` |
| db layer | ~40 | `backend.rs` 35, `keys.rs` 7, `schema.rs` 2, `write_bus.rs` 1 |
| embeddings | 22 | `state.rs` 15, `build.rs` 6, `control.rs` 1 |
| auth | 15 | `accounts.rs` 9, `tokens.rs` 6 |
| ontology | 8+ | `ontology/query.rs` 8, `sync.rs` |
| mcp + cli | ~10 | `handler.rs` 4, `tracking_db.rs` 2, `main.rs` 2 |
| misc | ~7 | `doc_indexer/paths.rs` 2, `retrieval/pipeline.rs` 1, `indexer/mod.rs` 1, others |
| integration tests | 16 files | use `run_script`/`FakeBackend` directly |
| docs mentions | ~271 across 30+ files | README, architecture, erd, mcp-tools, dated analyses |

Cargo.toml/Cargo.lock: already free of cozo/rocksdb deps. Removal is purely
the query layer + types + naming/docs.

## 3. Target design (decided)

### New SQL-first backend seam

```rust
pub enum SqlParam { Null, Bool, Int(i64), Float(f64), Text(String),
                    Bytes(Vec<u8>), Json(serde_json::Value), Vector(Vec<f32>) }

pub struct SqlRow { pub cols: Vec<(String, DataValue)> }   // reuse DataValue as the cell type initially; rename later
// or typed accessors: row.get::<str>("name"), row.int(0) ...

pub trait DbBackend: Send + Sync {
    fn query(&self, sql: &str, params: &[SqlParam]) -> Result<Vec<SqlRow>>;
    fn execute(&self, sql: &str, params: &[SqlParam]) -> Result<u64>;
    fn transaction<T>(&self, f: impl FnOnce(&dyn SqlTx) -> Result<T>) -> Result<T>;
    fn copy_import(&self, table: &str, columns: &[String], rows: Vec<Vec<SqlParam>>);
}
```

Decisions:
- **Keep `DataValue` as the cell type** through the migration (call sites
  already consume `.get_str()/.get_int()`); rename module later to `row.rs`
  semantics without changing call-site code twice. Drop `Bot`, `next`
  pagination chain on `NamedRows` at the end.
- **Delete, not wrap**: `translate.rs`, `mutability.rs`, Datalog parser paths,
  `run_script`/`submit_write`/`import_relations`/`submit_import`/
  `mutability_for` all leave the trait once the last caller converts.
- **FakeBackend dies**: tests move to live-PG scratch schemas behind the
  existing `test_pg_available()` probe (skip-not-fail when docker is down).
  Local PG docker (`leankg-pg-phase0`, :5433) is mandatory per project rules.
- Write bus (`write_bus.rs`) re-targets from datalog scripts to
  `(sql, params)` work items.

### TDD protocol (every phase)

1. RED: characterization test against live-PG scratch schema asserting current
   behavior of the target function (or parity via existing
   `tests/pg_translate_parity_test.rs` harness while it exists).
2. GREEN: convert call site(s) to parameterized SQL; test stays green.
3. REFACTOR: drop the now-unused helper/script constants.
4. Gate per wave: `cargo build --release && cargo test` green before merge of
   that wave's commit.

## 4. Phases

### P0 Baseline [-]
- [x] Worktree created, branch `feat/remove-cozo-datalog`.
- [ ] Record baseline: `cargo build --release` + `cargo test` results.

### P1 SQL seam in db layer [ ]
- [ ] Add `SqlParam`, `SqlRow`, `query/execute/transaction/copy_import` to
      `PostgresBackend` (trait methods with default error impls so the tree
      compiles mid-migration).
- [ ] Unit tests for the seam against live-PG scratch schema (TDD).
- [ ] Keep `run_script` delegating to nothing new; both paths coexist until P3.

### P2 Call-site conversion waves (fan-out subagents, one agent per wave)
- [ ] W1 `src/db/mod.rs` helpers (46) + schema.rs + keys.rs + write_bus.rs
- [ ] W2 `src/graph/query.rs` part A (read/query shapes)
- [ ] W3 `src/graph/query.rs` part B (writes/aggregation/vector) +
      inventory.rs + clustering.rs + nl_query/l1_cache if touched
- [ ] W4 embeddings state/build/control + retrieval/pipeline.rs
- [ ] W5 auth tokens/accounts + ontology query/sync
- [ ] W6 mcp handler/tracking_db/main/indexer/doc_indexer/pack
- [ ] Each wave: tests first (characterization), then convert, then gate.

Wave ordering rationale: W1 converts the shared helpers everything calls;
W2-W6 are leaf domains that can run in parallel once W1 lands.

### P3 Deletion sweep [ ]
- [ ] Delete `src/db/pg/translate.rs`, `src/db/pg/mutability.rs`,
      `cozo_to_pg`, legacy script plumbing in `backend.rs`.
- [ ] Shrink `value.rs`: drop `Bot`, `NamedRows::next`; rename to honest
      names (`SqlRow` etc.).
- [ ] Delete `src/db/fake.rs`; migrate its test users to live-PG scratch
      schema pattern (probe-gated).
- [ ] Trait reduced to `query/execute/transaction/copy_import/redacted_url/
      is_read_only`.
- [ ] Integration tests: rewrite direct `run_script` usage in the 16 test
      files to SQL.
- [ ] Docs sweep: update living docs (README, CLAUDE.md, docs/architecture.md,
      erd.md, mcp-tools.md, tech-stack.md, index-embed-flow.md ...); delete
      obsolete cozo migration planning docs; CHANGELOG history entries stay
      verbatim (historical record).
- [ ] Regenerate or patch UI bundle mention (`src/embed/assets/index-*.js`).

### P4 Final verification [ ]
- [ ] `grep -ri "cozo\|datalog" src/ tests/ Cargo.toml` → zero hits
      (excluding CHANGELOG).
- [ ] `cargo build --release` clean; `cargo clippy` no new warnings.
- [ ] `cargo test` with local PG up: fully green; without PG: skips only.
- [ ] Smoke: `init` + `index ./src` + one MCP tool query against :5433.

## 5. Risks

| Risk | Mitigation |
|------|------------|
| Recursive/transitive graph queries in query.rs have no trivial SQL form | Use recursive CTEs; parity-test against old path before deleting translator |
| FakeBackend-dependent unit tests lose no-DB property | Scratch-schema + probe-skip pattern is established; docker PG is mandatory locally anyway |
| Hidden datalog built dynamically (string interpolation) | Wave agents must grep their files for format!/push_str into scripts; flag any to orchestrator |
| Concurrent subagent edits conflict | Waves own disjoint file sets; only shared file is backend seam (P1, done first, by orchestrator) |
