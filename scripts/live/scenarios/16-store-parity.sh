# Scenario 16 (issues #588 and #592): store-layer parity fixes.
#
# Binary checks (SQLite, run against LEANKG_BIN):
#   (a) [fixed] an update re-inserts the knowledge_fts index row on its entry's
#       rowid, so the triggers can delete by rowid (schema migration 20).
#   (b) [fixed] a store written by the baseline binary (schema 19) migrates to
#       20 on open, realigns its index, and keyword search still answers. Needs
#       LIVE_BASELINE_BIN=<absolute path to the baseline leankg>; SKIP without it.
#   (c) [fixed] a read-only store whose emb_stamp table is absent (a store that
#       was never embedded) reports healthy:true with no embeddings_error (#592).
#   (d) [fixed] the rowid alignment survives `leankg vacuum --full`.
#
# Unit gates (no CLI or HTTP or MCP path reaches these fixes, so the named test
# runs and its PASS line is asserted; see the justification in each label):
#   partial-word keyword recall (SQLite and PostgreSQL), pre-migration-19
#   degraded ranking, the pgvector iterative-scan version gate, rollback of a
#   cancelled migration, the absent emb_stamp read on PostgreSQL, and negative K
#   in SQLite SearchVectors (#588).
#
# PostgreSQL: the PG unit gates run only when LIVE_PG_URL is set, against that
# database (use a private pgvector container, never the CI runner's). Without it
# they SKIP with a reason. They are the only checks that need PostgreSQL.
#
# Environment: LIVE_BASELINE_BIN and LIVE_PG_URL come from run.sh's --baseline-bin
# and --pg-url flags (or the make line). run.sh clears them from the ambient
# environment, so a shell export has no effect.
scenario_main() {
  local fts="$WORK/s16/fts" legacy="$WORK/s16/legacy" st="$WORK/s16/status"

  if ! command -v sqlite3 >/dev/null 2>&1; then
    skip "sqlite3 is not on PATH; the store-level binary checks (a)-(d) need it"
  else
    s16_fts_rowid "$fts"
    s16_migration_legacy "$legacy"
    s16_status_absent_stamp "$st"
  fi

  s16_unit_gates
}

# s16_db DB SQL: one sqlite3 query, printed bare. Read-only, except the DROP that
# s16_status_absent_stamp runs to model a store that was never embedded.
s16_db() {
  sqlite3 -noheader "$1" "$2"
}

# s16_drift DB: index rows whose rowid differs from their entry's rowid.
s16_drift() {
  s16_db "$1" "SELECT COUNT(*) FROM knowledge_fts f JOIN knowledge_entries e ON e.id = f.entry_id WHERE f.rowid <> e.rowid"
}

# s16_write_prd DIR TEXT: a PRD with two requirements; FR-AB-01 carries TEXT.
s16_write_prd() {
  mkdir -p "$1/docs"
  printf '# PRD\n\n## Requirements\n\n- FR-AB-01 %s\n- FR-AB-02 keyword search over the knowledge base\n' "$2" >"$1/docs/prd.md"
}

# s16_fts_rowid DIR: (a) and (d). An update that is not the newest index row
# drifts the baseline's index rowid; the same update must not drift here, and
# a vacuum must not break the alignment.
s16_fts_rowid() {
  local dir=$1 db
  write_fixture "$dir"
  run_cmd fts-index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "[guard] fts store indexes with exit 0" 0 "$RC"
  db="$dir/.leankg/leankg.db"

  run_cmd fts-note "$LEANKG_BIN" note --project "$dir" --target a.go::Alpha \
    --content "socket resume after a dropped transport" --env local
  assert_eq "[guard] note add exits 0" 0 "$RC"

  s16_write_prd "$dir" "socket resume after a dropped transport"
  run_cmd fts-prd-1 "$LEANKG_BIN" prd --source docs/prd.md --project "$dir"
  assert_eq "[guard] first prd import exits 0" 0 "$RC"

  s16_write_prd "$dir" "socket resume after a dropped link"
  run_cmd fts-prd-2 "$LEANKG_BIN" prd --source docs/prd.md --project "$dir"
  assert_eq "[guard] prd re-import with an updated requirement exits 0" 0 "$RC"
  assert_eq "[fixed] index rowids match entry rowids after an update" 0 "$(s16_drift "$db")"

  assert_eq "[guard] keyword search finds the note through the index" 1 \
    "$(s16_db "$db" "SELECT COUNT(*) FROM knowledge_fts JOIN knowledge_entries e ON e.id = knowledge_fts.entry_id WHERE knowledge_fts MATCH 'socket' AND e.id LIKE 'NOTE-%'")"

  run_cmd fts-vacuum "$LEANKG_BIN" vacuum --project "$dir" --full --quiet
  assert_eq "[fixed] index rowids still match entry rowids after leankg vacuum --full" 0 "$(s16_drift "$db")"
}

# s16_migration_legacy DIR: (b). The store is written by LIVE_BASELINE_BIN, so
# it carries the schema-19 layout; the binary under test then opens it.
s16_migration_legacy() {
  local dir=$1 db base=${LIVE_BASELINE_BIN:-}
  if [ -z "$base" ] || [ ! -x "$base" ]; then
    skip "LIVE_BASELINE_BIN is not an executable; the legacy-store migration check (b) needs the baseline binary"
    return 0
  fi
  write_fixture "$dir"
  db="$dir/.leankg/leankg.db"
  run_cmd legacy-index "$base" index "$dir" --no-embed
  assert_eq "[guard] baseline indexes the legacy store with exit 0" 0 "$RC"
  run_cmd legacy-note "$base" note --project "$dir" --target a.go::Alpha \
    --content "socket resume after a dropped transport" --env local
  assert_eq "[guard] baseline note add exits 0" 0 "$RC"
  s16_write_prd "$dir" "socket resume after a dropped transport"
  run_cmd legacy-prd-1 "$base" prd --source docs/prd.md --project "$dir"
  s16_write_prd "$dir" "socket resume after a dropped link"
  run_cmd legacy-prd-2 "$base" prd --source docs/prd.md --project "$dir"
  assert_eq "[guard] baseline store is at schema 19 before the upgrade" 19 \
    "$(s16_db "$db" "SELECT MAX(version) FROM schema_migrations")"

  run_cmd legacy-open "$LEANKG_BIN" prd --source docs/prd.md --project "$dir"
  assert_eq "[fixed] the binary under test opens the legacy store with exit 0 and migrates it to schema 20" 20 \
    "$(s16_db "$db" "SELECT MAX(version) FROM schema_migrations")"
  assert_eq "[fixed] legacy index rowids match entry rowids after the upgrade" 0 "$(s16_drift "$db")"
  assert_eq "[guard] keyword search over the legacy store still finds the note" 1 \
    "$(s16_db "$db" "SELECT COUNT(*) FROM knowledge_fts JOIN knowledge_entries e ON e.id = knowledge_fts.entry_id WHERE knowledge_fts MATCH 'socket' AND e.id LIKE 'NOTE-%'")"
}

# s16_status_absent_stamp DIR: (c). A store that was never embedded has no
# emb_stamp rows; the table itself is dropped here to model the read-only case.
s16_status_absent_stamp() {
  local dir=$1 db
  write_fixture "$dir"
  db="$dir/.leankg/leankg.db"
  run_cmd status-index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "[guard] status store indexes with exit 0" 0 "$RC"
  if ! s16_db "$db" "DROP TABLE emb_stamp" >/dev/null 2>&1; then
    skip "could not drop emb_stamp from the status store; check (c) cannot run"
    return 0
  fi

  run_cmd status-absent-stamp "$LEANKG_BIN" status --project "$dir"
  assert_eq "[guard] status exits 0 when emb_stamp is absent" 0 "$RC"
  assert_json "[fixed] status of a never-embedded store is healthy" '.healthy' true "$OUT"
  assert_json "[fixed] status reports no embeddings_error for an absent emb_stamp" \
    '.embeddings_error // "none"' none "$OUT"
  assert_json "[guard] status lists no embeddings for an absent emb_stamp" '.embeddings | length' 0 "$OUT"
  assert_eq "[guard] status is read-only: emb_stamp is still absent afterwards" 0 \
    "$(s16_db "$db" "SELECT COUNT(*) FROM sqlite_master WHERE name = 'emb_stamp'")"
}

s16_unit_gates() {
  # Negative K has no CLI or MCP path: core clamps the semantic limit to at
  # least 10 and the hybrid window to at least 50, so SearchVectors only sees a
  # negative K from direct store callers. The #588 fix is pinned by this test.
  unit_gate ./internal/store/ TestSearchVectorsNegativeKReturnsEmpty "#588 negative K in SQLite SearchVectors does not panic"

  # Pre-migration-19 read-only store: no CLI verb reaches KnowledgeEntriesSearch
  # (orgknowledge.SearchAnnotations has no caller), so the store test drives it.
  unit_gate ./internal/store/ TestKnowledgeSearchPreMigration19ReportsDegradedRanking "#588 item 3 pre-19 read-only store reports degraded ranking"

  # Partial-word recall on SQLite, same reason: no CLI or MCP path to knowledge
  # keyword search. The shared conformance check runs on both engines.
  unit_gate ./internal/store/ TestKnowledgeKeywordSearchSQLite "#588 item 1 partial-word keyword recall, SQLite"

  # Version gate: pure function, no PostgreSQL needed.
  unit_gate ./internal/store/ TestPGVectorHasIterativeScan "#588 item 4 iterative-scan version gate (pure)"

  if [ -z "${LIVE_PG_URL:-}" ]; then
    skip "LIVE_PG_URL is not set; the PostgreSQL unit gates (partial-word parity, iterative-scan session setting, cancelled-migration rollback, absent emb_stamp) need a private pgvector database"
    return 0
  fi
  unit_gate ./internal/store/ TestPGKnowledgeKeywordSearch "#588 item 1 partial-word keyword recall, PostgreSQL parity" pg
  unit_gate ./internal/store/ TestPGIterativeScanFollowsInstalledVector "#588 item 4 iterative_scan follows the installed pgvector" pg
  unit_gate ./internal/store/ TestPGMigrateRollsBackWhenCtxCancelled "#588 item 5 cancelled migration leaves no open transaction" pg
  unit_gate ./internal/store/ TestPGStoreOpensWithoutEmbStamp "#592 absent emb_stamp reads empty on PostgreSQL" pg
}
