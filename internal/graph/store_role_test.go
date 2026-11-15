package graph

import (
	"context"
	"reflect"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// readerOnly implements exactly the four store methods graph calls. Passing it
// to the verbs proves the dependency is narrowed: it would not compile against
// store.Backend.
type readerOnly struct {
	els []store.Element
	out map[string][]store.Relationship
	in  map[string][]store.Relationship
}

func (r *readerOnly) FindExact(_ context.Context, name string) ([]store.Element, error) {
	var got []store.Element
	for _, e := range r.els {
		if e.QualifiedName == name {
			got = append(got, e)
		}
	}
	return got, nil
}

func (r *readerOnly) Elements(_ context.Context) ([]store.Element, error) {
	return r.els, nil
}

func (r *readerOnly) Outgoing(_ context.Context, source string) ([]store.Relationship, error) {
	return r.out[source], nil
}

func (r *readerOnly) Incoming(_ context.Context, target string) ([]store.Relationship, error) {
	return r.in[target], nil
}

func TestImpactOverNarrowedReader(t *testing.T) {
	// b calls a, so the blast radius of a is a (depth 0) and b (depth 1).
	st := &readerOnly{
		els: []store.Element{{QualifiedName: "a", ElementType: "function"}},
		in:  map[string][]store.Relationship{"a": {{Source: "b", Target: "a", RelType: "calls"}}},
	}

	hits, _, err := Impact(context.Background(), st, "a", 2)
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	want := []Hit{{QN: "a", Depth: 0}, {QN: "b", Depth: 1}}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("impact = %+v; want %+v", hits, want)
	}
}

func TestOversizedFunctionsOverNarrowedReader(t *testing.T) {
	st := &readerOnly{els: []store.Element{
		{QualifiedName: "big", ElementType: FunctionElementType, LineStart: 1, LineEnd: 50},
		{QualifiedName: "small", ElementType: FunctionElementType, LineStart: 1, LineEnd: 3},
	}}

	got, err := OversizedFunctions(context.Background(), st, 10, "")
	if err != nil {
		t.Fatalf("oversized: %v", err)
	}
	if len(got) != 1 || got[0].QualifiedName != "big" {
		t.Fatalf("oversized = %+v; want only big", got)
	}
}
