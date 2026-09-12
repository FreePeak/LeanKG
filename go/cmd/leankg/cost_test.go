package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// TestCountSLOC pins the Rust count_sloc heuristic: blank lines and lines made
// only of braces/slashes/whitespace do not count.
func TestCountSLOC(t *testing.T) {
	src := "package p\n\nfunc F() {\n}\n// keep me\n{\n \n\t\nx\n"
	if got := countSLOC([]byte(src)); got != 4 {
		t.Fatalf("countSLOC = %d, want 4", got)
	}
	if got := countSLOC(nil); got != 0 {
		t.Fatalf("countSLOC(empty) = %d, want 0", got)
	}
}

// TestEstimateCost pins the LOCOMO defaults and the per-file arithmetic.
func TestEstimateCost(t *testing.T) {
	dir := t.TempDir()
	src := "package p\n\nfunc F() {\n\treturn\n}\n" // 5 physical lines, 3 sloc
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	est := estimateCost([]string{"a.go", "missing.go"}, dir)
	if est.Model != "locomo-default" {
		t.Fatalf("model = %q", est.Model)
	}
	if len(est.Files) != 1 {
		t.Fatalf("files = %+v, want only the existing file", est.Files)
	}
	f := est.Files[0]
	if f.Lines != 5 || f.SLOC != 3 || f.Bytes != len(src) {
		t.Fatalf("file cost = %+v", f)
	}
	if f.InTokens != 3*costTokensPerSLOC || f.OutTokens != costOutTokensPerFile {
		t.Fatalf("token fields = %+v", f)
	}
	if est.TotalSLOC != 3 || est.InTokens != 39 || est.OutTokens != 256 {
		t.Fatalf("totals = %+v", est)
	}
	// Absolute paths bypass the base dir join.
	est = estimateCost([]string{filepath.Join(dir, "a.go")}, "/nonexistent")
	if len(est.Files) != 1 {
		t.Fatalf("absolute-path estimate = %+v", est)
	}
}

// TestCostCLI pins the verb's text and JSON output for a seeded project.
func TestCostCLI(t *testing.T) {
	els := []store.Element{
		{QualifiedName: "main.go", ElementType: "file", Name: "main.go", FilePath: "main.go", Language: "go"},
	}
	dir := seedCLIProject(t, els, nil)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLI(t, "cost", "--project", dir)
	if code != 0 {
		t.Fatalf("cost exit = %d, stderr: %s", code, stderr)
	}
	for _, want := range []string{"1 files, 2 sloc, 29 bytes", "est. in 26 tokens / out 256 tokens", "main.go  (2 sloc, in 26 / out 256)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("cost text output missing %q:\n%s", want, stdout)
		}
	}

	stdout, _, code = runCLI(t, "cost", "--project", dir, "--format", "json")
	if code != 0 {
		t.Fatalf("cost --format json exit = %d", code)
	}
	var est struct {
		Model      string `json:"model"`
		TotalSLOC  int    `json:"total_sloc"`
		TotalBytes int    `json:"total_bytes"`
		InTokens   int    `json:"in_tokens"`
		OutTokens  int    `json:"out_tokens"`
		Files      []struct {
			File string `json:"file"`
			SLOC int    `json:"sloc"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(stdout), &est); err != nil {
		t.Fatalf("cost JSON invalid: %v\n%s", err, stdout)
	}
	if est.Model != "locomo-default" || est.TotalSLOC != 2 || est.TotalBytes != 29 ||
		est.InTokens != 26 || est.OutTokens != 256 || len(est.Files) != 1 {
		t.Fatalf("cost JSON = %+v", est)
	}

	if _, _, code = runCLI(t, "cost", "--project", dir, "--format", "yaml"); code != 2 {
		t.Fatalf("unknown format exit = %d, want 2", code)
	}
}
