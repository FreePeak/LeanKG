package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// setupProject copies the testdata fixtures into a fresh temp project with a
// migrated store and returns both.
func setupProject(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"sample.go", "sample.rs", "sample.py", "sample.ts", "sample.md"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return dir, st
}

func index(t *testing.T, st *store.Store, dir string) Result {
	t.Helper()
	res, err := IndexDir(context.Background(), st, dir)
	if err != nil {
		t.Fatalf("IndexDir: %v", err)
	}
	return res
}

// mustFind asserts exactly one element with the given qualified name and
// returns it.
func mustFind(t *testing.T, st *store.Store, qn string) store.Element {
	t.Helper()
	els, err := st.FindExact(qn)
	if err != nil {
		t.Fatal(err)
	}
	if len(els) != 1 {
		t.Fatalf("FindExact(%q): got %d elements, want 1", qn, len(els))
	}
	return els[0]
}

func assertNoElement(t *testing.T, st *store.Store, name string) {
	t.Helper()
	els, err := st.FindExact(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(els) != 0 {
		t.Fatalf("FindExact(%q): got %d elements, want 0", name, len(els))
	}
	hits, err := st.FindFuzzy(name, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("FindFuzzy(%q): %d stale FTS hits, want 0", name, len(hits))
	}
}

func TestIndexDirElements(t *testing.T) {
	dir, st := setupProject(t)
	res := index(t, st, dir)
	if res.Files != 5 || res.Elements != 16 || res.Skipped != 0 {
		t.Fatalf("first run: %+v, want Files=5 Elements=16 Skipped=0", res)
	}

	cases := []struct {
		qn     string
		etype  string
		lang   string
		parent string
		start  int
		end    int
	}{
		{"sample.go::Widget", "type", "go", "", 5, 8},
		{"sample.go::greet", "function", "go", "", 9, 12},
		{"sample.go::Widget.Shout", "method", "go", "", 13, 15},
		{"sample.rs::Config", "type", "rust", "", 1, 4},
		{"sample.rs::parse", "function", "rust", "", 5, 9},
		{"sample.rs::normalize", "function", "rust", "", 10, 12},
		{"sample.py::Greeter", "class", "py", "", 1, 6},
		{"sample.py::Greeter.__init__", "method", "py", "sample.py::Greeter", 2, 3},
		{"sample.py::Greeter.hello", "method", "py", "sample.py::Greeter", 5, 6},
		{"sample.py::standalone", "function", "py", "", 9, 11},
		{"sample.ts::ping", "function", "ts", "", 1, 4},
		{"sample.ts::double", "function", "ts", "", 5, 9},
		{"sample.ts::Math", "class", "ts", "", 10, 14},
		{"sample.md::Design Notes", "doc", "md", "", 1, 11},
		{"sample.md::API", "doc", "md", "sample.md::Design Notes", 5, 11},
		{"sample.md::Details", "doc", "md", "sample.md::API", 9, 11},
	}
	for _, tc := range cases {
		e := mustFind(t, st, tc.qn)
		if e.ElementType != tc.etype || e.Language != tc.lang {
			t.Errorf("%s: got %s/%s, want %s/%s", tc.qn, e.ElementType, e.Language, tc.etype, tc.lang)
		}
		if e.ParentQualified != tc.parent {
			t.Errorf("%s: parent %q, want %q", tc.qn, e.ParentQualified, tc.parent)
		}
		if e.LineStart != tc.start || e.LineEnd != tc.end {
			t.Errorf("%s: lines %d-%d, want %d-%d", tc.qn, e.LineStart, e.LineEnd, tc.start, tc.end)
		}
		if e.Content == "" {
			t.Errorf("%s: empty content", tc.qn)
		}
	}
}

func TestRelationships(t *testing.T) {
	dir, st := setupProject(t)
	res := index(t, st, dir)
	want := map[string]int{
		"sample.go": 3, // greet->Widget, Shout->greet, Shout->Widget
		"sample.rs": 3, // parse->Config, parse->normalize, normalize->Config
		"sample.py": 6, // 2 contains + Greeter mentions its methods + standalone->Greeter/hello
		"sample.ts": 1, // ping->double
		"sample.md": 2, // Design Notes->API, API->Details (contains)
	}
	// Cross-file: sample.go greet's content says `hello %s`, matching
	// sample.py::Greeter.hello -> one more calls edge.
	if res.Relationships != 16 {
		t.Fatalf("Relationships = %d, want 16 (%v)", res.Relationships, want)
	}
}

// TestRelationshipsPerFile exercises the edge rules directly: dedupe,
func TestRelationshipsPerFile(t *testing.T) {
	fe, err := extractFile("sample.go", filepath.Join("testdata", "sample.go"))
	if err != nil {
		t.Fatal(err)
	}
	rels := goRels(fe)
	wantCalls := [][2]string{
		{"sample.go::greet", "sample.go::Widget"},
		{"sample.go::Widget.Shout", "sample.go::greet"},
		{"sample.go::Widget.Shout", "sample.go::Widget"},
	}
	if len(rels) != len(wantCalls) {
		t.Fatalf("go edges = %d (%v), want %d (dedupe/self-drop broken)", len(rels), rels, len(wantCalls))
	}
	for _, pair := range wantCalls {
		found := false
		for _, r := range rels {
			if r.Source == pair[0] && r.Target == pair[1] {
				if r.RelType != "calls" || r.Confidence != 0.5 {
					t.Errorf("edge %v: got %s/%v, want calls/0.5", pair, r.RelType, r.Confidence)
				}
				found = true
			}
		}
		if !found {
			t.Errorf("missing calls edge %v", pair)
		}
	}

	// Python: contains edges at 1.0 coexisting with same-pair calls at 0.5.
	py, err := extractFile("sample.py", filepath.Join("testdata", "sample.py"))
	if err != nil {
		t.Fatal(err)
	}
	pyRels := goRels(py)
	byType := map[string]int{}
	for _, r := range pyRels {
		byType[r.RelType]++
		if r.RelType == "contains" && r.Confidence != 1.0 {
			t.Errorf("contains edge %v->%v confidence %v, want 1.0", r.Source, r.Target, r.Confidence)
		}
	}
	if byType["contains"] != 2 || byType["calls"] != 4 {
		t.Fatalf("python edges: %v, want 2 contains + 4 calls (%v)", byType, pyRels)
	}
	for _, pair := range [][2]string{
		{"sample.py::Greeter", "sample.py::Greeter.__init__"},
		{"sample.py::Greeter", "sample.py::Greeter.hello"},
		{"sample.py::standalone", "sample.py::Greeter"},
		{"sample.py::standalone", "sample.py::Greeter.hello"},
	} {
		found := false
		for _, r := range pyRels {
			if r.Source == pair[0] && r.Target == pair[1] {
				found = true
			}
		}
		if !found {
			t.Errorf("missing python edge %v", pair)
		}
	}
}

// goRels builds the this-run name map for one file's elements and computes
// its relationship batch.
func goRels(fe fileElements) []store.Relationship {
	names := map[string][]string{}
	for _, e := range fe.elements {
		names[e.name] = append(names[e.name], e.qn)
	}
	return relationships(fe.elements, names)
}

func TestSkipList(t *testing.T) {
	dir, st := setupProject(t)
	skipped := map[string]string{
		".git": "x.go", "node_modules": "a.js", "vendor": "b.go",
		"dist": "c.js", "build": "d.go", ".hidden": "e.go", "target": "f.rs",
	}
	for d, f := range skipped {
		p := filepath.Join(dir, d, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("func skipme() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := index(t, st, dir)
	if res.Files != 5 {
		t.Fatalf("Files = %d, want 5 (skip list leaked)", res.Files)
	}
	assertNoElement(t, st, "skipme")
}

func TestChangeDetection(t *testing.T) {
	dir, st := setupProject(t)
	first := index(t, st, dir)
	if first.Skipped != 0 || first.Files != 5 {
		t.Fatalf("first run: %+v", first)
	}
	count, err := st.ElementCount()
	if err != nil {
		t.Fatal(err)
	}

	// Second run, untouched: everything skipped, no writes.
	second := index(t, st, dir)
	if second.Files != 0 || second.Elements != 0 || second.Skipped != 5 {
		t.Fatalf("second run: %+v, want Skipped=5", second)
	}
	if n, _ := st.ElementCount(); n != count {
		t.Fatalf("element count changed on skip-only run: %d -> %d", count, n)
	}

	// Size+mtime fast path invalidated (touch), content identical: SHA
	// compare must still skip with no writes.
	pyPath := filepath.Join(dir, "sample.py")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(pyPath, future, future); err != nil {
		t.Fatal(err)
	}
	touchRes := index(t, st, dir)
	if touchRes.Files != 0 || touchRes.Elements != 0 || touchRes.Skipped != 5 {
		t.Fatalf("touched run: %+v, want Skipped=5", touchRes)
	}
	if n, _ := st.ElementCount(); n != count {
		t.Fatalf("element count changed on touched run: %d -> %d", count, n)
	}

	// Edit: rename standalone -> runner (removes one element, adds one).
	b, err := os.ReadFile(pyPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := string(b)
	if !strings.Contains(edited, "def standalone(name):") {
		t.Fatal("fixture changed: standalone missing")
	}
	edited = strings.Replace(edited, "def standalone(name):", "def runner(name):", 1)
	if err := os.WriteFile(pyPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	third := index(t, st, dir)
	if third.Files != 1 || third.Skipped != 4 || third.Elements != 4 {
		t.Fatalf("edited run: %+v, want Files=1 Skipped=4 Elements=4", third)
	}
	mustFind(t, st, "sample.py::runner")
	assertNoElement(t, st, "standalone") // FTS + exact both clean
	if n, _ := st.ElementCount(); n != count {
		t.Fatalf("element count after edit: %d, want %d (rename swaps one element)", n, count)
	}
}

func TestDeletion(t *testing.T) {
	dir, st := setupProject(t)
	index(t, st, dir)
	count, _ := st.ElementCount()
	files, _ := st.FileCount()
	if files != 5 {
		t.Fatalf("FileCount = %d, want 5", files)
	}

	if err := os.Remove(filepath.Join(dir, "sample.md")); err != nil {
		t.Fatal(err)
	}
	res := index(t, st, dir)
	if res.Skipped != 4 || res.Files != 0 {
		t.Fatalf("deletion run: %+v, want Skipped=4 Files=0", res)
	}
	if n, _ := st.ElementCount(); n != count-3 {
		t.Fatalf("element count after deletion: %d, want %d", n, count-3)
	}
	if n, _ := st.FileCount(); n != 4 {
		t.Fatalf("FileCount after deletion: %d, want 4", n)
	}
	assertNoElement(t, st, "Details") // element + FTS rows gone
	recs, err := st.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Path == "sample.md" {
			t.Fatal("stale code_files row for deleted file")
		}
	}
}

// TestQualifiedNameFormat pins the documented `<rel/path>::<name>` shape.
func TestQualifiedNameFormat(t *testing.T) {
	fe, err := extractFile("sub/dir/sample.go", filepath.Join("testdata", "sample.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range fe.elements {
		got[e.qn] = true
	}
	for _, want := range []string{
		"sub/dir/sample.go::Widget",
		"sub/dir/sample.go::greet",
		"sub/dir/sample.go::Widget.Shout",
	} {
		if !got[want] {
			t.Errorf("missing qualified name %q in %v", want, got)
		}
	}
}
