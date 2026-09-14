// CLI verb tests for the portfolio registry (#376). These run the verb
// FUNCTIONS in-process (main.go's dispatch table is Main's file): the verbs
// write to os.Stdout, so each test swaps in a pipe and asserts on the captured
// text and the registry state the verb left behind.
package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// seedIndexedProject writes a real sqlite store for dir: two elements and one
// file row, so the register verb's counts are non-trivial.
func seedIndexedProject(t *testing.T, dir string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertElements([]store.Element{
		{QualifiedName: "pkg.Alpha", ElementType: "function", Name: "Alpha", FilePath: "a.go", Language: "go"},
		{QualifiedName: "pkg.Beta", ElementType: "function", Name: "Beta", FilePath: "a.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFiles([]store.FileRecord{{Path: "a.go", Size: 10, MtimeNS: 1, ContentHash: "h"}}); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterProjectAndProjectsVerbs(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "portfolio.db")
	t.Setenv(portfolioreg.DBPathEnv, registry)
	t.Setenv(portfolioreg.MaxReposEnv, "1")
	dir := t.TempDir()
	seedIndexedProject(t, dir)

	out := captureStdout(t, func() { cmdRegisterProject([]string{dir, "--name", "api"}) })
	if !strings.Contains(out, "registered api") || !strings.Contains(out, "elements=2 files=1") {
		t.Fatalf("register-project output = %q", out)
	}

	// The verb wrote a real registry row: read it back through the package API
	// rather than trusting the printed line.
	rows, err := portfolioreg.Projects(t.Context(), portfolioreg.Options{DBPath: registry})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "api" || rows[0].ElementCount != 2 ||
		rows[0].FileCount != 1 || rows[0].LastIndexed == nil {
		t.Fatalf("registry row = %+v", rows)
	}

	// The manifest is T0: registry counts + on-disk presence, hot set bounded.
	var manifest struct {
		Count    int `json:"count"`
		HotLimit int `json:"hot_limit"`
		Projects []struct {
			Project     string `json:"project"`
			Elements    int    `json:"elements"`
			Files       int    `json:"files"`
			StoreOnDisk bool   `json:"store_on_disk"`
			Hot         bool   `json:"hot"`
		} `json:"projects"`
	}
	out = captureStdout(t, func() { cmdProjects([]string{"--json"}) })
	if err := json.Unmarshal([]byte(out), &manifest); err != nil {
		t.Fatalf("projects --json output %q: %v", out, err)
	}
	if manifest.Count != 1 || manifest.HotLimit != 1 || len(manifest.Projects) != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}
	p := manifest.Projects[0]
	if p.Project != "api" || p.Elements != 2 || p.Files != 1 || !p.StoreOnDisk || !p.Hot {
		t.Fatalf("manifest project = %+v", p)
	}

	// Text mode renders the same row for a human.
	out = captureStdout(t, func() { cmdProjects(nil) })
	for _, want := range []string{"PROJECT", "api", "on-disk"} {
		if !strings.Contains(out, want) {
			t.Fatalf("projects table %q missing %q", out, want)
		}
	}

	// --forget removes the registration and says so.
	out = captureStdout(t, func() { cmdProjects([]string{"--forget", dir}) })
	if !strings.Contains(out, "forgot") {
		t.Fatalf("projects --forget output = %q", out)
	}
	if rows, err := portfolioreg.Projects(t.Context(), portfolioreg.Options{DBPath: registry}); err != nil || len(rows) != 0 {
		t.Fatalf("registry after forget: %+v, %v", rows, err)
	}
}

func TestProjectsVerbWithoutRegistry(t *testing.T) {
	t.Setenv(portfolioreg.DBPathEnv, filepath.Join(t.TempDir(), "missing.db"))
	out := captureStdout(t, func() { cmdProjects(nil) })
	if !strings.Contains(out, "no projects registered") {
		t.Fatalf("projects without a registry = %q; want a plain empty listing, not an error", out)
	}
}

func TestRegisterProjectWithoutStoreStampsNothing(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "portfolio.db")
	t.Setenv(portfolioreg.DBPathEnv, registry)
	dir := filepath.Join(t.TempDir(), "never-indexed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { cmdRegisterProject([]string{dir}) })
	if !strings.Contains(out, "never indexed") {
		t.Fatalf("register-project of an unindexed dir = %q", out)
	}
	rows, err := portfolioreg.Projects(t.Context(), portfolioreg.Options{DBPath: registry})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].LastIndexed != nil || rows[0].ElementCount != 0 {
		t.Fatalf("unindexed registration = %+v; want no stamp and zero counts", rows)
	}
	if rows[0].Name != "never-indexed" {
		t.Fatalf("name default = %q; want the directory base name", rows[0].Name)
	}
}
