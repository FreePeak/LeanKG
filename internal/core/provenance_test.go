package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/gitobj/gittest"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/store"
)

// provenanceExempt lists query actions the per-answer contract does not apply
// to, with the reason. Each one is an explicit skip in the test output.
var provenanceExempt = map[string]string{
	"lsp": "needs a language server on the host; the golden pins the refusal, and the env-gated vocabulary test covers the positive path",
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
			access, _ := out["access"].(map[string]any)
			if role, _ := access["role"].(string); role == "" {
				t.Errorf("access.role missing: access=%v", out["access"])
			}
			if project, _ := access["project"].(string); project == "" {
				t.Errorf("access.project missing: access=%v", out["access"])
			}
			if why, ok := provenanceGaps[key]; ok {
				if _, has := out["retrieval"]; has {
					t.Fatalf("query/%s now carries retrieval: remove it from provenanceGaps", key)
				}
				t.Skip("known gap: " + why)
			}
			r, _ := out["retrieval"].(map[string]any)
			if rung, _ := r["rung"].(string); rung == "" {
				t.Errorf("retrieval.rung missing: retrieval=%v", out["retrieval"])
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
	// Checking out the indexed commit moves HEAD back onto the store's tree.
	gittest.Git(t, dir, "checkout", "-q", "--detach", "HEAD~1")
	if got := ask(); got != wantHeadSyncMatch {
		t.Fatalf("head_sync = %q; want %q after checking out the indexed commit", got, wantHeadSyncMatch)
	}
}

// TestAccessBlockUnrestrictedCaller: with no caller in the context (stdio, the
// CLI) the answer names an unrestricted local admin and the served project.
// An open gate (no token configured) reads as admin via open. Neither carries
// a withheld count.
func TestAccessBlockUnrestrictedCaller(t *testing.T) {
	f := newActionFixture(t)
	out, err := f.e.Query(context.Background(), QueryRequest{Query: "Thing"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"role": "admin", "via": "local", "restricted": false, "project": "proj"}
	if !reflect.DeepEqual(out["access"], want) {
		t.Fatalf("no-caller access = %v; want %v", out["access"], want)
	}

	open := auth.WithAuth(context.Background(), auth.AuthContext{Role: auth.Admin, Via: auth.ViaOpen})
	out, err = f.e.Query(open, QueryRequest{Query: "Thing"})
	if err != nil {
		t.Fatal(err)
	}
	want["via"] = "open"
	if !reflect.DeepEqual(out["access"], want) {
		t.Fatalf("open-gate access = %v; want %v", out["access"], want)
	}
}

// TestAccessBlockRestrictedToken: a token limited to project proj gets the role
// it was granted, via token, restricted true. Nothing identifying the token,
// its account or its org reaches the answer, and neither does a scope or the
// name of another project.
func TestAccessBlockRestrictedToken(t *testing.T) {
	f := newActionFixture(t)
	ctx := auth.WithAuth(context.Background(), auth.AuthContext{
		AccountID: "acct-7f3c", OrgID: "org-91aa", TokenID: "tok-5e2d",
		Role: auth.Viewer, Via: auth.ViaToken,
		Scopes: []string{"project:proj", "project:zeta-private"},
	})
	out, err := f.e.Query(ctx, QueryRequest{Query: "Thing"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"role": "viewer", "via": "token", "restricted": true, "project": "proj"}
	if !reflect.DeepEqual(out["access"], want) {
		t.Fatalf("restricted access = %v; want %v", out["access"], want)
	}
	body, _ := json.Marshal(out)
	for _, leak := range []string{"acct-7f3c", "org-91aa", "tok-5e2d", "zeta-private", "project:"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("answer leaks %q: %s", leak, body)
		}
	}
}

// TestAccessBlockPortfolioWithheld: a fan-out names no single project, so it
// has no access.project. Its withheld count is the existing projects_withheld
// value, and the answer names no withheld project, id or scope.
func TestAccessBlockPortfolioWithheld(t *testing.T) {
	fleet := seedGrantFleet(t)
	ctx := auth.WithAuth(context.Background(), auth.AuthContext{
		TokenID: "tok-5e2d", Role: auth.Viewer, Via: auth.ViaToken, Scopes: []string{"project:alpha"},
	})
	out, err := fleet.engine.Query(ctx, QueryRequest{Action: "portfolio", Query: "Shared", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	access, _ := out["access"].(map[string]any)
	if access["restricted"] != true || access["via"] != "token" || access["role"] != "viewer" {
		t.Fatalf("granted portfolio access = %v; want viewer via token, restricted", access)
	}
	if _, ok := access["project"]; ok {
		t.Fatalf("portfolio access names a single project: %v", access)
	}
	withheld, _ := access["withheld"].(map[string]any)
	if withheld["projects"] != out["projects_withheld"] || withheld["projects"] != 1 {
		t.Fatalf("access.withheld = %v; want projects equal to projects_withheld (%v)", access["withheld"], out["projects_withheld"])
	}
	body, _ := json.Marshal(out)
	for _, leak := range []string{"beta", fleet.betaDir, fleet.regDB, "tok-5e2d", "project:"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("restricted portfolio answer leaks %q: %s", leak, body)
		}
	}

	open, err := fleet.engine.Query(context.Background(), QueryRequest{Action: "portfolio", Query: "Shared", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := open["access"].(map[string]any)["withheld"]; ok {
		t.Fatalf("unrestricted portfolio carries a withheld block: %v", open["access"])
	}
}
