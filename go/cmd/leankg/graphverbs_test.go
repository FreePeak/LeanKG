package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetectClustersCLI is the acceptance guard for `leankg detect-clusters`:
// a seeded project must yield communities with every element assigned, in the
// Rust CLI's deterministic order (member count descending).
func TestDetectClustersCLI(t *testing.T) {
	els, rels := graphFixture()
	dir := seedCLIProject(t, els, rels)

	stdout, stderr, code := runCLI(t, "detect-clusters", "--path", dir, "--min-hub-edges", "5")
	if code != 0 {
		t.Fatalf("detect-clusters exit = %d, stderr: %s", code, stderr)
	}
	var got struct {
		Clusters []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			MemberCount int    `json:"member_count"`
		} `json:"clusters"`
		Assignments map[string]string `json:"assignments"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("detect-clusters output is not JSON: %v\n%s", err, stdout)
	}
	if len(got.Clusters) == 0 {
		t.Fatalf("no clusters detected for the seeded graph:\n%s", stdout)
	}
	for _, qn := range []string{"pkg.Alpha", "pkg.Beta", "pkg.Gamma", "pkg.Delta"} {
		if got.Assignments[qn] == "" {
			t.Errorf("element %s has no cluster assignment:\n%s", qn, stdout)
		}
	}
	for i := 1; i < len(got.Clusters); i++ {
		if got.Clusters[i-1].MemberCount < got.Clusters[i].MemberCount {
			t.Fatalf("clusters must be ordered by member count descending: %+v", got.Clusters)
		}
	}
}

// TestGodsCLI pins the god-node ranking and its --limit/percentile flags.
func TestGodsCLI(t *testing.T) {
	els, rels := graphFixture()
	dir := seedCLIProject(t, els, rels)

	stdout, stderr, code := runCLI(t, "gods", "--project", dir, "--limit", "2")
	if code != 0 {
		t.Fatalf("gods exit = %d, stderr: %s", code, stderr)
	}
	var nodes []struct {
		QualifiedName string `json:"qualified_name"`
		Degree        int    `json:"degree"`
	}
	if err := json.Unmarshal([]byte(stdout), &nodes); err != nil {
		t.Fatalf("gods output is not JSON: %v\n%s", err, stdout)
	}
	if len(nodes) != 2 {
		t.Fatalf("gods --limit 2 returned %d nodes:\n%s", len(nodes), stdout)
	}
	if nodes[0].QualifiedName != "pkg.Alpha" || nodes[0].Degree != 3 {
		t.Fatalf("top god node = %+v, want pkg.Alpha degree 3", nodes[0])
	}

	// The percentile filter drops the high-degree tail before the limit.
	stdout, _, code = runCLI(t, "gods", "--project", dir, "--limit", "4", "--exclude-hubs-percentile", "50")
	if code != 0 {
		t.Fatalf("gods --exclude-hubs-percentile exit = %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &nodes); err != nil {
		t.Fatalf("gods output is not JSON: %v\n%s", err, stdout)
	}
	if len(nodes) != 2 {
		t.Fatalf("exclude-hubs-percentile 50 of 4 nodes returned %d:\n%s", len(nodes), stdout)
	}
}

// TestReportCLI pins the report payload and the GRAPH_REPORT.md side effect
// (Rust writes it by default).
func TestReportCLI(t *testing.T) {
	els, rels := graphFixture()
	dir := seedCLIProject(t, els, rels)

	stdout, stderr, code := runCLI(t, "report", "--project", dir, "--project-name", "fixture")
	if code != 0 {
		t.Fatalf("report exit = %d, stderr: %s", code, stderr)
	}
	var report struct {
		Project            string `json:"project"`
		TotalElements      int    `json:"total_elements"`
		TotalRelationships int    `json:"total_relationships"`
		FunctionCount      int    `json:"function_count"`
		GodNodes           []struct {
			QualifiedName string `json:"qualified_name"`
		} `json:"god_nodes"`
		ConfidenceDistribution []struct {
			Label string `json:"label"`
			Count int    `json:"count"`
		} `json:"confidence_distribution"`
		SuggestedQuestions []string `json:"suggested_questions"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("report output is not JSON: %v\n%s", err, stdout)
	}
	if report.Project != "fixture" || report.TotalElements != 4 || report.TotalRelationships != 4 {
		t.Fatalf("report headline = %+v", report)
	}
	if report.FunctionCount != 4 || len(report.GodNodes) == 0 || len(report.SuggestedQuestions) == 0 {
		t.Fatalf("report body incomplete: %+v", report)
	}
	if !strings.Contains(stderr, "wrote graph report to") {
		t.Fatalf("report must announce the markdown write, stderr: %q", stderr)
	}
	md, err := os.ReadFile(filepath.Join(dir, ".leankg", "GRAPH_REPORT.md"))
	if err != nil {
		t.Fatalf("GRAPH_REPORT.md: %v", err)
	}
	for _, want := range []string{"# Graph Report: fixture", "## Top God Nodes", "pkg.Alpha"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("GRAPH_REPORT.md missing %q:\n%s", want, md)
		}
	}

	// --out redirects the markdown without changing the JSON payload.
	out := filepath.Join(t.TempDir(), "custom.md")
	stdout, _, code = runCLI(t, "report", "--project", dir, "--out", out)
	if code != 0 {
		t.Fatalf("report --out exit = %d", code)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("report --out did not write %s: %v", out, err)
	}
	if !strings.Contains(stdout, `"total_elements": 4`) {
		t.Fatalf("report --out stdout lost the payload:\n%s", stdout)
	}
}
