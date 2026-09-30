//go:build !tstree

package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// newScratchStore opens a migrated store inside dir (the same shape
// setupProject builds, minus the fixture copy).
func newScratchStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestWalkSkipsDotFilesNotJustDotDirs pins the trust boundary at the walk.
//
// The walk skips dot-prefixed DIRECTORIES (`.git`, `.worktrees`, any `.*`), so
// a reader reasonably assumes a dot-prefixed FILE is invisible too. It is not:
// the file gate only checks the extension, so every dotfile with a language
// extension lands in the graph — `.pr-body.md` did, and on this repository it
// answered a real agent query ("where is the single flight lock taken") at
// rank 1 on BOTH arms, outranking the source that documents the loop.
//
// The failure is silent and it is the wrong direction: agent scratch files,
// harness configs and dotfile-shaped notes are transient by definition, and
// anything an agent writes mid-session pollutes the index it is then querying.
// The security-adjacent case is worse — `.env`, `.npmrc` and friends are exactly
// the files that must never be copied into a store that gets embedded, exported
// or pushed. A dot-directory skip is a partial secret guard, not a complete one,
// and an incomplete secret guard is the kind that reads as complete.
func TestWalkSkipsDotFilesNotJustDotDirs(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("visible.go", "package p\n\nfunc Visible() int { return 1 }\n")
	// One dotfile per indexable shape, all carrying a body an agent would
	// never want answered as repository knowledge.
	write(".scratch.md", "# Scratch\n\n## Todo\n\nagent-only notes\n")
	write(".env", "API_KEY=sk-live-not-a-real-key\nDATABASE_URL=postgres://u:pw@host/db\n")
	write(".config.json", "{\"token\":\"gh-not-a-real-token\"}\n")
	write(".github", "") // a FILE named like a directory, to pin the file gate
	if err := os.Remove(filepath.Join(dir, ".github")); err != nil {
		t.Fatal(err)
	}
	write(".gitignore", "*.log\n")

	// A dot-DIRECTORY was already skipped and must stay skipped.
	if err := os.MkdirAll(filepath.Join(dir, ".hidden", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(".hidden/deep/inner.md", "## Inner\n\nhidden\n")
	// A dot-directory that is explicitly listed must stay skipped too: the
	// skip is unconditional, not "unless named".
	if err := os.MkdirAll(filepath.Join(dir, ".worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(".worktrees/other.md", "## Other worktree\n\ncontent\n")

	st := newScratchStore(t, dir)
	res := index(t, st, dir)

	if res.Elements == 0 {
		t.Fatal("fixture produced no elements; the walk itself is broken, not the gate")
	}
	// The visible source is indexed — this is a gate test, not a walk test.
	if els, _ := st.FindExact("Visible"); len(els) != 1 {
		t.Fatalf("visible.go must still index (got %d elements for Visible)", len(els))
	}

	files, err := st.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		base := filepath.Base(f.Path)
		if len(base) > 1 && base[0] == '.' {
			t.Errorf("dot-prefixed file %q reached the index (%d elements); the walk skips dot-DIRECTORIES "+
				"but not dot-FILES, so agent scratch files and .env-shaped secrets enter the store and the search results",
				f.Path, res.Elements)
		}
	}

	// And the content gate: no element may carry the secret-shaped body.
	els, err := st.Elements()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range els {
		for _, needle := range []string{"sk-live-", "postgres://u:pw", "gh-not-a-real-token", "agent-only notes"} {
			if strings.Contains(e.Content, needle) {
				t.Errorf("%s carries %q from a dotfile into the graph", e.QualifiedName, needle)
			}
		}
	}
}
