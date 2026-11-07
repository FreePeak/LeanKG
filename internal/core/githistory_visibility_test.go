package core

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
)

// openVisibilityEngine returns an engine over a fresh store holding one code
// element and one of each git history row (commit, author, file), all of which
// mention the word "parse" so every keyword rung can reach them.
func openVisibilityEngine(t *testing.T, extra ...store.Element) *Engine {
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
	els := append([]store.Element{
		{QualifiedName: "store/parse.go::Parse", ElementType: "function", Name: "Parse", FilePath: "store/parse.go", Language: "go", Content: "parse the input"},
		{QualifiedName: "git:commit:abc123", ElementType: "commit", Name: "abc123 parse the input", Content: "parse the input\n\nAuthor: Parse Author <a@x.io>"},
		{QualifiedName: "git:author:a@x.io", ElementType: "author", Name: "Parse Author", Content: "Parse Author <a@x.io>"},
		{QualifiedName: "git:file:store/parse.go", ElementType: "file", Name: "store/parse.go"},
	}, extra...)
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	e := New(st, nil, nil)
	e.SetProjectDir(dir)
	return e
}

func qnsOf(hits []map[string]any) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, fmt.Sprint(h["qualified_name"]))
	}
	return out
}

func hasQN(qns []string, want string) bool {
	for _, q := range qns {
		if q == want {
			return true
		}
	}
	return false
}

// TestSearchHidesGitHistoryRowsUnlessOptedIn pins the default population of
// the keyword and exact rungs: commit, author and git:file rows are history,
// not code, so they are answered only by history/cochange/churn or by an
// explicit include_git opt-in.
func TestSearchHidesGitHistoryRowsUnlessOptedIn(t *testing.T) {
	e := openVisibilityEngine(t)
	ctx := context.Background()
	optIn := map[string]any{"include_git": true}

	cases := []struct {
		name   string
		req    QueryRequest
		code   string
		hidden []string
	}{
		{"fuzzy", QueryRequest{Action: "fuzzy", Query: "parse"}, "store/parse.go::Parse",
			[]string{"git:commit:abc123", "git:author:a@x.io", "git:file:store/parse.go"}},
		// Not an identifier: "parse" alone would stop at L1, where the code
		// symbol Parse is an exact (case-insensitive) name match.
		{"ladder", QueryRequest{Query: "parse the input"}, "store/parse.go::Parse",
			[]string{"git:commit:abc123", "git:author:a@x.io", "git:file:store/parse.go"}},
		{"exact", QueryRequest{Action: "exact", Query: "git:commit:abc123"}, "",
			[]string{"git:commit:abc123"}},
	}
	for _, tc := range cases {
		t.Run(tc.name+" default hides history", func(t *testing.T) {
			out, err := e.Query(ctx, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			qns := qnsOf(hitsOf(out))
			for _, h := range tc.hidden {
				if hasQN(qns, h) {
					t.Errorf("default %s search returned history row %s: %v", tc.name, h, qns)
				}
			}
			if tc.code != "" && !hasQN(qns, tc.code) {
				t.Errorf("default %s search lost the code element %s: %v", tc.name, tc.code, qns)
			}
		})
		t.Run(tc.name+" include_git reaches history", func(t *testing.T) {
			req := tc.req
			req.Args = optIn
			out, err := e.Query(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			qns := qnsOf(hitsOf(out))
			for _, h := range tc.hidden {
				if !hasQN(qns, h) {
					t.Errorf("include_git %s search must return %s, got %v", tc.name, h, qns)
				}
			}
		})
	}
}

// TestSearchKeepsCodeWhenHistoryFillsTheTopRanks guards the over-fetch: when
// history rows outrank every code element, the default keyword rung must still
// fill its limit with the code that matches, not return the history rows'
// absence as an empty answer.
func TestSearchKeepsCodeWhenHistoryFillsTheTopRanks(t *testing.T) {
	var commits []store.Element
	for i := 0; i < 15; i++ {
		commits = append(commits, store.Element{
			QualifiedName: fmt.Sprintf("git:commit:parse%02d", i), ElementType: "commit",
			Name: "parse change", Content: "parse parse parse",
		})
	}
	e := openVisibilityEngine(t, commits...)

	out, err := e.Query(context.Background(), QueryRequest{Action: "fuzzy", Query: "parse", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	qns := qnsOf(hitsOf(out))
	if !hasQN(qns, "store/parse.go::Parse") {
		t.Fatalf("default fuzzy search must return the code element behind 15 history rows, got %v", qns)
	}
	for _, q := range qns {
		if len(q) > 4 && q[:4] == "git:" {
			t.Fatalf("default fuzzy search returned history row %s", q)
		}
	}
}

// TestSemanticHidesGitHistoryVectors pins the L3 rung: commit vectors exist
// (commits are embedded), but a default semantic answer must not return them.
func TestSemanticHidesGitHistoryVectors(t *testing.T) {
	ctx := context.Background()
	p := embed.Deterministic(16)
	const text = "rank the merged lists"
	elements := []store.Element{
		{QualifiedName: "store/real.go::Rank", ElementType: "function", Name: "Rank", FilePath: "store/real.go", Language: "go", Content: text},
		{QualifiedName: "git:commit:f00dfeed", ElementType: "commit", Name: "f00dfeed rank the merged lists", Content: text},
	}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteStamp(embed.StampOf(p)); err != nil {
		t.Fatal(err)
	}
	for _, el := range elements {
		if err := st.UpsertElements([]store.Element{el}); err != nil {
			t.Fatal(err)
		}
		qvec, err := p.Embed(ctx, embed.Query, []string{el.Content})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertVectors(p.ModelID(), []store.VectorRow{{QualifiedName: el.QualifiedName, Vec: qvec[0]}}); err != nil {
			t.Fatal(err)
		}
	}
	e := New(st, nil, QueryEmbedderFromProvider(p))
	e.SetProjectDir(dir)

	out, err := e.Query(ctx, QueryRequest{Action: "semantic", Query: text, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if qns := qnsOf(hitsOf(out)); hasQN(qns, "git:commit:f00dfeed") || !hasQN(qns, "store/real.go::Rank") {
		t.Fatalf("default semantic search must return the code element and no commit vector, got %v", qns)
	}

	out, err = e.Query(ctx, QueryRequest{Action: "semantic", Query: text, Limit: 10, Args: map[string]any{"include_git": true}})
	if err != nil {
		t.Fatal(err)
	}
	if qns := qnsOf(hitsOf(out)); !hasQN(qns, "git:commit:f00dfeed") {
		t.Fatalf("include_git semantic search must return the commit vector, got %v", qns)
	}
}
