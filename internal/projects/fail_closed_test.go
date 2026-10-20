package projects

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnknownNameDoesNotFallBackToTheContainingProject is the regression for
// a defect measured live on the 286-project self-host.
//
// The serve process's cwd is the leankg repository, and that repository is a
// REGISTERED project. The resolver canonicalized a bare name against the
// cwd and then walked up the filesystem, so an unknown name climbed to the
// cwd and resolved to that project:
//
//	{"project":"totally-not-a-real-project-xyz"} → the leankg project's
//	status (11,246 elements) — a confident answer from the WRONG graph
//	?project=totally-not-a-real-project-xyz          → HTTP 200
//
// FR-ZCP-02 says an explicit route key that resolves to nothing NEVER falls
// back to another project. The old test for that rule passed only because
// the test process's cwd was not a registered project: the walk climbed out
// of the repository and found nothing. This test puts the cwd INSIDE a
// registered project — the live configuration — and the rule holds.
func TestUnknownNameDoesNotFallBackToTheContainingProject(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// The project that CONTAINS the cwd: the serve process's own project
	// on the self-host.
	containing := cwd
	for parent := filepath.Dir(cwd); parent != cwd; parent = filepath.Dir(parent) {
		containing = parent
		break
	}
	r := NewRouter(containing, Config{})

	if got, err := r.resolve("totally-not-a-real-project-xyz"); err == nil {
		t.Fatalf("an unknown name resolved to %s — it must not fall back to the project containing the cwd", got)
	} else if !strings.Contains(err.Error(), "unknown project") {
		t.Fatalf("err = %v; it must name the unknown project", err)
	}

	// A PATH inside the project still routes to it: the ancestor walk is
	// for paths, and that behavior is not what changed.
	inner := filepath.Join(containing, "some", "file.go")
	if got, err := r.resolve(inner); err != nil || got != canonical(containing) {
		t.Fatalf("resolve(%s) = %s,%v want %s", inner, got, err, canonical(containing))
	}
}

// TestAmbiguousNameNamesTheCandidates: "several registered projects share
// that name" left the caller with no way to choose. The candidates are now
// named, so the retry can pass a path.
func TestAmbiguousNameNamesTheCandidates(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "x", "same")
	b := filepath.Join(base, "y", "same")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRouter(t.TempDir(), Config{ExtraDirs: []string{a, b}})

	_, err := r.resolve("same")
	if err == nil {
		t.Fatal("an ambiguous name must error")
	}
	msg := err.Error()
	for _, want := range []string{"ambiguous", canonical(a), canonical(b), "absolute path"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error must name %q, got %q", want, msg)
		}
	}
}
