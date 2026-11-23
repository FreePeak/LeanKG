package core

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
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

// widgetPopulation is one name held by a production symbol, a symbol in each
// kind of non-production path, and two docs (one archived). scope=prod keeps
// the production symbol and the current doc; scope=code keeps only the symbol.
func widgetPopulation() []store.Element {
	const body = "rank the merged lists with the widget"
	return []store.Element{
		{QualifiedName: "store/widget.go::Widget", ElementType: "function", Name: "Widget", FilePath: "store/widget.go", Language: "go", Content: body},
		{QualifiedName: "src/widget.test.ts::Widget", ElementType: "function", Name: "Widget", FilePath: "src/widget.test.ts", Language: "typescript", Content: body},
		{QualifiedName: "tests/test_widget.py::Widget", ElementType: "function", Name: "Widget", FilePath: "tests/test_widget.py", Language: "python", Content: body},
		{QualifiedName: "pkg/widget_test.go::Widget", ElementType: "function", Name: "Widget", FilePath: "pkg/widget_test.go", Language: "go", Content: body},
		{QualifiedName: "testdata/fx/widget.go::Widget", ElementType: "function", Name: "Widget", FilePath: "testdata/fx/widget.go", Language: "go", Content: body},
		{QualifiedName: "web/fixtures.js::Widget", ElementType: "function", Name: "Widget", FilePath: "web/fixtures.js", Language: "javascript", Content: body},
		{QualifiedName: "docs/archive/old.md#widget", ElementType: "doc", Name: "Widget", FilePath: "docs/archive/old.md", Language: "markdown", Content: body},
		{QualifiedName: "docs/guide.md#widget", ElementType: "doc", Name: "Widget", FilePath: "docs/guide.md", Language: "markdown", Content: body},
	}
}

// TestScopeProdIsOnePopulationOnEveryRung pins one definition of prod and code
// across L1, L2, L3 and the ladder: test files (any convention), fixtures and
// docs/archive/ are out of prod; markdown is also out of code. Before the
// change prod kept the test-convention files and the archived doc.
func TestScopeProdIsOnePopulationOnEveryRung(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(dir, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(16)
	if err := st.WriteStamp(context.Background(), embed.StampOf(p)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, el := range widgetPopulation() {
		if err := st.UpsertElements(ctx, []store.Element{el}); err != nil {
			t.Fatal(err)
		}
		qvec, err := p.Embed(ctx, embed.Query, []string{el.Content})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertVectors(ctx, p.Identity().Model, []store.VectorRow{{QualifiedName: el.QualifiedName, Vec: qvec[0]}}); err != nil {
			t.Fatal(err)
		}
	}
	e := New(st, Options{Embedder: p, ProjectDir: dir})

	want := map[string][]string{
		"prod": {"docs/guide.md#widget", "store/widget.go::Widget"},
		"code": {"store/widget.go::Widget"},
	}
	rungs := []struct {
		name string
		req  QueryRequest
	}{
		{"L1 exact", QueryRequest{Action: "exact", Query: "Widget"}},
		{"L2 fuzzy", QueryRequest{Action: "fuzzy", Query: "widget"}},
		{"L3 semantic", QueryRequest{Action: "semantic", Query: "rank the merged lists with the widget"}},
		{"ladder", QueryRequest{Query: "Widget"}},
	}
	for scope, wantQNs := range want {
		for _, r := range rungs {
			t.Run(scope+" "+r.name, func(t *testing.T) {
				req := r.req
				req.Args = map[string]any{"scope": scope}
				out, err := e.Query(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				got := qnsOf(hitsOf(out))
				sort.Strings(got)
				if !reflect.DeepEqual(got, wantQNs) {
					t.Errorf("scope=%s hits = %v, want %v", scope, got, wantQNs)
				}
			})
		}
	}
}

// TestScopeProdIsOnePopulationOnCallers pins the same population on the graph
// verbs: callers under prod drop the test-convention caller, the archived doc
// caller, and keep the current doc caller; code drops every doc caller.
func TestScopeProdIsOnePopulationOnCallers(t *testing.T) {
	e, _ := newEngine(t)
	const target = "store/widget.go::Widget"
	callers := map[string]string{
		"store/use.go::Run":            "store/use.go",
		"src/widget.test.ts::TestW":    "src/widget.test.ts",
		"tests/test_widget.py::test_w": "tests/test_widget.py",
		"docs/archive/old.md#old":      "docs/archive/old.md",
		"docs/guide.md#guide":          "docs/guide.md",
	}
	els := []store.Element{{QualifiedName: target, ElementType: "function", Name: "Widget", FilePath: "store/widget.go", LineStart: 1, Language: "go"}}
	var rels []store.Relationship
	for qn, file := range callers {
		typ := "function"
		if strings.HasSuffix(file, ".md") {
			typ = "section"
		}
		els = append(els, store.Element{QualifiedName: qn, ElementType: typ, Name: "c", FilePath: file, LineStart: 2, Language: "go"})
		rels = append(rels, store.Relationship{Source: qn, Target: target, RelType: "calls", Confidence: 1, Metadata: map[string]any{"line": float64(4)}})
	}
	if err := e.st.UpsertElements(context.Background(), els); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertRelationships(context.Background(), rels); err != nil {
		t.Fatal(err)
	}
	for scope, want := range map[string][]string{
		"prod": {"docs/guide.md#guide", "store/use.go::Run"},
		"code": {"store/use.go::Run"},
	} {
		out := callersOf(t, e, target, map[string]any{"scope": scope})
		got, _ := out["callers"].([]string)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("callers scope=%s = %v, want %v", scope, got, want)
		}
	}
}
