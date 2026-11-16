package export

import (
	"context"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// graphDump implements exactly the three store methods export calls. Passing
// it proves the dependency is narrowed: it would not compile against
// store.Backend.
type graphDump struct {
	els  []store.Element
	rels []store.Relationship
}

func (g *graphDump) Elements(context.Context) ([]store.Element, error) {
	return g.els, nil
}

func (g *graphDump) RelationshipsAll(context.Context, int) ([]store.Relationship, error) {
	return g.rels, nil
}

func (g *graphDump) Outgoing(context.Context, string) ([]store.Relationship, error) {
	return nil, nil
}

func TestAllOverNarrowedStore(t *testing.T) {
	st := &graphDump{
		els:  []store.Element{{QualifiedName: "a"}, {QualifiedName: "b"}},
		rels: []store.Relationship{{Source: "a", Target: "b", RelType: "calls"}},
	}

	els, rels, err := All(context.Background(), st)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(els) != 2 || len(rels) != 1 {
		t.Fatalf("all = %d elements, %d relationships; want 2 and 1", len(els), len(rels))
	}
}
