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

// checkRareTokenBound seeds 2511 elements and places a token at each side of
// the rarity boundary. The bound for 2511 elements is 5 (elements/500); under
// the SQLite bound of elements/5000 (floor 2) "quartz" (5 names) would be
// dropped. Both engines must keep quartz (5 is not above the bound) and drop
// widget (6 is above it).
func checkRareTokenBound(t *testing.T, s rareTokenClassifier) {
	t.Helper()
	var els []Element
	add := func(name string, n int) {
		for i := 0; i < n; i++ {
			els = append(els, Element{QualifiedName: fmt.Sprintf("pkg.%s%d", name, i), ElementType: "function",
				Name: name, FilePath: "pkg/" + name + ".go", Language: "go"})
		}
	}
	add("quartz", 5)
	add("widget", 6)
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
