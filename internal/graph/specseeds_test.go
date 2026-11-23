package graph

import (
	"context"
	"fmt"
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
	got, truncated, err := ExpandSpecSeeds(context.Background(), st, []string{specIndexWriter})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{specIndexWriter, implStore, implPG, specLsp, specPrd}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExpandSpecSeeds(spec) = %v, want %v", got, want)
	}
	if truncated {
		t.Fatalf("truncated = true for a small fixture, want false")
	}
}

const (
	hubSpec = "hub.go::Hub.Do"
	hubUse  = "use.go::Use"
)

// newHubFixture: the interface method Hub.Do is reached by one caller, Use,
// through its spec, and Use dispatches to impls concrete implementers (named
// Impl%03d, so FindExact("Do") sees only the spec). extraSpecs more specs
// named Do, with no edges, share the name.
func newHubFixture(t *testing.T, impls, extraSpecs int) store.Backend {
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
	site := func(res string) map[string]any { return map[string]any{"resolution": res, "line": float64(7)} }
	els := []store.Element{
		{QualifiedName: hubSpec, ElementType: "method", Name: "Do", FilePath: "hub.go", LineStart: 1, Metadata: spec},
		{QualifiedName: hubUse, ElementType: "function", Name: "Use", FilePath: "use.go", LineStart: 5},
	}
	rels := []store.Relationship{
		{Source: hubUse, Target: hubSpec, RelType: "calls", Confidence: 0.3, Metadata: site("interface-spec")},
	}
	for i := range extraSpecs {
		els = append(els, store.Element{QualifiedName: fmt.Sprintf("many/s%03d.go::S%03d.Do", i, i), ElementType: "method", Name: "Do", FilePath: fmt.Sprintf("many/s%03d.go", i), LineStart: 1, Metadata: spec})
	}
	for i := range impls {
		qn := fmt.Sprintf("impl/i%03d.go::Impl%03d.Do", i, i)
		els = append(els, store.Element{QualifiedName: qn, ElementType: "method", Name: fmt.Sprintf("Impl%03d", i), FilePath: fmt.Sprintf("impl/i%03d.go", i), LineStart: 1})
		rels = append(rels, store.Relationship{Source: hubUse, Target: qn, RelType: "calls", Confidence: 0.3, Metadata: site("interface-dispatch")})
	}
	if err := s.UpsertElements(ctx, els); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	if err := s.UpsertRelationships(ctx, rels); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}
	return s
}

// A hub with more implementers than specSeedLimit adds exactly specSeedLimit
// seeds after the given one, keeps the given seed first, and reports truncation.
func TestExpandSpecSeedsCapsAddedSeedsAtLimit(t *testing.T) {
	st := newHubFixture(t, 300, 0)
	got, truncated, err := ExpandSpecSeeds(context.Background(), st, []string{hubSpec})
	if err != nil {
		t.Fatal(err)
	}
	if want := 1 + specSeedLimit; len(got) != want {
		t.Fatalf("len(ExpandSpecSeeds) = %d, want %d (the given seed plus specSeedLimit)", len(got), want)
	}
	if got[0] != hubSpec {
		t.Fatalf("first seed = %q, want the given seed %q", got[0], hubSpec)
	}
	if !truncated {
		t.Fatalf("truncated = false, want true: 300 implementers exceed specSeedLimit")
	}
}

// A same-name lookup that fills FindExact's row cap may have dropped sharing
// specs, so the expansion reports truncation.
func TestExpandSpecSeedsReportsTruncatedSharingLookup(t *testing.T) {
	st := newHubFixture(t, 1, 249) // 250 specs named Do: FindExact returns its LIMIT 200
	_, truncated, err := ExpandSpecSeeds(context.Background(), st, []string{hubSpec})
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatalf("truncated = false, want true: the sharing-spec lookup returned the row cap")
	}
}

// Store reads stay bounded for a hub-sized interface: the expansion plus the
// site query over the expanded seeds read a count set by specSeedLimit, not by
// the number of implementers.
func TestExpandSpecSeedsReadBudgetOnHub(t *testing.T) {
	st := newHubFixture(t, 300, 0)
	ctx := context.Background()
	counted := &readCounter{Backend: st} // after is 0, so it never cancels
	walk, _, err := ExpandSpecSeeds(ctx, counted, []string{hubSpec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SitesBySeed(ctx, counted, walk, true, ""); err != nil {
		t.Fatal(err)
	}
	// Expansion: Incoming(spec) and Outgoing(Use). Site query: one Incoming per seed.
	if budget := 2 + (1 + specSeedLimit); counted.reads > budget {
		t.Fatalf("store reads = %d, want at most %d for a hub of 300 implementers", counted.reads, budget)
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
			got, _, err := ExpandSpecSeeds(ctx, st, tc.seeds)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ExpandSpecSeeds(%v) = %v, want %v", tc.seeds, got, tc.want)
			}
		})
	}
}
