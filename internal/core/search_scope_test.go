package core

import (
	"context"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestKeywordRungRanksCodeAboveDocs pins the doc demotion: a markdown section
// that matches the question as well as a Go function must not rank above it.
// Before this change the doc section won the bm25 ranking and the first answer
// was a CLAUDE.md chunk instead of the symbol that answers the question.
func TestKeywordRungRanksCodeAboveDocs(t *testing.T) {
	e, _ := newEngine(t)
	els := []store.Element{
		{QualifiedName: "CLAUDE.md#embed-version", ElementType: "doc", Name: "Embed version",
			FilePath: "CLAUDE.md", Language: "markdown",
			Content: "The embed version source of truth is the version file. Version source of truth."},
		{QualifiedName: "cmd/leankg/version.go::embedVersion", ElementType: "function", Name: "embedVersion",
			FilePath: "cmd/leankg/version.go", Language: "go",
			Content: "func embedVersion() string { return version } // version source of truth"},
	}
	if err := e.st.UpsertElements(context.Background(), els); err != nil {
		t.Fatal(err)
	}
	out, err := e.Query(context.Background(), QueryRequest{Query: "embed version source of truth"})
	if err != nil {
		t.Fatal(err)
	}
	hits := hitsOf(out)
	if len(hits) < 2 {
		t.Fatalf("want both the doc and the code hit, got %v", qnsOf(hits))
	}
	if got := hits[0]["qualified_name"]; got != "cmd/leankg/version.go::embedVersion" {
		t.Fatalf("first hit = %v, want the Go symbol above the doc section: %v", got, qnsOf(hits))
	}
}

// TestScopeCodeAppliesOnEveryKeywordRung pins that scope=code is a population
// rule, not a label: the L1 exact rung and the L2 keyword rung both drop
// markdown hits, and the answer still names scope=code.
func TestScopeCodeAppliesOnEveryKeywordRung(t *testing.T) {
	e, _ := newEngine(t)
	els := []store.Element{
		{QualifiedName: "CLAUDE.md#widget", ElementType: "doc", Name: "Widget",
			FilePath: "CLAUDE.md", Language: "markdown", Content: "rank the merged lists with the widget"},
		{QualifiedName: "docs/guide.md#widget", ElementType: "doc", Name: "Widget",
			FilePath: "docs/guide.md", Language: "markdown", Content: "rank the merged lists with the widget"},
		{QualifiedName: "store/widget.go::Widget", ElementType: "function", Name: "Widget",
			FilePath: "store/widget.go", Language: "go", Content: "func Widget() {} // rank the merged lists"},
	}
	if err := e.st.UpsertElements(context.Background(), els); err != nil {
		t.Fatal(err)
	}
	scoped := map[string]any{"scope": "code"}

	t.Run("L1 exact", func(t *testing.T) {
		out, err := e.Query(context.Background(), QueryRequest{Query: "Widget", Args: scoped})
		if err != nil {
			t.Fatal(err)
		}
		requireOnlyCodeHits(t, out, "L1")
	})
	t.Run("L2 keyword", func(t *testing.T) {
		out, err := e.Query(context.Background(), QueryRequest{Action: "fuzzy", Query: "rank the merged lists", Args: scoped})
		if err != nil {
			t.Fatal(err)
		}
		requireOnlyCodeHits(t, out, "L2")
	})
}

// requireOnlyCodeHits fails when a scope=code answer carries a markdown hit, or
// does not name its scope and its rung.
func requireOnlyCodeHits(t *testing.T, out map[string]any, rung string) {
	t.Helper()
	hits := hitsOf(out)
	if len(hits) != 1 || hits[0]["qualified_name"] != "store/widget.go::Widget" {
		t.Fatalf("%s scope=code hits = %v, want only store/widget.go::Widget", rung, qnsOf(hits))
	}
	r := out["retrieval"].(map[string]any)
	if r["rung"] != rung[:2] || r["scope"] != "code" {
		t.Fatalf("%s retrieval = %v, want rung %s and scope code", rung, r, rung[:2])
	}
	if strings.HasSuffix(fmtPath(hits[0]), ".md") {
		t.Fatalf("%s returned a markdown hit under scope=code", rung)
	}
}

func fmtPath(h map[string]any) string {
	p, _ := h["file_path"].(string)
	return p
}

// TestCommitLookupIsHexOnly pins which queries are commit references: 7 to 40
// hex characters. Anything else keeps the query text and the default history
// population.
func TestCommitLookupIsHexOnly(t *testing.T) {
	for q, want := range map[string]struct {
		prefix string
		ok     bool
	}{
		"57c220d": {"57c220d", true},
		"57C220D56FDA547F7F7D27CB86DCC53F51EC9DF6": {"57c220d", true},
		"deadbeef":        {"deadbee", true},
		"cafe":            {"cafe", false},
		"accede":          {"accede", false},
		"zzzzzzz":         {"zzzzzzz", false},
		"parse the input": {"parse the input", false},
		"57c220d56fda547f7f7d27cb86dcc53f51ec9df67": {"57c220d56fda547f7f7d27cb86dcc53f51ec9df67", false},
	} {
		got, ok := commitLookup(q)
		if got != want.prefix || ok != want.ok {
			t.Errorf("commitLookup(%q) = (%q, %v), want (%q, %v)", q, got, ok, want.prefix, want.ok)
		}
	}
}
