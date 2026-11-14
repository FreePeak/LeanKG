package store

import "testing"

// A store written before migration 19 has knowledge rows and no knowledge_fts
// index. Migrate must backfill the existing rows, and a second Migrate must
// neither re-run the step nor duplicate index rows.
func TestKnowledgeFTSBackfillsExistingRows(t *testing.T) {
	s := openTestStore(t)
	for _, e := range []KnowledgeEntry{
		{ID: "K-1", KnowledgeType: "note", Title: "Socket resume", Content: "after a dropped transport", Environment: "local", Author: "a", CreatedAt: 1, UpdatedAt: 10},
		{ID: "K-2", KnowledgeType: "note", Title: "Other", Content: "resume later", Environment: "local", Author: "a", CreatedAt: 2, UpdatedAt: 20},
	} {
		if err := s.KnowledgeEntryUpsert(e); err != nil {
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

	if err := s.Migrate(); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if n := knowledgeFTSRows(t, s); n != 2 {
		t.Fatalf("knowledge_fts rows after backfill = %d; want 2", n)
	}
	hits, err := s.KnowledgeEntriesSearch("socket resume", "", "", 10)
	if err != nil || len(hits) != 1 || hits[0].ID != "K-1" {
		t.Fatalf("search after backfill = %+v, %v; want K-1", hits, err)
	}

	if err := s.Migrate(); err != nil {
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
