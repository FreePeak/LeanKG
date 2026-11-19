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

// TestGoQualifiedCallHasNoEdgeYet: store.Open(...) names a package through an
// import, not the same-package Open. No edge is written (cross-package
// resolution is the next slice), so the same-named local Open is not linked.
func TestGoQualifiedCallHasNoEdgeYet(t *testing.T) {
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
