package orgknowledge

import (
	"context"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// entryFake embeds the package Store as nil, so only the methods it overrides
// are callable. A passing test proves the read needs the knowledge role alone.
type entryFake struct {
	Store
	entry store.KnowledgeEntry
}

func (f *entryFake) KnowledgeEntryByID(_ context.Context, id string) (store.KnowledgeEntry, bool, error) {
	if id != f.entry.ID {
		return store.KnowledgeEntry{}, false, nil
	}
	return f.entry, true, nil
}

func TestAnnotationReadsThroughKnowledgeRole(t *testing.T) {
	k := New(&entryFake{entry: store.KnowledgeEntry{ID: "note-1"}})
	got, found, err := k.Annotation(context.Background(), "note-1")
	if err != nil {
		t.Fatalf("Annotation: %v", err)
	}
	if !found || got.ID != "note-1" {
		t.Fatalf("Annotation = %+v found=%v, want note-1", got, found)
	}
}
