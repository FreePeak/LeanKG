package store

import (
	"path/filepath"
	"testing"
)

// openPre017Store opens a store whose schema is the REAL pre-017 one: every
// sqlite migration up to 015 applied, and 017 NOT applied.
//
// The sqlite migration plan is 1-11, 13, 14, 15, 17 — there is NO sqlite 016
// (fts-identifier-terms is PostgreSQL-only) — so 015 is the last version
// before the text-cap migration. That is the schema a store has when it was
// last written by a binary that predates the text-cap column, and it is what
// a READ-ONLY handle sees today (a read-only handle never migrates, so the
// schema it reads is the one the last writer left behind).
//
// The fixture is built from the engine's OWN migration DDL — the same
// statements Migrate() runs, in the same order — so it is a genuine
// pre-017 store, not a hand-edited one.
func openPre017Store(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	for _, m := range migrations {
		if m.version > 15 {
			continue
		}
		if _, err := s.db.Exec(m.ddl); err != nil {
			t.Fatalf("migration %03d %s: %v", m.version, m.name, err)
		}
		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, '')`,
			m.version, m.name); err != nil {
			t.Fatalf("record migration %03d: %v", m.version, err)
		}
	}
	return s
}

// TestStampReadsPre017Store pins that the stamp READ survives a store that
// has not run migration 017.
//
// The defect: stampSelect named emb_stamp.text_cap unconditionally, so on a
// pre-017 store every stamp read died with "no such column: text_cap". The
// collection was THERE (measured: 2314 vectors in the store) and the L3 rung
// still reported "no vector collection" — a false cause for a rebuild that
// could not have helped.
//
// The read must succeed, and the cap must read as its documented default
// (0 = "cap never recorded"), not as a missing row.
//
// The stamp row is written with the raw pre-017 column set (what the
// pre-017 writer itself wrote), so the READ is what this test proves.
func TestStampReadsPre017Store(t *testing.T) {
	s := openPre017Store(t)
	// The fixture is genuinely pre-017: the column does not exist.
	if _, err := s.db.Exec(`SELECT text_cap FROM emb_stamp`); err == nil {
		t.Fatal("fixture must be pre-017: emb_stamp.text_cap exists")
	}
	// The row a pre-017 writer left behind: no text_cap column to write.
	if _, err := s.db.Exec(`INSERT INTO emb_stamp (model_id, revision, dimensions, distance, provider, chunker_version, query_prefix, document_prefix)
		VALUES ('pre017', 'r1', 8, 'cosine', 'local', 3, 'q: ', 'd: ')`); err != nil {
		t.Fatalf("seed pre-017 stamp row: %v", err)
	}

	got, err := s.Stamp("pre017")
	if err != nil {
		t.Fatalf("read stamp on a pre-017 store: %v", err)
	}
	if got == nil {
		t.Fatal("stamp is missing from a store that holds one")
	}
	if got.TextCap != 0 {
		t.Fatalf("TextCap = %d, want 0 (cap never recorded)", got.TextCap)
	}
	// The identity the store DOES carry still reads back intact.
	if got.ModelID != "pre017" || got.Revision != "r1" || got.Dimensions != 8 ||
		got.Distance != "cosine" || got.Provider != "local" ||
		got.ChunkerVersion != 3 || got.QueryPrefix != "q: " || got.DocumentPrefix != "d: " {
		t.Fatalf("stamp identity = %+v", got)
	}

	all, err := s.Stamps()
	if err != nil {
		t.Fatalf("list stamps on a pre-017 store: %v", err)
	}
	if len(all) != 1 || all[0].TextCap != 0 || all[0].ModelID != "pre017" {
		t.Fatalf("Stamps() = %+v, want one row with TextCap 0", all)
	}
}

// TestWriteStampOnPre017Store pins that a pre-017 store stays WRITABLE: the
// upsert carries only the columns the schema has, so the write does not die
// with "no such column: text_cap" either.
func TestWriteStampOnPre017Store(t *testing.T) {
	s := openPre017Store(t)
	want := ModelStamp{
		ModelID: "pre017", Revision: "r1", Dimensions: 8, Distance: "cosine",
		Provider: "local", ChunkerVersion: 3, QueryPrefix: "q: ", DocumentPrefix: "d: ",
	}
	if err := s.WriteStamp(want); err != nil {
		t.Fatalf("write stamp on a pre-017 store: %v", err)
	}
	got, err := s.Stamp(want.ModelID)
	if err != nil {
		t.Fatalf("read stamp back: %v", err)
	}
	if got == nil || got.TextCap != 0 || got.ChunkerVersion != 3 {
		t.Fatalf("stamp = %+v, want the identity with TextCap 0", got)
	}
}

// TestStampAfterMigratingPre017Store pins that a pre-017 store becomes a
// normal one: the migration adds the cap column, the legacy row reads back
// as "cap never recorded", and a stamp written afterwards carries its cap.
func TestStampAfterMigratingPre017Store(t *testing.T) {
	s := openPre017Store(t)
	legacy := ModelStamp{
		ModelID: "legacy", Revision: "r1", Dimensions: 8, Distance: "cosine",
		Provider: "local", ChunkerVersion: 3,
	}
	if _, err := s.db.Exec(`INSERT INTO emb_stamp (model_id, revision, dimensions, distance, provider, chunker_version, query_prefix, document_prefix)
		VALUES ('legacy', 'r1', 8, 'cosine', 'local', 3, '', '')`); err != nil {
		t.Fatalf("seed pre-017 stamp row: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate to 017: %v", err)
	}
	if _, err := s.db.Exec(`SELECT text_cap FROM emb_stamp`); err != nil {
		t.Fatalf("migration 017 did not add text_cap: %v", err)
	}
	got, err := s.Stamp(legacy.ModelID)
	if err != nil {
		t.Fatalf("read stamp after migrating: %v", err)
	}
	if got == nil || got.TextCap != 0 {
		t.Fatalf("legacy stamp = %+v, want TextCap 0 (never recorded)", got)
	}
	// A stamp written after the migration records its cap.
	fresh := ModelStamp{
		ModelID: "fresh", Revision: "r1", Dimensions: 8, Distance: "cosine",
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
