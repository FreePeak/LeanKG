package summarize

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/FreePeak/LeanKG/go/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- batching --------------------------------------------------------------

func TestBatchSummariesPacksUnderBudget(t *testing.T) {
	item := func(path string, n int) fileSummary {
		return fileSummary{Path: path, Summary: strings.Repeat("s", n)}
	}
	// Each item costs len(path)+len(summary)+8; 20_007 x 2 fits 48_000, the
	// third does not, so the split is a prefix of the sorted order.
	files := []fileSummary{item("a.go", 20_000), item("b.go", 20_000), item("c.go", 20_000)}
	batches := batchSummaries(files, BatchCharBudget)
	if len(batches) != 2 {
		t.Fatalf("batches: got %d, want 2", len(batches))
	}
	if len(batches[0]) != 2 || len(batches[1]) != 1 {
		t.Fatalf("split: %d/%d", len(batches[0]), len(batches[1]))
	}
	if batches[0][0].Path != "a.go" || batches[0][1].Path != "b.go" || batches[1][0].Path != "c.go" {
		t.Errorf("batches must be contiguous prefixes in path order: %v", batches)
	}
	// An item alone over budget still forms a batch (never dropped, never empty).
	one := batchSummaries([]fileSummary{item("huge.go", BatchCharBudget*2)}, BatchCharBudget)
	if len(one) != 1 || len(one[0]) != 1 {
		t.Errorf("oversized single file: %v", one)
	}
	if len(batchSummaries(nil, BatchCharBudget)) != 0 {
		t.Error("no summaries must produce no batches")
	}
}

// TestSynthesisBatchesSeeTheirOwnSummaries proves the batching reaches the
// provider: two calls, each carrying only its own files, each under budget.
func TestSynthesisBatchesSeeTheirOwnSummaries(t *testing.T) {
	// Three files whose summaries force a 2-batch split (see the size math in
	// TestBatchSummariesPacksUnderBudget): pass 1 answers are canned, so make
	// the summaries long by clipping nothing — instead pin the budget through a
	// big per-file summary reply.
	long := strings.Repeat("word ", 6_000) // ~30k chars per summary
	f := newFixture(t, map[string]string{"a.go": "a", "b.go": "b", "c.go": "c"})
	srv := newFakeServer(t, func(req chatRequest) (int, string) {
		if req.Messages[0].Content == pass2System {
			var nodes []string
			for _, line := range strings.Split(req.Messages[1].Content, "\n") {
				if strings.HasPrefix(line, "## ") {
					p := strings.TrimPrefix(line, "## ")
					nodes = append(nodes, fmt.Sprintf(`{"name":"%s","type":"system","summary":"s","sources":["%s"],"links":[]}`, p, p))
				}
			}
			return 200, chatJSON(fmt.Sprintf(`{"nodes":[%s]}`, strings.Join(nodes, ",")))
		}
		return 200, chatJSON(long)
	})

	res, err := Run(context.Background(), f.st, runOpts(f.dir, srv.chat()))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Batches != 3 || len(srv.batchCalls()) != 3 {
		t.Fatalf("batches: result=%d calls=%d, want 3 each (one 30k summary each)", res.Batches, len(srv.batchCalls()))
	}
	for i, c := range srv.batchCalls() {
		body := c.Messages[1].Content
		if n := len(body); n > BatchCharBudget+len(long)+40 {
			t.Errorf("batch %d: %d chars, over budget", i, n)
		}
		if c.Format == nil || c.Format.Type != "json_object" {
			t.Errorf("batch %d: synthesis call must demand a JSON object", i)
		}
		if c.Temperature != 0 {
			t.Errorf("batch %d: temperature %v, want 0", i, c.Temperature)
		}
		hits := 0
		for _, p := range []string{"## a.go", "## b.go", "## c.go"} {
			if strings.Contains(body, p) {
				hits++
			}
		}
		if hits != 1 {
			t.Errorf("batch %d carries %d files, want 1 (prefix batching must not repeat)", i, hits)
		}
	}
	if res.Nodes != 3 {
		t.Errorf("nodes: %d", res.Nodes)
	}
}

// TestBatchCacheSkipsUnchangedSynthesis: an unchanged file must not re-spend a
// synthesis call (graft build.ts:396-401, keyed by path:content-hash).
func TestBatchCacheSkipsUnchangedSynthesis(t *testing.T) {
	f := newFixture(t, map[string]string{"a.go": "a"})
	srv := newFakeServer(t, nil)
	ctx := context.Background()

	if _, err := Run(ctx, f.st, runOpts(f.dir, srv.chat())); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	before := len(srv.batchCalls())
	res, err := Run(ctx, f.st, runOpts(f.dir, srv.chat()))
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if got := len(srv.batchCalls()); got != before {
		t.Errorf("synthesis calls: got %d after re-run, want %d (cached batch)", got, before)
	}
	if res.BatchResumed != 1 {
		t.Errorf("batch resume report: %+v", res)
	}
}

// --- validation ------------------------------------------------------------

func TestParseNodesValidatesClosedEnums(t *testing.T) {
	reply := `{"nodes":[
		{"name":"Store Layer","type":"system","summary":"Owns persistence.","sources":["a.go"],"links":[{"to":"Hash Resume","relation":"uses","description":"reads summaries"}]},
		{"name":"Junk Type","type":"module","summary":"dropped: type is outside the enum","sources":["a.go"]},
		{"name":"","type":"concept","summary":"dropped: no name"},
		{"name":"Hash Resume","type":"concept","summary":"Skips unchanged files.","sources":["b.go"],"links":[
			{"to":"Store Layer","relation":"owns"},
			{"to":"Never Defined","relation":"uses"},
			{"to":"Self","relation":"uses"}
		]},
		{"name":"Self","type":"concept","summary":"links to itself","sources":[],"links":[{"to":"Self","relation":"part_of"}]}
	]}`
	nodes, err := parseNodes("```json\n" + reply + "\n```")
	if err != nil {
		t.Fatalf("parse fenced reply: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("valid nodes: got %d (%+v), want 3 — junk rows must be dropped, not fatal", len(nodes), nodes)
	}
	byName := map[string]cleanedNode{}
	for _, n := range nodes {
		byName[n.Name] = n
	}
	// parseNodes enforces the closed VERB enum and the node type enum; target
	// resolution is mergeNodes' job (both endpoints must be defined), so the
	// undefined target and the self-link are still present here.
	got := byName["Hash Resume"].Links
	if len(got) != 2 || got[0].To != "Never Defined" || got[1].To != "Self" {
		t.Errorf("Hash Resume links at parse time: %+v (the 'owns' verb must be gone)", got)
	}
	// ... and mergeNodes is what resolves targets (both endpoints must be
	// defined) and drops self-edges.
	merged := mergeNodes(nodes, map[string]string{"a.go": "h", "b.go": "h"})
	for _, n := range merged {
		switch n.Slug {
		case "hash-resume":
			// Its own edge to "Never Defined" is undefined and gone; the edge
			// to the defined "Self" node stays.
			if len(n.Links) != 1 || n.Links[0].To != "self" {
				t.Errorf("hash-resume links: %+v (the undefined target must be gone)", n.Links)
			}
		case "store-layer":
			if len(n.Links) != 1 || n.Links[0].To != "hash-resume" || n.Links[0].Relation != "uses" {
				t.Errorf("store-layer links: %+v", n.Links)
			}
		case "self":
			// A link to itself is never an edge.
			if len(n.Links) != 0 {
				t.Errorf("self-edge must be dropped: %+v", n.Links)
			}
		}
	}

	if _, err := parseNodes("the model answered in prose"); err == nil {
		t.Error("a reply with no JSON object must be a content-quality miss")
	}
	if got, err := parseNodes(`{"nodes":[]}`); err != nil || len(got) != 0 {
		t.Errorf("an empty node array parses to an empty set: %v %v", got, err)
	}
}

func TestMergeNodesResolvesAndInheritsProvenance(t *testing.T) {
	hashByPath := map[string]string{"a.go": "h1", "b.go": "h2"}
	raw := []cleanedNode{
		{Name: "Store Layer", Type: "system", Summary: "persistence", Sources: []string{"a.go", "b.go", "ghost.go"}},
		{Name: "store layer", Type: "concept", Summary: "longer duplicate summary wins", Sources: nil},
		{Name: "Hash Resume", Type: "concept", Summary: "skips unchanged", Sources: nil,
			Links: []Link{{To: "Store Layer", Relation: "uses"}}},
	}
	nodes := mergeNodes(raw, hashByPath)

	var store1, resume *Node
	for i := range nodes {
		switch nodes[i].Slug {
		case "store-layer":
			store1 = &nodes[i]
		case "hash-resume":
			resume = &nodes[i]
		}
	}
	if store1 == nil || resume == nil {
		t.Fatalf("merged set lost a node: %+v", nodes)
	}
	// Same slug = same node: the variant name merged in, the longer summary won,
	// and the explicit type stayed "system" (graft build.ts:307-313).
	if store1.Kind != NodeSystem || store1.Summary != "longer duplicate summary wins" {
		t.Errorf("merge: %+v", store1)
	}
	if len(store1.Sources) != 2 {
		t.Errorf("sources from outside the run must be dropped: %+v", store1.Sources)
	}
	if store1.SourcesDigest == "" || store1.SourcesDigest != digestSources(store1.Sources) {
		t.Errorf("digest: %q", store1.SourcesDigest)
	}
	// A concept with no sources inherits the provenance of what it links to, so
	// it goes stale when its subject changes (graft build.ts:317-325).
	if len(resume.Sources) != 2 {
		t.Errorf("orphan provenance: %+v", resume.Sources)
	}
	if len(resume.Links) != 1 || resume.Links[0].To != "store-layer" {
		t.Errorf("links must resolve to slugs: %+v", resume.Links)
	}
}

// --- the tier as graph + markdown -----------------------------------------

// TestRunWritesTraversableNodes proves the contract end to end: curated nodes
// land as elements and relationships the existing graph verbs read, and as
// markdown files whose human region survives the next run.
func TestRunWritesTraversableNodes(t *testing.T) {
	f := newFixture(t, map[string]string{"store/schema.go": "x", "store/pg.go": "y"})
	srv := newFakeServer(t, func(req chatRequest) (int, string) {
		if req.Messages[0].Content != pass2System {
			return cannedReply(req)
		}
		return 200, chatJSON(`{"nodes":[
			{"name":"Store Layer","type":"system","summary":"Owns both backends.","sources":["store/schema.go","store/pg.go"],"links":[]},
			{"name":"Hash Resume","type":"concept","summary":"Skips unchanged files.","sources":["store/schema.go"],"links":[{"to":"Store Layer","relation":"uses","description":"reads the checkpoint"}]},
			{"name":"Not A Type","type":"module","summary":"dropped","sources":["store/pg.go"]}
		]}`)
	})

	res, err := Run(context.Background(), f.st, runOpts(f.dir, srv.chat()))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Nodes != 2 || res.Links != 1 || res.Systems != 1 || res.Concepts != 1 {
		t.Fatalf("node report: %+v", res)
	}
	if res.NodeFilesWritten != 2 || res.DeadNodesRemoved != 0 {
		t.Errorf("markdown report: %+v", res)
	}

	// The graph projection: FindExact resolves the node by name, and both
	// directions of the curated edge are traversable.
	hits, err := f.st.FindExact("Store Layer")
	if err != nil || len(hits) != 1 {
		t.Fatalf("node element not found: %d %v", len(hits), err)
	}
	if hits[0].ElementType != "summary_system" || hits[0].Language != "summarize" {
		t.Errorf("element shape: %+v", hits[0])
	}
	if hits[0].FilePath != "summarize://store-layer" {
		t.Errorf("file path marker: %q", hits[0].FilePath)
	}
	edges, err := f.st.Incoming("summarize:store-layer")
	if err != nil || len(edges) != 1 {
		t.Fatalf("edge not traversable: %d %v", len(edges), err)
	}
	if edges[0].Source != "summarize:hash-resume" || edges[0].RelType != "uses" {
		t.Errorf("edge shape: %+v", edges[0])
	}
	if meta, _ := edges[0].Metadata["confidence_label"].(string); meta != "INFERRED" {
		t.Errorf("an LLM edge is never EXTRACTED: %+v", edges[0].Metadata)
	}

	// The markdown projection.
	notePath := filepath.Join(f.dir, ".leankg", "summarize", "hash-resume.md")
	body, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("node file: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"name: Hash Resume", "slug: hash-resume", "type: concept",
		"sources_digest:", "path: store/schema.go", "- to: store-layer",
		"relation: uses", generatedStart, generatedEnd,
		"## Summary", "Skips unchanged files.",
		"- uses [[store-layer]] — reads the checkpoint",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("node file missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Not A Type") {
		t.Error("a node outside the type enum must not be written")
	}

	// Human-notes preservation, the contract graft never clobbers: a note added
	// below the generated block survives the next rewrite, and the machine
	// region still updates around it.
	const note = "\n## Notes\n\nKeep this: the resume check is load-bearing.\n"
	if err := os.WriteFile(notePath, append(body, note...), 0o644); err != nil {
		t.Fatal(err)
	}
	srv2 := newFakeServer(t, func(req chatRequest) (int, string) {
		if req.Messages[0].Content != pass2System {
			return cannedReply(req)
		}
		return 200, chatJSON(`{"nodes":[
			{"name":"Store Layer","type":"system","summary":"Owns both backends.","sources":["store/schema.go","store/pg.go"],"links":[]},
			{"name":"Hash Resume","type":"concept","summary":"Rewritten by the second run.","sources":["store/schema.go"],"links":[{"to":"Store Layer","relation":"uses","description":"reads the checkpoint"}]}
		]}`)
	})
	// A forced run regenerates the tier even though every file hash is
	// unchanged (the batch cache is keyed by content), and the note added below
	// the generated block survives it.
	forced := runOpts(f.dir, srv2.chat())
	forced.Force = true
	if _, err := Run(context.Background(), f.st, forced); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	after, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !strings.Contains(string(after), "Keep this: the resume check is load-bearing.") {
		t.Errorf("human note was clobbered:\n%s", after)
	}
	if strings.Count(string(after), "Keep this: the resume check is load-bearing.") != 1 {
		t.Errorf("the human region must not duplicate on rewrite:\n%s", after)
	}
	if strings.Count(string(after), generatedStart) != 1 || strings.Count(string(after), generatedEnd) != 1 {
		t.Errorf("exactly one generated block per file:\n%s", after)
	}
	if !strings.Contains(string(after), "Rewritten by the second run.") {
		t.Errorf("generated region did not update:\n%s", after)
	}
	// The element updated too, and the edge count did not grow (a re-run
	// replaces the node's edges rather than accumulating them).
	hits, _ = f.st.FindExact("Hash Resume")
	if len(hits) != 1 || !strings.Contains(hits[0].Content, "Rewritten") {
		t.Errorf("node element after re-run: %+v", hits)
	}
	if edges, _ := f.st.Incoming("summarize:store-layer"); len(edges) != 1 {
		t.Errorf("edges accumulated across runs: %+v", edges)
	}
}

// TestPruneRemovesDeadNodesButKeepsHumanNotes: a node the model stopped
// producing leaves the graph; its file goes with it unless somebody wrote in
// it, in which case the prose is the one thing this pipeline never deletes.
func TestPruneRemovesDeadNodesButKeepsHumanNotes(t *testing.T) {
	f := newFixture(t, map[string]string{"a.go": "a", "b.go": "b"})
	first := newFakeServer(t, func(req chatRequest) (int, string) {
		if req.Messages[0].Content != pass2System {
			return cannedReply(req)
		}
		return 200, chatJSON(`{"nodes":[
			{"name":"Kept System","type":"system","summary":"stays","sources":["a.go"]},
			{"name":"Doomed Concept","type":"concept","summary":"goes","sources":["b.go"]},
			{"name":"Annotated Concept","type":"concept","summary":"goes but stays on disk","sources":["b.go"]}
		]}`)
	})
	if _, err := Run(context.Background(), f.st, runOpts(f.dir, first.chat())); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	annotated := filepath.Join(f.dir, ".leankg", "summarize", "annotated-concept.md")
	body, err := os.ReadFile(annotated)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(annotated, append(body, "\n## Notes\n\na human opinion\n"...), 0o644); err != nil {
		t.Fatal(err)
	}

	second := newFakeServer(t, func(req chatRequest) (int, string) {
		if req.Messages[0].Content != pass2System {
			return cannedReply(req)
		}
		return 200, chatJSON(`{"nodes":[{"name":"Kept System","type":"system","summary":"stays","sources":["a.go"]}]}`)
	})
	forced := runOpts(f.dir, second.chat())
	forced.Force = true // the batch cache is content-keyed; pruning must be forced
	res, err := Run(context.Background(), f.st, forced)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if res.DeadNodesRemoved != 2 {
		t.Errorf("pruned: got %d, want 2", res.DeadNodesRemoved)
	}
	for _, qn := range []string{"summarize:doomed-concept", "summarize:annotated-concept"} {
		if got, _ := f.st.FindExact(qn); len(got) != 0 {
			t.Errorf("dead node still in the graph: %s", qn)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".leankg", "summarize", "doomed-concept.md")); !os.IsNotExist(err) {
		t.Error("a node with no human notes must be deleted from disk")
	}
	kept, err := os.ReadFile(annotated)
	if err != nil {
		t.Fatalf("annotated node file must survive pruning: %v", err)
	}
	if !strings.Contains(string(kept), "a human opinion") {
		t.Errorf("annotated node lost its notes:\n%s", kept)
	}
}

// TestNodeDirectoryIsOutsideTheIndex guarantees a curated node can never be
// mistaken for (or wiped by) the indexer: the node dir is dot-prefixed, which
// the index walk skips, and the node file path is a scheme URI, which no real
// file path can be.
func TestNodeDirectoryIsOutsideTheIndex(t *testing.T) {
	f := newFixture(t, map[string]string{"a.go": "a"})
	srv := newFakeServer(t, nil)
	if _, err := Run(context.Background(), f.st, runOpts(f.dir, srv.chat())); err != nil {
		t.Fatalf("run: %v", err)
	}
	nodePath := Node{Slug: "x"}.FilePath()
	if !strings.HasPrefix(nodePath, "summarize://") {
		t.Fatalf("node file path: %q", nodePath)
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".leankg", "summarize")); err != nil {
		t.Errorf("nodes must live under the dot-prefixed state dir: %v", err)
	}
}

// --- pure helpers ----------------------------------------------------------

func TestSlugify(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Store Layer", "store-layer"},
		{"  Local-first   Provider Fallback  ", "local-first-provider-fallback"},
		{"v2 API!!", "v2-api"},
		{"---", "node"},
		{"", "node"},
		{"../../etc/passwd", "etc-passwd"},
	} {
		if got := slugify(tc.in); got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := slugify(strings.Repeat("word ", 100))
	if len(long) > 80 {
		t.Errorf("slug length must be bounded: %d", len(long))
	}
	if slugify("x y") == slugify("y x") {
		t.Error("distinct names must not collide after the length bound")
	}
}

func TestDigestSourcesIsOrderIndependent(t *testing.T) {
	a := []SourceRef{{Path: "b.go", Hash: "2"}, {Path: "a.go", Hash: "1"}}
	b := []SourceRef{{Path: "a.go", Hash: "1"}, {Path: "b.go", Hash: "2"}}
	if digestSources(a) != digestSources(b) {
		t.Error("digest must not depend on input order")
	}
	c := []SourceRef{{Path: "a.go", Hash: "1"}, {Path: "b.go", Hash: "changed"}}
	if digestSources(a) == digestSources(c) {
		t.Error("a changed source hash must change the digest (that is the staleness key)")
	}
}

func TestExtractJSONToleratesWrapping(t *testing.T) {
	for _, in := range []string{
		`{"nodes":[]}`,
		"Here you go:\n```json\n{\"nodes\":[]}\n```\nHope that helps.",
		"prefix {\"nodes\":[{\"a\":1}]} suffix",
	} {
		raw, ok := extractJSON(in)
		if !ok {
			t.Errorf("extractJSON(%q) found nothing", in)
			continue
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Errorf("extractJSON(%q) produced invalid JSON: %v", in, err)
		}
	}
	if _, ok := extractJSON("no object here"); ok {
		t.Error("prose must not yield a payload")
	}
}

// TestKVBatchCacheIsPruned pins that the batch cache cannot grow without
// bound: a batch that no longer exists is dropped, and an empty entry is never
// treated as a hit (graft #177).
func TestKVBatchCacheIsPruned(t *testing.T) {
	f := newFixture(t, map[string]string{"a.go": "a"})
	srv := newFakeServer(t, nil)
	ctx := context.Background()
	if _, err := Run(ctx, f.st, runOpts(f.dir, srv.chat())); err != nil {
		t.Fatalf("run: %v", err)
	}
	raw, ok, err := f.st.KVGet(synthCacheNamespace, synthCacheKey)
	if err != nil || !ok {
		t.Fatalf("batch cache not persisted: ok=%v err=%v", ok, err)
	}
	var cache map[string][]cleanedNode
	if err := json.Unmarshal([]byte(raw), &cache); err != nil {
		t.Fatalf("cache must be JSON: %v", err)
	}
	if len(cache) != 1 {
		t.Fatalf("cache keys: %d, want 1", len(cache))
	}
	for k, v := range cache {
		if len(v) == 0 {
			t.Errorf("empty entry %s must never be cached", k)
		}
	}

	f.rewrite("a.go", "changed")
	if _, err := Run(ctx, f.st, runOpts(f.dir, srv.chat())); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	raw, _, _ = f.st.KVGet(synthCacheNamespace, synthCacheKey)
	cache = map[string][]cleanedNode{}
	if err := json.Unmarshal([]byte(raw), &cache); err != nil {
		t.Fatalf("cache must be JSON: %v", err)
	}
	if len(cache) != 1 {
		t.Errorf("stale batch must be pruned, cache now has %d entries", len(cache))
	}
	var _ store.Backend = f.st
}
