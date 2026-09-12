package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

const testTS = "2026-08-22T00:00:00Z"

// markdownFixture mirrors the Rust export_markdown test fixture: two files,
// one hub, a dangling edge and two folder clusters.
func markdownFixture() *MarkdownDoc {
	return &MarkdownDoc{
		Project:        "demo",
		GeneratedAtUTC: testTS,
		Elements: []store.Element{
			el("src/app.rs::hub", "hub", "function", "src/app.rs", 1),
			el("src/app.rs::leaf", "leaf", "function", "src/app.rs", 10),
			el("src/lib.rs::init", "init", "function", "src/lib.rs", 1),
			el("src/lib.rs", "lib.rs", "file", "src/lib.rs", 0),
		},
		Relationships: []store.Relationship{
			rel("src/app.rs::hub", "src/app.rs::leaf", "calls"),
			rel("src/lib.rs::init", "src/app.rs::leaf", "calls"),
			rel("src/app.rs::hub", "src/lib.rs::init", "imports"),
			rel("x", "y", "references"), // dangling endpoints still counted
		},
		GodNodes: []GodNode{
			{QualifiedName: "src/app.rs::hub", Name: "hub", ElementType: "function", Degree: 3},
			{QualifiedName: "src/lib.rs::init", Name: "init", ElementType: "function", Degree: 1},
		},
		Clusters: []Cluster{
			{
				ID: "dir:src/app", Label: "app",
				Members:             []string{"src/app.rs::hub", "src/app.rs::leaf"},
				RepresentativeFiles: []string{"src/app.rs"},
			},
			{
				ID: "dir:src/lib", Label: "lib",
				Members:             []string{"src/lib.rs::init"},
				RepresentativeFiles: []string{"src/lib.rs"},
			},
		},
	}
}

// sectionRange is the Rust test helper: from `start` to the next "\n## ".
func sectionRange(t *testing.T, md, start string) string {
	t.Helper()
	i := strings.Index(md, start)
	if i < 0 {
		t.Fatalf("missing section %q", start)
	}
	rest := md[i+len(start):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return md[i : i+len(start)+j+1]
	}
	return md[i:]
}

func TestMarkdownSectionsEmittedInDeclaredOrder(t *testing.T) {
	md := Markdown(markdownFixture())
	order := []string{
		"# LeanKG Graph Docs",
		"## Overview",
		"### Elements by type",
		"### Relationships by type",
		"## Top Clusters",
		"## God Nodes (top 10 by degree)",
		"## Architecture Tree",
		"## Cluster Details",
	}
	last := 0
	for _, h := range order {
		pos := strings.Index(md, h)
		if pos < 0 {
			t.Fatalf("missing %q", h)
		}
		if pos < last {
			t.Fatalf("section %q out of order", h)
		}
		last = pos
	}
}

func TestMarkdownOverviewCountsAndTypes(t *testing.T) {
	md := Markdown(markdownFixture())
	for _, want := range []string{
		"---\ntitle: LeanKG Graph Docs\nproject: demo\ngenerated_at: " + testTS + "\n---\n",
		"- Project: `demo`\n",
		"- Elements: 4\n",
		"- Relationships: 4\n",
		"- Clusters: 2\n",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("missing %q in:\n%s", want, md)
		}
	}

	elems := sectionRange(t, md, "### Elements by type")
	if !strings.Contains(elems, "| function | 3 |\n") || !strings.Contains(elems, "| file | 1 |\n") {
		t.Fatalf("element type table:\n%s", elems)
	}
	if strings.Index(elems, "| file |") > strings.Index(elems, "| function |") {
		t.Fatalf("type rows must be sorted:\n%s", elems)
	}

	rels := sectionRange(t, md, "### Relationships by type")
	for _, row := range []string{"| calls | 2 |\n", "| imports | 1 |\n", "| references | 1 |\n"} {
		if !strings.Contains(rels, row) {
			t.Fatalf("missing %q in:\n%s", row, rels)
		}
	}
}

func TestMarkdownEmptyGraph(t *testing.T) {
	md := Markdown(&MarkdownDoc{Project: "demo", GeneratedAtUTC: testTS})
	if !strings.HasPrefix(md, "---\ntitle: LeanKG Graph Docs\n") {
		t.Fatalf("front matter:\n%s", md)
	}
	for _, want := range []string{"- Elements: 0\n", "- Relationships: 0\n", "- Clusters: 0\n", "| Type | Count |", "_No indexed elements._"} {
		if !strings.Contains(md, want) {
			t.Fatalf("missing %q in:\n%s", want, md)
		}
	}
	if strings.Contains(md, "## Cluster Details") {
		t.Fatalf("cluster details must be skipped when there are no clusters:\n%s", md)
	}
}

func TestMarkdownByteDeterministicRegardlessOfInputOrder(t *testing.T) {
	a := Markdown(markdownFixture())

	d := markdownFixture()
	reverseElements(d.Elements)
	reverseRels(d.Relationships)
	reverseGods(d.GodNodes)
	reverseClusters(d.Clusters)
	b := Markdown(d)
	if a != b {
		t.Fatalf("output must be a pure function of graph state:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
	if again := Markdown(markdownFixture()); a != again {
		t.Fatal("two renders of the same document must be byte-equal")
	}
}
func TestMarkdownGodNodesCappedAtTen(t *testing.T) {
	gods := make([]GodNode, 0, 14)
	els := make([]store.Element, 0, 14)
	for i := 0; i < 14; i++ {
		qn := fmt.Sprintf("src/m.rs::n%02d", i)
		gods = append(gods, GodNode{QualifiedName: qn, Name: qn, ElementType: "function", Degree: i % 3})
		els = append(els, el(qn, qn, "function", "src/m.rs", 1))
	}
	reverseGods(gods) // scrambled input order
	md := Markdown(&MarkdownDoc{
		Project: "demo", GeneratedAtUTC: testTS, Elements: els, GodNodes: gods,
	})
	table := sectionRange(t, md, "## God Nodes")

	var degrees []int
	for _, l := range strings.Split(table, "\n") {
		if !strings.HasPrefix(l, "| ") || strings.Contains(l, "Degree") {
			continue
		}
		var d int
		if _, err := fmt.Sscanf(strings.TrimSpace(strings.Split(l, "|")[1]), "%d", &d); err != nil {
			t.Fatalf("unparsable degree row %q", l)
		}
		degrees = append(degrees, d)
	}
	if len(degrees) != 10 {
		t.Fatalf("exactly top-10 rendered, got %d:\n%s", len(degrees), table)
	}
	// Four deg-2 nodes, five deg-1 nodes, then the first deg-0 by qualified
	// name (the same expected sequence the Rust test asserts).
	want := []int{2, 2, 2, 2, 1, 1, 1, 1, 1, 0}
	for i := range want {
		if degrees[i] != want[i] {
			t.Fatalf("degree desc, ties by qualified name asc:\n got %v\nwant %v", degrees, want)
		}
	}
}

func TestMarkdownArchitectureTreeDepthCappedAtFour(t *testing.T) {
	els := []store.Element{
		el("a/b/c/d/e/f.rs::deep_fn", "deep_fn", "function", "a/b/c/d/e/f.rs", 7),
		el("src/app.rs::hub", "hub", "function", "src/app.rs", 1),
	}
	md := Markdown(&MarkdownDoc{Project: "demo", GeneratedAtUTC: testTS, Elements: els})
	tree := sectionRange(t, md, "## Architecture Tree")

	for _, want := range []string{"- src/\n", "  - app.rs\n", "    - hub (function)\n", "- … (1 more)\n"} {
		if !strings.Contains(tree, want) {
			t.Fatalf("missing %q in:\n%s", want, tree)
		}
	}
	for _, forbidden := range []string{"e/", "f.rs", "deep_fn"} {
		if strings.Contains(tree, forbidden) {
			t.Fatalf("must not expand past depth 4 (%q):\n%s", forbidden, tree)
		}
	}
}

func TestMarkdownTopClustersRanked(t *testing.T) {
	md := Markdown(markdownFixture())
	table := sectionRange(t, md, "## Top Clusters")
	appPos := strings.Index(table, "`dir:src/app`")
	libPos := strings.Index(table, "`dir:src/lib`")
	if appPos < 0 || libPos < 0 {
		t.Fatalf("cluster rows missing:\n%s", table)
	}
	if appPos > libPos {
		t.Fatalf("bigger cluster first:\n%s", table)
	}
	if !strings.Contains(table, "| `dir:src/app` | app | 2 |\n") {
		t.Fatalf("cluster row shape:\n%s", table)
	}
}

func TestMarkdownClusterDetails(t *testing.T) {
	d := markdownFixture()
	d.Clusters[0].Members = append(d.Clusters[0].Members, "src/app.rs::aaa")
	d.Elements = append(d.Elements, el("src/app.rs::aaa", "aaa", "function", "src/app.rs", 20))

	md := Markdown(d)
	block := sectionRange(t, md, "## Cluster Details")
	for _, want := range []string{
		"### app (`dir:src/app`)",
		"3 members across 1 files.",
		"- `src/app.rs`\n",
		"(src/app.rs#L1)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("missing %q in:\n%s", want, block)
		}
	}
	aaa := strings.Index(block, "src/app.rs::aaa")
	hub := strings.Index(block, "src/app.rs::hub")
	leaf := strings.Index(block, "src/app.rs::leaf")
	if !(aaa < hub && hub < leaf) {
		t.Fatalf("key symbols sorted by qualified name:\n%s", block)
	}
}

func TestCollectMarkdownFromStore(t *testing.T) {
	st := newFixture(t, testFixElements, testFixRels)
	doc, err := CollectMarkdown(st, "demo", testTS)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(doc.Elements) != 4 || len(doc.Relationships) != 4 {
		t.Fatalf("collected %d elements, %d relationships", len(doc.Elements), len(doc.Relationships))
	}
	// God nodes rank by degree (hub: 2 out + 0 in; leaf: 0 out + 2 in).
	// God nodes rank by degree (hub/leaf/init at 2, the dangling x→y endpoints
	// at 1 with no element row to enrich them from).
	if len(doc.GodNodes) != 5 {
		t.Fatalf("god nodes: %+v", doc.GodNodes)
	}
	if doc.GodNodes[0].QualifiedName != "src/app.rs::hub" || doc.GodNodes[0].Degree != 2 {
		t.Fatalf("god node ranking: %+v", doc.GodNodes[0])
	}
	if doc.GodNodes[0].ElementType != "function" {
		t.Fatalf("god node enrichment: %+v", doc.GodNodes[0])
	}
	if last := doc.GodNodes[4]; last.QualifiedName != "y" || last.ElementType != "" {
		t.Fatalf("unranked endpoints keep empty metadata: %+v", last)
	}
	// Clusters fall back to the deterministic folder grouping; the fixture's
	// four files share the src/ directory, so it yields exactly one cluster.
	if len(doc.Clusters) != 1 {
		t.Fatalf("folder clusters: %+v", doc.Clusters)
	}
	c := doc.Clusters[0]
	if c.ID != "dir:src" || c.Label != "src" || len(c.RepresentativeFiles) != 1 || c.RepresentativeFiles[0] != "src" {
		t.Fatalf("folder cluster: %+v", c)
	}
	if got := c.Members; len(got) != 4 || got[0] != "src/app.rs::hub" {
		t.Fatalf("cluster members: %v", got)
	}

	a := Markdown(doc)
	b := Markdown(mustCollect(t, st))
	if a != b {
		t.Fatal("same store must produce byte-identical markdown")
	}
}

func TestExportMarkdownWritesArtifact(t *testing.T) {
	st := newFixture(t, testFixElements, testFixRels)
	project := t.TempDir()

	res, err := ExportMarkdown(st, project, "")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	want := filepath.Join(project, ".leankg", "graph-docs.md")
	if res.Path != want {
		t.Fatalf("default destination: %q, want %q", res.Path, want)
	}
	body, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if !strings.Contains(string(body), "# LeanKG Graph Docs") || !strings.Contains(string(body), "- Elements: 4\n") {
		t.Fatalf("artifact body:\n%s", body)
	}
	if res.Elements != 4 || res.Relationships != 4 || res.Clusters != 1 {
		t.Fatalf("result: %+v", res)
	}

	// A relative --out anchors on the project root and creates its parents.
	res, err = ExportMarkdown(st, project, "docs/graph.md")
	if err != nil {
		t.Fatalf("export rel: %v", err)
	}
	if res.Path != filepath.Join(project, "docs", "graph.md") {
		t.Fatalf("relative destination: %q", res.Path)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatalf("relative artifact: %v", err)
	}
}

func mustCollect(t *testing.T, st store.Backend) *MarkdownDoc {
	t.Helper()
	doc, err := CollectMarkdown(st, "demo", testTS)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return doc
}

func reverseElements(s []store.Element) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func reverseRels(s []store.Relationship) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func reverseGods(s []GodNode) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func TestFolderClustersGroupByDirectory(t *testing.T) {
	els := []store.Element{
		el("src/b.rs::g", "g", "function", "src/b.rs", 1),
		el("src/a.rs::f", "f", "function", "src/a.rs", 1),
		el("web/main.go::run", "run", "function", "web/main.go", 1),
	}
	clusters := folderClusters(els)
	if len(clusters) != 2 {
		t.Fatalf("clusters: %+v", clusters)
	}
	first := clusters[0]
	if first.ID != "dir:src" || first.Label != "src" {
		t.Fatalf("first cluster: %+v", first)
	}
	if len(first.Members) != 2 || first.Members[0] != "src/a.rs::f" || first.Members[1] != "src/b.rs::g" {
		t.Fatalf("members must be sorted by qualified name: %v", first.Members)
	}
	if second := clusters[1]; second.ID != "dir:web" || second.Label != "web" {
		t.Fatalf("second cluster: %+v", second)
	}
}

func reverseClusters(s []Cluster) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
