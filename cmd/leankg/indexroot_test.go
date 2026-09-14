package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestSameIndexRoot pins the guard behind the `index . --source <path>` data-loss
// fix: the source walk and the store's project root must be recognised as
// different, including through the macOS /tmp -> /private/tmp symlink that
// silently defeated path comparisons elsewhere in this repo's history.
func TestSameIndexRoot(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", dir, dir, true},
		{"trailing separator", dir, dir + string(filepath.Separator), true},
		{"dot is not a sibling", dir, ".", false},
		{"subdirectory is not the root", dir, sub, false},
	}
	for _, tc := range cases {
		got, err := sameIndexRoot(tc.a, tc.b)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: sameIndexRoot(%q, %q) = %v, want %v", tc.name, tc.a, tc.b, got, tc.want)
		}
	}
	// A symlinked view of the same directory must count as the same root.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, err := sameIndexRoot(dir, link); err != nil || !got {
		t.Errorf("symlinked root must compare equal (got %v, err %v)", got, err)
	}
}

// TestIndexSourceDoesNotSweepProject is the destructive regression for the
// --source data-loss bug: `leankg index . --source <foreign tree>` walked the
// source with the project's store open, and the store-wide reconcile swept
// every element the walk could not see — replacing the project's content with
// the source's at identical counters. Assert the SURVIVORS by qualified name
// (count-only assertions passed on the bug: 1 element before, 1 after, all
// while own.go::OwnThing died and lib.go::FromSource took its place) and that
// the run is refused, not silently destructive.
func TestIndexSourceDoesNotSweepProject(t *testing.T) {
	bin := buildLeanKG(t)

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "own.go"),
		[]byte("package own\n\nfunc OwnThing() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "lib.go"),
		[]byte("package lib\n\nfunc FromSource() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))

	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = proj
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if _, err := run("index", "."); err != nil {
		t.Fatalf("baseline index: %v", err)
	}

	out, err := run("index", ".", "--source", src)
	if err == nil {
		t.Fatalf("index --source <foreign tree> was accepted:\n%s", out)
	}
	if want := "refusing to index"; !strings.Contains(out, want) {
		t.Errorf("refusal must explain itself, got:\n%s", out)
	}

	// Survivors, read through the store's own bookkeeping: the project's file is
	// still there and the refused source's file never arrived.
	st, oerr := store.Open(filepath.Join(proj, ".leankg", "leankg.db"), store.RO)
	if oerr != nil {
		t.Fatalf("open store: %v", oerr)
	}
	defer st.Close()
	files, ferr := st.Files()
	if ferr != nil {
		t.Fatal(ferr)
	}
	var seen []string
	for _, f := range files {
		seen = append(seen, f.Path)
	}
	joined := strings.Join(seen, ",")
	if !strings.Contains(joined, "own.go") {
		t.Errorf("own.go swept from the store by a run that was refused; survivors: %s", joined)
	}
	if strings.Contains(joined, "lib.go") {
		t.Errorf("the refused source run still wrote lib.go into the project store: %s", joined)
	}
}
