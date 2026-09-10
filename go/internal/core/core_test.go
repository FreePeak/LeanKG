package core

import (
	"context"
	"github.com/FreePeak/LeanKG/go/internal/ontology"
	"github.com/FreePeak/LeanKG/go/internal/session"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/embed"
	"github.com/FreePeak/LeanKG/go/internal/memory"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

func newEngine(t *testing.T) (*Engine, *memory.Memory) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return New(st, mem, nil), mem
}

func TestResolveEnvelope(t *testing.T) {
	for tool, want := range map[string]string{
		"import": "import", "query": "query", "status": "status",
		"set": "import", "get": "query",
	} {
		got, err := ResolveEnvelope(tool)
		if err != nil || got != want {
			t.Fatalf("envelope %q: got %q err %v, want %q", tool, got, err, want)
		}
	}
	if _, err := ResolveEnvelope("delete_everything"); err == nil || !strings.Contains(err.Error(), "valid tools") {
		t.Fatalf("unknown envelope must error naming the surface: %v", err)
	}
}

func TestQueryLadderOrderAndProvenance(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	// L0 cold: guidance, never an error.
	out, err := e.Query(ctx, QueryRequest{Query: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	if out["freshness"] != "cold" || out["retrieval"].(map[string]any)["rung"] != "L0" {
		t.Fatalf("cold: %+v", out)
	}

	// Seed: exact-match element + fuzzy-only element.
	if err := e.st.UpsertElements([]store.Element{
		{QualifiedName: "a.parseConfig", ElementType: "function", Name: "parseConfig", FilePath: "a.go", Language: "go", Content: "parse config file"},
		{QualifiedName: "a.Widget", ElementType: "type", Name: "Widget", FilePath: "a.go", Language: "go", Content: "the frobnicator widget struct"},
	}); err != nil {
		t.Fatal(err)
	}

	// L1 exact identifier wins.
	out, err = e.Query(ctx, QueryRequest{Query: "parseConfig"})
	if err != nil {
		t.Fatal(err)
	}
	if r := out["retrieval"].(map[string]any); r["rung"] != "L1" {
		t.Fatalf("want L1, got %v (%v)", r, out["hits"])
	}
	if len(hitsOf(out)) == 0 || hitsOf(out)[0]["name"] != "parseConfig" {
		t.Fatalf("L1 hits: %v", out["hits"])
	}

	// L2 fuzzy for prose that only matches content.
	out, err = e.Query(ctx, QueryRequest{Query: "frobnicator widget struct"})
	if err != nil {
		t.Fatal(err)
	}
	if r := out["retrieval"].(map[string]any); r["rung"] != "L2" {
		t.Fatalf("want L2, got %v", r)
	}

	// Provenance present on every answer.
	if _, ok := out["retrieval"].(map[string]any)["reason"]; !ok {
		t.Fatal("retrieval.reason missing")
	}
}

func TestQuerySemanticDegradesToL2(t *testing.T) {
	// No provider wired: action=semantic degrades with a reason, no error.
	e, _ := newEngine(t)
	out, err := e.Query(context.Background(), QueryRequest{Action: "semantic", Query: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	r := out["retrieval"].(map[string]any)
	if r["rung"] != "L2" || !strings.Contains(r["reason"].(string), "no embedding provider") {
		t.Fatalf("semantic degrade: %v", r)
	}
}

func TestL3SemanticWithDeterministicProvider(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(16)
	// Deterministic vectors are kind-salted (query/document prefixes,
	// FR-ZCP-11 port), so to assert a CONTROLLED top hit we store the
	// query-kind embedding of "alpha code" as m.alpha's vector: querying
	// "alpha code" must then land on m.alpha with similarity 1.
	els := []store.Element{
		{QualifiedName: "m.alpha", ElementType: "function", Name: "alpha", FilePath: "m.go", Language: "go", Content: "alpha code"},
		{QualifiedName: "m.beta", ElementType: "function", Name: "beta", FilePath: "m.go", Language: "go", Content: "beta code"},
	}
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteStamp(store.ModelStamp{ModelID: p.ModelID(), Revision: p.Revision(), Dimensions: p.Dimensions(), Distance: p.Distance(), Provider: p.Provider()}); err != nil {
		t.Fatal(err)
	}
	qvec, err := p.Embed(context.Background(), embed.Query, []string{"alpha code"})
	if err != nil || len(qvec) != 1 {
		t.Fatalf("embed query: %v", err)
	}
	if err := st.UpsertVectors(p.ModelID(), []store.VectorRow{{QualifiedName: "m.alpha", Vec: qvec[0]}}); err != nil {
		t.Fatal(err)
	}

	embedder := QueryEmbedderFromProvider(p)
	e := New(st, nil, embedder)
	out, err := e.Query(context.Background(), QueryRequest{Action: "semantic", Query: "alpha code"})
	if err != nil {
		t.Fatal(err)
	}
	r := out["retrieval"].(map[string]any)
	if r["rung"] != "L3" {
		t.Fatalf("want L3, got %v (hits %v)", r, out["hits"])
	}
	if len(hitsOf(out)) == 0 || hitsOf(out)[0]["name"] != "alpha" {
		t.Fatalf("L3 top hit: %v", out["hits"])
	}

	// No-failure query through the same path: must not error regardless of rung.
	out, err = e.Query(context.Background(), QueryRequest{Action: "semantic", Query: "widget"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("no-failure query rung %v (fine)", out["retrieval"])
}

type failingProvider struct{ inner embed.Provider }

func (f failingProvider) ModelID() string  { return f.inner.ModelID() }
func (f failingProvider) Revision() string { return f.inner.Revision() }
func (f failingProvider) Dimensions() int  { return f.inner.Dimensions() }
func (f failingProvider) Distance() string { return f.inner.Distance() }
func (f failingProvider) Provider() string { return f.inner.Provider() }
func (f failingProvider) Embed(_ context.Context, _ embed.TextKind, _ []string) ([][]float32, error) {
	return nil, context.DeadlineExceeded
}

func TestL3ProviderFailureDegrades(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertElements([]store.Element{
		{QualifiedName: "x.y", ElementType: "function", Name: "y", FilePath: "x.go", Language: "go", Content: "some content here"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteStamp(store.ModelStamp{ModelID: "boom", Revision: "r", Dimensions: 4, Distance: "cosine", Provider: "test"}); err != nil {
		t.Fatal(err)
	}
	embedder := QueryEmbedderFromProvider(failingProvider{inner: embed.Deterministic(4)})
	e := New(st, nil, embedder)
	out, err := e.Query(context.Background(), QueryRequest{Action: "semantic", Query: "content"})
	if err != nil {
		t.Fatalf("provider failure must degrade, not error: %v", err)
	}
	r := out["retrieval"].(map[string]any)
	if r["rung"] != "L2" || !strings.Contains(r["reason"].(string), "degraded from L3") {
		t.Fatalf("degrade provenance: %v", r)
	}
}

func TestImportUnknownActionErrors(t *testing.T) {
	e, _ := newEngine(t)
	if _, err := e.Import(context.Background(), ImportRequest{Action: "explode"}); err == nil {
		t.Fatal("unknown import action must error")
	}
	if _, err := e.Import(context.Background(), ImportRequest{Action: "repo"}); err == nil {
		t.Fatal("repo without path must error")
	}
}

func TestMemoryWriteAndReadRouting(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()
	if _, err := e.Import(ctx, ImportRequest{
		Action: "memory", Command: "create", Path: "MEMORY.md", Args: map[string]any{"content": "# Core notes"},
	}); err != nil {
		t.Fatalf("memory create: %v", err)
	}
	out, err := e.MemoryRead("snapshot", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out["content"].(string), "# Core notes") {
		t.Fatalf("snapshot: %v", out)
	}
	out, err = e.MemoryRead("search", "", "notes", 5)
	if err != nil {
		t.Fatal(err)
	}
	hits := out["hits"].([]memory.Hit)
	if len(hits) != 1 || hits[0].Path != "MEMORY.md" {
		t.Fatalf("memory search: %+v", hits)
	}
}

func TestStatusShape(t *testing.T) {
	e, _ := newEngine(t)
	out, err := e.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tools := out["tools"].([]string)
	if len(tools) != 3 {
		t.Fatalf("status tools = %v, want exactly 3", tools)
	}
	if out["freshness"] != "cold" {
		t.Fatalf("freshness: %v", out["freshness"])
	}
	if out["backend"] != "sqlite" {
		t.Fatalf("backend: %v", out["backend"])
	}
}

func TestFreshnessStaleAfterWrite(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()
	if err := e.st.UpsertElements([]store.Element{
		{QualifiedName: "f.F", ElementType: "function", Name: "F", FilePath: "f.go", Language: "go", Content: "c"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.refreshInventory(); err != nil {
		t.Fatal(err)
	}
	out, _ := e.Query(ctx, QueryRequest{Query: "F"})
	if out["freshness"] != "fresh" {
		t.Fatalf("fresh after inventory: %v", out["freshness"])
	}
	// A write after the inventory flips the answer to possibly_stale — the
	// DB-resident watermark contract (no TTL cache to race).
	if err := e.st.UpsertRelationships([]store.Relationship{{Source: "f.F", Target: "f.F", RelType: "calls"}}); err != nil {
		t.Fatal(err)
	}
	out, _ = e.Query(ctx, QueryRequest{Query: "F"})
	if out["freshness"] != "possibly_stale" {
		t.Fatalf("stale after post-inventory write: %v", out["freshness"])
	}
}

func TestNLRoutePicksIdentifier(t *testing.T) {
	if got := NLRoute("how does the parseConfig function work?"); got != "parseConfig" {
		t.Fatalf("NLRoute = %q, want parseConfig", got)
	}
	// Longest identifier-looking token wins.
	if got := NLRoute("explain handle_request and parse helpers"); got != "handle_request" {
		t.Fatalf("NLRoute = %q, want handle_request", got)
	}
	// No identifier-looking token at all → input passes through unchanged.
	if got := NLRoute("42 + 7?"); got != "42 + 7?" {
		t.Fatalf("non-identifier input must pass through: %q", got)
	}
}

// TestL3StampDriftDegrades pins the query-side stamp contract (FR-ZCP-11
// part 2 port): a stored collection whose revision no longer matches the
// query embedder DEGRADES to L2 with a mismatch reason — never silently
// wrong L3 answers over a drifted collection.
func TestL3StampDriftDegrades(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertElements([]store.Element{
		{QualifiedName: "x.y", ElementType: "function", Name: "y", FilePath: "x.go", Language: "go", Content: "some content here"},
	}); err != nil {
		t.Fatal(err)
	}
	p := embed.Deterministic(8)
	if err := st.WriteStamp(store.ModelStamp{ModelID: p.ModelID(), Revision: "OLD-revision", Dimensions: 8, Distance: "cosine", Provider: p.Provider()}); err != nil {
		t.Fatal(err)
	}
	e := New(st, nil, QueryEmbedderFromProvider(p))
	out, err := e.Query(context.Background(), QueryRequest{Action: "semantic", Query: "content"})
	if err != nil {
		t.Fatal(err)
	}
	r := out["retrieval"].(map[string]any)
	if r["rung"] != "L2" || !strings.Contains(r["reason"].(string), "stamp mismatch") {
		t.Fatalf("drifted collection must degrade with mismatch reason: %v", r)
	}
}

// TestSessionOffloadRoundTripThroughCore proves internal/session is reachable
// from the 3-tool surface (not dead code): import{action:"session"} offloads,
// the query tool's session action recalls it bit-for-bit, and the canvas lists it.
func TestSessionOffloadRoundTripThroughCore(t *testing.T) {
	e, _ := newEngine(t)
	e.SetProjectDir(t.TempDir())
	ctx := context.Background()

	if _, err := e.Import(ctx, ImportRequest{
		Action: "session", Command: "offload",
		Args: map[string]any{"session_id": "s1", "node_id": "node-1", "payload": "bulky payload text", "summary": "a summary"},
	}); err != nil {
		t.Fatalf("offload: %v", err)
	}
	out, err := e.SessionRead("recall", "s1", "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if out["payload"] != "bulky payload text" {
		t.Fatalf("recall payload: %v", out["payload"])
	}
	out, err = e.SessionRead("canvas", "s1", "")
	if err != nil {
		t.Fatal(err)
	}
	if refs, ok := out["refs"].([]session.Ref); !ok || len(refs) != 1 {
		t.Fatalf("canvas: %+v", out)
	}
}

// TestOntologyMatchThroughCore proves internal/ontology is reachable: a
// catalog matched against indexed elements persists and reads back.
func TestOntologyMatchThroughCore(t *testing.T) {
	e, _ := newEngine(t)
	if err := e.st.UpsertElements([]store.Element{
		{QualifiedName: "pkg.Login", ElementType: "function", Name: "Login", FilePath: "auth.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	catPath := filepath.Join(t.TempDir(), "concepts.json")
	if err := os.WriteFile(catPath, []byte(`{"concepts":[{"id":"auth","label":"Authentication","aliases":["login"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e.OntologyMatch(catPath)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if matches, ok := out["matches"].([]ontology.Match); !ok || len(matches) != 1 {
		t.Fatalf("matches: %+v", out)
	}
	again, err := e.OntologyMatches()
	if err != nil {
		t.Fatal(err)
	}
	if matches, ok := again["matches"].([]ontology.Match); !ok || len(matches) != 1 {
		t.Fatalf("persisted matches: %+v", again)
	}
}
