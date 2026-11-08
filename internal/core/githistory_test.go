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

// TestConcurrentGitImportsSerializeOnOneStore pins the import guard: two git
// imports against one store run one after the other. The first walks the full
// window; the second waits, then finds HEAD already stored and reports an
// incremental no-op. Without the guard both read an empty cursor, both walk the
// full history and both report incremental:false.
func TestConcurrentGitImportsSerializeOnOneStore(t *testing.T) {
	e, dir := gitHistoryEngine(t)
	other := New(e.st, nil, nil)
	other.SetProjectDir(dir)
	ctx := context.Background()

	const callers = 4
	type outcome struct {
		res githistory.Result
		err error
	}
	start := make(chan struct{})
	results := make(chan outcome, callers)
	for i := 0; i < callers; i++ {
		eng := e
		if i%2 == 1 {
			eng = other
		}
		go func(eng *Engine) {
			<-start
			out, err := eng.Import(ctx, ImportRequest{Action: "git"})
			res, _ := out["git"].(githistory.Result)
			results <- outcome{res: res, err: err}
		}(eng)
	}
	close(start)

	full := 0
	for i := 0; i < callers; i++ {
		o := <-results
		if o.err != nil {
			t.Fatalf("concurrent import: %v", o.err)
		}
		if o.res.Incremental {
			if o.res.Commits != 0 {
				t.Errorf("incremental no-op walked %d commits, want 0", o.res.Commits)
			}
			continue
		}
		full++
		if o.res.Commits != 2 {
			t.Errorf("full import wrote %d commits, want 2", o.res.Commits)
		}
	}
	if full != 1 {
		t.Fatalf("%d of %d concurrent imports ran a full walk, want exactly 1", full, callers)
	}

	hist, err := githistory.History(e.st, "main.go", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("history main.go after concurrent imports = %d commits, want 2 (no duplicates)", len(hist))
	}
	partners, err := githistory.CoChange(e.st, "main.go", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(partners) != 1 || partners[0].Count != 1 {
		t.Fatalf("cochange main.go after concurrent imports = %#v, want one partner with count 1", partners)
	}
}
