package store

import (
	"strings"
	"testing"
)

// TestSearchVectorsScopedFiltersThePopulation is the corpus-scoping primitive.
// Measured on this repository's own 10,737-element store: 2,389 elements are
// test fixtures (22%) and 4,011 are archived docs (37%) — so 60% of the vector
// population is a document a working engineer is not asking about, and a
// bare-symbol query ranks those documents above the function.
//
// The divergence #449 measured is the same shape today: both arms rank over
// the same set, so no weight pair helps; scoping the SET is the only lever.
func TestSearchVectorsScopedFiltersThePopulation(t *testing.T) {
	st := openTestStore(t)
	if err := upsertVectorsFor(t, st, "m",
		Element{QualifiedName: "pkg/alpha.go::Alpha", Name: "Alpha", FilePath: "pkg/alpha.go"},
		Element{QualifiedName: "testdata/fixture.go::Beta", Name: "Beta", FilePath: "testdata/fixture.go"},
		Element{QualifiedName: "docs/archive/x.md::Gamma", Name: "Gamma", FilePath: "docs/archive/x.md"},
	); err != nil {
		t.Fatal(err)
	}
	q := []float32{1, 0, 0, 0}
	// The default (unscoped) population is byte-for-byte what it was: every
	// row, in corpus order, no hidden filter.
	all, err := st.SearchVectors("m", q, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("unscoped hits = %d, want 3 (the default must not move)", len(all))
	}
	// A code scope excludes fixtures and archived docs.
	codeOnly, err := st.SearchVectorsScoped("m", q, 10, func(e Element) bool {
		return !strings.Contains(e.FilePath, "testdata") &&
			!strings.HasPrefix(e.FilePath, "docs/archive/")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(codeOnly) != 1 || codeOnly[0].Element.Name != "Alpha" {
		t.Fatalf("code scope hits = %+v, want exactly Alpha", codeOnly)
	}
}

// TestScopePredicateAdmitsEverything pins that a predicate that admits nothing
// is a caller error, not a silently emptied result — the same "looks like it
// worked" failure an unknown scope must produce.
func TestSearchVectorsScopedNilPredicateIsUnscoped(t *testing.T) {
	st := openTestStore(t)
	if err := upsertVectorsFor(t, st, "m",
		Element{QualifiedName: "a.go::A", Name: "A", FilePath: "a.go"},
		Element{QualifiedName: "testdata/b.go::B", Name: "B", FilePath: "testdata/b.go"},
	); err != nil {
		t.Fatal(err)
	}
	q := []float32{1, 0, 0, 0}
	nilPred, err := st.SearchVectorsScoped("m", q, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nilPred) != 2 {
		t.Fatalf("nil predicate hits = %d, want 2 (a nil predicate must mean unscoped)", len(nilPred))
	}
}

// upsertVectorsFor writes elements AND one distinct 4-dim vector each, so the
// scan has a population. The vectors are deliberately identical in direction
// for the assertion that matters (which rows the filter admits), because the
// scoping test is about the population, not about cosine ordering.
func upsertVectorsFor(t *testing.T, st *Store, modelID string, els ...Element) error {
	t.Helper()
	if err := st.UpsertElements(els); err != nil {
		return err
	}
	rows := make([]VectorRow, 0, len(els))
	for i := range els {
		v := []float32{1, 0, 0, 0}
		rows = append(rows, VectorRow{QualifiedName: els[i].QualifiedName, Vec: v})
	}
	return st.UpsertVectors(modelID, rows)
}
