package memory

import (
	"context"
	"strings"
	"testing"
)

// conceptVectorizer embeds text into fixed concept dimensions, so meaning
// (not shared words) decides similarity — a stand-in for a real model.
type conceptVectorizer struct {
	floor float64
	calls int
}

var concepts = [][]string{
	{"database", "postgres", "pgvector", "sql"},
	{"vector", "vectors", "embedding", "embeddings"},
	{"release", "ship", "binaries", "publish", "deploy"},
	{"cake", "chocolate", "flour", "bake"},
}

func (c *conceptVectorizer) Key() string            { return "concept-v1" }
func (c *conceptVectorizer) Floor() (float64, bool) { return c.floor, true }
func (c *conceptVectorizer) vec(t string) []float32 {
	v := make([]float32, len(concepts)+1)
	v[len(concepts)] = 0.3 // shared background, like a real model's anisotropy
	for _, w := range strings.Fields(strings.ToLower(t)) {
		w = strings.Trim(w, ".,?")
		for i, c := range concepts {
			for _, k := range c {
				if w == k {
					v[i]++
				}
			}
		}
	}
	return v
}
func (c *conceptVectorizer) EmbedQuery(_ context.Context, t string) ([]float32, error) {
	return c.vec(t), nil
}
func (c *conceptVectorizer) EmbedDocuments(_ context.Context, ts []string) ([][]float32, error) {
	c.calls += len(ts)
	out := make([][]float32, len(ts))
	for i, t := range ts {
		out[i] = c.vec(t)
	}
	return out, nil
}

// TestDenseRecallFindsParaphrasesAboveFloor pins RS-19: with a vectorizer,
// a question sharing no words with a memory recalls it by meaning; an
// unrelated question (below the noise floor, no shared words) recalls
// nothing; rows are embedded once.
func TestDenseRecallFindsParaphrasesAboveFloor(t *testing.T) {
	m := openTest(t)
	if err := m.RetainRaw("b", []Entry{
		{ID: "pg", Content: "We store embeddings in pgvector on Postgres."},
		{ID: "rel", Content: "CI will publish binaries when the tag lands."},
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Recall("b", "which database holds the vectors", 3); len(got) != 0 {
		t.Fatalf("precondition: lexical recall should miss the paraphrase, got %v", got)
	}
	cv := &conceptVectorizer{floor: 0.5}
	m.SetVectorizer(cv)
	got, err := m.Recall("b", "which database holds the vectors", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "pg" {
		t.Fatalf("paraphrase recall = %v, want pg first", got)
	}
	if got, _ := m.Recall("b", "chocolate cake recipe", 3); len(got) != 0 {
		t.Fatalf("unrelated question recalled %v", got)
	}
	before := cv.calls
	if _, err := m.Recall("b", "ship the release", 3); err != nil {
		t.Fatal(err)
	}
	if cv.calls != before {
		t.Fatalf("rows re-embedded on every recall: %d -> %d", before, cv.calls)
	}
	// Keyword-length queries stay lexical: "database" alone shares no word
	// with the pg row, so it recalls nothing.
	if got, _ := m.Recall("b", "database", 3); len(got) != 0 {
		t.Fatalf("one-word query used the dense arm: %v", got)
	}
}

// TestFuseRRFCharacterization pins the exact output of the dense-arm fusion
// before it is considered for reuse elsewhere: fused score is the sum of
// 1/(60+rank+1) over every occurrence, equal scores prefer the newer row, equal
// score and age fall back to first appearance, and each output row carries the
// content of its first occurrence with Score replaced by the fused sum.
func TestFuseRRFCharacterization(t *testing.T) {
	a := Entry{ID: "a", Content: "A-first", Timestamp: 100}
	b := Entry{ID: "b", Content: "B-first", Timestamp: 200}
	c := Entry{ID: "c", Content: "C", Timestamp: 300}
	d := Entry{ID: "d", Content: "D", Timestamp: 300}
	got := fuseRRF([]Entry{a, b, c}, []Entry{b, a, d})
	wantIDs := []string{"b", "a", "c", "d"}
	if len(got) != len(wantIDs) {
		t.Fatalf("fused rows = %d, want %d", len(got), len(wantIDs))
	}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %s, want %s (full: %v)", i, got[i].ID, id, got)
		}
	}
	// Runtime float64 operands: untyped constant arithmetic would round differently.
	rank := func(i int) float64 { return 1 / float64(60+i+1) }
	wantScore := map[string]float64{"a": rank(0) + rank(1), "b": rank(1) + rank(0), "c": rank(2), "d": rank(2)}
	for _, e := range got {
		if e.Score != wantScore[e.ID] {
			t.Fatalf("score[%s] = %v, want %v", e.ID, e.Score, wantScore[e.ID])
		}
	}
	if got[0].Content != "B-first" {
		t.Fatalf("content = %q, want the first occurrence's B-first", got[0].Content)
	}

	// Current behaviour, pinned on purpose: an ID that occurs twice in one
	// list is credited for both occurrences (cross-bank dense rows can repeat
	// an ID). The lexical arm dedupes instead; this arm does not.
	dup := fuseRRF([]Entry{{ID: "x", Content: "first"}, {ID: "x", Content: "second"}})
	if len(dup) != 1 || dup[0].Content != "first" {
		t.Fatalf("duplicate id rows = %v, want one row with first occurrence's content", dup)
	}
	if want := rank(0) + rank(1); dup[0].Score != want {
		t.Fatalf("duplicate id score = %v, want %v (both occurrences summed)", dup[0].Score, want)
	}
}
