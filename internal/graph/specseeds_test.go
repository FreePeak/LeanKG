package graph

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

const (
	specIndexWriter = "internal/store/roles.go::IndexWriter.UpsertElements"
	specPrd         = "internal/prdindex/prdindex.go::prdStore.UpsertElements"
	specLsp         = "internal/lsp/enrich.go::enrichStore.UpsertElements"
	specOther       = "internal/other/x.go::otherStore.UpsertElements"
	implStore       = "internal/store/elements.go::Store.UpsertElements"
	implPG          = "internal/store/store_pg.go::PGStore.UpsertElements"
	implMem         = "internal/other/x.go::memStore.UpsertElements"
)

// newSpecFixture wires the shape the index writes for one Go interface method
// called through four local interfaces. IndexWriter.UpsertElements, prdStore,
// enrichStore and otherStore are interface specs (metadata interface_spec).
// Store and PGStore are the concrete implementers. Each caller links to its
// own spec (interface-spec) and to the implementers it may dispatch to
// (interface-dispatch). Flush reaches otherStore, whose only implementer memStore
// is not one of the others, so otherStore shares no implementer with the rest.
func newSpecFixture(t *testing.T) store.Backend {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenBackend(ctx, store.OpenOptions{DBPath: filepath.Join(t.TempDir(), ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	spec := map[string]any{"interface_spec": true}
	if err := s.UpsertElements(ctx, []store.Element{
		{QualifiedName: specIndexWriter, ElementType: "method", Name: "UpsertElements", FilePath: "internal/store/roles.go", LineStart: 10, Metadata: spec},
		{QualifiedName: specPrd, ElementType: "method", Name: "UpsertElements", FilePath: "internal/prdindex/prdindex.go", LineStart: 11, Metadata: spec},
		{QualifiedName: specLsp, ElementType: "method", Name: "UpsertElements", FilePath: "internal/lsp/enrich.go", LineStart: 12, Metadata: spec},
		{QualifiedName: specOther, ElementType: "method", Name: "UpsertElements", FilePath: "internal/other/x.go", LineStart: 13, Metadata: spec},
		{QualifiedName: implStore, ElementType: "method", Name: "UpsertElements", FilePath: "internal/store/elements.go", LineStart: 20},
		{QualifiedName: implPG, ElementType: "method", Name: "UpsertElements", FilePath: "internal/store/store_pg.go", LineStart: 30},
		{QualifiedName: implMem, ElementType: "method", Name: "UpsertElements", FilePath: "internal/other/x.go", LineStart: 40},
		{QualifiedName: "internal/convo/convo.go::IndexItems", ElementType: "function", Name: "IndexItems", FilePath: "internal/convo/convo.go", LineStart: 5},
		{QualifiedName: "internal/store/elements.go::Store.Close", ElementType: "method", Name: "Close", FilePath: "internal/store/elements.go", LineStart: 50},
		{QualifiedName: "internal/prdindex/prdindex.go::Index", ElementType: "function", Name: "Index", FilePath: "internal/prdindex/prdindex.go", LineStart: 5},
		{QualifiedName: "internal/lsp/enrich.go::Bridge.Enrich", ElementType: "method", Name: "Enrich", FilePath: "internal/lsp/enrich.go", LineStart: 5},
		{QualifiedName: "internal/other/x.go::Flush", ElementType: "function", Name: "Flush", FilePath: "internal/other/x.go", LineStart: 5},
	}); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	site := func(spec string) map[string]any { return map[string]any{"resolution": spec, "line": float64(7)} }
	if err := s.UpsertRelationships(ctx, []store.Relationship{
		{Source: "internal/convo/convo.go::IndexItems", Target: specIndexWriter, RelType: "calls", Confidence: 0.3, Metadata: site("interface-spec")},
		{Source: "internal/convo/convo.go::IndexItems", Target: implStore, RelType: "calls", Confidence: 0.3, Metadata: site("interface-dispatch")},
		{Source: "internal/convo/convo.go::IndexItems", Target: implPG, RelType: "calls", Confidence: 0.3, Metadata: site("interface-dispatch")},
		// IndexItems also dispatches another interface's method (Close). That is
		// not an implementer of UpsertElements and must not join the expansion.
		{Source: "internal/convo/convo.go::IndexItems", Target: "internal/store/elements.go::Store.Close", RelType: "calls", Confidence: 0.3, Metadata: site("interface-dispatch")},
		{Source: "internal/prdindex/prdindex.go::Index", Target: specPrd, RelType: "calls", Confidence: 0.3, Metadata: site("interface-spec")},
		{Source: "internal/prdindex/prdindex.go::Index", Target: implStore, RelType: "calls", Confidence: 0.3, Metadata: site("interface-dispatch")},
		{Source: "internal/prdindex/prdindex.go::Index", Target: implPG, RelType: "calls", Confidence: 0.3, Metadata: site("interface-dispatch")},
		{Source: "internal/lsp/enrich.go::Bridge.Enrich", Target: specLsp, RelType: "calls", Confidence: 0.3, Metadata: site("interface-spec")},
		{Source: "internal/lsp/enrich.go::Bridge.Enrich", Target: implStore, RelType: "calls", Confidence: 0.3, Metadata: site("interface-dispatch")},
		{Source: "internal/lsp/enrich.go::Bridge.Enrich", Target: implPG, RelType: "calls", Confidence: 0.3, Metadata: site("interface-dispatch")},
		{Source: "internal/other/x.go::Flush", Target: specOther, RelType: "calls", Confidence: 0.3, Metadata: site("interface-spec")},
		{Source: "internal/other/x.go::Flush", Target: implMem, RelType: "calls", Confidence: 0.3, Metadata: site("interface-dispatch")},
	}); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}
	return s
}

// A spec seed expands to the implementers its callers dispatch to, and to the
// other specs of the same name that share one of those implementers. The
// unrelated otherStore spec shares none, so it stays out.
func TestExpandSpecSeedsWidensASpecToImplementersAndSharingSpecs(t *testing.T) {
	st := newSpecFixture(t)
	got, err := ExpandSpecSeeds(context.Background(), st, []string{specIndexWriter})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{specIndexWriter, implStore, implPG, specLsp, specPrd}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExpandSpecSeeds(spec) = %v, want %v", got, want)
	}
}

// A concrete method, an unknown name and a seed already in the list are not
// widened, and no seed appears twice.
func TestExpandSpecSeedsLeavesConcreteSeedsAlone(t *testing.T) {
	st := newSpecFixture(t)
	ctx := context.Background()
	cases := []struct {
		name  string
		seeds []string
		want  []string
	}{
		{"concrete method is not widened to specs", []string{implStore}, []string{implStore}},
		{"unknown name passes through", []string{"nope.Missing"}, []string{"nope.Missing"}},
		{"spec already listed is not repeated", []string{specIndexWriter, implStore}, []string{specIndexWriter, implStore, implPG, specLsp, specPrd}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExpandSpecSeeds(ctx, st, tc.seeds)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ExpandSpecSeeds(%v) = %v, want %v", tc.seeds, got, tc.want)
			}
		})
	}
}
