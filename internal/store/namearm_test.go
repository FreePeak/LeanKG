package store

import (
	"context"
	"testing"
)

// TestFindByNameTokenFiresOnProse pins the gate that replaced a shape test.
//
// Measured on main 2026-10-13 over all 84 labelled questions in
// docs/retrieval-label-set.md: the name arm fired on ZERO of them, because it
// required a query with no whitespace (a bare symbol name). Agents ask in
// prose — "take the single-flight lock for embedding" — and the informative
// token is a plain lowercase word, so a shape test rejects exactly the tokens
// the arm exists for.
//
// The replacement is rarity in THIS corpus: a token inside many symbol names
// discriminates nothing, a rare one is the name the agent typed.
func TestFindByNameTokenFiresOnProse(t *testing.T) {
	s := openTestStore(t)
	els := []Element{
		// The rare, informative token.
		{QualifiedName: "pkg/a.go::coverage", ElementType: "function", Name: "coverage", FilePath: "pkg/a.go", Language: "go", Content: "// coverage reports"},
		{QualifiedName: "pkg/b.go::reportCoverage", ElementType: "function", Name: "reportCoverage", FilePath: "pkg/b.go", Language: "go", Content: "func reportCoverage() {}"},
		// A COMMON token: many elements carry "read" in their name.
		{QualifiedName: "pkg/r1.go::readOne", ElementType: "function", Name: "readOne", FilePath: "pkg/r1.go", Language: "go", Content: "x"},
		{QualifiedName: "pkg/r2.go::readTwo", ElementType: "function", Name: "readTwo", FilePath: "pkg/r2.go", Language: "go", Content: "x"},
		{QualifiedName: "pkg/r3.go::readThree", ElementType: "function", Name: "readThree", FilePath: "pkg/r3.go", Language: "go", Content: "x"},
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	// PROSE, the way an agent asks. The rare token must reach the arm.
	got, err := s.FindByNameToken("what does coverage report", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("a prose question with a rare token did not reach the name arm")
	}
	// Shortest name first: `coverage` before `reportCoverage`.
	if got[0].Element.Name != "coverage" {
		t.Errorf("first hit = %q, want the shortest (most specific) name", got[0].Element.Name)
	}
	// A question of ONLY common words and function words reaches nothing —
	// the arm must not become a noise machine.
	none, err := s.FindByNameToken("read the one for how", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("a question of common words fired the arm: %d hits, first=%q", len(none), none[0].Element.Name)
	}
	_ = context.Background
}
