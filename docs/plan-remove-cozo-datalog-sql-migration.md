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

### P0 Baseline [x]
- [x] Worktree created, branch `feat/remove-cozo-datalog` (original session).
- [x] Record baseline: `feature/hackathon` @ `03b969c4` — `cargo test --release --lib`
      1195 passed / 0 failed (CARGO_TARGET_DIR=/tmp/opencode/t-w8).

### P1 SQL seam in db layer [x]
- [x] Add `SqlParam`, `SqlRow`, `sql_query`/`sql_query_gucs`/`sql_execute`/
      `sql_execute_batch`/`sql_copy_import` to the backend trait with default
      error impls (tree compiles mid-migration; FakeBackend inherits them).
      Ported from WIP `wip/cozo-sql-seam-backup` @ `dd8018fa`, adapted to the
      current tree (audit-ledger + dashboard trait methods landed since).
- [x] Unit tests for the seam against live-PG scratch schema (TDD):
      RED 7 pass / 8 fail on defaults, GREEN 15/15 after PostgresBackend impl
      (`src/db/sql.rs` tests, probe-gated skip when PG unreachable).
- [x] Keep `run_script` untouched; both paths coexist until P3 (the dual-path
      parity test relies on this).

### P2 Call-site conversion waves (fan-out subagents, one agent per wave)
- [-] W1 **SPLIT for hackathon C5**: wave-1a = `keys.rs` (7 sites) +
      `indexer/content_hash.rs` (2 sites) — DONE (see section 6). The remainder of
      the original W1 (`db/mod.rs` helpers, schema.rs, write_bus.rs) is
      wave-1b, still open.
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

## 6. W8 execution log (hackathon C5, feature/hackathon)

| Item | Status |
|------|--------|
| Plan adopted from dormant WIP | copied from `wip/cozo-sql-seam-backup` @ `dd8018fa`; this section logs deviations. |
| P0 seam commit | `feat(db): SQL-first seam adoption (W8 P0)` — `src/db/sql.rs` (655 ln), trait + PostgresBackend impls in `backend.rs`. |
| Wave-1a converted | `keys.rs`: create/list/revoke/validate -> typed trait methods (`insert_api_key`, `list_api_keys`, `mark_api_key_revoked`, `list_active_api_key_hashes`, `touch_api_key_last_used`). `content_hash.rs`: load/save -> generic seam (`sql_query` / `sql_execute_batch`), signature now takes `&dyn DbBackend` instead of `&GraphEngine`. |
| Parity gating | `tests/pg_sql_wave1_test.rs` (5 tests, #[ignore]-gated): lifecycle, dual-path parity old-Datalog-vs-new-SQL on identical rows, multi-key listing filter, content-hash upsert roundtrip, empty-store read. Dual path possible because the translator still runs during W8. |
| Deviation A | WIP `mod.rs` diff had replaced `pub mod schema;` with `pub mod sql;` — port keeps BOTH modules. |
| Deviation B | WIP's `bind()` helper (Vec of refs built from temporaries) was unsound/uncompilable — dropped; binding goes through `SqlParam::to_pg()` boxed values owned by the caller frame. |
| Deviation C | `SqlRow::text()` fixed to return `None` for `DataValue::Null`/`Bot` (WIP rendered NULL as the literal string "null", which broke every optional-column read; caught by wave-1 integration tests, locked by unit test). |
| Deviation D | Vector binding documented as `$n::text::vector` (matches translator convention; a direct `$n::vector` cast rejects the String bind at ToSql level). JSON binds as `serde_json::Value` (postgres `with-serde_json-1`). |
| Deviation E | `validate_key` no longer DELETE+re-inserts the row (legacy behavior wiped `name` and `created_at` on EVERY successful validation). SQL-first path updates only `last_used_at`; guarded by a lifecycle test assertion. |
| Deviation F | COPY text format: NULL emitted as raw backslash-N marker (escaping it produced a literal two-char string); empty string stays distinct from NULL — regression-tested. |
| Gates (wave-1a) | build --release 0 warnings - cargo test --release --lib green - fmt --check green - clippy CI gate green - pg_schema_test + pg_sql_wave1_test vs remote managed PG (LEANKG_PG_URL via repo .env; never Docker) green. |
| Live proof | `leankg api-key create/list/revoke` against remote PG with `LEANKG_PG_SQL_LOG=1`: `leankg::pg_sql` lines carry `kind="sql"` + plain SQL and NO `cozo=` field for converted ops (old paths always emit `cozo=`). |
