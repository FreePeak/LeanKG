package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cliBin is the once-built leankg binary shared by every CLI-level test in
// this package: the verbs under test assert process behavior (exit codes, the
// stdout/stderr split, files on disk) that in-process assertions cannot see.
var cliBin string

func TestMain(m *testing.M) {
	flag.Parse()
	buildDir := ""
	if !testing.Short() {
		var err error
		buildDir, err = os.MkdirTemp("", "leankg-cli-bin")
		if err != nil {
			fmt.Fprintf(os.Stderr, "cli test build dir: %v\n", err)
			os.Exit(1)
		}
		bin := filepath.Join(buildDir, "leankg")
		build := exec.Command("go", "build", "-o", bin, "./cmd/leankg")
		build.Dir = "../.."
		if out, berr := build.CombinedOutput(); berr != nil {
			fmt.Fprintf(os.Stderr, "cli test build: %v\n%s", berr, out)
			os.RemoveAll(buildDir)
			os.Exit(1)
		}
		cliBin = bin
	}
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// cliBinary returns the shared binary path, skipping binary-level tests under
// -short.
func cliBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs the leankg binary")
	}
	if cliBin == "" {
		t.Fatal("CLI binary was not built")
	}
	return cliBin
}

// runCLI executes the CLI and returns stdout, stderr, and the exit code.
func runCLI(t *testing.T, args ...string) (string, string, int) {
	return runCLIIn(t, "", args...)
}

// runCLIIn is runCLI with an explicit working directory ("" inherits the test
// process's). Verbs that resolve a project from the cwd — index's first-run
// setup choice, setup --reset — need the process to actually run inside the
// fixture instead of beside the repo's own .leankg.
func runCLIIn(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(cliBinary(t), args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// seedCLIProject writes a migrated sqlite store under a temp project dir and
// seeds it — the state every read verb reads.
func seedCLIProject(t *testing.T, els []store.Element, rels []store.Relationship) string {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(els) > 0 {
		if err := st.UpsertElements(els); err != nil {
			t.Fatalf("seed elements: %v", err)
		}
	}
	if len(rels) > 0 {
		if err := st.UpsertRelationships(rels); err != nil {
			t.Fatalf("seed relationships: %v", err)
		}
	}
	return dir
}

// graphFixture is a small two-folder call graph: Alpha and Beta under src/,
// Gamma and Delta under src/b.go's folder, with a cycle (Alpha<->Beta) so the
// caller/callee verbs have answers and a two-hop path Alpha -> Gamma -> Delta.
func graphFixture() ([]store.Element, []store.Relationship) {
	els := []store.Element{
		{QualifiedName: "pkg.Alpha", ElementType: "function", Name: "Alpha", FilePath: "src/a.go", LineStart: 3, Language: "go"},
		{QualifiedName: "pkg.Beta", ElementType: "function", Name: "Beta", FilePath: "src/a.go", LineStart: 10, Language: "go"},
		{QualifiedName: "pkg.Gamma", ElementType: "function", Name: "Gamma", FilePath: "src/b.go", LineStart: 5, Language: "go"},
		{QualifiedName: "pkg.Delta", ElementType: "function", Name: "Delta", FilePath: "src/b.go", LineStart: 20, Language: "go"},
	}
	rels := []store.Relationship{
		{Source: "pkg.Alpha", Target: "pkg.Beta", RelType: "calls", Confidence: 0.9},
		{Source: "pkg.Beta", Target: "pkg.Alpha", RelType: "calls", Confidence: 0.9},
		{Source: "pkg.Alpha", Target: "pkg.Gamma", RelType: "calls", Confidence: 0.9},
		{Source: "pkg.Gamma", Target: "pkg.Delta", RelType: "calls", Confidence: 0.9},
	}
	return els, rels
}
