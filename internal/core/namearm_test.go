package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestL3FusesANameArm pins the third arm: when a question's words ARE a
// symbol's name, the symbol must be reachable even when neither the vector arm
// nor the keyword arm ranks it.
//
// The measurement that motivated it, on this repository's own store with the
// 30 labelled questions in docs/retrieval-label-set.md: 18 of the 30 questions
// contain a token that appears in the answer's symbol name — "take the
// single-flight LOCK for embedding" contains `lock`, "compose the model STAMP
// for a provider" contains `stamp`, "cut a string to n RUNES" contains `runes`.
// The FTS arm searches name + qualified_name + content, so a question that
// paraphrases instead of quoting gets nothing, and the vector arm
// (bge-small, 384-d) ranks the two-line helper far below a five-hundred-line
// file that uses the same words — `coverage` sits at rank 1776 over production
// code alone.
//
// The name arm is a THIRD arm in the same fusion, not a shortcut: it competes on
// identifiers while the keyword arm competes on content, and a hit both of them
// liked is stronger evidence than either alone. It is deliberately the weakest
// arm — a substring of the query inside a symbol name is a weak signal — so it
// is weighted below 1.0, and it only fires when the query actually carries an
// identifier-shaped token (a capitalised run or a token with `_`/`::`), so a
// prose question never triggers a name search.
func TestL3FusesANameArm(t *testing.T) {
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
	// The answer: a two-line helper whose name is what the question says. The
	// decoy: a long file full of the same words, which is what the vector arm
	// prefers.
	els := []store.Element{
		{QualifiedName: "internal/embed/run.go::truncateRunes", ElementType: "function", Name: "truncateRunes",
			FilePath: "internal/embed/run.go", Language: "go",
			Content: "func truncateRunes(s string, n int) string {\n\tr := []rune(s)\n\treturn string(r[:n])\n}"},
		{QualifiedName: "internal/embed/provider.go::NotIt", ElementType: "function", Name: "NotIt",
			FilePath: "internal/embed/provider.go", Language: "go",
			Content: strings.Repeat("// truncate the text sent to a provider over and over again. ", 40)},
	}
	for _, e := range els {
		if err := st.UpsertElements([]store.Element{e}); err != nil {
			t.Fatal(err)
		}
		qvec, err := p.Embed(context.Background(), embed.Document, []string{e.Content})
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

	// A PROSE question that names the symbol: the ladder must reach L3 (an exact
	// identifier query short-circuits at L1 by design, which is a different and
	// already-correct path).
	out, err := e.Query(ctx, QueryRequest{Query: "where is truncateRunes used", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	hits := hitsOf(out)
	if len(hits) == 0 {
		t.Fatal("no hits for the symbol's own name")
	}
	// The name arm must be visible in the per-arm provenance, or an agent
	// cannot tell why an answer surfaced.
	// The transport keeps the ranks map as store's own type (map[string]int),
	// not map[string]any — the JSON shape is what an agent sees, and the golden
	// pins that, so the assertion reads the type the engine actually builds.
	ranks, _ := hits[0]["ranks"].(map[string]int)
	if _, ok := ranks[store.ArmName]; !ok {
		t.Errorf("the top hit must carry a %s rank, got %v", store.ArmName, hits[0]["ranks"])
	}
	// And in the retrieval reason, the one place an agent reads without parsing
	// per-hit detail.
	if !strings.Contains(retrievalString(out), store.ArmName) {
		t.Errorf("retrieval.reason must name the %s arm, got %q", store.ArmName, retrievalString(out))
	}
	// The ANSWER must be the symbol the question named — that is the whole
	// point of the arm, and the decoy (a long file repeating the words) is here
	// to prove the arm surfaces the symbol rather than adding noise.
	if hits[0]["qualified_name"] != "internal/embed/run.go::truncateRunes" {
		t.Fatalf("top hit = %v, want the symbol the question named", hits[0]["qualified_name"])
	}

	// A prose question with no identifier-shaped token must not trigger a name
	// search at all: the arm is for identifiers, and firing it on prose would
	// put every element whose name shares a common word in the result.
	out, err = e.Query(ctx, QueryRequest{Query: "how do i make the thing go faster please", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(retrievalString(out), store.ArmName) {
		t.Errorf("a prose question must not summon the name arm: %q", retrievalString(out))
	}
}

func retrievalString(out map[string]any) string {
	r, _ := out["retrieval"].(map[string]any)
	reason, _ := r["reason"].(string)
	return reason
}
