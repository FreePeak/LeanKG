package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindPositional(t *testing.T) {
	// A bare positional selects the project (the `leankg index <dir>` convention).
	p := ""
	bindPositional("run", []string{"/abs/child"}, &p)
	if p != "/abs/child" {
		t.Fatalf("positional ignored: project=%q — the silent-cwd-store footgun", p)
	}

	// --project wins over the positional.
	p = "--set"
	bindPositional("run", []string{"/abs/child"}, &p)
	if p != "--set" {
		t.Fatalf("explicit project overwritten: %q", p)
	}

	// Flag-only args leave project untouched.
	p = ""
	bindPositional("run", []string{"--model", "m"}, &p)
	if p != "" {
		t.Fatalf("flag value consumed as positional: %q", p)
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
