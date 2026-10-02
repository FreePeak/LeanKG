package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestL3ScopeArgReachesTheVectorArm pins the query-side half of the scope
// primitive: an agent that asks for production code must actually get it, and
// the answer must say so.
//
// The defect this guards is measured, not hypothetical. On this repository's own
// store (9,306 vectors, ranked by cosine), "reciprocal rank fusion of ranked
// lists" puts internal/store/pg_fts.go::FuseRRF at rank 54 overall and rank 7
// once 1,879 test fixtures and 4,148 documentation sections are excluded — the
// two populations this repository accumulated by indexing itself. Both arms of
// the fusion rank over the same set, so no weight can close a 47-rank gap; only
// scoping the set can. The scope is opt-in (a caller that says nothing gets the
// full corpus, byte for byte) and it is reported in retrieval, so an answer is
// never silently narrower than the corpus.
func TestL3ScopeArgReachesTheVectorArm(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(16)
	if err := st.WriteStamp(embed.StampOf(p)); err != nil {
		t.Fatal(err)
	}
	// One real element, one test fixture that paraphrases the same question.
	for _, e := range []store.Element{
		{QualifiedName: "store/real.go::Rank", ElementType: "function", Name: "Rank", FilePath: "store/real.go", Language: "go", Content: "rank the merged lists"},
		{QualifiedName: "store/real_test.go::TestRank", ElementType: "function", Name: "TestRank", FilePath: "store/real_test.go", Language: "go", Content: "rank the merged lists"},
	} {
		if err := st.UpsertElements([]store.Element{e}); err != nil {
			t.Fatal(err)
		}
		qvec, err := p.Embed(context.Background(), embed.Query, []string{e.Content})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertVectors(p.ModelID(), []store.VectorRow{{QualifiedName: e.QualifiedName, Vec: qvec[0]}}); err != nil {
			t.Fatal(err)
		}
	}
	e := New(st, nil, QueryEmbedderFromProvider(p))
	e.SetProjectDir(dir)
	ctx := context.Background()

	t.Run("no scope sees everything", func(t *testing.T) {
		out, err := e.Query(ctx, QueryRequest{Query: "rank the merged lists", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(hitsOf(out)) < 2 {
			t.Fatalf("unscoped query must see both elements, got %v", out["hits"])
		}
		if _, ok := out["retrieval"].(map[string]any)["scope"]; ok {
			t.Errorf("an unscoped answer must not claim a scope: %v", out["retrieval"])
		}
	})

	t.Run("code scope drops the test fixture and says so", func(t *testing.T) {
		out, err := e.Query(ctx, QueryRequest{
			Query: "rank the merged lists", Limit: 10,
			Args: map[string]any{"scope": "code"},
		})
		if err != nil {
			t.Fatal(err)
		}
		hits := hitsOf(out)
		if len(hits) != 1 {
			t.Fatalf("code scope returned %d hits, want 1: %v", len(hits), out["hits"])
		}
		if hits[0]["qualified_name"] != "store/real.go::Rank" {
			t.Fatalf("code scope kept the wrong element: %v", hits[0])
		}
		r := out["retrieval"].(map[string]any)
		if r["scope"] != "code" {
			t.Errorf("a scoped answer must name its scope in retrieval, got %v", r)
		}
		if r["rung"] != "L3" {
			t.Errorf("a scoped query must still reach L3, got %v", r)
		}
	})

	t.Run("an unknown scope is an error, not a silent full corpus", func(t *testing.T) {
		_, err := e.Query(ctx, QueryRequest{
			Query: "rank the merged lists", Args: map[string]any{"scope": "banana"},
		})
		if err == nil {
			t.Fatal(`scope="banana" must be refused: silently answering over the full corpus would look like the scope worked`)
		}
	})
}
