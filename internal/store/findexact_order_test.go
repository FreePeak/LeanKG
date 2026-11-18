package store

import (
	"context"
	"testing"
)

// TestFindExactRanksDeterministically pins the plain-name L1 order. A name
// matches case-insensitively, but an exact-case match ranks first, and the
// order does not depend on the order the rows were written (an incremental
// index writes them in a different order from a fresh one).
func TestFindExactRanksDeterministically(t *testing.T) {
	upper := Element{QualifiedName: "a.go::AlphaTwo", ElementType: "function", Name: "AlphaTwo", FilePath: "a.go", Language: "go"}
	lower := Element{QualifiedName: "b.go::alphaTwo", ElementType: "function", Name: "alphaTwo", FilePath: "b.go", Language: "go"}

	for _, order := range [][]Element{{upper, lower}, {lower, upper}} {
		s := openTestStore(t)
		if err := s.UpsertElements(context.Background(), order); err != nil {
			t.Fatalf("UpsertElements: %v", err)
		}
		cases := []struct{ query, want string }{
			{"AlphaTwo", "a.go::AlphaTwo"},
			{"alphaTwo", "b.go::alphaTwo"},
		}
		for _, c := range cases {
			got, err := s.FindExact(context.Background(), c.query)
			if err != nil || len(got) != 2 {
				t.Fatalf("FindExact(%q) = %d hits, err %v; want 2", c.query, len(got), err)
			}
			if got[0].QualifiedName != c.want {
				t.Errorf("order %s, FindExact(%q)[0] = %q; want %q",
					order[0].QualifiedName, c.query, got[0].QualifiedName, c.want)
			}
		}
	}
}

// TestFindExactTieBreaksByPath: two exact-case matches of the same length tie on
// every ranking key but the file path, which orders them ascending.
func TestFindExactTieBreaksByPath(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertElements(context.Background(), []Element{
		{QualifiedName: "b.go::Helper", ElementType: "function", Name: "Helper", FilePath: "b.go", Language: "go"},
		{QualifiedName: "a.go::Helper", ElementType: "function", Name: "Helper", FilePath: "a.go", Language: "go"},
	}); err != nil {
		t.Fatalf("UpsertElements: %v", err)
	}
	got, err := s.FindExact(context.Background(), "Helper")
	if err != nil || len(got) != 2 {
		t.Fatalf("FindExact = %d hits, err %v; want 2", len(got), err)
	}
	if got[0].QualifiedName != "a.go::Helper" {
		t.Errorf("FindExact(Helper)[0] = %q; want a.go::Helper (path ascending)", got[0].QualifiedName)
	}
}
