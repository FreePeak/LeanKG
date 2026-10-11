package store

import (
	"strings"
	"testing"
)

// TestHybridSearchFusesANameArm is the bare-identifier gap, measured on this
// machine's own self-host before the fix: with L3 pinned, the query
// "MultiProject" returned 10 hits whose top four were routeByProject,
// handlerFor, ServeHTTP and writeProjectError — the exact symbol ranked 9th,
// and only through a TEST name. Neither fused arm (vector, fts5 keyword)
// reads the element's name column, so the commonest agent question —
// a bare symbol name — is answered badly by the semantic rung.
func TestHybridSearchFusesANameArm(t *testing.T) {
	st := openTestStore(t)
	if err := st.UpsertElements([]Element{
		{QualifiedName: "pkg.Alpha", ElementType: "func", Name: "Alpha", FilePath: "pkg/a.go", LineStart: 1, Language: "go",
			Content: "Alpha does something unrelated to the words used below."},
		{QualifiedName: "pkg.Beta", ElementType: "func", Name: "Beta", FilePath: "pkg/b.go", LineStart: 1, Language: "go",
			Content: "Beta mentions the shared vocabulary a great many times over."},
	}); err != nil {
		t.Fatal(err)
	}
	// A query that names Alpha exactly and shares no vocabulary with it.
	hits, arms, err := st.HybridSearch("", "Alpha", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(arms, ArmName) {
		t.Fatalf("arms = %q; the fusion must include the %q arm", arms, ArmName)
	}
	if len(hits) == 0 || hits[0].Element.QualifiedName != "pkg.Alpha" {
		got := ""
		if len(hits) > 0 {
			got = hits[0].Element.QualifiedName
		}
		t.Fatalf("top hit = %q, want pkg.Alpha — an exact name match must outrank a vocabulary match", got)
	}
}
