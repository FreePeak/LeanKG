package store_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestVectorSearchCanBeScoped pins the capability the L3 rung needs and does
// not have: a vector search that can be restricted to part of the collection.
//
// Measured on this repository's own store (9,306 vectors), ranking one real
// question ("reciprocal rank fusion of ranked lists") by cosine:
//
//	full collection (9,306)                 rank  54
//	production code only (3,279)           rank   7
//
// The 6,027 vectors that pushed it out of the top ten are 1,879 test fixtures
// and 4,148 documentation sections — a corpus this repository accumulated by
// indexing itself. A vector search has no notion of what a caller WANTS, so an
// agent asking about the implementation gets whichever test fixture happens to
// paraphrase the question, and no weight can fix it: both arms rank over the
// same set.
//
// The scope is a caller's decision, not a default, so the primitive takes a
// filter and the store applies it in the same pass it already scans. Nil /
// empty filter is the current behaviour, so every existing caller is unchanged.
func TestVectorSearchCanBeScoped(t *testing.T) {
	st := newScopeFixture(t)
	defer st.Close()

	// Three vectors on a line; the middle one is the answer.
	one := func(x float32) []float32 { return []float32{x, 0, 0} }
	// The two decoys are named so a scope can EXCLUDE them by prefix.
	for _, e := range []struct {
		qn  string
		vec []float32
	}{
		{"internal/decoy_test.go::TestRank", one(1.0)},
		{"internal/decoy_test.go::TestRankAgain", one(0.99)},
		{"internal/real.go::Rank", one(0.98)},
	} {
		if err := st.UpsertElements([]store.Element{{
			QualifiedName: e.qn, ElementType: "function",
			Name: e.qn[strings.LastIndex(e.qn, "::")+2:], FilePath: e.qn[:strings.Index(e.qn, "::")],
			Language: "go", Content: "func rank() {}",
		}}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertVectors(scopeModel, []store.VectorRow{{QualifiedName: e.qn, Vec: e.vec}}); err != nil {
			t.Fatal(err)
		}
	}

	hits, err := st.SearchVectors(scopeModel, []float32{1, 0, 0}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("unscoped search returned %d hits, want 3", len(hits))
	}

	// The scoped form must drop the excluded prefix and keep the answer.
	scoped, err := st.SearchVectorsScoped(scopeModel, []float32{1, 0, 0}, 10, func(el store.Element) bool {
		return !strings.Contains(el.FilePath, "_test.go")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].Element.QualifiedName != "internal/real.go::Rank" {
		t.Fatalf("scoped search = %+v, want only internal/real.go::Rank", qualifiedNames(scoped))
	}

	// A nil filter is the old behaviour, exactly: this is an extension, never
	// a silent narrowing of what an unscoped caller sees.
	again, err := st.SearchVectorsScoped(scopeModel, []float32{1, 0, 0}, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(hits) {
		t.Fatalf("nil filter changed the result: %d hits, unscoped %d", len(again), len(hits))
	}
	for i := range hits {
		if hits[i].Element.QualifiedName != again[i].Element.QualifiedName {
			t.Errorf("nil filter reordered result %d: %q vs %q", i,
				again[i].Element.QualifiedName, hits[i].Element.QualifiedName)
		}
	}

	// A similarity order must survive the filter, not just the membership.
	ordered := []struct {
		qn string
		x  float32
	}{
		{"internal/a.go::First", 1.0}, {"internal/a.go::Second", 0.9}, {"internal/a.go::Third", 0.8},
	}
	for _, o := range ordered {
		if err := st.UpsertElements([]store.Element{{
			QualifiedName: o.qn, ElementType: "function", Name: o.qn[strings.LastIndex(o.qn, "::")+2:],
			FilePath: "internal/a.go", Language: "go", Content: "func f() {}",
		}}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertVectors(scopeModel, []store.VectorRow{{QualifiedName: o.qn, Vec: one(o.x)}}); err != nil {
			t.Fatal(err)
		}
	}
	filtered, err := st.SearchVectorsScoped(scopeModel, []float32{1, 0, 0}, 10, func(el store.Element) bool {
		return el.FilePath == "internal/a.go"
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"internal/a.go::First", "internal/a.go::Second", "internal/a.go::Third"}
	got := qualifiedNames(filtered)
	if len(got) != len(want) {
		t.Fatalf("filtered = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filtered order = %v, want descending similarity %v", got, want)
		}
	}
}

const scopeModel = "scope-model"

func qualifiedNames(hits []store.VectorSearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Element.QualifiedName)
	}
	return out
}

func newScopeFixture(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "scope.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
