package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindPositional(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		project     string // --project value before the call
		wantProject string
		wantModel   string
		wantErr     bool
	}{
		{name: "no args", args: nil, wantProject: ""},
		{name: "bare positional selects the project", args: []string{"/abs/child"}, wantProject: "/abs/child"},
		{name: "flag after positional is parsed", args: []string{"/abs/child", "--model", "m"}, wantProject: "/abs/child", wantModel: "m"},
		{name: "flag before positional", args: []string{"--model", "m", "/abs/child"}, wantProject: "/abs/child", wantModel: "m"},
		{name: "flag-only args leave project empty", args: []string{"--model", "m"}, wantProject: "", wantModel: "m"},
		{name: "project flag after positional, same dir", args: []string{"/abs/child", "--project", "/abs/child"}, wantProject: "/abs/child"},
		{name: "positional conflicts with --project", args: []string{"/abs/child", "--project", "/abs/other"}, wantErr: true},
		{name: "--project alone", args: []string{"--project", "/abs/child"}, wantProject: "/abs/child"},
		{name: "two positionals", args: []string{"/a", "/b"}, wantErr: true},
		{name: "positional then terminator then flag-like positional", args: []string{"/a", "--", "--model"}, wantErr: true},
		{name: "terminator ends flags", args: []string{"--", "/abs/child"}, wantProject: "/abs/child"},
		{name: "unknown flag", args: []string{"/abs/child", "--nope"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("run", flag.ContinueOnError)
			project := fs.String("project", tc.project, "")
			model := fs.String("model", "", "")
			err := bindPositional("run", fs, tc.args, project)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if *project != tc.wantProject {
				t.Fatalf("project = %q, want %q", *project, tc.wantProject)
			}
			if *model != tc.wantModel {
				t.Fatalf("model = %q, want %q (flag after the positional dropped)", *model, tc.wantModel)
			}
		})
	}
}

// A run stopped by its context returns the cancellation (the CLI exits
// non-zero) and records the run cancelled, after the defers that release the
// store and the sidecar have run.
func TestRunEmbedCancelledReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LEANKG_DB_PATH", filepath.Join(dir, "leankg.db"))
	t.Setenv("LEANKG_DB_ENGINE", "sqlite")
	t.Setenv("LEANKG_EMBED_PROVIDER", "deterministic")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runEmbed(ctx, []string{"--project", dir}, "incremental")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runEmbed error = %v, want context.Canceled", err)
	}
}

// TestRunExitsNonZeroWhenWriteCapUndetermined pins the CLI exit path for a
// cap-resolution error: a local provider whose /props gives no budget, with no
// LEANKG_EMBED_TEXT_CAP pin and no stored cap, must make `leankg-embed run` exit
// non-zero before any write, and the output must name the pin. The command's
// error path is log.Fatalf (exit 1), so the run is a subprocess: this test
// binary re-entered as main.
func TestRunExitsNonZeroWhenWriteCapUndetermined(t *testing.T) {
	if os.Getenv("LEANKG_EMBED_TEST_CHILD") == "1" {
		os.Args = []string{"leankg-embed", "run", "--project", os.Getenv("LEANKG_EMBED_TEST_PROJECT")}
		main()
		return
	}
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // /props 404: no budget
	}))
	t.Cleanup(srv.Close)

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunExitsNonZeroWhenWriteCapUndetermined$")
	// Later entries win in exec, so these override anything the shell exports.
	cmd.Env = append(os.Environ(),
		"LEANKG_EMBED_TEST_CHILD=1",
		"LEANKG_EMBED_TEST_PROJECT="+dir,
		"LEANKG_DB_PATH="+filepath.Join(dir, "leankg.db"),
		"LEANKG_DB_ENGINE=sqlite",
		"LEANKG_EMBED_PROVIDER=local",
		"LEANKG_EMBED_BASE_URL="+srv.URL+"/v1",
		"LEANKG_EMBED_MODEL=embeddinggemma-2",
		"LEANKG_EMBED_REVISION=",
		"LEANKG_EMBED_DIMS=",
		"LEANKG_EMBED_TEXT_CAP=",
		"LEANKG_EMBED_API_KEY=",
	)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("run with no derivable write cap: err = %v, want a non-zero exit\n%s", err, out)
	}
	if !strings.Contains(string(out), "LEANKG_EMBED_TEXT_CAP") {
		t.Fatalf("exit output does not name the pin:\n%s", out)
	}
}

// The verbs wrap their store and export errors with %w, so a cancelled run
// still reports context.Canceled (exit 130) instead of a plain failure.
func TestVerbErrorsKeepCancellation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LEANKG_DB_PATH", filepath.Join(dir, "leankg.db"))
	t.Setenv("LEANKG_DB_ENGINE", "sqlite")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	verbs := map[string]func() error{
		"export": func() error { return cmdExport(ctx, []string{"--project", dir, "--model", "m"}) },
		"status": func() error { return cmdStatus(ctx, []string{"--project", dir}) },
	}
	for name, run := range verbs {
		if err := run(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want one wrapping context.Canceled", name, err)
		}
	}
}
