package memory

import (
	"context"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
)

// stampedProvider is a dense arm with a fixed vector space identity — the
// shape the serve path wires (the identity names the model and the space it
// was embedded in).
type stampedProvider struct{ id embed.Identity }

func (s stampedProvider) Identity() embed.Identity { return s.id }
func (s stampedProvider) Probe(context.Context) error {
	return nil
}
func (s stampedProvider) Embed(_ context.Context, _ embed.TextKind, texts []string) ([][]float32, error) {
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
	liveID := embed.Identity{Provider: "local", Model: "embeddinggemma-2", Revision: "rev1", Dims: 512}
	live := liveID.Key()
	seedVector(t, m, stale, "bank", "old-1")
	seedVector(t, m, stale, "bank", "old-2")
	seedVector(t, m, live, "bank", "new-1")
	m.SetProvider(stampedProvider{id: liveID}, func(context.Context) (float64, bool) { return 0, true })
	if err := m.Compact(context.Background()); err != nil {
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
	if err := m.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if n := countVectors(t, m, stale); n != 1 {
		t.Fatalf("a collection was dropped with no live model known: %d rows left", n)
	}
}
