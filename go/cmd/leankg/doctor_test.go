package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// doctorProject seeds a project that passes every deep check except the
// ones the test deliberately breaks: one real file on disk, one indexed
// element pointing at it, fully-migrated schema.
func doctorProject(t *testing.T, broken bool) string {
	t.Helper()
	dir := t.TempDir()
	seedProjectDir(t, dir, "Hello")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if broken {
		st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		// A dangling edge: the target element is not indexed (the
		// delete-then-stale production path the orphan check exists for).
		if err := st.UpsertRelationships([]store.Relationship{
			{Source: "pkg.Hello", Target: "pkg.Ghost", RelType: "calls"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestDoctorDeepCLI pins the H9 exit-code contract: 0 all-pass, 2 any
// fail, with the table on stdout and the offending check named.
func TestDoctorDeepCLI(t *testing.T) {
	bin := buildLeanKG(t)

	healthy := doctorProject(t, false)
	cmd := exec.Command(bin, "doctor", "--deep", "--project", healthy)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("healthy --deep exit: %v\n%s", err, out)
	}
	for _, want := range []string{"pg-latency", "migrations", "index-freshness",
		"embedding-coverage", "pool-env", "orphaned-relationships", "duplicate-names",
		"leankg-dir", "exit 0"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("healthy --deep output missing %q:\n%s", want, out)
		}
	}

	broken := doctorProject(t, true)
	cmd = exec.Command(bin, "doctor", "--deep", "--project", broken)
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("broken --deep must exit non-zero\n%s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("broken --deep exit = %v, want 2\n%s", err, out)
	}
	if !strings.Contains(string(out), "orphaned-relationships") ||
		!strings.Contains(string(out), "FAIL") {
		t.Errorf("broken --deep must report the failing check:\n%s", out)
	}

	// --format json is the machine-readable envelope consumers parse.
	cmd = exec.Command(bin, "doctor", "--deep", "--format", "json", "--project", broken)
	out, _ = cmd.CombinedOutput()
	for _, want := range []string{`"findings"`, `"summary"`, `"fail": 1`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("json --deep output missing %q:\n%s", want, out)
		}
	}
}
