package graph

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// qualityFixture: functions of assorted spans, one method and one class that
// the scan must ignore (Rust filters element_type = "function" exactly).
func qualityFixture(t *testing.T) store.Backend {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	els := []store.Element{
		{QualifiedName: "a.go::fn50", ElementType: "function", Name: "fn50", FilePath: "a.go", LineStart: 1, LineEnd: 50, Language: "go"},
		{QualifiedName: "a.go::fn49", ElementType: "function", Name: "fn49", FilePath: "a.go", LineStart: 60, LineEnd: 108, Language: "go"},
		{QualifiedName: "b.go::fn200", ElementType: "function", Name: "fn200", FilePath: "b.go", LineStart: 10, LineEnd: 209, Language: "go"},
		{QualifiedName: "b.go::fn60", ElementType: "function", Name: "fn60", FilePath: "b.go", LineStart: 300, LineEnd: 359, Language: "go"},
		{QualifiedName: "c.py::py60", ElementType: "function", Name: "py60", FilePath: "c.py", LineStart: 1, LineEnd: 60, Language: "python"},
		{QualifiedName: "d.go::M.Method", ElementType: "method", Name: "Method", FilePath: "d.go", LineStart: 1, LineEnd: 400, Language: "go"},
		{QualifiedName: "d.go::Big", ElementType: "class", Name: "Big", FilePath: "d.go", LineStart: 1, LineEnd: 500, Language: "go"},
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	return s
}

func names(els []store.Element) []string {
	out := make([]string, 0, len(els))
	for _, e := range els {
		out = append(out, e.Name)
	}
	return out
}

func TestOversizedFunctionsThresholdsAndOrder(t *testing.T) {
	st := qualityFixture(t)

	got, err := OversizedFunctions(st, 50, "")
	if err != nil {
		t.Fatalf("OversizedFunctions: %v", err)
	}
	// Longest first: fn200 (200), fn60 (60), py60 (60), fn50 (50); fn49 is
	// below the threshold, the method and class are not functions.
	want := "fn200,fn60,py60,fn50"
	if strings.Join(names(got), ",") != want {
		t.Fatalf("names = %v, want %s", names(got), want)
	}
}

func TestOversizedFunctionsLanguageFilterAndBoundary(t *testing.T) {
	st := qualityFixture(t)

	got, err := OversizedFunctions(st, 50, "python")
	if err != nil {
		t.Fatalf("OversizedFunctions: %v", err)
	}
	if strings.Join(names(got), ",") != "py60" {
		t.Fatalf("python names = %v, want [py60]", names(got))
	}

	// The threshold is inclusive: a 49-line span is out, 50 is in.
	got, err = OversizedFunctions(st, 200, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(got), ",") != "fn200" {
		t.Fatalf("minLines=200 names = %v, want [fn200]", names(got))
	}
	got, err = OversizedFunctions(st, 201, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("minLines=201 matches = %v, want none", names(got))
	}
}

func TestOversizedTieBreakByQualifiedName(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	els := []store.Element{
		{QualifiedName: "z.go::zz", ElementType: "function", Name: "zz", FilePath: "z.go", LineStart: 1, LineEnd: 80, Language: "go"},
		{QualifiedName: "a.go::aa", ElementType: "function", Name: "aa", FilePath: "a.go", LineStart: 1, LineEnd: 80, Language: "go"},
	}
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	got, err := OversizedFunctions(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(got), ",") != "aa,zz" {
		t.Fatalf("tie order = %v, want [aa zz] (qualified-name tie-break)", names(got))
	}
}

func TestRenderOversized(t *testing.T) {
	if got := RenderOversized(nil, 50); got != "No functions found with >= 50 lines\n" {
		t.Fatalf("empty render = %q", got)
	}
	els := []store.Element{
		{Name: "fn200", FilePath: "b.go", LineStart: 10, LineEnd: 209},
		{Name: "fn50", FilePath: "a.go", LineStart: 1, LineEnd: 50},
	}
	want := "Found 2 oversized function(s) (>=50 lines):\n" +
		"  - fn200 (200 lines, b.go:10)\n" +
		"  - fn50 (50 lines, a.go:1)\n"
	if got := RenderOversized(els, 50); got != want {
		t.Fatalf("RenderOversized = %q, want %q", got, want)
	}
}
