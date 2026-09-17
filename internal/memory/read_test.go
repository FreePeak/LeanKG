package memory

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBankReadSurface covers the K3/K4 read routes (read.go): stats, by-id,
// list, and bank enumeration. One table drives every case; the fixture is
// three rows whose timestamps put them in a known order.
func TestBankReadSurface(t *testing.T) {
	m := openTest(t)
	bank := "demo"
	if err := m.RetainRaw(bank, []Entry{
		{ID: "a", Content: "first row", Timestamp: 100},
		{ID: "b", Content: "second row", Timestamp: 300,
			Metadata: map[string]any{"document_id": "doc-b", "tags": []any{"project:x"}}},
		{ID: "c", Content: "third row", Timestamp: 200},
	}); err != nil {
		t.Fatalf("RetainRaw: %v", err)
	}
	// A second bank, so the root-level counters have something to count.
	if err := m.RetainRaw("other", []Entry{{ID: "z", Content: "elsewhere", Timestamp: 50}}); err != nil {
		t.Fatalf("RetainRaw(other): %v", err)
	}

	t.Run("stats", func(t *testing.T) {
		got, err := m.Stats(bank)
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if got.Entries != 3 {
			t.Errorf("Entries = %d, want 3", got.Entries)
		}
		if got.LastRetain != 300 {
			t.Errorf("LastRetain = %d, want 300 (newest row)", got.LastRetain)
		}
		if got.Banks != 2 {
			t.Errorf("Banks = %d, want 2 (demo + other)", got.Banks)
		}
		if got.Bytes <= 0 {
			t.Errorf("Bytes = %d, want > 0", got.Bytes)
		}
		if got.Root != m.Root() {
			t.Errorf("Root = %q, want %q", got.Root, m.Root())
		}
	})
	t.Run("stats on a missing bank is empty, not an error", func(t *testing.T) {
		got, err := m.Stats("never-written")
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if got.Entries != 0 || got.Bytes != 0 || got.LastRetain != 0 {
			t.Errorf("missing bank stats = %+v, want zeroes", got)
		}
	})
	t.Run("by-id", func(t *testing.T) {
		e, found, err := m.ByID(bank, "b")
		if err != nil || !found {
			t.Fatalf("ByID(b) = %v, found=%v, err=%v", e, found, err)
		}
		if e.Content != "second row" {
			t.Errorf("ByID(b).Content = %q", e.Content)
		}
	})
	t.Run("by-id falls back to document_id", func(t *testing.T) {
		e, found, err := m.ByID(bank, "doc-b")
		if err != nil || !found {
			t.Fatalf("ByID(doc-b) found=%v err=%v", found, err)
		}
		if e.ID != "b" {
			t.Errorf("ByID(doc-b).ID = %q, want b", e.ID)
		}
	})
	t.Run("exact id wins over a document_id collision", func(t *testing.T) {
		if err := m.RetainRaw(bank, []Entry{{ID: "doc-b", Content: "id literally named doc-b", Timestamp: 400}}); err != nil {
			t.Fatal(err)
		}
		e, found, _ := m.ByID(bank, "doc-b")
		if !found || e.ID != "doc-b" {
			t.Errorf("ByID(doc-b) = %+v, want the row whose ID is doc-b", e)
		}
	})
	t.Run("missing id and empty id are both not-found", func(t *testing.T) {
		for _, id := range []string{"nope", ""} {
			if _, found, err := m.ByID(bank, id); err != nil || found {
				t.Errorf("ByID(%q) found=%v err=%v, want not-found", id, found, err)
			}
		}
	})
	t.Run("list is newest-first and routes out the two later rows", func(t *testing.T) {
		rows, total, err := m.List(bank, 0, 0)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 4 {
			t.Errorf("total = %d, want 4 (the collision fixture added one)", total)
		}
		want := []string{"doc-b", "b", "c", "a"}
		if len(rows) != len(want) {
			t.Fatalf("List returned %d rows, want %d", len(rows), len(want))
		}
		for i, id := range want {
			if rows[i].ID != id {
				t.Errorf("rows[%d].ID = %q, want %q", i, rows[i].ID, id)
			}
		}
	})
	t.Run("list paging", func(t *testing.T) {
		rows, total, err := m.List(bank, 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		if total != 4 {
			t.Errorf("total = %d, want 4 (total is the unpaged count)", total)
		}
		if len(rows) != 2 || rows[0].ID != "b" || rows[1].ID != "c" {
			t.Errorf("page(offset=1,limit=2) = %+v", rows)
		}
		if rows, _, err := m.List(bank, 99, 2); err != nil || len(rows) != 0 {
			t.Errorf("offset past the end = %+v err=%v, want empty", rows, err)
		}
	})
	t.Run("list on a missing bank is empty", func(t *testing.T) {
		rows, total, err := m.List("never-written", 0, 10)
		if err != nil || total != 0 || len(rows) != 0 {
			t.Errorf("missing bank list = %+v total=%d err=%v", rows, total, err)
		}
	})
	t.Run("banks enumerates the root sorted", func(t *testing.T) {
		got, err := m.Banks()
		if err != nil {
			t.Fatalf("Banks: %v", err)
		}
		if len(got) != 2 || got[0] != "demo" || got[1] != "other" {
			t.Errorf("Banks() = %v, want [demo other]", got)
		}
	})
	t.Run("a torn line does not fail the read", func(t *testing.T) {
		f, err := os.OpenFile(filepath.Join(m.Root(), "banks", "demo.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("{not json\n"); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if _, total, err := m.List(bank, 0, 0); err != nil || total != 4 {
			t.Errorf("after a torn line: total=%d err=%v, want 4 rows and no error", total, err)
		}
	})
}
