package core

import (
	"context"
	"path"
	"slices"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestContextOnPackageListsImporters: query action=context with a package
// qualified name (its directory) returns the incoming and outgoing imports edges
// grouped by relationship type, the same way it groups any element's edges.
func TestContextOnPackageListsImporters(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()
	pkg := func(qn string) store.Element {
		return store.Element{QualifiedName: qn, ElementType: "package", Name: path.Base(qn), FilePath: qn, Language: "go"}
	}
	if err := e.st.UpsertElements(ctx, []store.Element{pkg("cmd/app"), pkg("internal/mcp"), pkg("internal/store")}); err != nil {
		t.Fatal(err)
	}
	imports := func(src, dst string) store.Relationship {
		return store.Relationship{Source: src, Target: dst, RelType: "imports", Confidence: 1}
	}
	if err := e.st.UpsertRelationships(ctx, []store.Relationship{
		imports("cmd/app", "internal/store"),
		imports("cmd/app", "internal/mcp"),
		imports("internal/mcp", "internal/store"),
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := e.Query(ctx, QueryRequest{Action: "context", Query: "internal/store"})
	if err != nil {
		t.Fatal(err)
	}
	incoming := stringsOf(t, resp, "incoming", "imports")
	if want := []string{"cmd/app", "internal/mcp"}; !slices.Equal(incoming, want) {
		t.Fatalf("incoming imports of internal/store = %v, want %v", incoming, want)
	}
	outgoing := stringsOf(t, resp, "outgoing", "imports")
	if len(outgoing) != 0 {
		t.Fatalf("outgoing imports of internal/store = %v, want none", outgoing)
	}
	if resp["resolved_query"] != nil {
		t.Fatalf("a package path is its own seed, got resolved_query %v", resp["resolved_query"])
	}
}

// stringsOf reads resp[dir][rel] as a list of qualified names.
func stringsOf(t *testing.T, resp map[string]any, dir, rel string) []string {
	t.Helper()
	group, ok := resp[dir].(map[string]any)
	if !ok {
		t.Fatalf("resp[%q] = %T, want a map grouped by rel_type", dir, resp[dir])
	}
	var out []string
	list, _ := group[rel].([]any)
	for _, v := range list {
		out = append(out, v.(string))
	}
	slices.Sort(out)
	return out
}

// TestStatusCoverageIgnoresPackageElements: after a full embed, every embeddable
// element holds a vector, so status reports coverage 1 even though the index
// also holds the package elements of the project's Go directories.
func TestStatusCoverageIgnoresPackageElements(t *testing.T) {
	dir := writeProject(t)
	e := engineAt(t, dir)
	p := embed.Deterministic(64)
	e.embedder = p
	ctx := context.Background()
	if _, err := e.Import(ctx, ImportRequest{Action: "repo", Path: "."}); err != nil {
		t.Fatal(err)
	}
	if _, err := embed.Run(ctx, e.st, p, "full"); err != nil {
		t.Fatal(err)
	}
	st, err := e.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byType, _ := st["elements_by_type"].(map[string]int)
	if byType["package"] == 0 {
		t.Fatalf("fixture has no package element: %v", byType)
	}
	embs, _ := st["embeddings"].([]map[string]any)
	if len(embs) != 1 {
		t.Fatalf("embeddings = %v, want one stamped model", st["embeddings"])
	}
	if got := embs[0]["coverage"]; got != 1.0 {
		t.Fatalf("coverage = %v, want 1 (package elements are not embeddable)", got)
	}
}
