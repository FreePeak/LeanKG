package gitobj

import (
	"context"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
)

// historyFixture commits three changes: a.go + b.go together, then a.go alone,
// then a merge-free third commit touching c.go. Returns the repository root and
// the commit ids in creation order.
func historyFixture(t *testing.T) (string, []string) {
	t.Helper()
	gittest.SkipWithoutGit(t)
	root := t.TempDir()
	gittest.Git(t, root, "init", "-q")
	var ids []string
	commit := func(msg string, files map[string]string) {
		for name, body := range files {
			gittest.WriteFile(t, root, name, body)
		}
		gittest.Git(t, root, "add", "-A")
		gittest.Git(t, root, "commit", "-q", "-m", msg)
		ids = append(ids, gittest.Git(t, root, "rev-parse", "HEAD"))
	}
	commit("first\n\nbody line one", map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	commit("second", map[string]string{"a.go": "package a\n\nvar x = 1\n"})
	commit("third", map[string]string{"c.go": "package c\n"})
	return root, ids
}

func TestHistoryNewestFirstWithFilesAndMetadata(t *testing.T) {
	root, ids := historyFixture(t)
	commits, err := History(context.Background(), root, HistoryOptions{})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("commits = %d, want 3", len(commits))
	}
	if commits[0].SHA != ids[2] || commits[2].SHA != ids[0] {
		t.Fatalf("order = %s,%s,%s; want newest first %v", commits[0].SHA, commits[1].SHA, commits[2].SHA, ids)
	}
	first := commits[2]
	if first.Subject != "first" || !strings.Contains(first.Body, "body line one") {
		t.Fatalf("subject/body = %q / %q", first.Subject, first.Body)
	}
	if first.AuthorName != "t" || first.AuthorEmail != "t@example.com" {
		t.Fatalf("author = %q <%s>", first.AuthorName, first.AuthorEmail)
	}
	if first.Date == "" {
		t.Fatal("date is empty")
	}
	if len(first.Files) != 2 {
		t.Fatalf("files = %+v, want a.go and b.go", first.Files)
	}
	if first.Files[0].Path != "a.go" || first.Files[0].Insertions != 1 || first.Files[0].Deletions != 0 {
		t.Fatalf("a.go numstat = %+v", first.Files[0])
	}
	if len(commits[1].Files) != 1 || commits[1].Files[0].Path != "a.go" || commits[1].Files[0].Insertions != 2 {
		t.Fatalf("second commit files = %+v", commits[1].Files)
	}
}

func TestHistoryBoundedAndSinceExclusive(t *testing.T) {
	root, ids := historyFixture(t)
	ctx := context.Background()

	capped, err := History(ctx, root, HistoryOptions{Max: 2})
	if err != nil {
		t.Fatalf("History max: %v", err)
	}
	if len(capped) != 2 || capped[0].SHA != ids[2] {
		t.Fatalf("Max=2 gave %d commits", len(capped))
	}

	since, err := History(ctx, root, HistoryOptions{Since: ids[0]})
	if err != nil {
		t.Fatalf("History since: %v", err)
	}
	if len(since) != 2 || since[1].SHA != ids[1] {
		t.Fatalf("Since=first gave %d commits, want second and third", len(since))
	}
}

func TestIsAncestor(t *testing.T) {
	root, ids := historyFixture(t)
	ctx := context.Background()
	if ok, err := IsAncestor(ctx, root, ids[0]); err != nil || !ok {
		t.Fatalf("first commit should be an ancestor of HEAD: ok=%v err=%v", ok, err)
	}
	if ok, err := IsAncestor(ctx, root, strings.Repeat("0", 40)); err != nil || ok {
		t.Fatalf("unknown id should not be an ancestor: ok=%v err=%v", ok, err)
	}
}
