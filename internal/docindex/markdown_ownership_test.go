package docindex_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/docindex"
	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/store"
)

// Markdown has two producers: internal/index (keys rows `<rel>::<heading>`,
// every `leankg index` path) and internal/docindex (keys rows `<rel>#<slug>`,
// the docs step of `leankg refresh` and `import docs`). These tests pin the
// refresh order internal/refresh runs: index over the root, then docindex over
// root/docs. Each markdown file keeps exactly one producer, and an unchanged
// repository rewrites no doc rows on a rerun (CX-10).

const (
	ownReadme   = "# Readme\nhello\n## Usage\nrun it\n"
	ownGuideDoc = "# Guide\nintro\n## Setup\nsteps\n"
)

func openOwnershipStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

// ownershipRows counts the element rows a file holds under each key scheme.
// A row under neither scheme is a third producer and fails the test.
func ownershipRows(t *testing.T, st *store.Store, rel string) (indexRows, docRows int) {
	t.Helper()
	els, err := st.Elements()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range els {
		if e.FilePath != rel {
			continue
		}
		switch {
		case strings.HasPrefix(e.QualifiedName, rel+"::"):
			indexRows++
		case strings.HasPrefix(e.QualifiedName, rel+"#"):
			docRows++
		default:
			t.Errorf("%s: row %q matches neither producer's key scheme", rel, e.QualifiedName)
		}
	}
	return indexRows, docRows
}

// refreshOwnership runs the refresh order: code index over root, then docindex
// over root/docs.
func refreshOwnership(t *testing.T, st *store.Store, root string) (index.Result, docindex.Result) {
	t.Helper()
	ctx := context.Background()
	idx, err := index.IndexDir(ctx, st, root)
	if err != nil {
		t.Fatalf("IndexDir: %v", err)
	}
	doc, err := docindex.IndexDocsUnder(ctx, st, root, filepath.Join(root, "docs"))
	if err != nil {
		t.Fatalf("IndexDocsUnder: %v", err)
	}
	return idx, doc
}

// TestMarkdownOwnerGitRepoOneProducer: in a git repository every committed
// markdown file is produced by the index alone, and a second refresh of the
// unchanged repository rewrites zero doc rows.
func TestMarkdownOwnerGitRepoOneProducer(t *testing.T) {
	gittest.SkipWithoutGit(t)
	root := t.TempDir()
	gittest.Git(t, root, "init", "-q")
	gittest.WriteFile(t, root, "README.md", ownReadme)
	gittest.WriteFile(t, root, "docs/guide.md", ownGuideDoc)
	gittest.WriteFile(t, root, "a.go", "package a\n\nfunc Committed() {}\n")
	gittest.Git(t, root, "add", ".")
	gittest.Git(t, root, "commit", "-q", "-m", "fixture")
	st := openOwnershipStore(t)

	idx1, doc1 := refreshOwnership(t, st, root)
	if idx1.Files != 3 {
		t.Errorf("first index run: Files=%d, want 3", idx1.Files)
	}
	if doc1.Files != 0 || doc1.Elements != 0 {
		t.Errorf("first refresh: docindex wrote %+v for index-owned markdown, want Files=0 Elements=0", doc1)
	}
	for _, rel := range []string{"README.md", "docs/guide.md"} {
		indexRows, docRows := ownershipRows(t, st, rel)
		if indexRows == 0 {
			t.Errorf("%s: no index rows, the markdown has no producer", rel)
		}
		if docRows != 0 {
			t.Errorf("%s: %d docindex rows next to %d index rows, want one producer", rel, docRows, indexRows)
		}
	}

	idx2, doc2 := refreshOwnership(t, st, root)
	if idx2.Files != 0 || doc2.Files != 0 || doc2.Elements != 0 {
		t.Errorf("second refresh of unchanged repo: index %+v, docindex %+v; want 0 files and 0 elements rewritten", idx2, doc2)
	}
}

// TestMarkdownOwnerWalkRefreshOneProducer: outside git the index walk and
// docindex share the size+mtime signal, so the refresh order leaves the index
// as the only producer and a rerun rewrites nothing.
func TestMarkdownOwnerWalkRefreshOneProducer(t *testing.T) {
	root := t.TempDir()
	gittest.WriteFile(t, root, "README.md", ownReadme)
	gittest.WriteFile(t, root, "docs/guide.md", ownGuideDoc)
	gittest.WriteFile(t, root, "a.go", "package a\n\nfunc Walked() {}\n")
	st := openOwnershipStore(t)

	_, doc1 := refreshOwnership(t, st, root)
	if doc1.Files != 0 {
		t.Errorf("first refresh: docindex rewrote %d files already produced by the index, want 0", doc1.Files)
	}
	for _, rel := range []string{"README.md", "docs/guide.md"} {
		indexRows, docRows := ownershipRows(t, st, rel)
		if indexRows == 0 || docRows != 0 {
			t.Errorf("%s: index rows=%d docindex rows=%d, want index-only", rel, indexRows, docRows)
		}
	}

	idx2, doc2 := refreshOwnership(t, st, root)
	if idx2.Files != 0 || doc2.Files != 0 {
		t.Errorf("second refresh of unchanged tree: index %+v, docindex %+v; want 0 files rewritten", idx2, doc2)
	}
}
