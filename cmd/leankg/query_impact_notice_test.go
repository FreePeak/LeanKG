package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestQueryImpactNoticesTruncation pins CX-30's CLI gap: when the graph node
// budget truncates an impact walk, the CLI says so on stderr and still prints
// the partial result on stdout.
func TestQueryImpactNoticesTruncation(t *testing.T) {
	const callers = 5001 // one over the 5000-node budget
	els := []store.Element{{QualifiedName: "hub.Target", ElementType: "function", Name: "Target", FilePath: "src/hub.go", LineStart: 1, Language: "go"}}
	rels := make([]store.Relationship, 0, callers)
	for i := range callers {
		qn := fmt.Sprintf("caller.F%05d", i)
		els = append(els, store.Element{QualifiedName: qn, ElementType: "function", Name: qn, FilePath: "src/c.go", LineStart: i + 1, Language: "go"})
		rels = append(rels, store.Relationship{Source: qn, Target: "hub.Target", RelType: "calls", Confidence: 0.9})
	}
	dir := seedCLIProject(t, els, rels)

	stdout, stderr, code := runCLI(t, "query", "hub.Target", "--kind", "impact", "--project", dir)
	if code != 0 {
		t.Fatalf("query --kind impact exit = %d, stderr: %s", code, stderr)
	}
	var hits []json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &hits); err != nil || len(hits) == 0 {
		t.Fatalf("stdout is not the partial impact array: %v (%d hits)", err, len(hits))
	}
	if !strings.Contains(stderr, "truncated") {
		t.Fatalf("stderr has no truncation notice for a budget-truncated walk; stderr: %q", stderr)
	}
}

// TestQueryImpactSilentWhenComplete guards the other side: a complete walk
// prints no truncation notice.
func TestQueryImpactSilentWhenComplete(t *testing.T) {
	dir := queryFixtureProject(t)
	_, stderr, code := runCLI(t, "query", "pkg.Alpha", "--kind", "impact", "--project", dir)
	if code != 0 {
		t.Fatalf("query --kind impact exit = %d, stderr: %s", code, stderr)
	}
	if strings.Contains(stderr, "truncated") {
		t.Fatalf("complete walk printed a truncation notice: %q", stderr)
	}
}
