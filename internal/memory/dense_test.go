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
