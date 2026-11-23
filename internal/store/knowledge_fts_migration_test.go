package store

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"strings"
	"testing"
)

// A store written before migration 19 has knowledge rows and no knowledge_fts
// index. Migrate must backfill the existing rows, and a second Migrate must
// neither re-run the step nor duplicate index rows.
func TestKnowledgeFTSBackfillsExistingRows(t *testing.T) {
	s := openTestStore(t)
	for _, e := range []KnowledgeEntry{
		{ID: "K-1", KnowledgeType: "note", Title: "Socket resume", Content: "after a dropped transport", Environment: "local", Author: "a", CreatedAt: 1, UpdatedAt: 10},
		{ID: "K-2", KnowledgeType: "note", Title: "Other", Content: "resume later", Environment: "local", Author: "a", CreatedAt: 2, UpdatedAt: 20},
	} {
		if err := s.KnowledgeEntryUpsert(context.Background(), e); err != nil {
			t.Fatalf("upsert %s: %v", e.ID, err)
		}
	}
	// Recreate the pre-19 shape: no index, and no ledger row for version 19.
	for _, stmt := range []string{
		`DROP TABLE knowledge_fts`,
		`DELETE FROM schema_migrations WHERE version = 19`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if n := knowledgeFTSRows(t, s); n != 2 {
		t.Fatalf("knowledge_fts rows after backfill = %d; want 2", n)
	}
	hits, err := s.KnowledgeEntriesSearch(context.Background(), "socket resume", "", "", 10)
	if err != nil || len(hits) != 1 || hits[0].ID != "K-1" {
		t.Fatalf("search after backfill = %+v, %v; want K-1", hits, err)
	}

	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if n := knowledgeFTSRows(t, s); n != 2 {
		t.Fatalf("knowledge_fts rows after second Migrate = %d; want 2 (no duplicates)", n)
	}
	var ledger int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 19`).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if ledger != 1 {
		t.Fatalf("schema_migrations rows for version 19 = %d; want 1", ledger)
	}
}

func knowledgeFTSRows(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM knowledge_fts`).Scan(&n); err != nil {
		t.Fatalf("count knowledge_fts: %v", err)
	}
	return n
}

// The index row of an entry must carry the entry's own rowid: the triggers
// delete by rowid, so an index row that drifted from it would be missed by the
// delete and left behind. An update re-inserts the index row, so it is the
// write that exposes a drift.
func TestKnowledgeFTSRowidTracksEntryRowid(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, e := range []KnowledgeEntry{
		{ID: "K-1", KnowledgeType: "note", Title: "Socket resume", Content: "after a dropped transport", Environment: "local", Author: "a", CreatedAt: 1, UpdatedAt: 10},
		{ID: "K-2", KnowledgeType: "note", Title: "Other", Content: "resume later", Environment: "local", Author: "a", CreatedAt: 2, UpdatedAt: 20},
	} {
		if err := s.KnowledgeEntryUpsert(ctx, e); err != nil {
			t.Fatalf("upsert %s: %v", e.ID, err)
		}
	}
	if err := s.KnowledgeEntryUpsert(ctx, KnowledgeEntry{ID: "K-1", KnowledgeType: "note", Title: "Socket resume", Content: "after a dropped link", Environment: "local", Author: "a", CreatedAt: 1, UpdatedAt: 30}); err != nil {
		t.Fatalf("re-upsert K-1: %v", err)
	}
	if n := knowledgeFTSRowidDrift(t, s); n != 0 {
		t.Fatalf("knowledge_fts rows whose rowid differs from their entry's rowid = %d; want 0", n)
	}
	hits, err := s.KnowledgeEntriesSearch(ctx, "dropped link", "", "", 10)
	if err != nil || len(hits) != 1 || hits[0].ID != "K-1" {
		t.Fatalf("search after update = %+v, %v; want K-1 once", hits, err)
	}
	if err := s.KnowledgeEntryDelete(ctx, "K-1"); err != nil {
		t.Fatalf("delete K-1: %v", err)
	}
	if n := knowledgeFTSRows(t, s); n != 1 {
		t.Fatalf("knowledge_fts rows after delete = %d; want 1", n)
	}
}

// A store whose index rows were written with drifted rowids (the layout the
// migration-19 triggers leave behind) is rebuilt by migration 20: afterwards
// every index row sits on its entry's rowid and search still answers.
func TestKnowledgeFTSRebuildRealignsRowids(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, e := range []KnowledgeEntry{
		{ID: "K-1", KnowledgeType: "note", Title: "Socket resume", Content: "after a dropped transport", Environment: "local", Author: "a", CreatedAt: 1, UpdatedAt: 10},
		{ID: "K-2", KnowledgeType: "note", Title: "Other", Content: "resume later", Environment: "local", Author: "a", CreatedAt: 2, UpdatedAt: 20},
	} {
		if err := s.KnowledgeEntryUpsert(ctx, e); err != nil {
			t.Fatalf("upsert %s: %v", e.ID, err)
		}
	}
	driftKnowledgeFTS(t, s)
	// Drop the version-20 ledger row to make Migrate apply the rebuild.
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version = 20`); err != nil {
		t.Fatalf("drop version-20 ledger row: %v", err)
	}
	if n := knowledgeFTSRowidDrift(t, s); n == 0 {
		t.Fatal("fixture did not drift: the rebuild would prove nothing")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if n := knowledgeFTSRowidDrift(t, s); n != 0 {
		t.Fatalf("rowid drift after migration 20 = %d; want 0", n)
	}
	if n := knowledgeFTSRows(t, s); n != 2 {
		t.Fatalf("knowledge_fts rows after rebuild = %d; want 2", n)
	}
	hits, err := s.KnowledgeEntriesSearch(ctx, "socket resume", "", "", 10)
	if err != nil || len(hits) != 1 || hits[0].ID != "K-1" {
		t.Fatalf("search after rebuild = %+v, %v; want K-1", hits, err)
	}
}

// A read-only store that predates migration 19 has no index. Its keyword
// search still answers from the substring arm, but the ranking it lost must
// be reported, not discarded.
func TestKnowledgeSearchPreMigration19ReportsDegradedRanking(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), ".leankg", "leankg.db")
	rw, err := openSQLite(ctx, path, RW)
	if err != nil {
		t.Fatalf("open RW: %v", err)
	}
	if err := rw.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := rw.KnowledgeEntryUpsert(ctx, KnowledgeEntry{ID: "K-1", KnowledgeType: "note", Title: "Socket resume", Content: "after a dropped transport", Environment: "local", Author: "a", CreatedAt: 1, UpdatedAt: 10}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// The pre-19 shape: no index, its triggers, and no ledger rows from 19 on.
	for _, stmt := range []string{
		`DROP TRIGGER IF EXISTS knowledge_fts_insert`,
		`DROP TRIGGER IF EXISTS knowledge_fts_update`,
		`DROP TRIGGER IF EXISTS knowledge_fts_delete`,
		`DROP TABLE knowledge_fts`,
		`DELETE FROM schema_migrations WHERE version >= 19`,
	} {
		if _, err := rw.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("close RW: %v", err)
	}

	ro, err := openSQLite(ctx, path, RO)
	if err != nil {
		t.Fatalf("open RO: %v", err)
	}
	defer ro.Close()
	var logged bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(prev) })
	hits, err := ro.KnowledgeEntriesSearch(ctx, "socket", "", "", 10)
	if err != nil {
		t.Fatalf("degraded search error = %v; want nil: the hits are still complete", err)
	}
	if len(hits) != 1 || hits[0].ID != "K-1" {
		t.Fatalf("degraded search hits = %+v; want the substring match K-1", hits)
	}
	if !strings.Contains(logged.String(), "keyword ranking degraded") {
		t.Fatalf("no degraded-ranking warning logged; log = %q", logged.String())
	}
}

// driftKnowledgeFTS rewrites the index with its rows inserted in reverse entry
// order, so every index row carries the rowid of the other entry.
func driftKnowledgeFTS(t *testing.T, s *Store) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM knowledge_fts`,
		`INSERT INTO knowledge_fts (entry_id, title, content)
			SELECT id, title, content FROM knowledge_entries ORDER BY id DESC`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// seedKnowledgeFTSPair writes the two entries the VACUUM tests search over.
func seedKnowledgeFTSPair(t *testing.T, s *Store) {
	t.Helper()
	for _, e := range []KnowledgeEntry{
		{ID: "K-1", KnowledgeType: "note", Title: "Socket resume", Content: "after a dropped transport", Environment: "local", Author: "a", CreatedAt: 1, UpdatedAt: 10},
		{ID: "K-2", KnowledgeType: "note", Title: "Other", Content: "resume later", Environment: "local", Author: "a", CreatedAt: 2, UpdatedAt: 20},
	} {
		if err := s.KnowledgeEntryUpsert(context.Background(), e); err != nil {
			t.Fatalf("upsert %s: %v", e.ID, err)
		}
	}
}

// VACUUM may renumber the rowids of tables without an INTEGER PRIMARY KEY, and
// the index rowids must match their entries'. Every VACUUM path must realign
// the index afterwards. The drifted index stands in for a renumbered VACUUM:
// without the realign the drift survives the pass.
func TestVacuumPathsRealignKnowledgeFTS(t *testing.T) {
	ops := []MaintainOp{{Kind: MaintainUpgrade}, {Kind: MaintainReclaimFull}}
	for _, op := range ops {
		t.Run(string(op.Kind), func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()
			seedKnowledgeFTSPair(t, s)
			driftKnowledgeFTS(t, s)
			if n := knowledgeFTSRowidDrift(t, s); n == 0 {
				t.Fatal("fixture did not drift: the realign would prove nothing")
			}
			if _, err := s.Maintain(ctx, op); err != nil {
				t.Fatalf("Maintain(%s): %v", op.Kind, err)
			}
			if n := knowledgeFTSRowidDrift(t, s); n != 0 {
				t.Fatalf("rowid drift after %s = %d; want 0", op.Kind, n)
			}
			if n := knowledgeFTSRows(t, s); n != 2 {
				t.Fatalf("knowledge_fts rows after %s = %d; want 2", op.Kind, n)
			}
			hits, err := s.KnowledgeEntriesSearch(ctx, "resume later", "", "", 10)
			if err != nil || len(hits) == 0 || hits[0].ID != "K-2" {
				t.Fatalf("search after %s = %+v, %v; want K-2 first", op.Kind, hits, err)
			}
		})
	}
}

// UpgradePageSize rewrites the store through its own handle, so its VACUUM is
// a separate path that needs the same realign.
func TestUpgradePageSizeRealignsKnowledgeFTS(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), ".leankg", "leankg.db")
	rw, err := openSQLite(ctx, path, RW)
	if err != nil {
		t.Fatalf("open RW: %v", err)
	}
	if err := rw.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	seedKnowledgeFTSPair(t, rw)
	driftKnowledgeFTS(t, rw)
	if err := rw.Close(); err != nil {
		t.Fatalf("close RW: %v", err)
	}

	if _, err := UpgradePageSize(ctx, path, 4096); err != nil {
		t.Fatalf("UpgradePageSize: %v", err)
	}
	after, err := openSQLite(ctx, path, RW)
	if err != nil {
		t.Fatalf("reopen RW: %v", err)
	}
	defer after.Close()
	if n := knowledgeFTSRowidDrift(t, after); n != 0 {
		t.Fatalf("rowid drift after page-size rewrite = %d; want 0", n)
	}
}

// knowledgeFTSRowidDrift counts index rows whose rowid is not the rowid of
// the entry they index.
func knowledgeFTSRowidDrift(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM knowledge_fts f
		JOIN knowledge_entries e ON e.id = f.entry_id WHERE f.rowid <> e.rowid`).Scan(&n); err != nil {
		t.Fatalf("count rowid drift: %v", err)
	}
	return n
}
