package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func queryFixtureProject(t *testing.T) string {
	t.Helper()
	els, rels := graphFixture()
	dir := seedCLIProject(t, els, rels)
	mainGo := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainGo, []byte("package main\n\n// keep\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestQueryKindPathsUnchanged pins the two local --kind verbs: the --action
// extension must not have changed their behavior or output shapes.
func TestQueryKindPathsUnchanged(t *testing.T) {
	dir := queryFixtureProject(t)

	stdout, stderr, code := runCLI(t, "query", "pkg.Alpha", "--kind", "name", "--project", dir)
	if code != 0 {
		t.Fatalf("query --kind name exit = %d, stderr: %s", code, stderr)
	}
	var els []struct {
		QualifiedName string `json:"qualified_name"`
	}
	if err := json.Unmarshal([]byte(stdout), &els); err != nil || len(els) != 1 || els[0].QualifiedName != "pkg.Alpha" {
		t.Fatalf("kind name result = %s (err %v)", stdout, err)
	}

	stdout, _, code = runCLI(t, "query", "pkg.Alpha", "--kind", "impact", "--project", dir)
	if code != 0 {
		t.Fatalf("query --kind impact exit = %d", code)
	}
	var hits []struct {
		QN    string `json:"qn"`
		Depth int    `json:"depth"`
	}
	if err := json.Unmarshal([]byte(stdout), &hits); err != nil || len(hits) != 2 {
		t.Fatalf("kind impact result = %s (err %v)", stdout, err)
	}
	if hits[0].QN != "pkg.Alpha" || hits[0].Depth != 0 {
		t.Fatalf("impact seed = %+v", hits[0])
	}

	// The `leankg impact` alias dispatches the same verb (and must not crash
	// on its own argv handling).
	stdout, stderr, code = runCLI(t, "impact", "pkg.Alpha", "--project", dir)
	if code != 0 {
		t.Fatalf("impact alias exit = %d, stderr: %s", code, stderr)
	}
	var aliasHits []struct {
		QN string `json:"qn"`
	}
	if err := json.Unmarshal([]byte(stdout), &aliasHits); err != nil || len(aliasHits) != 2 {
		t.Fatalf("impact alias result = %s (err %v)", stdout, err)
	}
}

// TestQueryActionPassthrough covers the envelope actions the CLI could not
// reach before: they must run through the same core wiring as the MCP tool
// and print the response JSON.
func TestQueryActionPassthrough(t *testing.T) {
	dir := queryFixtureProject(t)
	run := func(args ...string) map[string]any {
		t.Helper()
		stdout, stderr, code := runCLI(t, append([]string{"query"}, args...)...)
		if code != 0 {
			t.Fatalf("query %v exit = %d, stderr: %s", args, code, stderr)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("query %v output is not JSON: %v\n%s", args, err, stdout)
		}
		return out
	}

	callees := run("pkg.Alpha", "--action", "callees", "--project", dir)
	if callees["action"] != "callees" {
		t.Fatalf("callees response = %v", callees)
	}

	path := run("pkg.Alpha", "--action", "path", "--to", "pkg.Delta", "--project", dir)
	if path["reachable"] != true {
		t.Fatalf("path response = %v", path)
	}
	pathList, _ := path["path"].([]any)
	if len(pathList) != 3 {
		t.Fatalf("path Alpha->Delta = %v, want 3 hops", pathList)
	}

	explain := run("pkg.Alpha", "--action", "explain", "--project", dir)
	if explain["out_degree"] != float64(2) || explain["in_degree"] != float64(1) {
		t.Fatalf("explain response = %v", explain)
	}

	ctx := run("pkg.Alpha", "--action", "context", "--limit", "5", "--project", dir)
	if ctx["action"] != "context" {
		t.Fatalf("context response = %v", ctx)
	}

	exact := run("pkg.Alpha", "--action", "exact", "--project", dir)
	if exact["retrieval"] == nil {
		t.Fatalf("exact response missing retrieval: %v", exact)
	}

	langs := run("noop", "--action", "languages", "--project", dir)
	if langs == nil {
		t.Fatal("languages response empty")
	}

	// pattern: structural search either matches (ast-grep present) or
	// degrades to the L2 rung — both are a retrieval-tagged JSON answer, and
	// both must exit 0 rather than crash.
	pat := run("noop", "--action", "pattern", "--pattern", "func $F()", "--lang", "go", "--project", dir)
	if pat["retrieval"] == nil {
		t.Fatalf("pattern response missing retrieval: %v", pat)
	}

	// lsp: an unknown language must fail as a JSON error, not a panic or an
	// attempt to spawn a server.
	_, stderr, code := runCLI(t, "query", "noop", "--action", "lsp", "--lang", "nosuchlang", "--project", dir)
	if code != 1 || !strings.Contains(stderr, "unknown language") {
		t.Fatalf("lsp unknown language: code=%d stderr=%s", code, stderr)
	}

	// read: the reader-mode compression path routes through core.Import.
	stdout, stderr, code := runCLI(t, "query", filepath.Join(dir, "main.go"),
		"--action", "read", "--mode", "full", "--project", dir)
	if code != 0 {
		t.Fatalf("query read exit = %d, stderr: %s", code, stderr)
	}
	var read struct {
		Mode    string `json:"mode"`
		Content string `json:"content"`
		Tokens  int    `json:"tokens"`
	}
	if err := json.Unmarshal([]byte(stdout), &read); err != nil {
		t.Fatalf("query read output is not JSON: %v\n%s", err, stdout)
	}
	if read.Mode != "full" || !strings.Contains(read.Content, "func Hello()") || read.Tokens == 0 {
		t.Fatalf("query read payload incomplete: %+v", read)
	}

	// compress: command-output shaping with the output as the positional body.
	stdout, _, code = runCLI(t, "query", "On branch main\nNothing to commit",
		"--action", "compress", "--cmd", "git status", "--project", dir)
	if code != 0 {
		t.Fatalf("query compress exit = %d", code)
	}
	var comp struct {
		Cmd     string `json:"cmd"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(stdout), &comp); err != nil {
		t.Fatalf("query compress output is not JSON: %v\n%s", err, stdout)
	}
	if comp.Cmd != "git status" || comp.Content == "" {
		t.Fatalf("query compress payload = %+v", comp)
	}

	// path without --to is an envelope-arg error (exit 1, JSON error body).
	stdout, stderr, code = runCLI(t, "query", "pkg.Alpha", "--action", "path", "--project", dir)
	if code != 1 || !strings.Contains(stderr, "args.to") {
		t.Fatalf("path without --to: code=%d stderr=%s", code, stderr)
	}

	// Unknown actions fail at the CLI surface with the vocabulary.
	_, stderr, code = runCLI(t, "query", "x", "--action", "bogus", "--project", dir)
	if code != 2 || !strings.Contains(stderr, "unknown --action") {
		t.Fatalf("unknown action: code=%d stderr=%s", code, stderr)
	}
}
