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

	"github.com/FreePeak/LeanKG/internal/embed/embedtest"
	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/store"
)

// cliBin is the once-built leankg binary shared by every CLI-level test in
// this package: the verbs under test assert process behavior (exit codes, the
// stdout/stderr split, files on disk) that in-process assertions cannot see.
var cliBin string

func TestMain(m *testing.M) {
	// Hermeticity: the embed provider is configured entirely by the
	// LEANKG_EMBED_* environment, so the test binary starts from an
	// UNCONFIGURED embed environment whatever the operator's shell exported.
	// A test that needs one states it.
	embedtest.UnsetProcess()
	flag.Parse()
	// Hermeticity: `leankg index` stamps the fleet registry (#376), whose
	// default location is $HOME/.leankg/portfolio.db. A test suite must never
	// write real user state — and a shared registry is cross-test coupled,
	// because whichever project registers last poisons every later reader.
	// Point it at a throwaway file; spawned CLI children inherit the env.
	regDir, rerr := os.MkdirTemp("", "leankg-cli-registry")
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "cli test registry dir: %v\n", rerr)
		os.Exit(1)
	}
	os.Setenv(portfolioreg.DBPathEnv, filepath.Join(regDir, "portfolio.db"))
	// Hermeticity: `leankg index` embeds after it indexes, and the unset
	// provider is local (it spawns llama-server). Every CLI child inherits this
	// environment, so no test in the package reaches a real provider.
	os.Setenv("LEANKG_EMBED_PROVIDER", "deterministic")
	// The engine is pinned for the same reason: an operator's LEANKG_DB_ENGINE
	// or LEANKG_PG_URL must never route a test to a shared Postgres.
	os.Setenv("LEANKG_DB_ENGINE", store.EngineSQLite)
	os.Unsetenv("LEANKG_PG_URL")
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
	_ = os.RemoveAll(regDir)
	os.Exit(code)
}

// TestSuitePinsSQLiteEngine: TestMain pins the engine for the whole process, so
// no operator environment can route the --apply tests to a shared Postgres.
func TestSuitePinsSQLiteEngine(t *testing.T) {
	if got := os.Getenv("LEANKG_DB_ENGINE"); got != store.EngineSQLite {
		t.Errorf("LEANKG_DB_ENGINE = %q in the test process; want %q", got, store.EngineSQLite)
	}
	if _, ok := os.LookupEnv("LEANKG_PG_URL"); ok {
		t.Errorf("LEANKG_PG_URL is set in the test process; want it unset")
	}
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
	return runCLIWith(t, dir, nil, args...)
}

// runCLIWith is runCLIIn with extra environment entries for the child only. A
// later entry overrides an earlier one with the same key, so a test can pin a
// variable (LEANKG_DB_ENGINE, say) without touching the test process's own.
func runCLIWith(t *testing.T, dir string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(cliBinary(t), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
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
