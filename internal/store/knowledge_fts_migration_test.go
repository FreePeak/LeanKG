package store

import (
	"context"
	"errors"
	"path/filepath"
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
	// Reverse the index insertion order so its rowids no longer match the
	// entries', then drop the version-20 ledger row to make Migrate apply it.
	for _, stmt := range []string{
		`DELETE FROM knowledge_fts`,
		`INSERT INTO knowledge_fts (entry_id, title, content)
			SELECT id, title, content FROM knowledge_entries ORDER BY id DESC`,
		`DELETE FROM schema_migrations WHERE version = 20`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
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
	hits, err := ro.KnowledgeEntriesSearch(ctx, "socket", "", "", 10)
	if !errors.Is(err, ErrRankingDegraded) {
		t.Fatalf("search error = %v; want ErrRankingDegraded", err)
	}
	if len(hits) != 1 || hits[0].ID != "K-1" {
		t.Fatalf("degraded search hits = %+v; want the substring match K-1", hits)
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
