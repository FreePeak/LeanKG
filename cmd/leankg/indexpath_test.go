package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIndexRefusesAPathThatDoesNotExist pins the precondition the walk
// silently lacked: `leankg index <dir>` requires <dir> to BE a directory.
//
// The defect: the walk treats a missing root as an empty tree, so
// `leankg index ./nope` printed the ordinary success line
// ("indexed ./nope: files=0 elements=0 … skipped=0") and exited 0 — after
// having created a store AT the missing path. Every downstream signal then
// agrees with the lie: `leankg status` reports `cold`, `doctor` reports the
// store is empty, and an agent wiring the project sees a healthy-looking
// pipeline that indexed nothing. The "0 files indexed" reading is also
// indistinguishable from "a real, genuinely empty project", which is the one
// case where a zero exit is correct.
//
// A typo in a path is the most common mistake this verb can be handed, and it
// is the one that costs the most: the agent proceeds as if it had indexed.
func TestIndexRefusesAPathThatDoesNotExist(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	base := t.TempDir()
	proj := filepath.Join(base, "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "p.go"),
		[]byte("package p\n\nfunc Exists() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLIIn(t, proj, "index", "./nope")
	if code == 0 {
		t.Fatalf("index of a missing path must fail, not print a success line (exit 0):\n%s\n%s", stdout, stderr)
	}
	if strings.Contains(stdout, "indexed") {
		t.Fatalf("index of a missing path must not report success:\n%s", stdout)
	}
	if !strings.Contains(stderr+stdout, "nope") {
		t.Fatalf("the error must name the path it refused:\n%s\n%s", stdout, stderr)
	}
	// And it must not have created a store where the tree should have been.
	if _, err := os.Stat(filepath.Join(proj, "nope")); err == nil {
		t.Fatal("index must not create the missing path it was asked to index")
	}
}

// TestIndexStillAcceptsAnEmptyDirectory is the other half: an existing but
// empty directory is a legitimate project (a fresh repo, a subpackage) and
// must keep reporting the zero counts with exit 0. Without this the guard
// above would "fix" the defect by refusing a valid input.
func TestIndexStillAcceptsAnEmptyDirectory(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	proj := t.TempDir()
	empty := filepath.Join(proj, "sub")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runCLIIn(t, empty, "index", ".")
	if code != 0 {
		t.Fatalf("an existing empty directory is a valid project (exit %d):\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "files=0") {
		t.Fatalf("an empty project must still report its counts:\n%s", stdout)
	}
}
