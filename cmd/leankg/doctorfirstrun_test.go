package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorOnAnUnindexedProjectSaysWhatToRun pins the first-run posture of
// `doctor`, which an agent hits before it has indexed anything.
//
// The defect: the missing store is reported as the raw open error —
// "FAIL store: store: read-only open of missing store
// <path>/.leankg/leankg.db: stat ...: no such file or directory" — and exits 2.
// Every token of that is implementation detail: the sentence names a syscall
// and a file, not the next command. An agent reading it cannot tell "this
// project is not indexed yet" (ordinary) from "the store is broken" (not), and
// the two deserve opposite reactions.
//
// `status` already got this right on the same state — it prints
// {"elements":0,"freshness":"cold","note":...} and exits 0, because a
// never-indexed project is legitimately cold. `doctor --deep` already says
// "run `leankg index` first" in its own error. This makes the plain verb
// agree with both, which is the whole point of a self-diagnosis tool.
func TestDoctorOnAnUnindexedProjectSaysWhatToRun(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.go"),
		[]byte("package p\n\nfunc P() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLIIn(t, dir, "doctor")
	body := stdout + stderr
	if code != 2 {
		t.Fatalf("an unindexed project is a FAIL in the diagnostic sense, exit 2 expected, got %d:\n%s", code, body)
	}
	if !strings.Contains(body, "leankg index") {
		t.Fatalf("doctor on an unindexed project must name the command that fixes it:\n%s", body)
	}
	// The raw syscall text is what makes this unreadable: it leaks the storage
	// layer into a user-facing diagnosis and buries the action.
	for _, leak := range []string{"read-only open of missing store", "no such file or directory"} {
		if strings.Contains(body, leak) {
			t.Errorf("doctor leaks the storage error %q instead of the next step:\n%s", leak, body)
		}
	}
	// And it must still name the store, so the operator can see WHERE the
	// engine looked — that part is diagnostic, not noise.
	if !strings.Contains(body, ".leankg") {
		t.Errorf("doctor must still name the store path it looked for:\n%s", body)
	}
}
