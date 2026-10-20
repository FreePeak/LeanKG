package memory

import (
	"os"
	"testing"
)

// TestRecallRanksByMeaningfulTerms pins RS-18 with the cases the validation
// run reproduced against the token-overlap recall.
func TestRecallRanksByMeaningfulTerms(t *testing.T) {
	m := openTest(t)
	es := []Entry{
		{ID: "pg", Content: "We decided to use Postgres with the pgvector extension for storing embeddings."},
		{ID: "rel", Content: "The deploy pipeline publishes four binaries when the release pull request merges."},
		{ID: "vi", Content: "Quyết định: dùng Postgres để lưu vector nhúng cho tìm kiếm ngữ nghĩa."},
		{ID: "noise", Content: "the the the and of to in is it that"},
	}
	if err := m.RetainRaw("q", es); err != nil {
		t.Fatal(err)
	}
	first := func(q string) string {
		got, err := m.Recall("q", q, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			return ""
		}
		return got[0].ID
	}
	if got := first("embedding"); got != "pg" {
		t.Errorf("stemming: Recall(embedding) top = %q, want pg (embeddings)", got)
	}
	if got := first("tim kiem ngu nghia"); got != "vi" {
		t.Errorf("accent folding: top = %q, want vi", got)
	}
	if got := first("what is the plan"); got == "noise" {
		t.Errorf("stopword-only row outranked content")
	}
	if got := first("which database did we choose for vector storage"); got == "noise" {
		t.Errorf("stopword row won a content query")
	}
	if got := first("release binaries"); got != "rel" {
		t.Errorf("Recall(release binaries) top = %q, want rel", got)
	}
}

// TestIndexRebuildsFromSources pins the derived-index contract (RS-18/RS-20):
// deleting index.db loses nothing — rows and retain cursors come back from
// the JSONL banks and Markdown files.
func TestIndexRebuildsFromSources(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Create("topics/a.md", "kubernetes upgrade notes\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Retain("b", []Entry{{Content: "giraffes at turn three", Metadata: map[string]any{"session_id": "s1"}}}, 3); err != nil {
		t.Fatal(err)
	}
	m.Close()
	for _, f := range []string{"index.db", "index.db-wal", "index.db-shm"} {
		_ = os.Remove(m.Root() + "/" + f)
	}
	m2, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if hits, _ := m2.Search("kubernetes", 5); len(hits) != 1 {
		t.Fatalf("file rows not rebuilt: %v", hits)
	}
	if got, _ := m2.Recall("b", "giraffes", 5); len(got) != 1 {
		t.Fatalf("bank rows not rebuilt: %v", got)
	}
	if c := m2.bankCursor("b"); c != 3 {
		t.Fatalf("bank cursor after rebuild = %d, want 3", c)
	}
	if c, ok := m2.sessionCursor("s1"); !ok || c != 3 {
		t.Fatalf("session cursor after rebuild = %d %v, want 3", c, ok)
	}
	// The rebuilt cursor still gates: a batch at turn 3 is skipped.
	res, err := m2.Retain("b", []Entry{{Content: "dup"}}, 3)
	if err != nil || res.Skipped != 1 {
		t.Fatalf("cursor gate after rebuild: %+v %v", res, err)
	}
}
