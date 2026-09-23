//go:build dshusage

package dsusage

import "testing"

func TestClassifySessionLostIsCritical(t *testing.T) {
	issues := Classify(Step{
		Tool:    "mcp__leankg__status",
		Output:  "ERROR Error: Streamable HTTP error: Error POSTing to endpoint: session not found",
		IsError: true,
	})
	if Top(issues) != SevCritical {
		t.Fatalf("got %s, want critical: %+v", Top(issues), issues)
	}
	if issues[0].Rule != "mcp_session_lost" {
		t.Fatalf("rule %s", issues[0].Rule)
	}
}

func TestClassifyOmittedProjectOnBEWorkspace(t *testing.T) {
	issues := Classify(Step{
		Tool:       "mcp__leankg__query",
		Workspace:  "--Users-linh.doan-work-be--",
		Input:      `{"query":"menu service grpc API proto"}`,
		Output:     `{"freshness":"fresh","hits":[{"content":"func refsResourceDir"}]}`,
		UserPrompt: "trace the menu cadence workflow",
	})
	if !hasRule(issues, "project_not_passed") {
		t.Fatalf("missing project rule: %+v", issues)
	}
	if Top(issues) != SevCritical {
		t.Fatalf("severity %s", Top(issues))
	}
}

func TestClassifyLeankgWorkspaceMayOmitProject(t *testing.T) {
	issues := Classify(Step{
		Tool:      "mcp__leankg__query",
		Workspace: "--Users-linh.doan-work-harvey-freepeak-leankg--",
		Input:     `{"query":"Backend"}`,
		Output:    `{"freshness":"fresh","hits":[{"content":"type Backend"}]}`,
	})
	if hasRule(issues, "project_not_passed") {
		t.Fatalf("leankg workspace should not flag omitted project: %+v", issues)
	}
}

func TestClassifyAmbiguousProject(t *testing.T) {
	issues := Classify(Step{
		Tool:    "mcp__leankg__query",
		Input:   `{"query":"plugin","project":"deepseek-harness"}`,
		Output:  `Error: ambiguous project "deepseek-harness" — several registered projects share that name`,
		IsError: true,
	})
	if !hasRule(issues, "ambiguous_project") {
		t.Fatalf("%+v", issues)
	}
}

func TestClassifyColdStore(t *testing.T) {
	issues := Classify(Step{
		Tool:   "mcp__leankg__query",
		Input:  `{"query":"ApplyPromo","project":"be-food-promotion"}`,
		Output: `{"freshness":"cold","guidance":"Index a repository first: leankg import","hits":[]}`,
	})
	if !hasRule(issues, "cold_store") {
		t.Fatalf("%+v", issues)
	}
}

func TestClassifyProjectRequiredServerError(t *testing.T) {
	issues := Classify(Step{
		Tool:    "mcp__leankg__query",
		Input:   `{"query":"promo API"}`,
		Output:  `LEANKG_ERROR_MISSING_PARAM: this server hosts multiple projects and the call omitted project. Fix: pass project=<repo basename> or an absolute path (docs: docs/archive/mcp-tools.md)`,
		IsError: true,
	})
	if !hasRule(issues, "project_required") {
		t.Fatalf("%+v", issues)
	}
}

func hasRule(issues []Issue, rule string) bool {
	for _, i := range issues {
		if i.Rule == rule {
			return true
		}
	}
	return false
}
