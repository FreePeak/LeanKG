package store

import (
	"fmt"
	"slices"
	"testing"
)

// rareTokenClassifier is the part of an engine the CX-11 conformance check
// needs: seed elements, then ask which query tokens the name arm treats as rare.
type rareTokenClassifier interface {
	UpsertElements([]Element) error
	rareNameTokens(query string) ([]string, error)
}

// TestNameArmBoundIsElementsOver5000 pins the measured divisor. The shared
// bound is elements/5000 with floor 2 (CX-11, evidence in elements.go). Both
// engines call nameArmBound, so this one table covers the PostgreSQL bound too,
// without a live database. The /500 divisor it replaces gives 21 at 10,885
// elements and 2000 at a million, so every row with a non-floor value fails it.
func TestNameArmBoundIsElementsOver5000(t *testing.T) {
	t.Setenv("LEANKG_NAME_ARM_BOUND", "")
	cases := []struct{ total, want int }{
		{0, 2},
		{2511, 2},
		{10885, 2},
		{12413, 2},
		{50000, 10},
		{1000000, 200},
	}
	for _, c := range cases {
		if got := nameArmBound(c.total); got != c.want {
			t.Errorf("nameArmBound(%d) = %d, want %d (elements/5000, floor 2)", c.total, got, c.want)
		}
	}
}

// TestNameArmBoundOverride keeps the sweep hook: LEANKG_NAME_ARM_BOUND replaces
// the derived bound, so a corpus can be re-swept without a code change.
func TestNameArmBoundOverride(t *testing.T) {
	t.Setenv("LEANKG_NAME_ARM_BOUND", "7")
	if got := nameArmBound(10885); got != 7 {
		t.Errorf("nameArmBound(10885) with LEANKG_NAME_ARM_BOUND=7 = %d, want 7", got)
	}
}

// checkRareTokenBound seeds 2511 elements (bound 2 under elements/5000) and
// places a token at each side of the rarity boundary: "quartz" in 2 names is
// at the bound and stays, "widget" in 3 names is above it and is dropped. The
// old elements/500 bound (5 at this size) keeps both, so this fails on it. Both
// engines must classify the two tokens identically.
func checkRareTokenBound(t *testing.T, s rareTokenClassifier) {
	t.Helper()
	var els []Element
	add := func(name string, n int) {
		for i := 0; i < n; i++ {
			els = append(els, Element{QualifiedName: fmt.Sprintf("pkg.%s%d", name, i), ElementType: "function",
				Name: name, FilePath: "pkg/" + name + ".go", Language: "go"})
		}
	}
	add("quartz", 2)
	add("widget", 3)
	for i := 0; i < 2500; i++ {
		els = append(els, Element{QualifiedName: fmt.Sprintf("pkg.filler%04d", i), ElementType: "function",
			Name: fmt.Sprintf("filler%04d", i), FilePath: "pkg/filler.go", Language: "go"})
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	toks, err := s.rareNameTokens("quartz widget")
	if err != nil {
		t.Fatalf("rareNameTokens: %v", err)
	}
	if want := []string{"quartz"}; !slices.Equal(toks, want) {
		t.Fatalf("rare tokens = %v; want %v (quartz at the bound stays, widget above it is dropped)", toks, want)
	}
}

// TestRareTokenBoundSQLite and TestPGRareTokenBound share one bound:
// nameArmBound. The two engines must classify the same token the same way.
func TestRareTokenBoundSQLite(t *testing.T) {
	checkRareTokenBound(t, openTestStore(t))
}

func TestPGRareTokenBound(t *testing.T) {
	checkRareTokenBound(t, openPGTest(t))
}
