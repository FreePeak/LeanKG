package githistory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
	"github.com/FreePeak/LeanKG/internal/store"
)

func openStore(t *testing.T) store.Backend {
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

// repoWithCommits returns a repository whose first commit touches a.go and
// b.go together, and whose second commit touches a.go alone.
func repoWithCommits(t *testing.T) string {
	t.Helper()
	gittest.SkipWithoutGit(t)
	root := t.TempDir()
	gittest.Git(t, root, "init", "-q")
	gittest.WriteFile(t, root, "a.go", "package a\n")
	gittest.WriteFile(t, root, "b.go", "package b\n")
	gittest.Git(t, root, "add", "-A")
	gittest.Git(t, root, "commit", "-q", "-m", "first")
	gittest.WriteFile(t, root, "a.go", "package a\n\nvar x = 1\n")
	gittest.Git(t, root, "add", "-A")
	gittest.Git(t, root, "commit", "-q", "-m", "second")
	return root
}

func commitOn(t *testing.T, root, name, body, msg string) {
	t.Helper()
	gittest.WriteFile(t, root, name, body)
	gittest.Git(t, root, "add", "-A")
	gittest.Git(t, root, "commit", "-q", "-m", msg)
}

func mustImport(t *testing.T, st store.Backend, root string) Result {
	t.Helper()
	res, err := Import(context.Background(), st, root, Options{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return res
}

// edgeCount counts outgoing edges of one relationship type from source.
func edgeCount(t *testing.T, st store.Backend, source, relType string) int {
	t.Helper()
	rels, err := st.Outgoing(source)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rels {
		if r.RelType == relType {
			n++
		}
	}
	return n
}

// coChangeCount reads the stored co_changed_with count from file a to file b
// (0 when there is no edge).
func coChangeCount(t *testing.T, st store.Backend, a, b string) int {
	t.Helper()
	rels, err := st.Outgoing("git:file:" + a)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rels {
		if r.RelType == "co_changed_with" && r.Target == "git:file:"+b {
			f, _ := r.Metadata["count"].(float64)
			return int(f)
		}
	}
	return 0
}

func TestImportWritesCommitAuthorFileElementsAndEdges(t *testing.T) {
	root := repoWithCommits(t)
	st := openStore(t)
	res := mustImport(t, st, root)
	if res.Commits != 2 || res.Incremental {
		t.Fatalf("result = %+v, want 2 commits, full import", res)
	}
	head := gittest.Git(t, root, "rev-parse", "HEAD")
	if els, _ := st.FindExact("git:commit:" + head); len(els) != 1 || els[0].ElementType != "commit" {
		t.Fatalf("head commit element missing or wrong type: %+v", els)
	}
	if els, _ := st.FindExact("git:author:t@example.com"); len(els) != 1 || els[0].ElementType != "author" {
		t.Fatalf("author element missing: %+v", els)
	}
	if got := edgeCount(t, st, "git:file:a.go", "modified_in"); got != 2 {
		t.Fatalf("a.go modified_in edges = %d, want 2", got)
	}
	if got := edgeCount(t, st, "git:commit:"+head, "authored_by"); got != 1 {
		t.Fatalf("head authored_by edges = %d, want 1", got)
	}
	if got := coChangeCount(t, st, "a.go", "b.go"); got != 1 {
		t.Fatalf("a.go->b.go co_change count = %d, want 1", got)
	}
	if v, ok, _ := st.KVGet(kvNamespace, kvHeadKey); !ok || v != head {
		t.Fatalf("recorded history head = %q ok=%v, want %s", v, ok, head)
	}
}

func TestImportIncrementalWalksOnlyNewCommits(t *testing.T) {
	root := repoWithCommits(t)
	st := openStore(t)
	mustImport(t, st, root)

	commitOn(t, root, "a.go", "package a\n\nvar y = 2\n", "third")
	gittest.WriteFile(t, root, "a.go", "package a\n\nvar y = 3\n")
	gittest.WriteFile(t, root, "b.go", "package b\n\nvar z = 3\n")
	gittest.Git(t, root, "add", "-A")
	gittest.Git(t, root, "commit", "-q", "-m", "fourth")
	res := mustImport(t, st, root)
	if !res.Incremental || res.Commits != 2 {
		t.Fatalf("result = %+v, want incremental with 2 new commits", res)
	}
	if got := edgeCount(t, st, "git:file:a.go", "modified_in"); got != 4 {
		t.Fatalf("a.go modified_in = %d, want 4", got)
	}
	// The first commit (1) plus the new fourth commit (1): merged, not replaced.
	if got := coChangeCount(t, st, "a.go", "b.go"); got != 2 {
		t.Fatalf("a.go->b.go co_change after incremental = %d, want 2", got)
	}
	again := mustImport(t, st, root)
	if again.Commits != 0 {
		t.Fatalf("re-import with no new commits walked %d commits", again.Commits)
	}
}

func TestImportRebuildsWhenRecordedHeadIsNotAncestor(t *testing.T) {
	root := repoWithCommits(t)
	st := openStore(t)
	mustImport(t, st, root)
	if err := st.KVSet(kvNamespace, kvHeadKey, "0000000000000000000000000000000000000000"); err != nil {
		t.Fatal(err)
	}
	res := mustImport(t, st, root)
	if res.Incremental || res.Commits != 2 {
		t.Fatalf("result = %+v, want full rebuild over 2 commits", res)
	}
	if got := coChangeCount(t, st, "a.go", "b.go"); got != 1 {
		t.Fatalf("rebuild added to stored co-change count: got %d, want 1", got)
	}
}
