package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// This file is BLACK BOX: it drives the built `leankg` binary and reads what
// the process printed, so it also compiles against the pre-fix source — which
// is what makes it a regression test rather than a description of the fix.

// runDashboardCLI runs `leankg dashboard ...` in a throwaway project dir and
// returns stdout, stderr and the exit code. The run is bounded by a timeout:
// if the verb wrongly took the web path it would serve until a signal, and
// the test must fail instead of hanging.
func runDashboardCLI(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	bin := cliBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"dashboard"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+dir,
		"LEANKG_HOME="+dir,
		"LEANKG_DB_PATH="+filepath.Join(dir, "leankg.db"),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("dashboard %v did not exit (the web server started?): stdout=%q stderr=%q", args, stdout.String(), stderr.String())
	}
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("dashboard %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// seedDashboardStore creates the project store the tables path reads, at the
// path the child process is pointed at (LEANKG_DB_PATH).
//
// The tables path opens the project store read-write, and a read-write open
// CREATES a missing store. A `leankg dashboard --format text` against a
// project nobody indexed would therefore materialise a `.leankg` where none
// belonged. So the store is seeded here: these tests are about FLAG ROUTING
// (one FlagSet, `--format` selects the tables, the web flags survive), not
// about store creation, and seeding keeps them honest about that.
func seedDashboardStore(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "leankg.db")
	st, err := store.Open(path, store.RW)
	if err != nil {
		t.Fatalf("seed store %s: %v", path, err)
	}
	if err := st.Migrate(); err != nil {
		_ = st.Close()
		t.Fatalf("migrate store %s: %v", path, err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store %s: %v", path, err)
	}
}

// TestDashboardAcceptsTheWebFlagsWithTheFormatFlag is the regression test for
// the `leankg dashboard --addr 127.0.0.1:9701 --no-open --format text`
// failure: the mode was chosen by scanning argv for "--format", so the WHOLE
// argv went to the text-only FlagSet and the web flags died with "flag
// provided but not defined: -addr" — even though AGENTS.md and
// docs/plan-dashboard.md document exactly that invocation.
//
// The run must print the usage tables and exit 0. It must NOT start the web
// server (the banner would mean a port was bound and a browser opened), so
// the command is given a timeout rather than a port to free.
func TestDashboardAcceptsTheWebFlagsWithTheFormatFlag(t *testing.T) {
	dir := t.TempDir()
	seedDashboardStore(t, dir)
	stdout, stderr, code := runDashboardCLI(t, dir, "--addr", "127.0.0.1:9701", "--no-open", "--format", "text")
	if code != 0 {
		t.Fatalf("dashboard --addr 127.0.0.1:9701 --no-open --format text exited %d\nstdout=%q\nstderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stderr, "flag provided but not defined") {
		t.Errorf("stderr = %q, want no flag error", stderr)
	}
	if !strings.Contains(stdout, "LeanKG usage dashboard") {
		t.Errorf("stdout = %q, want the usage tables", stdout)
	}
	if strings.Contains(stdout, "LeanKG dashboard: http://") {
		t.Errorf("stdout = %q, want the tables and NOT the web server banner", stdout)
	}
}

// TestDashboardFormatJSONEmitsJSON: `--format json` reaches the table path
// and emits the JSON payload — the same one `leankg metrics --json` users
// script against. The web banner must be absent, so no port was bound.
func TestDashboardFormatJSONEmitsJSON(t *testing.T) {
	dir := t.TempDir()
	seedDashboardStore(t, dir)
	stdout, stderr, code := runDashboardCLI(t, dir, "--format", "json")
	if code != 0 {
		t.Fatalf("dashboard --format json exited %d\nstdout=%q\nstderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout, "LeanKG dashboard: http://") {
		t.Errorf("stdout = %q, want the JSON payload and NOT the web server banner", stdout)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout=%q", err, stdout)
	}
	for _, key := range []string{"totals", "by_tool", "by_day", "by_project", "patterns"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("JSON payload is missing %q: %s", key, stdout)
		}
	}
}
