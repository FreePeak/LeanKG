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
)

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

// TestBareDashboardTakesTheWebPath: a bare `leankg dashboard` starts the web
// dashboard (DS-20). The decision is asserted on the PARSED flags, never by
// running the verb: the web path binds a port, opens a browser and serves
// until a signal, so a bare run in a test would hang and leave a listener
// behind.
func TestBareDashboardTakesTheWebPath(t *testing.T) {
	f := parseDashboardFlags(nil)
	if f.mode != dashboardModeWeb {
		t.Errorf("bare `leankg dashboard` mode = %v, want the web mode", f.mode)
	}
	if f.addr != defaultDashboardAddr {
		t.Errorf("bare `leankg dashboard` addr = %q, want %q", f.addr, defaultDashboardAddr)
	}
}
