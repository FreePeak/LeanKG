package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorOnAnUnindexedProjectSaysWhatToRun pins the user-facing half of a
// #449 wave-24 fix that never merged.
//
// Measured live on this machine 2026-10-11, from a directory with source and
// no store:
//
//	$ leankg doctor
//	FAIL store: store: read-only open of missing store /tmp/nostore/.leankg/leankg.db:
//	  stat /tmp/nostore/.leankg/leankg.db: no such file or directory
//
// The exit code (2) is right. What is wrong is the sentence: it leaks the
// storage layer into a user-facing diagnosis ("read-only open of missing
// store", a raw stat error) and never names the command that fixes it. A
// reader learns that something under .leankg is absent, not that the project
// has not been indexed — and the two want the same action, stated once.
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
		t.Errorf("doctor on an unindexed project must name the command that fixes it:\n%s", body)
	}
	for _, leak := range []string{"read-only open of missing store", "no such file or directory"} {
		if strings.Contains(body, leak) {
			t.Errorf("the diagnosis leaks the storage layer (%q), which buries the action:\n%s", leak, body)
		}
	}
}
