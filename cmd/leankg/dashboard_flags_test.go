package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	bin := cliBinary(t)
	dir := t.TempDir()
	store := filepath.Join(dir, "leankg.db")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "dashboard", "--addr", "127.0.0.1:9701", "--no-open", "--format", "text")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+dir,
		"LEANKG_HOME="+dir,
		"LEANKG_DB_PATH="+store,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("dashboard did not exit (the web server started?): stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if err != nil {
		t.Fatalf("dashboard %v: %v\nstdout=%q\nstderr=%q", cmd.Args, err, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Errorf("stderr = %q, want no flag error", stderr.String())
	}
	if !strings.Contains(stdout.String(), "LeanKG usage dashboard") {
		t.Errorf("stdout = %q, want the usage tables", stdout.String())
	}
	if strings.Contains(stdout.String(), "LeanKG dashboard: http://") {
		t.Errorf("stdout = %q, want the tables and NOT the web server banner", stdout.String())
	}
}
