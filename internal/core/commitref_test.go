package core

import (
	"context"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

const (
	shaA = "57c220d56fda547f7f7d27cb86dcc53f51ec9df6"
	shaB = "57c220d0000000000000000000000000000000ff"
)

// commitRow is the element githistory writes for one commit: the QN is
// git:commit:<sha>, the name starts with the 7-character prefix.
func commitRow(sha, subject string) store.Element {
	return store.Element{QualifiedName: "git:commit:" + sha, ElementType: "commit", Name: sha[:7] + " " + subject,
		FilePath: "", Content: subject}
}

// TestCommitFuzzyFindsHexNamedCode pins the S5 regression: a hex-looking query
// is searched in full. Before the fix "cafebabe12" was cut to "cafebab" and the
// keyword rung found nothing.
func TestCommitFuzzyFindsHexNamedCode(t *testing.T) {
	e, _ := newEngine(t)
	if err := e.st.UpsertElements(context.Background(), []store.Element{
		{QualifiedName: "pkg/h.go::cafebabe12", ElementType: "function", Name: "cafebabe12", FilePath: "pkg/h.go", LineStart: 1, Language: "go", Content: "func cafebabe12() {}"},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := e.Query(context.Background(), QueryRequest{Action: "fuzzy", Query: "cafebabe12"})
	if err != nil {
		t.Fatal(err)
	}
	if got := qnsOf(hitsOf(out)); len(got) != 1 || got[0] != "pkg/h.go::cafebabe12" {
		t.Fatalf("fuzzy hits = %v, want pkg/h.go::cafebabe12", got)
	}
}

// TestCommitFullSHAMatchesItsRowAtL1 pins the 40-character rule: the full SHA
// names git:commit:<sha> exactly, so the ladder answers at L1.
func TestCommitFullSHAMatchesItsRowAtL1(t *testing.T) {
	e, _ := newEngine(t)
	if err := e.st.UpsertElements(context.Background(), []store.Element{commitRow(shaA, "fix the parser")}); err != nil {
		t.Fatal(err)
	}
	out, err := e.Query(context.Background(), QueryRequest{Query: shaA})
	if err != nil {
		t.Fatal(err)
	}
	if r := out["retrieval"].(map[string]any); r["rung"] != "L1" {
		t.Errorf("retrieval = %v, want rung L1 for a full SHA", r)
	}
	if got := qnsOf(hitsOf(out)); len(got) != 1 || got[0] != "git:commit:"+shaA {
		t.Errorf("hits = %v, want the one commit row", got)
	}
}

// TestCommitPrefixCollisionNamesOneCommitAndAmbiguousPrefix pins the prefix
// collision: two commits share their first seven characters. The full SHA of
// one answers with that commit alone; the shared prefix returns both and says
// it is ambiguous.
func TestCommitPrefixCollisionNamesOneCommitAndAmbiguousPrefix(t *testing.T) {
	e, _ := newEngine(t)
	if err := e.st.UpsertElements(context.Background(), []store.Element{
		commitRow(shaA, "fix the parser"), commitRow(shaB, "add the cache"),
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	out, err := e.Query(ctx, QueryRequest{Query: shaA})
	if err != nil {
		t.Fatal(err)
	}
	if got := qnsOf(hitsOf(out)); len(got) != 1 || got[0] != "git:commit:"+shaA {
		t.Errorf("full SHA %s hits = %v, want only its own commit", shaA, got)
	}

	out, err = e.Query(ctx, QueryRequest{Action: "fuzzy", Query: "57c220d"})
	if err != nil {
		t.Fatal(err)
	}
	if got := qnsOf(hitsOf(out)); len(got) != 2 {
		t.Errorf("prefix hits = %v, want both commits", got)
	}
	if g, _ := out["guidance"].(string); !strings.Contains(g, "2 commits") {
		t.Errorf("guidance = %q, want the ambiguity note naming 2 commits", g)
	}
}

// TestCommitRefRequiresADigitBelowTwelve pins the word cases: a 7-to-12 hex
// token needs a digit, so hex-letter words are not SHAs. A 13-to-40 token is
// a SHA without one.
func TestCommitRefRequiresADigitBelowTwelve(t *testing.T) {
	for q, want := range map[string]bool{
		"defaced":         false,
		"effaced":         false,
		"deadbeef":        false,
		"accede":          false,
		"cafebabe12":      true,
		"1234567":         true,
		"deadbeefcafebab": true,
		"ABCDEF0123456789ABCDEF0123456789ABCDEF01": true,
		"zzzzzzz":         false,
		"parse the input": false,
	} {
		if _, got := commitRef(q); got != want {
			t.Errorf("commitRef(%q) ok = %v, want %v", q, got, want)
		}
	}
}
