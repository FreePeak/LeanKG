package index

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// edgeTo returns the edge src -> dst of type rel, read back from the store.
func edgeTo(t *testing.T, st store.Backend, src, dst, rel string) (store.Relationship, bool) {
	t.Helper()
	rels, err := st.Outgoing(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rels {
		if r.Target == dst && r.RelType == rel {
			return r, true
		}
	}
	return store.Relationship{}, false
}

// lineMeta reads the call-site line an edge carries in its metadata.
func lineMeta(r store.Relationship) (int, bool) {
	v, ok := r.Metadata["line"].(float64)
	return int(v), ok
}

// TestGoCallPastContentClipKeepsEdgeAndLine: the call sits more than the
// 8000-character content bound from the element's start. The AST walks the
// whole body, so the edge exists and carries the call-site line.
func TestGoCallPastContentClipKeepsEdgeAndLine(t *testing.T) {
	const filler = 1500
	var b strings.Builder
	b.WriteString("package a\n\nfunc Big() int {\n")
	b.WriteString(strings.Repeat("\t_ = 1\n", filler))
	b.WriteString("\treturn Target()\n}\n\nfunc Target() int { return 2 }\n")
	st := indexedGraph(t, map[string]string{"a.go": b.String()})

	r, ok := edgeTo(t, st, "a.go::Big", "a.go::Target", "calls")
	if !ok {
		t.Fatal("call past the 8000-char clip lost its calls edge")
	}
	want := 3 + filler + 1
	if line, _ := lineMeta(r); line != want {
		t.Fatalf("call-site line = %d (metadata %v), want %d", line, r.Metadata, want)
	}
}

// TestGoCommentNamingFunctionIsNoEdge: a doc comment and a body comment name
// Target. Comments are not code, so neither produces an edge.
func TestGoCommentNamingFunctionIsNoEdge(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\nfunc Target() {}\n\n" +
		"// Caller explains that Target is unrelated.\nfunc Caller() {\n\t// Target is mentioned here too.\n\t_ = 1\n}\n"})
	if got := edgeTypes(t, st, "a.go::Caller", "a.go::Target"); len(got) != 0 {
		t.Fatalf("comment prose produced edges %v", got)
	}
}

// TestGoConstReferenceIsReferencesEdgeWithLine: a package-level const read in a
// function is a value use, so it is a references edge at the use line.
func TestGoConstReferenceIsReferencesEdgeWithLine(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\nconst Limit = 5\n\n" +
		"func F() int {\n\treturn Limit\n}\n"})
	r, ok := edgeTo(t, st, "a.go::F", "a.go::Limit", "references")
	if !ok {
		t.Fatalf("const read in F has no references edge; edge types %v", edgeTypes(t, st, "a.go::F", "a.go::Limit"))
	}
	if line, _ := lineMeta(r); line != 6 {
		t.Fatalf("reference line = %d, want 6", line)
	}
}

// TestGoLocalShadowingPackageNameIsNoEdge: a local := and a parameter named like
// package-level functions are declared in the function, so their uses are not
// references to the package-level names.
func TestGoLocalShadowingPackageNameIsNoEdge(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\n" +
		"func count() int { return 1 }\n\nfunc Total() int { return 1 }\n\n" +
		"func F() int {\n\tcount := 2\n\treturn count\n}\n\n" +
		"func G(Total int) int { return Total }\n"})
	if got := edgeTypes(t, st, "a.go::F", "a.go::count"); len(got) != 0 {
		t.Fatalf("local count shadows the package func but produced edges %v", got)
	}
	if got := edgeTypes(t, st, "a.go::G", "a.go::Total"); len(got) != 0 {
		t.Fatalf("parameter Total shadows the package func but produced edges %v", got)
	}
}

// TestGoMethodByNameEdgeIsLowConfidenceWithLine: x.Run() where x is not an
// import resolves to the same-package method Run at confidence 0.4, with the
// call-site line and the resolution recorded in metadata.
func TestGoMethodByNameEdgeIsLowConfidenceWithLine(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\ntype T struct{}\n\n" +
		"func (T) Run() {}\n\nfunc Caller(t T) {\n\tt.Run()\n}\n"})
	r, ok := edgeTo(t, st, "a.go::Caller", "a.go::T.Run", "calls")
	if !ok {
		t.Fatalf("x.Run() has no calls edge to T.Run; edge types %v", edgeTypes(t, st, "a.go::Caller", "a.go::T.Run"))
	}
	if r.Confidence != 0.4 {
		t.Fatalf("confidence = %v, want 0.4", r.Confidence)
	}
	if line, _ := lineMeta(r); line != 8 {
		t.Fatalf("call-site line = %d, want 8", line)
	}
	if got, _ := r.Metadata["resolution"].(string); got != "method-by-name" {
		t.Fatalf("resolution = %q, want method-by-name", got)
	}
}

// TestGoQualifiedCallWithoutRepoPackageHasNoEdge: store.Open(...) names a package
// through an import, not the same-package Open. The import's package is not in
// the run (an external path), so nothing resolves and the same-named local Open
// is not linked.
func TestGoQualifiedCallWithoutRepoPackageHasNoEdge(t *testing.T) {
	st := indexedGraph(t, map[string]string{
		"a.go": "package a\n\nimport \"example.com/x/store\"\n\nfunc Use() {\n\tstore.Open(\"p\")\n}\n",
		"b.go": "package a\n\nfunc Open(p string) {}\n",
	})
	if got := edgeTypes(t, st, "a.go::Use", "b.go::Open"); len(got) != 0 {
		t.Fatalf("qualified store.Open produced an edge to the same-package Open: %v", got)
	}
}

// TestGoQualifiedSeedCarriedOnFileResult: the qualified call is kept on the
// per-file result (alias, name, line, call) for the cross-package resolver.
func TestGoQualifiedSeedCarriedOnFileResult(t *testing.T) {
	src := "package a\n\nimport \"example.com/x/store\"\n\nfunc Use() {\n\tstore.Open(\"p\")\n}\n"
	fe := extractAs(t, "a.go", []byte(src), "go")
	want := qualifiedSeed{Source: "a.go::Use", Alias: "store", Name: "Open", Line: 6, Call: true}
	if !slices.Contains(fe.qualified, want) {
		t.Fatalf("per-file qualified seeds = %+v, want %+v", fe.qualified, want)
	}
}

// TestNonGoCallsUnchanged pins the Python heuristic: a whole-word call matches
// another element at confidence 0.5 and carries no line metadata.
func TestNonGoCallsUnchanged(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.py": "def caller_fn():\n    helper_fn()\n\n\ndef helper_fn():\n    pass\n"})
	rels, err := st.Outgoing(context.Background(), "a.py::caller_fn")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rels {
		if r.RelType == "calls" && r.Target == "a.py::helper_fn" {
			if r.Confidence != 0.5 {
				t.Fatalf("python calls confidence = %v, want 0.5", r.Confidence)
			}
			if _, has := r.Metadata["line"]; has {
				t.Fatalf("python calls edge carries a line: %v", r.Metadata)
			}
			got = append(got, r.RelType)
		}
	}
	if len(got) != 1 {
		t.Fatalf("python calls edge count = %d (%v), want 1", len(got), got)
	}
}

// TestGoReturnedMethodValueIsReference: `return s.Close` hands the method out as
// a value, so it is a references edge at the return line, like a method value
// passed as an argument.
func TestGoReturnedMethodValueIsReference(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\ntype Server struct{}\n\n" +
		"func (s *Server) Close() {}\n\n" +
		"func Closer(s *Server) func() {\n\treturn s.Close\n}\n"})
	r, ok := edgeTo(t, st, "a.go::Closer", "a.go::Server.Close", "references")
	if !ok {
		t.Fatalf("returned method value s.Close has no references edge; edge types %v", edgeTypes(t, st, "a.go::Closer", "a.go::Server.Close"))
	}
	if line, _ := lineMeta(r); line != 8 {
		t.Fatalf("reference line = %d, want 8", line)
	}
}

// TestGoExplicitInstantiationIsCall: Map[int](2) calls the generic Map, so the
// edge is calls, not references.
func TestGoExplicitInstantiationIsCall(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\nfunc Map[T any](v T) T { return v }\n\n" +
		"func Caller() int {\n\treturn Map[int](2)\n}\n"})
	r, ok := edgeTo(t, st, "a.go::Caller", "a.go::Map", "calls")
	if !ok {
		t.Fatalf("Map[int](2) has no calls edge; edge types %v", edgeTypes(t, st, "a.go::Caller", "a.go::Map"))
	}
	if line, _ := lineMeta(r); line != 6 {
		t.Fatalf("call line = %d, want 6", line)
	}
}

// TestGoInstantiatedConstructorKeepsReceiverType: NewSet[int]() is a call of the
// generic constructor, and its result still types s, so s.Add(1) links to
// Set.Add.
func TestGoInstantiatedConstructorKeepsReceiverType(t *testing.T) {
	st := indexedGraph(t, map[string]string{"a.go": "package a\n\ntype Set[T any] struct{ items []T }\n\n" +
		"func NewSet[T any]() *Set[T] { return &Set[T]{} }\n\n" +
		"func (s *Set[T]) Add(v T) {}\n\n" +
		"func Use() {\n\ts := NewSet[int]()\n\ts.Add(1)\n}\n"})
	if _, ok := edgeTo(t, st, "a.go::Use", "a.go::NewSet", "calls"); !ok {
		t.Fatalf("NewSet[int]() has no calls edge; edge types %v", edgeTypes(t, st, "a.go::Use", "a.go::NewSet"))
	}
	if _, ok := edgeTo(t, st, "a.go::Use", "a.go::Set.Add", "calls"); !ok {
		t.Fatal("s.Add(1) lost its edge: the instantiated constructor did not type s")
	}
}

// TestGoBlockLocalNameHidesAliasForWholeDeclaration pins the scope ceiling of
// seedWalker (hardening, no red test: it documents current behaviour). A name
// bound in one block hides the import alias of the same name for the whole
// declaration, so pkg.Target() after the block loses its qualified seed and
// becomes a method seed on an unknown receiver.
func TestGoBlockLocalNameHidesAliasForWholeDeclaration(t *testing.T) {
	src := "package a\n\nimport \"x/pkg\"\n\nfunc F(ok bool) {\n\tif ok {\n\t\tpkg := 1\n\t\t_ = pkg\n\t}\n\tpkg.Target()\n}\n"
	fe := extractAs(t, "a.go", []byte(src), "go")
	got := seedSet(t, fe, "F")
	if want := []string{"ident:bool 5 false", "method:Target 10 true"}; !slices.Equal(got, want) {
		t.Fatalf("F seeds:\n got: %q\nwant: %q", got, want)
	}
}

// TestGoExternalTestPackageIsSeparateNamespace: ext_test.go is package p_test, a
// separate package of the same directory. Its Shared is not a name of package p,
// so Caller links to p's own Shared and to its same-package sibling Util, never
// to the external test's Shared.
func TestGoExternalTestPackageIsSeparateNamespace(t *testing.T) {
	st := indexedGraph(t, map[string]string{
		"p/a.go":        "package p\n\nfunc Caller() {\n\tShared()\n\tUtil()\n}\n\nfunc Shared() {}\n",
		"p/util.go":     "package p\n\nfunc Util() {}\n",
		"p/ext_test.go": "package p_test\n\nfunc Shared() {}\n",
	})
	if _, ok := edgeTo(t, st, "p/a.go::Caller", "p/a.go::Shared", "calls"); !ok {
		t.Fatal("same-package Shared lost its calls edge")
	}
	if _, ok := edgeTo(t, st, "p/a.go::Caller", "p/util.go::Util", "calls"); !ok {
		t.Fatal("same-package sibling Util lost its calls edge")
	}
	if _, ok := edgeTo(t, st, "p/a.go::Caller", "p/ext_test.go::Shared", "calls"); ok {
		t.Fatal("package p_test's Shared received the calls edge of package p's Caller")
	}
}

// TestGoMainPackageAndItsTestPackageAreSeparate: a directory of package main and
// its external test package main_test. Run in main_test is not a name of main.
func TestGoMainPackageAndItsTestPackageAreSeparate(t *testing.T) {
	st := indexedGraph(t, map[string]string{
		"cmd/main.go":      "package main\n\nfunc main() {\n\tRun()\n}\n\nfunc Run() {}\n",
		"cmd/main_test.go": "package main_test\n\nfunc Run() {}\n",
	})
	if _, ok := edgeTo(t, st, "cmd/main.go::main", "cmd/main.go::Run", "calls"); !ok {
		t.Fatal("main's call of Run lost its calls edge")
	}
	if _, ok := edgeTo(t, st, "cmd/main.go::main", "cmd/main_test.go::Run", "calls"); ok {
		t.Fatal("package main_test's Run received the calls edge of main")
	}
}

// TestGoExternalTestPackageLeavesOnePackageElement: a directory's package element
// is one per directory, whatever packages its files declare. The external test
// package adds no second element and keeps the directory's element name.
func TestGoExternalTestPackageLeavesOnePackageElement(t *testing.T) {
	st := indexedGraph(t, map[string]string{
		"p/a.go":        "package p\n\nfunc A() {}\n",
		"p/ext_test.go": "package p_test\n\nfunc B() {}\n",
	})
	els, err := st.Elements(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var pkgs []string
	for _, e := range els {
		if e.ElementType == packageElementType {
			pkgs = append(pkgs, e.QualifiedName)
		}
	}
	if !slices.Equal(pkgs, []string{"p"}) {
		t.Fatalf("package elements = %q, want exactly [p]", pkgs)
	}
}
