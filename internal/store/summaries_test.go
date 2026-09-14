// file_summaries (migration 014) — the pass-1 checkpoint of the LLM-meaning
// pipeline. The contract under test is the resume one: a row is keyed by path
// and carries the content hash and model that produced it, so a reader can tell
// "these exact bytes, this model" from "something changed".
package store

import (
	"strings"
	"testing"
)

func TestSummaryRoundTripAndResumeFields(t *testing.T) {
	s := openTestStore(t)

	if _, ok, err := s.SummaryGet("missing.go"); err != nil || ok {
		t.Fatalf("absent row: ok=%v err=%v", ok, err)
	}
	row := FileSummary{Path: "a.go", ContentHash: "h1", Model: "m1", Summary: "first prose"}
	if err := s.SummaryUpsert(row); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, ok, err := s.SummaryGet("a.go")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ContentHash != "h1" || got.Model != "m1" || got.Summary != "first prose" {
		t.Errorf("round trip: %+v", got)
	}
	if got.UpdatedAt == "" {
		t.Error("updated_at must be stamped by the store")
	}

	// Re-summarizing replaces the row: one row per path, the latest hash wins.
	row.ContentHash, row.Summary = "h2", "second prose"
	if err := s.SummaryUpsert(row); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if err := s.SummaryUpsert(FileSummary{Path: "b.go", ContentHash: "h3", Model: "m1", Summary: "b prose"}); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	all, err := s.SummariesAll()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 || all[0].Path != "a.go" || all[1].Path != "b.go" {
		t.Fatalf("list must be ordered by path: %+v", all)
	}
	if all[0].ContentHash != "h2" || all[0].Summary != "second prose" {
		t.Errorf("replace lost: %+v", all[0])
	}

	// A path is required: a row without one could never be resumed.
	if err := s.SummaryUpsert(FileSummary{Summary: "no path"}); err == nil {
		t.Error("an empty path must be rejected")
	}
}

// TestSummaryTableExistsAfterMigrate pins migration 014: the ledger records the
// version and the table answers a query, on a database whose 1..13 already ran.
func TestSummaryTableExistsAfterMigrate(t *testing.T) {
	s := openTestStore(t)
	var name string
	if err := s.db.QueryRow(`SELECT name FROM schema_migrations WHERE version = 14`).Scan(&name); err != nil {
		t.Fatalf("migration 014 not recorded: %v", err)
	}
	if name != "file-summaries" {
		t.Errorf("migration name: got %q", name)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM file_summaries`).Scan(&n); err != nil {
		t.Fatalf("file_summaries missing: %v", err)
	}
}

// TestPGSummaryRoundTrip mirrors the sqlite round trip against PostgreSQL
// (gated behind LEANKG_TEST_PG_URL like the rest of the PG suite).
func TestPGSummaryRoundTrip(t *testing.T) {
	s := openPGTest(t)
	if err := s.SummaryUpsert(FileSummary{Path: "a.go", ContentHash: "h1", Model: "m1", Summary: "prose"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, ok, err := s.SummaryGet("a.go")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ContentHash != "h1" || got.Model != "m1" || got.Summary != "prose" || got.UpdatedAt == "" {
		t.Errorf("round trip: %+v", got)
	}
	if err := s.SummaryUpsert(FileSummary{Path: "a.go", ContentHash: "h2", Model: "m2", Summary: "prose 2"}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	all, err := s.SummariesAll()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 || all[0].ContentHash != "h2" || all[0].Model != "m2" {
		t.Fatalf("replace: %+v", all)
	}
	var name string
	if err := s.pool.QueryRow(pgCtx, `SELECT name FROM schema_migrations WHERE version = 14`).Scan(&name); err != nil {
		t.Fatalf("migration 014 not recorded: %v", err)
	}
	if !strings.Contains(name, "file-summaries") {
		t.Errorf("migration name: %q", name)
	}
}
