package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// elementSet renders every element as "<qn> <type> <start>-<end>" so a test
// pins names, kinds, owners (inside the qualified name) and line ranges at
// once. The list is sorted; the expected lists are sorted the same way.
func elementSet(fe fileElements) []string {
	out := make([]string, 0, len(fe.elements))
	for _, e := range fe.elements {
		out = append(out, fmt.Sprintf("%s %s %d-%d", e.qn, e.etype, e.start, e.end))
	}
	slices.Sort(out)
	return out
}

// TestGoDefinitionsAreElements pins the exact element set the Go extractor
// emits for each declaration shape. Package-level const and var names are
// elements, every type kind is a type element, an interface is one element
// (its method specs are not), a generic receiver owns its method by the bare
// type name, and text inside a comment or a blank name is never an element.
func TestGoDefinitionsAreElements(t *testing.T) {
	cases := []struct {
		name, rel, src string
		want           []string
	}{
		{
			name: "const block",
			rel:  "a.go",
			src:  "package a\n\nconst (\n\tA = 1\n\tB, C = 2, 3\n\t_ = 4\n)\n",
			want: []string{
				"a.go::A constant 4-4",
				"a.go::B constant 5-5",
				"a.go::C constant 5-5",
			},
		},
		{
			name: "grouped var block and var with initializer",
			rel:  "a.go",
			src:  "package a\n\nvar (\n\tembeddedVersion = \"1.0\"\n\tok bool\n)\n\nvar x = compute()\n\nfunc compute() int { return 1 }\n",
			want: []string{
				"a.go::embeddedVersion variable 4-4",
				"a.go::ok variable 5-5",
				"a.go::x variable 8-8",
				"a.go::compute function 10-10",
			},
		},
		{
			name: "defined, func and alias types",
			rel:  "a.go",
			src:  "package a\n\ntype Name string\n\ntype Handler func()\n\ntype Alias = int\n\ntype Point struct {\n\tX int\n}\n",
			want: []string{
				"a.go::Name type 3-3",
				"a.go::Handler type 5-5",
				"a.go::Alias type 7-7",
				"a.go::Point type 9-11",
			},
		},
		{
			name: "interface is one element, method specs are not",
			rel:  "a.go",
			src:  "package a\n\ntype Reader interface {\n\tRead(p []byte) (int, error)\n\tClose() error\n}\n",
			want: []string{
				"a.go::Reader type 3-6",
			},
		},
		{
			name: "generic receiver owns its method by the bare type name",
			rel:  "a.go",
			src:  "package a\n\ntype Set[T comparable] struct {\n\tm map[T]struct{}\n}\n\nfunc (s *Set[T]) Add(v T) {\n\ts.m[v] = struct{}{}\n}\n",
			want: []string{
				"a.go::Set type 3-5",
				"a.go::Set.Add method 7-9",
			},
		},
		{
			name: "a func named inside a block comment is not an element",
			rel:  "a.go",
			src:  "package a\n\n/*\nfunc Ghost() {}\n*/\nfunc Real() {}\n",
			want: []string{
				"a.go::Real function 6-6",
			},
		},
		{
			name: "TestFoo in a _test.go file is a function",
			rel:  "a_test.go",
			src:  "package a\n\nimport \"testing\"\n\nfunc TestFoo(t *testing.T) {\n\tt.Log(\"x\")\n}\n",
			want: []string{
				"a_test.go::TestFoo function 5-7",
			},
		},
		{
			name: "blank identifiers are not elements",
			rel:  "a.go",
			src:  "package a\n\nvar _ I = (*T)(nil)\n\nconst _ = 1\n\nvar _, kept = 1, 2\n",
			want: []string{
				"a.go::kept variable 7-7",
			},
		},
		{
			name: "embedded struct fields are not elements",
			rel:  "a.go",
			src:  "package a\n\ntype Inner struct{}\n\ntype Outer struct {\n\tInner\n\t*Other\n\tX int\n}\n",
			want: []string{
				"a.go::Inner type 3-3",
				"a.go::Outer type 5-9",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := elementSet(extractAs(t, c.rel, []byte(c.src), "go"))
			want := slices.Clone(c.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("element set mismatch\n got: %q\nwant: %q", got, want)
			}
		})
	}
}

// TestGoParseErrorFallsBackToRegex pins that a file the Go parser rejects is
// still indexed: the regex tier answers and its function elements survive.
func TestGoParseErrorFallsBackToRegex(t *testing.T) {
	src := "package a\n\nfunc Good() {}\n\nfunc Broken( {\n"
	got := elementSet(extractAs(t, "a.go", []byte(src), "go"))
	if !slices.Contains(got, "a.go::Good function 3-3") {
		t.Fatalf("regex fallback lost Good: %q", got)
	}
}

// TestGoImportAliasesRecorded pins that explicit import aliases are kept on
// the file result (alias -> import path). Blank and unaliased imports carry
// no alias and are not recorded.
func TestGoImportAliasesRecorded(t *testing.T) {
	src := "package a\n\nimport (\n\tcfg \"github.com/x/config\"\n\t\"fmt\"\n\t_ \"embed\"\n)\n\nfunc F() { fmt.Println(cfg.X) }\n"
	fe := extractAs(t, "a.go", []byte(src), "go")
	if len(fe.imports) != 1 || fe.imports["cfg"] != "github.com/x/config" {
		t.Fatalf("imports = %v, want map[cfg:github.com/x/config]", fe.imports)
	}
}

// TestGoPackageLevelDeclarationsAreQueryable indexes a repository shaped
// like this one (an embeddedVersion var, a version const, a ToolAuthenticate
// const) and asserts each is a stored element at its declaration line.
func TestGoPackageLevelDeclarationsAreQueryable(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(dir, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"cmd/leankg/version.go":  "package main\n\nvar embeddedVersion = \"0.1.0\"\n",
		"internal/core/core.go":  "package core\n\nconst (\n\tToolQuery = \"query\"\n\tToolAuthenticate = \"authenticate\"\n)\n",
		"internal/mcp/server.go": "package mcp\n\nconst version = \"0.1.0\"\n",
	}
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := IndexDir(context.Background(), st, dir); err != nil {
		t.Fatalf("IndexDir: %v", err)
	}

	cases := []struct {
		qn, etype string
		line      int
	}{
		{"cmd/leankg/version.go::embeddedVersion", "variable", 3},
		{"internal/core/core.go::ToolAuthenticate", "constant", 5},
		{"internal/mcp/server.go::version", "constant", 3},
	}
	for _, c := range cases {
		els, err := st.FindExact(context.Background(), c.qn)
		if err != nil {
			t.Fatal(err)
		}
		if len(els) != 1 {
			t.Fatalf("FindExact(%q): got %d elements, want 1", c.qn, len(els))
		}
		if el := els[0]; el.ElementType != c.etype || el.LineStart != c.line {
			t.Errorf("%s: type=%q line=%d, want type=%q line=%d", c.qn, el.ElementType, el.LineStart, c.etype, c.line)
		}
	}
}

// seedSet renders the name seeds of the element named name as "label line call"
// strings, in source order, so a test pins kind, name, line and call flag.
func seedSet(t *testing.T, fe fileElements, name string) []string {
	t.Helper()
	for _, e := range fe.elements {
		if e.name != name {
			continue
		}
		var out []string
		for _, s := range e.seeds {
			label := s.Name
			if s.Kind == "qualified" {
				label = s.Alias + "." + s.Name
			}
			out = append(out, fmt.Sprintf("%s:%s %d %t", s.Kind, label, s.Line, s.Call))
		}
		return out
	}
	t.Fatalf("no element named %q", name)
	return nil
}

// TestGoNameSeeds pins what the AST walk records per element: calls versus
// references, the qualified/method/ident kinds, declared names and locals that
// are never uses, and the struct-field and composite-literal keys that are.
func TestGoNameSeeds(t *testing.T) {
	src := "package a\n\n" +
		"import (\n\tcfg \"x/config\"\n\t\"x/util/v2\"\n)\n\n" +
		"type Widget struct{}\n\n" +
		"func F(w *Widget) int {\n\treturn helper(w.Name)\n}\n\n" +
		"func G(s Widget) {\n\tcfg.Load()\n\tutil.Do(s.Run(), cfg.Value)\n}\n\n" +
		"func H(items []Widget) int {\n\ttotal := 0\n\tfor _, it := range items {\n\t\ttotal += it.N\n\t}\n\treturn total\n}\n\n" +
		"type Config struct {\n\tName  string\n\tWidth Widget\n}\n\n" +
		"var def = Config{Name: fallback()}\n"
	fe := extractAs(t, "a.go", []byte(src), "go")
	cases := []struct {
		name string
		want []string
	}{
		{"F", []string{"ident:Widget 10 false", "ident:int 10 false", "ident:helper 11 true"}},
		{"G", []string{"ident:Widget 14 false", "qualified:cfg.Load 15 true", "qualified:util.Do 16 true", "method:Run 16 true", "qualified:cfg.Value 16 false"}},
		{"H", []string{"ident:Widget 19 false", "ident:int 19 false"}},
		{"Config", []string{"ident:string 28 false", "ident:Widget 29 false"}},
		{"def", []string{"ident:Config 32 false", "ident:fallback 32 true"}},
	}
	for _, c := range cases {
		got := seedSet(t, fe, c.name)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s seeds:\n got: %q\nwant: %q", c.name, got, c.want)
		}
	}
}
