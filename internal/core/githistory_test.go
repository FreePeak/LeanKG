package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/githistory"
	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
	"github.com/FreePeak/LeanKG/internal/store"
)

// gitHistoryEngine returns an engine over a fresh store, pointed at a git
// repository with two commits: main.go alone, then main.go with util.go.
func gitHistoryEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := gitRepoWithFile(t)
	gittest.WriteFile(t, dir, "main.go", "package main\n\nfunc Main() { _ = 1 }\n")
	gittest.WriteFile(t, dir, "util.go", "package main\n\nfunc Util() {}\n")
	gittest.Git(t, dir, "add", ".")
	gittest.Git(t, dir, "commit", "-m", "second")
	st, err := store.Open(filepath.Join(t.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	e := New(st, nil, nil)
	e.SetProjectDir(dir)
	return e, dir
}

func TestGitImportThenHistoryCochangeAndChurn(t *testing.T) {
	e, _ := gitHistoryEngine(t)
	ctx := context.Background()

	out, err := e.Import(ctx, ImportRequest{Action: "git"})
	if err != nil {
		t.Fatalf("import git: %v", err)
	}
	res, ok := out["git"].(githistory.Result)
	if !ok || res.Commits != 2 {
		t.Fatalf("import git result = %#v, want 2 commits", out["git"])
	}

	hist, err := e.Query(ctx, QueryRequest{Action: "history", Query: "main.go"})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if commits, _ := hist["commits"].([]githistory.Change); len(commits) != 2 {
		t.Fatalf("history main.go = %#v, want 2 commits", hist["commits"])
	}

	co, err := e.Query(ctx, QueryRequest{Action: "cochange", Query: "main.go"})
	if err != nil {
		t.Fatalf("cochange: %v", err)
	}
	partners, _ := co["partners"].([]githistory.Partner)
	if len(partners) != 1 || partners[0].Path != "util.go" {
		t.Fatalf("cochange main.go = %#v, want util.go", co["partners"])
	}

	churn, err := e.Query(ctx, QueryRequest{Action: "churn"})
	if err != nil {
		t.Fatalf("churn: %v", err)
	}
	files, _ := churn["files"].([]githistory.ChurnEntry)
	if len(files) == 0 || files[0].Path != "main.go" || files[0].Commits != 2 {
		t.Fatalf("churn files = %#v, want main.go first with 2 commits", churn["files"])
	}
}
