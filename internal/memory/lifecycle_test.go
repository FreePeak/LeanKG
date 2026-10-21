package memory

import (
	"context"
	"testing"
)

// stampedVectorizer is a dense arm with a fixed vector space key — the
// shape the serve path wires (the key names the model and the space it was
// embedded in).
type stampedVectorizer struct{ key string }

func (s stampedVectorizer) Key() string            { return s.key }
func (s stampedVectorizer) Floor() (float64, bool) { return 0, true }
func (s stampedVectorizer) EmbedQuery(context.Context, string) ([]float32, error) {
	return []float32{1, 0, 0}, nil
}
func (s stampedVectorizer) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}

// seedVector inserts one vector row into the memory index under a model key.
func seedVector(t *testing.T, m *Memory, model, bank, entryID string) {
	t.Helper()
	if _, err := m.fts.Exec(`INSERT OR REPLACE INTO memory_vectors (model, bank, entry_id, vec) VALUES (?, ?, ?, ?)`,
		model, bank, entryID, []byte{1, 0, 0}); err != nil {
		t.Fatalf("seed vector %s: %v", model, err)
	}
}

// countVectors counts the vector rows under one model key.
func countVectors(t *testing.T, m *Memory, model string) int {
	t.Helper()
	var n int
	if err := m.fts.QueryRow(`SELECT COUNT(*) FROM memory_vectors WHERE model = ?`, model).Scan(&n); err != nil {
		t.Fatalf("count vectors %s: %v", model, err)
	}
	return n
}

// TestCompactPrunesStaleVectorCollection pins C.5: the memory index holds a
// collection under a model id nobody queries any more (dense.go keys
// vectors by model, so the old collection was never looked at again and
// never collected), and the sweep drops it while the live collection
// survives.
//
// Measured before the fix: 427 vectors (655,872 B) under the stale model id
// "local|bge-small-en-v1.5-f16.gguf|local|384" next to the live
// collection's 690 rows (1,413,120 B).
func TestCompactPrunesStaleVectorCollection(t *testing.T) {
	m := openTest(t)
	const stale = "local|bge-small-en-v1.5-f16.gguf|local|384"
	const live = "embeddinggemma-2|rev1|local|512"
	seedVector(t, m, stale, "bank", "old-1")
	seedVector(t, m, stale, "bank", "old-2")
	seedVector(t, m, live, "bank", "new-1")
	m.SetVectorizer(stampedVectorizer{key: live})
	if err := m.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if n := countVectors(t, m, stale); n != 0 {
		t.Fatalf("stale collection still holds %d vectors, want 0", n)
	}
	if n := countVectors(t, m, live); n != 1 {
		t.Fatalf("live collection holds %d vectors, want 1", n)
	}
}

// TestCompactKeepsEveryCollectionWithoutALiveModel pins the safety rule: a
// caller that does not know which collection is live must not be able to
// delete one. With no vectorizer wired and no keep set named, the sweep
// removes nothing.
func TestCompactKeepsEveryCollectionWithoutALiveModel(t *testing.T) {
	m := openTest(t)
	const stale = "local|bge-small-en-v1.5-f16.gguf|local|384"
	seedVector(t, m, stale, "bank", "old-1")
	if err := m.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if n := countVectors(t, m, stale); n != 1 {
		t.Fatalf("a collection was dropped with no live model known: %d rows left", n)
	}
}
