package docindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// setupStore opens a migrated SQLite store under a fresh temp project dir.
func setupStore(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
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

func indexDocs(t *testing.T, st *store.Store, dir string) Result {
	t.Helper()
	res, err := IndexDocs(context.Background(), st, dir)
	if err != nil {
		t.Fatalf("IndexDocs: %v", err)
	}
	return res
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustFind asserts exactly one element with the given qualified name.
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

const nestedFixture = `# Guide
intro
## Install
steps
### Details
deep
## Usage
notes
`

func TestIndexDocsSections(t *testing.T) {
	dir, st := setupStore(t)
	writeFile(t, dir, "guide.md", nestedFixture)

	res := indexDocs(t, st, dir)
	if res.Files != 1 || res.Elements != 4 || res.Skipped != 0 {
		t.Fatalf("run: %+v, want Files=1 Elements=4 Skipped=0", res)
	}

	guide := mustFind(t, st, "guide.md#guide")
	if guide.ElementType != "doc" || guide.Language != "markdown" {
		t.Fatalf("guide metadata: %+v", guide)
	}
	if guide.Name != "Guide" || guide.FilePath != "guide.md" {
		t.Fatalf("guide identity: %+v", guide)
	}
	if guide.LineStart != 1 || guide.LineEnd != 2 {
		t.Fatalf("guide lines: %d-%d, want 1-2", guide.LineStart, guide.LineEnd)
	}
	if guide.Content != "# Guide\nintro" {
		t.Fatalf("guide content: %q", guide.Content)
	}
	if guide.ParentQualified != "" {
		t.Fatalf("guide has parent %q, want none", guide.ParentQualified)
	}

	install := mustFind(t, st, "guide.md#install")
	if install.LineStart != 3 || install.LineEnd != 4 {
		t.Fatalf("install lines: %d-%d, want 3-4", install.LineStart, install.LineEnd)
	}
	if install.ParentQualified != "guide.md#guide" {
		t.Fatalf("install parent: %q", install.ParentQualified)
	}
	details := mustFind(t, st, "guide.md#details")
	if details.ParentQualified != "guide.md#install" {
		t.Fatalf("details parent: %q", details.ParentQualified)
	}
	usage := mustFind(t, st, "guide.md#usage")
	if usage.ParentQualified != "guide.md#guide" {
		t.Fatalf("usage parent: %q", usage.ParentQualified)
	}

	// contains edges from the section hierarchy, confidence 1.0.
	rels, err := st.Outgoing("guide.md#guide")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 {
		t.Fatalf("guide outgoing: %d, want 2 (install, usage)", len(rels))
	}
	for _, r := range rels {
		if r.RelType != "contains" || r.Confidence != 1.0 {
			t.Fatalf("edge %+v, want contains@1.0", r)
		}
	}
	rels, err = st.Outgoing("guide.md#install")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 || rels[0].Target != "guide.md#details" {
		t.Fatalf("install outgoing: %+v, want one edge to details", rels)
	}
}

func TestIndexDocsDuplicateSlugs(t *testing.T) {
	dir, st := setupStore(t)
	writeFile(t, dir, "dup.md", "# Section\none\n## Section\ntwo\n# Section\nthree\n")

	res := indexDocs(t, st, dir)
	if res.Elements != 3 {
		t.Fatalf("elements: %d, want 3", res.Elements)
	}
	mustFind(t, st, "dup.md#section")
	mustFind(t, st, "dup.md#section-2")
	mustFind(t, st, "dup.md#section-3")

	// Last top-level Section pops the stack: its parent is nothing, while
	// the second Section nests under the first.
	if e := mustFind(t, st, "dup.md#section-2"); e.ParentQualified != "dup.md#section" {
		t.Fatalf("section-2 parent: %q", e.ParentQualified)
	}
	if e := mustFind(t, st, "dup.md#section-3"); e.ParentQualified != "" {
		t.Fatalf("section-3 parent: %q, want none", e.ParentQualified)
	}
}

func TestIndexDocsChangeDetection(t *testing.T) {
	dir, st := setupStore(t)
	writeFile(t, dir, "doc.md", "# One\nbody\n")

	if res := indexDocs(t, st, dir); res.Files != 1 || res.Skipped != 0 {
		t.Fatalf("first run: %+v", res)
	}
	// Second run: size+mtime match -> skipped without any write.
	if res := indexDocs(t, st, dir); res.Files != 0 || res.Skipped != 1 {
		t.Fatalf("unchanged run: %+v, want Skipped=1 Files=0", res)
	}

	// Same content, new mtime: size+mtime mismatch, SHA-256 confirms
	// identical content -> skipped without writes.
	writeFile(t, dir, "doc.md", "# One\nbody\n")
	if res := indexDocs(t, st, dir); res.Files != 0 || res.Skipped != 1 {
		t.Fatalf("hash-confirm run: %+v, want Skipped=1 Files=0", res)
	}

	// Changed content: re-extracted, old element replaced.
	writeFile(t, dir, "doc.md", "# Two\nbody\n")
	res := indexDocs(t, st, dir)
	if res.Files != 1 || res.Skipped != 0 || res.Elements != 1 {
		t.Fatalf("changed run: %+v, want Files=1 Elements=1 Skipped=0", res)
	}
	mustFind(t, st, "doc.md#two")
	if els, err := st.FindExact("doc.md#one"); err != nil || len(els) != 0 {
		t.Fatalf("stale element after re-index: %v, %v", els, err)
	}
	if n, _ := st.ElementCount(); n != 1 {
		t.Fatalf("element count: %d, want 1", n)
	}
}

func TestIndexDocsDeletion(t *testing.T) {
	dir, st := setupStore(t)
	writeFile(t, dir, "keep.md", "# Keep\nk\n")
	writeFile(t, dir, "gone.md", "# Gone\ng\n")

	if res := indexDocs(t, st, dir); res.Files != 2 {
		t.Fatalf("first run: %+v", res)
	}

	if err := os.Remove(filepath.Join(dir, "gone.md")); err != nil {
		t.Fatal(err)
	}
	res := indexDocs(t, st, dir)
	if res.Skipped != 1 || res.Files != 0 {
		t.Fatalf("deletion run: %+v, want Skipped=1 Files=0", res)
	}
	if n, _ := st.ElementCount(); n != 1 {
		t.Fatalf("element count after deletion: %d, want 1", n)
	}
	if n, _ := st.FileCount(); n != 1 {
		t.Fatalf("file count after deletion: %d, want 1", n)
	}
	recs, err := st.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Path == "gone.md" {
			t.Fatal("stale code_files row for deleted file")
		}
	}
	if els, err := st.FindExact("gone.md#gone"); err != nil || len(els) != 0 {
		t.Fatalf("stale element + FTS rows after deletion: %v, %v", els, err)
	}
}

func TestIndexDocsSkipList(t *testing.T) {
	dir, st := setupStore(t)
	for _, d := range []string{".git", "node_modules", "vendor", ".leankg", "target", "dist", "build", ".hidden"} {
		writeFile(t, dir, filepath.ToSlash(filepath.Join(d, "ignored.md")), "# Ignored\nx\n")
	}
	writeFile(t, dir, "readme.md", "# Read\nr\n")
	writeFile(t, dir, "notes.txt", "# Not Markdown\nx\n")

	res := indexDocs(t, st, dir)
	if res.Files != 1 || res.Elements != 1 || res.Skipped != 0 {
		t.Fatalf("run: %+v, want Files=1 Elements=1", res)
	}
	mustFind(t, st, "readme.md#read")
}

// TestIndexDocsScopedToMarkdown pins that docindex only manages .md file
// records: a code-file record (internal/index's domain) survives a
// docindex run even when its file is absent, while a vanished .md record
// is cleaned up.
func TestIndexDocsScopedToMarkdown(t *testing.T) {
	dir, st := setupStore(t)
	writeFile(t, dir, "doc.md", "# Doc\nx\n")
	if err := st.UpsertFiles([]store.FileRecord{{Path: "src.go", Size: 10, MtimeNS: 1, ContentHash: "deadbeef"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertElements([]store.Element{{
		QualifiedName: "src.go::Widget", Name: "Widget", ElementType: "function",
		FilePath: "src.go", Language: "go", LineStart: 1, LineEnd: 2,
	}}); err != nil {
		t.Fatal(err)
	}

	if res := indexDocs(t, st, dir); res.Files != 1 {
		t.Fatalf("run: %+v", res)
	}

	kept := false
	recs, err := st.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Path == "src.go" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("docindex deleted a non-markdown file record")
	}
	mustFind(t, st, "src.go::Widget") // code element untouched
}

func TestIndexDocsCodeFences(t *testing.T) {
	dir, st := setupStore(t)
	writeFile(t, dir, "fenced.md", "# Real\n\ntext\n\n```\n# Not A Heading\n```\n\n# After\n")

	res := indexDocs(t, st, dir)
	if res.Elements != 2 {
		t.Fatalf("elements: %d, want 2 (fenced heading ignored)", res.Elements)
	}
	mustFind(t, st, "fenced.md#real")
	mustFind(t, st, "fenced.md#after")
}

func TestIndexDocsContentBound(t *testing.T) {
	dir, st := setupStore(t)
	long := strings.Repeat("a", 9000)
	writeFile(t, dir, "big.md", "# Big\n"+long+"\n")

	if res := indexDocs(t, st, dir); res.Elements != 1 {
		t.Fatalf("run: %+v", res)
	}
	e := mustFind(t, st, "big.md#big")
	if len(e.Content) != 8000 {
		t.Fatalf("content length: %d, want 8000", len(e.Content))
	}
	if want := "# Big\n" + strings.Repeat("a", 7994); e.Content != want {
		t.Fatalf("content head/tail: %q...%q", e.Content[:8], e.Content[len(e.Content)-4:])
	}
}

// TestIndexDocsHeadinglessFile mirrors internal/index: a changed file with
// no extractable headings still gets a file record.
func TestIndexDocsHeadinglessFile(t *testing.T) {
	dir, st := setupStore(t)
	writeFile(t, dir, "plain.md", "no headings here\n")

	res := indexDocs(t, st, dir)
	if res.Files != 1 || res.Elements != 0 {
		t.Fatalf("run: %+v, want Files=1 Elements=0", res)
	}
	if n, _ := st.FileCount(); n != 1 {
		t.Fatalf("file count: %d, want 1", n)
	}
}
