package store

import (
	"context"
	"reflect"
	"testing"
)

// knowledgeKeywordStore is the surface the knowledge keyword conformance check
// needs from either engine.
type knowledgeKeywordStore interface {
	KnowledgeEntryUpsert(ctx context.Context, e KnowledgeEntry) error
	KnowledgeEntryDelete(ctx context.Context, id string) error
	KnowledgeEntriesSearch(ctx context.Context, query, knowledgeType, environment string, limit int) ([]KnowledgeEntry, error)
}

// checkKnowledgeKeywordSearch asserts the keyword contract both engines share:
// every query term must appear, hits rank by keyword strength rather than
// recency, a query with no terms falls back to substring recall, and the index
// follows upserts and deletes.
func checkKnowledgeKeywordSearch(t *testing.T, s knowledgeKeywordStore) {
	t.Helper()
	ctx := context.Background()
	for _, e := range []KnowledgeEntry{
		{ID: "K-old", KnowledgeType: "note", Title: "Socket resume", Content: "socket resume after a dropped transport", Environment: "production", Author: "a", CreatedAt: 1, UpdatedAt: 100},
		{ID: "K-mid", KnowledgeType: "note", Title: "Resume link", Content: "resume link, then resume the link again", Environment: "production", Author: "a", CreatedAt: 2, UpdatedAt: 150},
		{ID: "K-new", KnowledgeType: "note", Title: "Reconnect notes", Content: "resume the session once the link is back", Environment: "production", Author: "a", CreatedAt: 3, UpdatedAt: 200},
		{ID: "K-cpp", KnowledgeType: "general", Title: "C++ build flags", Content: "use c++ flags for the go cgo build", Environment: "local", Author: "a", CreatedAt: 4, UpdatedAt: 300},
	} {
		if err := s.KnowledgeEntryUpsert(ctx, e); err != nil {
			t.Fatalf("upsert %s: %v", e.ID, err)
		}
	}
	expectIDs := func(label string, got []KnowledgeEntry, err error, want ...string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		var ids []string
		for _, e := range got {
			ids = append(ids, e.ID)
		}
		if !reflect.DeepEqual(ids, want) && !(len(ids) == 0 && len(want) == 0) {
			t.Fatalf("%s = %v; want %v", label, ids, want)
		}
	}

	// Every term must appear: K-new and K-mid lack "socket", so only K-old.
	hits, err := s.KnowledgeEntriesSearch(ctx, "socket resume", "", "", 10)
	expectIDs(`"socket resume"`, hits, err, "K-old")

	// A partial word is no index term, so only the substring arm finds it:
	// both engines must recall K-old for "sock" (issue #588).
	hits, err = s.KnowledgeEntriesSearch(ctx, "sock", "", "", 10)
	expectIDs(`"sock" partial word`, hits, err, "K-old")

	// Ranked, not newest-first: K-new is the newest hit but K-mid carries both
	// terms more often, so K-mid comes first. K-old lacks "link" and is excluded.
	hits, err = s.KnowledgeEntriesSearch(ctx, "resume link", "", "", 10)
	expectIDs(`"resume link"`, hits, err, "K-mid", "K-new")

	hits, err = s.KnowledgeEntriesSearch(ctx, "resume link", "", "", 1)
	expectIDs(`"resume link" limit 1`, hits, err, "K-mid")

	// A query with no terms ("++") is substring recall on both engines.
	hits, err = s.KnowledgeEntriesSearch(ctx, "++", "", "", 10)
	expectIDs(`"++" substring fallback`, hits, err, "K-cpp")

	// Filters narrow the ranked hits.
	hits, err = s.KnowledgeEntriesSearch(ctx, "resume link", "note", "production", 10)
	expectIDs(`"resume link" type+env`, hits, err, "K-mid", "K-new")
	hits, err = s.KnowledgeEntriesSearch(ctx, "resume link", "general", "", 10)
	expectIDs(`"resume link" type filter`, hits, err)

	// The index follows an upsert: K-mid loses "link", so it stops matching.
	if err := s.KnowledgeEntryUpsert(ctx, KnowledgeEntry{ID: "K-mid", KnowledgeType: "note", Title: "Resume", Content: "resume only now", Environment: "production", Author: "a", CreatedAt: 2, UpdatedAt: 150}); err != nil {
		t.Fatalf("re-upsert K-mid: %v", err)
	}
	hits, err = s.KnowledgeEntriesSearch(ctx, "resume link", "", "", 10)
	expectIDs(`"resume link" after upsert`, hits, err, "K-new")

	// The index follows a delete.
	if err := s.KnowledgeEntryDelete(ctx, "K-new"); err != nil {
		t.Fatalf("delete K-new: %v", err)
	}
	hits, err = s.KnowledgeEntriesSearch(ctx, "resume link", "", "", 10)
	expectIDs(`"resume link" after delete`, hits, err)
}

func TestKnowledgeKeywordSearchSQLite(t *testing.T) {
	checkKnowledgeKeywordSearch(t, openTestStore(t))
}

func TestPGKnowledgeKeywordSearch(t *testing.T) {
	checkKnowledgeKeywordSearch(t, openPGTest(t))
}
