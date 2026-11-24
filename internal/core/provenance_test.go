package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/store"
)

// provenanceRungs is the closed retrieval.rung vocabulary: the ladder rungs,
// ast-grep and lsp, and the graph, memory and git reads that stampProvenance names.
var provenanceRungs = map[string]bool{
	"L0": true, "L1": true, "L2": true, "L3": true,
	"ast-grep": true, "lsp": true, "graph": true, "memory": true, "git": true,
}

// provenanceExempt lists query actions the per-answer contract does not apply
// to, with the reason. Each one is an explicit skip in the test output.
var provenanceExempt = map[string]string{
	"lsp": "a positive answer needs a language server on the host: lspQuery sets retrieval.rung=lsp only after one answers, and without one the query errors, so the golden pins the refusal and no answer reaches this check",
	"portfolio": "a fan-out has no single rung: each child answer carries its own retrieval and freshness " +
		"(TestPortfolioChildCarriesProvenance in internal/portfolioreg)",
}

// provenanceGaps lists query actions whose answer carries no retrieval rung
// today. Their payloads are catalog, PRD, session, incident or language reads,
// and none of them has a rung in the closed vocabulary (L0-L3, ast-grep, lsp,
// graph, memory, git). The test fails when one of them starts carrying
// retrieval, so its entry is removed in the same change.
var provenanceGaps = map[string]string{
	"ontology":        "ontology catalog read; no rung in the closed vocabulary",
	"session":         "session offload and lesson read; no rung in the closed vocabulary",
	"prd":             "PRD requirement trace; no rung in the closed vocabulary",
	"incidents":       "org-knowledge incident read; no rung in the closed vocabulary",
	"env_conflicts":   "org-knowledge environment read; no rung in the closed vocabulary",
	"service_context": "org-knowledge service read; no rung in the closed vocabulary",
	"compress":        "tool-output compression; no index lookup, so no rung",
	"languages":       "language registry listing; no index lookup, so no rung",
}

// queryActionCase returns the golden case that answers query/key: the first
// case covering it that does not error. The error cases are refusals, which
// are not answers.
func queryActionCase(t *testing.T, key string) (actionCase, map[string]any) {
	t.Helper()
	want := "query/" + key
	if key == "" {
		want = "query/(empty)"
	}
	for _, c := range actionCases {
		if !slices.Contains(c.covers(), want) {
			continue
		}
		f := newActionFixture(t)
		if c.setup != nil {
			c.setup(f)
		}
		out, err := c.run(f)
		if err == nil {
			return c, out
		}
	}
	t.Fatalf("no golden case answers %s without error", want)
	return actionCase{}, nil
}

// TestQueryAnswersCarryProvenance walks the query registry. Every answer must
// say which rung produced it (retrieval.rung) and how fresh the index was
// (freshness). Actions the contract does not cover are listed above with a
// reason and reported as skipped, not dropped.
func TestQueryAnswersCarryProvenance(t *testing.T) {
	keys := make([]string, 0, len(queryActions))
	for k := range queryActions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		name := key
		if name == "" {
			name = "ladder"
		}
		t.Run(name, func(t *testing.T) {
			if why, ok := provenanceExempt[key]; ok {
				t.Skip(why)
			}
			_, out := queryActionCase(t, key)
			if why, ok := provenanceGaps[key]; ok {
				if _, has := out["retrieval"]; has {
					t.Fatalf("query/%s now carries retrieval: remove it from provenanceGaps", key)
				}
				t.Skip("known gap: " + why)
			}
			r, _ := out["retrieval"].(map[string]any)
			if rung, _ := r["rung"].(string); !provenanceRungs[rung] {
				t.Errorf("retrieval.rung = %q (retrieval=%v); want one of the closed vocabulary", rung, out["retrieval"])
			}
			state, _ := out["freshness"].(string)
			switch state {
			case store.FreshnessCold, store.FreshnessFresh, store.FreshnessPossiblyStale:
			default:
				t.Errorf("freshness state = %q (%T); want cold|fresh|possibly_stale", out["freshness"], out["freshness"])
			}
		})
	}
}

// TestStampProvenanceKeepsLadderRung: a rung a ladder step already set is kept,
// and freshness that is already on the answer is not derived again. An answer
// with neither gets the given rung and the store's freshness.
func TestStampProvenanceKeepsLadderRung(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	kept, err := e.stampProvenance(ctx, map[string]any{
		"retrieval": map[string]any{"rung": "L2", "reason": "FTS5 keyword match"},
	}, "memory", "agent memory search")
	if err != nil {
		t.Fatal(err)
	}
	if r := kept["retrieval"].(map[string]any); r["rung"] != "L2" {
		t.Fatalf("ladder rung overwritten: %v", r)
	}

	already, err := e.stampProvenance(ctx, map[string]any{"freshness": "fresh"}, "memory", "agent memory search")
	if err != nil {
		t.Fatal(err)
	}
	if already["freshness"] != "fresh" {
		t.Fatalf("freshness recomputed over an existing value: %v", already["freshness"])
	}

	bare, err := e.stampProvenance(ctx, map[string]any{}, "memory", "agent memory search")
	if err != nil {
		t.Fatal(err)
	}
	if r := bare["retrieval"].(map[string]any); r["rung"] != "memory" || r["reason"] != "agent memory search" {
		t.Fatalf("bare answer retrieval = %v", r)
	}
	if bare["freshness"] != store.FreshnessCold {
		t.Fatalf("bare answer freshness = %v; want cold on an empty store", bare["freshness"])
	}
}

// TestStampProvenanceDegradesOnCountError: a store that cannot count its
// elements still answers a memory, git, lsp or pattern read. The answer keeps
// its rung and is labelled possibly_stale, the label store.Freshness gives an
// index it cannot read.
func TestStampProvenanceDegradesOnCountError(t *testing.T) {
	e, _ := newEngine(t)
	e.st = elementCountFails{Backend: e.st, cause: errors.New("count unreadable")}
	out, err := e.stampProvenance(context.Background(), map[string]any{}, "memory", "agent memory search")
	if err != nil {
		t.Fatalf("a count error failed the answer: %v", err)
	}
	if out["freshness"] != store.FreshnessPossiblyStale {
		t.Fatalf("freshness = %v; want %s", out["freshness"], store.FreshnessPossiblyStale)
	}
	if r := out["retrieval"].(map[string]any); r["rung"] != "memory" {
		t.Fatalf("retrieval = %v; want the given rung kept", r)
	}
}

// TestAnswerCarriesIndexedAt: an answer over a written index says when the
// index was last written (the watermark time that status already reports).
// The store here is not git-indexed, so head_sync is omitted, not "unknown".
func TestAnswerCarriesIndexedAt(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()
	if err := e.st.UpsertElements(ctx, []store.Element{
		{QualifiedName: "a.Thing", ElementType: "function", Name: "Thing", FilePath: "a.go", Language: "go", Content: "c"},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := e.Query(ctx, QueryRequest{Query: "Thing"})
	if err != nil {
		t.Fatal(err)
	}
	at, _ := out["indexed_at"].(int64)
	if at == 0 {
		t.Fatalf("indexed_at missing or zero after a write: keys=%v", keysOf(out))
	}
	_, seqAt, err := e.st.Watermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if at != seqAt {
		t.Fatalf("indexed_at = %d; want the watermark time %d", at, seqAt)
	}
	if _, ok := out["head_sync"]; ok {
		t.Fatalf("head_sync present for a store that was not git-indexed: %v", out["head_sync"])
	}
}

// TestAnswerHeadSyncTellsTheTreeMoved: a git-indexed answer says whether the
// store still mirrors HEAD, the same comparison status reports as head_sync.
func TestAnswerHeadSyncTellsTheTreeMoved(t *testing.T) {
	dir := gitRepoWithFile(t)
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(t.TempDir(), "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := index.IndexDir(context.Background(), st, dir); err != nil {
		t.Fatal(err)
	}
	e := New(st, Options{ProjectDir: dir})
	ask := func() string {
		t.Helper()
		out, err := e.Query(context.Background(), QueryRequest{Query: "Main"})
		if err != nil {
			t.Fatal(err)
		}
		hs, _ := out["head_sync"].(string)
		return hs
	}
	if got := ask(); got != wantHeadSyncMatch {
		t.Fatalf("head_sync = %q; want %q right after indexing", got, wantHeadSyncMatch)
	}
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, dir, "add", "-A")
	gittest.Git(t, dir, "commit", "-m", "drop main.go")
	if got := ask(); got != wantHeadSyncStale {
		t.Fatalf("head_sync = %q; want %q after the tree moved", got, wantHeadSyncStale)
	}
}
