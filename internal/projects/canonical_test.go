package projects

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCanonicalResolvesAMissingLeaf pins the contract: a selector resolves
// the same way whether or not the file it names exists yet.
//
// EvalSymlinks fails outright when the leaf does not exist, so a path to a
// not-yet-created file came back UNRESOLVED while the project directory it
// points into came back resolved (/tmp -> /private/tmp on macOS) — and the
// two never compared equal. A model pointing at a file it is about to
// create therefore routed nowhere.
func TestCanonicalResolvesAMissingLeaf(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "not", "created", "yet.go")
	got := canonical(missing)
	want := canonical(base) + "/not/created/yet.go"
	if got != want {
		t.Fatalf("canonical(%s) = %s, want %s", missing, got, want)
	}
	// A path that exists resolves to itself, as before.
	if got, want := canonical(base), canonical(base); got != want {
		t.Fatalf("canonical(base) = %s, want %s", got, want)
	}
}

// TestPathToAMissingFileRoutesToItsProject is the user-visible half: the
// documented contract is that a file inside a routed project routes to that
// project, and a file that does not exist yet is still a file inside it.
func TestPathToAMissingFileRoutesToItsProject(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRouter(proj, Config{})

	missing := filepath.Join(proj, "internal", "not-created-yet.go")
	if got, err := r.resolve(missing); err != nil || got != canonical(proj) {
		t.Fatalf("resolve(%s) = %s,%v want %s", missing, got, err, canonical(proj))
	}
}

// TestPathInsideAProjectBeatsASameNamedProject pins the other half of the
// resolver's order: a path that lies inside a registered project resolves
// to THAT project, even when its basename is also the name of another
// registered project. The basename match used to win, so ".../outer/api/file.go"
// routed to whichever project was named "api" — not the one the path is in.
func TestPathInsideAProjectBeatsASameNamedProject(t *testing.T) {
	base := t.TempDir()
	outer := filepath.Join(base, "outer")
	named := filepath.Join(base, "other", "api")
	for _, d := range []string{outer, named} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRouter(outer, Config{ExtraDirs: []string{named}})

	inner := filepath.Join(outer, "api", "file.go")
	if got, err := r.resolve(inner); err != nil || got != canonical(outer) {
		t.Fatalf("resolve(%s) = %s,%v want %s", inner, got, err, canonical(outer))
	}
	// The name still resolves to the project that carries it.
	if got, err := r.resolve("api"); err != nil || got != canonical(named) {
		t.Fatalf("resolve(api) = %s,%v want %s", got, err, canonical(named))
	}
}

// TestDotSelectorIsAPlace pins that "." keeps meaning "here": it is a
// path, so the ancestor walk may run and it resolves to the project that
// contains the cwd. Treated as a NAME it would be matched against a
// project's basename ("projects") and refused — and a client that sends
// ?project=. would get an error instead of its own project.
func TestDotSelectorIsAPlace(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// The project that CONTAINS the cwd's directory.
	containing := filepath.Dir(cwd)
	r := NewRouter(containing, Config{})

	got, err := r.resolve(".")
	if err != nil {
		t.Fatalf("resolve(.) failed: %v", err)
	}
	if got != canonical(containing) {
		t.Fatalf("resolve(.) = %s, want %s", got, canonical(containing))
	}
}
