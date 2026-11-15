package main

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestQueryImpactFileSeedOrderIsDeterministic pins the output order of the
// file-seed impact path. A file seed aggregates the impact of every element in
// the file through a map keyed by qualified name, so the raw order depended on
// Go's randomised map iteration. The output must be stable across runs and
// ordered by (depth, qualified name), the same order graph.Impact uses for an
// element seed.
func TestQueryImpactFileSeedOrderIsDeterministic(t *testing.T) {
	els := []store.Element{
		{QualifiedName: "src/hub.go::A", ElementType: "function", Name: "A", FilePath: "src/hub.go", LineStart: 1, Language: "go"},
		{QualifiedName: "src/hub.go::B", ElementType: "function", Name: "B", FilePath: "src/hub.go", LineStart: 5, Language: "go"},
		{QualifiedName: "c.X1", ElementType: "function", Name: "X1", FilePath: "src/c.go", LineStart: 1, Language: "go"},
		{QualifiedName: "c.X2", ElementType: "function", Name: "X2", FilePath: "src/c.go", LineStart: 2, Language: "go"},
		{QualifiedName: "c.Y1", ElementType: "function", Name: "Y1", FilePath: "src/c.go", LineStart: 3, Language: "go"},
		{QualifiedName: "c.Y2", ElementType: "function", Name: "Y2", FilePath: "src/c.go", LineStart: 4, Language: "go"},
		{QualifiedName: "c.Z", ElementType: "function", Name: "Z", FilePath: "src/c.go", LineStart: 5, Language: "go"},
	}
	rels := []store.Relationship{
		{Source: "c.X1", Target: "src/hub.go::A", RelType: "calls", Confidence: 0.9},
		{Source: "c.X2", Target: "src/hub.go::B", RelType: "calls", Confidence: 0.9},
		{Source: "c.Y1", Target: "c.X1", RelType: "calls", Confidence: 0.9},
		{Source: "c.Y2", Target: "c.X2", RelType: "calls", Confidence: 0.9},
		{Source: "c.Z", Target: "src/hub.go::A", RelType: "calls", Confidence: 0.9},
	}
	dir := seedCLIProject(t, els, rels)

	type hit struct {
		QN    string `json:"qn"`
		Depth int    `json:"depth"`
	}
	var first string
	for i := range 20 {
		stdout, stderr, code := runCLI(t, "query", "src/hub.go", "--kind", "impact", "--project", dir)
		if code != 0 {
			t.Fatalf("run %d: query --kind impact exit = %d, stderr: %s", i, code, stderr)
		}
		if i == 0 {
			first = stdout
			continue
		}
		if stdout != first {
			t.Fatalf("run %d output differs from run 0:\nrun 0:\n%s\nrun %d:\n%s", i, first, i, stdout)
		}
	}

	var hits []hit
	if err := json.Unmarshal([]byte(first), &hits); err != nil {
		t.Fatalf("output is not the impact array: %v\n%s", err, first)
	}
	sorted := sort.SliceIsSorted(hits, func(a, b int) bool {
		if hits[a].Depth != hits[b].Depth {
			return hits[a].Depth < hits[b].Depth
		}
		return hits[a].QN < hits[b].QN
	})
	if !sorted {
		t.Fatalf("impact hits are not ordered by (depth, qn): %+v", hits)
	}
}
