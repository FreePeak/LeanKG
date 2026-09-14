package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// TestMigrateCLI pins the applied/skipped result: a fresh project applies the
// embedded plan on the first run and reports everything up to date on the
// second.
func TestMigrateCLI(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := runCLI(t, "migrate", "--project", dir)
	if code != 0 {
		t.Fatalf("migrate exit = %d, stderr: %s", code, stderr)
	}
	var first struct {
		Engine  string `json:"engine"`
		Planned int    `json:"planned"`
		Applied []struct {
			Version int    `json:"version"`
			Name    string `json:"name"`
		} `json:"applied"`
		UpToDate        []json.RawMessage `json:"up_to_date"`
		PendingAfterRun []json.RawMessage `json:"pending_after_run"`
	}
	if err := json.Unmarshal([]byte(stdout), &first); err != nil {
		t.Fatalf("migrate output is not JSON: %v\n%s", err, stdout)
	}
	if first.Engine != "sqlite" {
		t.Fatalf("engine = %q, want sqlite", first.Engine)
	}
	if first.Planned == 0 || len(first.Applied) != first.Planned || len(first.UpToDate) != 0 {
		t.Fatalf("fresh migrate must apply every planned step: planned=%d applied=%d up_to_date=%d",
			first.Planned, len(first.Applied), len(first.UpToDate))
	}
	if len(first.PendingAfterRun) != 0 {
		t.Fatalf("pending after migrate = %+v", first.PendingAfterRun)
	}

	stdout, _, code = runCLI(t, "migrate", "--project", dir)
	if code != 0 {
		t.Fatalf("second migrate exit = %d", code)
	}
	var second struct {
		Applied  []json.RawMessage `json:"applied"`
		UpToDate []json.RawMessage `json:"up_to_date"`
	}
	if err := json.Unmarshal([]byte(stdout), &second); err != nil {
		t.Fatalf("second migrate output is not JSON: %v\n%s", err, stdout)
	}
	if len(second.Applied) != 0 || len(second.UpToDate) != first.Planned {
		t.Fatalf("re-run must be a no-op: applied=%d up_to_date=%d", len(second.Applied), len(second.UpToDate))
	}

	if _, _, code = runCLI(t, "migrate", "--project", dir, "--engine", "bogus"); code != 2 {
		t.Fatalf("unknown engine exit = %d, want 2", code)
	}
}

// TestCtagsCLI pins the ctags rendering and its --out side effect.
func TestCtagsCLI(t *testing.T) {
	els, _ := graphFixture()
	dir := seedCLIProject(t, els, nil)

	stdout, stderr, code := runCLI(t, "ctags", "--project", dir)
	if code != 0 {
		t.Fatalf("ctags exit = %d, stderr: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) != len(els) {
		t.Fatalf("ctags lines = %d, want %d:\n%s", len(lines), len(els), stdout)
	}
	// Deterministic (name, file, line) order: Alpha, Beta (src/a.go) then
	// Delta, Gamma (src/b.go).
	for i, want := range []string{"Alpha", "Beta", "Delta", "Gamma"} {
		if !strings.HasPrefix(lines[i], want+"\t") {
			t.Errorf("line %d = %q, want a %s tag", i, lines[i], want)
		}
	}
	for _, want := range []string{"src/a.go\t3;\t", "kind:f", "language:go", "element:function"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("Alpha tag %q missing %q", lines[0], want)
		}
	}

	// --out writes the file and reports the row count on stderr.
	out := filepath.Join(dir, "tags")
	stdout, stderr, code = runCLI(t, "ctags", "--project", dir, "--out", out)
	if code != 0 {
		t.Fatalf("ctags --out exit = %d, stderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("ctags --out stdout must stay empty: %q", stdout)
	}
	if !strings.Contains(stderr, "wrote 4 tags to") {
		t.Fatalf("ctags --out stderr = %q", stderr)
	}
	if body, err := os.ReadFile(out); err != nil || len(body) == 0 {
		t.Fatalf("tags file unreadable: %v", err)
	}

	if _, _, code = runCLI(t, "ctags", "--project", dir, "--format", "gtags"); code != 2 {
		t.Fatalf("unknown format exit = %d, want 2", code)
	}
}

// TestCtagsRenderer is the in-process table test over the pure renderer:
// kind mapping, path normalization, escaping, sanitization, and determinism.
func TestCtagsRenderer(t *testing.T) {
	els := []store.Element{
		{Name: "zebra", FilePath: "src/z.go", LineStart: 1, ElementType: "class", Language: "go", QualifiedName: "p.zebra"},
		{Name: "Alpha", FilePath: "src/a.go", LineStart: 2, ElementType: "function", Language: "go", QualifiedName: "p.Alpha"},
		{Name: "with space", FilePath: "src/s.go", LineStart: 3, ElementType: "function", Language: "go", QualifiedName: "p.ws"},
		{Name: "", FilePath: "src/x.go", LineStart: 1, ElementType: "function", Language: "go"},
		{Name: "noPath", FilePath: "", LineStart: 1, ElementType: "function", Language: "go"},
		{Name: "tab\tname", FilePath: "src/t.go", LineStart: 1, ElementType: "function", Language: "go"},
	}
	out := renderTags(els)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("tag rows = %d, want 3 (empty name/path and tab names dropped):\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "Alpha\t") {
		t.Fatalf("first row = %q, want Alpha first (name sort)", lines[0])
	}
	if !strings.Contains(lines[0], "src/a.go\t2;\t") {
		t.Fatalf("address field wrong: %q", lines[0])
	}
	// "with space" sorts between Alpha and zebra; the escaped pattern anchors
	// the name and escapes the space as its codepoint.
	if !strings.Contains(lines[1], "^with\\32space$") || !strings.Contains(lines[1], "kind:f") {
		t.Fatalf("escaped-pattern row wrong: %q", lines[1])
	}
	// A "./"-prefixed path is stored repo-relative.
	if got := normalizeTagPath("./src/a.go"); got != "src/a.go" {
		t.Fatalf("normalizeTagPath = %q", got)
	}
	// Kind table spot-check.
	kinds := map[string]string{"file": "f", "class": "c", "interface": "c", "struct": "s",
		"enum": "g", "method": "m", "property": "p", "var": "v", "document": "d", "weird": "x"}
	for et, want := range kinds {
		if got := ctagsKindFor(et); got != want {
			t.Errorf("ctagsKindFor(%q) = %q, want %q", et, got, want)
		}
	}
}
