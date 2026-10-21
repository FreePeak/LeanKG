package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// seedUnmigratedStore writes a store file with NO migration ledger — the
// state a project indexed by an older leankg is in. The file exists, so the
// project counts as indexed and `leankg migrate` has something to migrate;
// the ledger is empty, so the whole embedded plan is pending.
//
// The ledger table is created by the first migration, so the query that
// materialises the file necessarily fails on "no such table" — the file is
// what this helper is after, and the error is how the connection is forced
// open.
func seedUnmigratedStore(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, ".leankg", "leankg.db")
	st, err := store.Open(path, store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	// The query fails (no schema_migrations table yet) and that is what
	// forces the connection — and so the file — to exist.
	_, _ = store.AppliedMigrations(st)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the store file must exist for the project to count as indexed: %v", err)
	}
}

// TestWriterVerbsRefuseAnUnindexedProject pins the defect this branch fixes:
// three verbs opened a store read-write without asking whether the project
// HAD one, and opening a missing store CREATES it (the file and its schema).
// So a `leankg metrics` against a project nobody indexed materialised a
// `<dir>/.leankg` where none belonged.
//
// The blast radius is not local. The portfolio registry holds a row for
// `/private/tmp` (name `tmp`), so a writer sweep over the fleet materialises
// `/private/tmp/.leankg` — and that one stray directory BREAKS THE WHOLE REPO
// TEST SUITE, because `internal/projectcfg.FindProjectRoot` walks up and
// latches onto the first ancestor that owns a `.leankg` entry:
//
//	$ mkdir -p /private/tmp/.leankg
//	$ go test ./internal/projectcfg/
//	FAIL internal/projectcfg  "no marker: got \"/tmp\", want \"/tmp/TestFindProjectRootWalksUp.../002\""
//	$ rm -rf /private/tmp/.leankg
//	$ go test ./internal/projectcfg/     # ok
//
// So the behaviour pinned here is the CONSUMER-VISIBLE one: against a project
// with no store, `metrics`, `dashboard --format text` and `migrate` each
// refuse — exit 2, a diagnosis on stderr, an empty stdout (the payload
// contract is not corrupted), and NO store on disk. Before the fix every one
// of them exited 0 and left a store behind.
func TestWriterVerbsRefuseAnUnindexedProject(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	// A standalone path override would point every project at one file, so
	// this case is the plain project-scoped store.
	t.Setenv("LEANKG_DB_PATH", "")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"metrics", []string{"metrics"}},
		{"dashboard text", []string{"dashboard", "--format", "text"}},
		{"dashboard json", []string{"dashboard", "--format", "json"}},
		{"migrate", []string{"migrate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A project directory with source and NO store: the state a
			// first `leankg index .` is supposed to be run against.
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "p.go"),
				[]byte("package p\n\nfunc P() int { return 1 }\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			args := append(append([]string{}, tc.args...), "--project", dir)
			stdout, stderr, code := runCLIIn(t, dir, args...)
			body := stdout + stderr

			if code != 2 {
				t.Fatalf("%s on an unindexed project must refuse with exit 2, got %d:\n%s",
					tc.name, code, body)
			}
			if !strings.Contains(stderr, "not indexed") {
				t.Errorf("%s must say the project is not indexed, on stderr:\n%s", tc.name, body)
			}
			// The diagnosis has to be actionable: it names the project and
			// the command that fixes it.
			if !strings.Contains(stderr, dir) {
				t.Errorf("%s must name the project it refused:\n%s", tc.name, body)
			}
			if !strings.Contains(stderr, "leankg index .") {
				t.Errorf("%s must name the command that creates the store:\n%s", tc.name, body)
			}
			// The refusal is a DIAGNOSIS, not a payload: stdout stays empty
			// so a JSON consumer sees no half-written document.
			if stdout != "" {
				t.Errorf("%s must not write to stdout when it refuses:\n%s", tc.name, stdout)
			}
			// The part that proves the defect: nothing was created on disk.
			if _, err := os.Stat(filepath.Join(dir, ".leankg")); err == nil {
				t.Fatalf("%s CREATED a store for a project nobody indexed: %s",
					tc.name, filepath.Join(dir, ".leankg"))
			}
		})
	}
}

// TestWriterVerbsStillRunOnAnIndexedProject is the other half of the pin: the
// guard must refuse a project that has NO store and nothing else. A project
// that HAS one keeps working — a guard that refused everything would pass the
// test above and break the product.
func TestWriterVerbsStillRunOnAnIndexedProject(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	t.Setenv("LEANKG_DB_PATH", "")

	dir := seedCLIProject(t, nil, nil)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"metrics", []string{"metrics"}},
		{"dashboard text", []string{"dashboard", "--format", "text"}},
		{"migrate", []string{"migrate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string{}, tc.args...), "--project", dir)
			stdout, stderr, code := runCLIIn(t, dir, args...)
			if code != 0 {
				t.Fatalf("%s on an INDEXED project must still succeed, got %d:\n%s\n%s",
					tc.name, code, stdout, stderr)
			}
			if strings.Contains(stderr, "not indexed") {
				t.Errorf("%s must not refuse a project that has a store:\n%s", tc.name, stderr)
			}
		})
	}
}
