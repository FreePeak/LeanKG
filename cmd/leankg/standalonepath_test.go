package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestStandaloneDBPathReachesEveryVerb pins the FR-P2 contract: the store
// location is configured ONCE (LEANKG_DB_PATH env or leankg.yaml
// db.standalone_db_path) and every verb that opens a store must honour it.
//
// The defect: only `serve` consulted the override (cmd/leankg/main.go). Every
// other verb passed "" as OpenBackend's dbPath, and OpenBackend's sqlite
// branch hardcodes projectDir+"/.leankg/leankg.db". So the documented
// standalone path split one store in two: `leankg index` wrote into
// <project>/.leankg/leankg.db while `serve`, `status` and `query` opened the
// configured file and reported an empty, `cold` graph. The user sees a
// successful index followed by "no hits" forever.
//
// The writer/reader split is the real assertion: a store written by `index`
// through the env override must be visible to the read verbs through the SAME
// override, and the project-scoped store must stay empty (no silent second
// copy of the corpus).
func TestStandaloneDBPathReachesEveryVerb(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "p.go"),
		[]byte("package p\n\nfunc Standalone() int { return 7 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The store lives OUTSIDE the project: standalone mode is exactly the
	// "the project dir is not the store" shape, so a store that still lands
	// under <project>/.leankg is the bug, not a fallback.
	storePath := filepath.Join(t.TempDir(), "standalone", "leankg.db")
	t.Setenv("LEANKG_DB_PATH", storePath)

	if out, _, code := runCLIIn(t, proj, "index", "."); code != 0 {
		t.Fatalf("index exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("index must write the configured store %s: %v", storePath, err)
	}
	projectScoped := filepath.Join(proj, ".leankg", "leankg.db")
	if _, err := os.Stat(projectScoped); err == nil {
		t.Fatalf("standalone mode must not also create the project-scoped store %s "+
			"(a second copy of the corpus is the split this test exists to prevent)", projectScoped)
	}

	// Every read verb must now see the one store `index` just wrote. A verb
	// that silently opened a missing/absent project-scoped store instead
	// reports zero elements, which is the user-visible symptom.
	for _, tc := range []struct {
		verb string
		args []string
	}{
		{"status", []string{"status"}},
		{"doctor", []string{"doctor"}},
		{"query", []string{"query", "Standalone"}},
	} {
		stdout, stderr, code := runCLIIn(t, proj, tc.args...)
		if code != 0 {
			t.Fatalf("%s exit %d:\n%s\n%s", tc.verb, code, stdout, stderr)
		}
		if !strings.Contains(stdout, "Standalone") && !strings.Contains(stdout, "elements=1") {
			t.Fatalf("%s did not read the standalone store (want the element index wrote):\n%s\n%s",
				tc.verb, stdout, stderr)
		}
	}
}

// TestStandaloneEmbedRunIsSelfContained is the same contract for the embed
// writer: `leankg-embed run` took its single-flight flock at
// <project>/.leankg/embed.lock, a path that does not exist in standalone mode,
// so the run died with a bare os error before it touched the store. The lock
// belongs next to the store, not next to the project.
func TestStandaloneEmbedRunIsSelfContained(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	t.Setenv("LEANKG_EMBED_PROVIDER", "deterministic") // offline; no sidecar

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "p.go"),
		[]byte("package p\n\nfunc Embedded() int { return 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(t.TempDir(), "standalone", "leankg.db")
	t.Setenv("LEANKG_DB_PATH", storePath)

	if out, _, code := runCLIIn(t, proj, "index", "."); code != 0 {
		t.Fatalf("index exit %d:\n%s", code, out)
	}
	embedBin := filepath.Join(t.TempDir(), "leankg-embed")
	build := exec.Command("go", "build", "-o", embedBin, "./cmd/leankg-embed")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build leankg-embed: %v\n%s", err, out)
	}
	cmd := exec.Command(embedBin, "run", "--project", proj)
	cmd.Dir = proj
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("leankg-embed run must work with the store outside the project: %v\n%s", err, out)
	}
	// The project-scoped STORE must not appear: one configured store, not a
	// second copy. (.leankg/config.json — the persisted first-run setup choice
	// — is project config, not store state, so it may exist.)
	if _, err := os.Stat(filepath.Join(proj, ".leankg", "leankg.db")); err == nil {
		t.Fatalf("leankg-embed must not write a second store at %s/.leankg/leankg.db in standalone mode", proj)
	}
	if _, err := os.Stat(filepath.Join(proj, ".leankg", "embed.lock")); err == nil {
		t.Fatalf("the embed lock belongs next to the store, not at %s/.leankg/embed.lock", proj)
	}
	st, err := store.Open(storePath, store.RO)
	if err != nil {
		t.Fatalf("open standalone store: %v", err)
	}
	defer st.Close()
	covered, _, err := st.VectorCoverage("deterministic-384")
	if err != nil {
		t.Fatalf("vector coverage: %v", err)
	}
	if covered == 0 {
		t.Fatal("leankg-embed reported success but wrote no vectors to the standalone store")
	}
}
