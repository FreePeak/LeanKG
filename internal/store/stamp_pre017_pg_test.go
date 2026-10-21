package store

import (
	"context"
	"os"
	"testing"
)

// openPGPre017Store opens a PostgreSQL store whose schema is the REAL
// pre-017 one: every PG migration up to 016 applied, 017 NOT applied.
//
// The PG migration plan is 1-17 with no gap (016 is the PostgreSQL-only FTS
// step), so 016 is the last version before the text-cap column. The
// fixture is built from the engine's OWN migration DDL — the same statements
// Migrate() runs, in the same order — so it is a genuine pre-017 schema, not
// a hand-edited one.
func openPGPre017Store(t *testing.T) *PGStore {
	t.Helper()
	url := os.Getenv(pgTestURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping PostgreSQL backend tests", pgTestURLEnv)
	}
	b, err := OpenPG(context.Background(), url, t.TempDir(), RW)
	if err != nil {
		t.Fatalf("OpenPG: %v", err)
	}
	s := b.(*PGStore)
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+s.schema+` CASCADE`)
		_ = s.Close()
	})
	// The ledger is the ENGINE'S OWN: pgmigrate.go creates it with
	// `applied_at TEXT NOT NULL`, and pgNow (to_char(...)) yields text, so a
	// TIMESTAMPTZ column would reject the engine's own insert. This fixture
	// must be the schema a real pre-017 store has, or it proves nothing.
	if _, err := s.pool.Exec(pgCtx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	for _, m := range pgMigrations {
		if m.version > 16 {
			continue
		}
		for _, stmt := range m.bestEffort {
			_, _ = s.pool.Exec(pgCtx, stmt)
		}
		tx, err := s.pool.Begin(pgCtx)
		if err != nil {
			t.Fatalf("migration %03d %s: %v", m.version, m.name, err)
		}
		if m.ddl != "" {
			if _, err := tx.Exec(pgCtx, m.ddl); err != nil {
				_ = tx.Rollback(pgCtx)
				t.Fatalf("migration %03d %s: %v", m.version, m.name, err)
			}
		}
		if _, err := tx.Exec(pgCtx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, `+pgNow+`)`,
			m.version, m.name); err != nil {
			_ = tx.Rollback(pgCtx)
			t.Fatalf("record migration %03d: %v", m.version, err)
		}
		if err := tx.Commit(pgCtx); err != nil {
			t.Fatalf("commit migration %03d: %v", m.version, err)
		}
	}
	return s
}

// TestPGStampReadsPre017Store is the PostgreSQL twin of
// TestStampReadsPre017Store: the stamp read survives a store that has not
// run migration 017, and the cap reads as its documented default (0 = "cap
// never recorded") rather than failing the read.
func TestPGStampReadsPre017Store(t *testing.T) {
	s := openPGPre017Store(t)
	var textCapCols int
	if err := s.pool.QueryRow(pgCtx, `SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = 'emb_stamp' AND column_name = 'text_cap'`,
		s.schema).Scan(&textCapCols); err != nil {
		t.Fatalf("probe text_cap: %v", err)
	}
	if textCapCols != 0 {
		t.Fatal("fixture must be pre-017: emb_stamp.text_cap exists")
	}

	want := ModelStamp{
		ModelID: "pg-pre017", Revision: "r1", Dimensions: 8, Distance: "cosine",
		Provider: "local", ChunkerVersion: 3, QueryPrefix: "q: ", DocumentPrefix: "d: ",
	}
	if err := s.WriteStamp(want); err != nil {
		t.Fatalf("write stamp on a pre-017 store: %v", err)
	}
	got, err := s.Stamp(want.ModelID)
	if err != nil {
		t.Fatalf("read stamp on a pre-017 store: %v", err)
	}
	if got == nil {
		t.Fatal("stamp is missing from a store that holds one")
	}
	if got.TextCap != 0 {
		t.Fatalf("TextCap = %d, want 0 (cap never recorded)", got.TextCap)
	}
	if got.ChunkerVersion != want.ChunkerVersion || got.QueryPrefix != want.QueryPrefix ||
		got.DocumentPrefix != want.DocumentPrefix {
		t.Fatalf("stamp identity = %+v, want %+v", got, want)
	}
	all, err := s.Stamps()
	if err != nil {
		t.Fatalf("list stamps on a pre-017 store: %v", err)
	}
	if len(all) != 1 || all[0].TextCap != 0 || all[0].ModelID != want.ModelID {
		t.Fatalf("Stamps() = %+v, want one row with TextCap 0", all)
	}
}

// TestPGStampAfterMigratingPre017Store is the PostgreSQL twin of
// TestStampAfterMigratingPre017Store: the migration adds the cap column, the
// legacy row reads back as "cap never recorded", and a stamp written
// afterwards carries its cap.
func TestPGStampAfterMigratingPre017Store(t *testing.T) {
	s := openPGPre017Store(t)
	legacy := ModelStamp{
		ModelID: "pg-legacy", Revision: "r1", Dimensions: 8, Distance: "cosine",
		Provider: "local", ChunkerVersion: 3,
	}
	if err := s.WriteStamp(legacy); err != nil {
		t.Fatalf("write stamp on a pre-017 store: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate to 017: %v", err)
	}
	got, err := s.Stamp(legacy.ModelID)
	if err != nil {
		t.Fatalf("read stamp after migrating: %v", err)
	}
	if got == nil || got.TextCap != 0 {
		t.Fatalf("legacy stamp = %+v, want TextCap 0 (never recorded)", got)
	}
	fresh := ModelStamp{
		ModelID: "pg-fresh", Revision: "r1", Dimensions: 8, Distance: "cosine",
		Provider: "local", TextCap: 8000,
	}
	if err := s.WriteStamp(fresh); err != nil {
		t.Fatalf("write stamp after migrating: %v", err)
	}
	got, err = s.Stamp(fresh.ModelID)
	if err != nil {
		t.Fatalf("read fresh stamp: %v", err)
	}
	if got == nil || got.TextCap != 8000 {
		t.Fatalf("fresh stamp = %+v, want TextCap 8000", got)
	}
}

// TestPGStoreOpensWithoutEmbStamp pins that a schema with no emb_stamp
// table at all reports the truth: the read fails with PostgreSQL's own
// "relation emb_stamp does not exist", not with a silently empty answer.
func TestPGStoreOpensWithoutEmbStamp(t *testing.T) {
	s := openPGPre017Store(t)
	if _, err := s.pool.Exec(pgCtx, `DROP TABLE emb_stamp`); err != nil {
		t.Fatalf("drop emb_stamp: %v", err)
	}
	if _, err := s.Stamp("whatever"); err == nil {
		t.Fatal("a missing emb_stamp table must fail the stamp read")
	}
}
