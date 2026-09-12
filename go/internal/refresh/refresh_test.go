package refresh

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// isolateEnv pins the embedding provider to the offline deterministic
// implementation and the engine to sqlite, so no sidecar spawn and no ambient
// Postgres config leaks into the run.
func isolateEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LEANKG_EMBED_PROVIDER", "deterministic")
	t.Setenv("LEANKG_EMBED_BASE_URL", "")
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
}

func newProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// go.mod is the Go repo marker: lazy language activation keys off it, so a
	// marker-less temp tree activates languages only through the extension census.
	mustWrite(t, filepath.Join(dir, "go.mod"), "module example\n\ngo 1.25\n")
	mustWrite(t, filepath.Join(dir, "src", "main.go"),
		"package main\n\nfunc hello() int {\n\treturn 1\n}\n\nfunc world() int {\n\treturn 2\n}\n")
	mustWrite(t, filepath.Join(dir, "docs", "guide.md"),
		"# Guide\n\nintro\n\n## Setup\n\nsteps\n\n## Usage\n\nmore steps\n")
	return dir
}

func TestRunIndexesCodeDocsAndEmbeds(t *testing.T) {
	isolateEnv(t)
	dir := newProject(t)

	res, err := Run(context.Background(), Options{Project: dir, Path: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Path != dir {
		t.Fatalf("Path = %q, want %q", res.Path, dir)
	}
	if res.Code.Files == 0 {
		t.Fatalf("code stage indexed no files: %+v", res.Code)
	}
	if !res.DocsIndexed || res.DocsDir != filepath.Join(dir, "docs") {
		t.Fatalf("docs stage = indexed:%v dir:%q", res.DocsIndexed, res.DocsDir)
	}
	if res.DocsResult.Files == 0 || res.DocsResult.Elements == 0 {
		t.Fatalf("docs stage counters = %+v", res.DocsResult)
	}
	if res.Embed.Mode != "incremental" {
		t.Fatalf("embed mode = %q, want incremental", res.Embed.Mode)
	}
	if res.Embed.Embedded == 0 {
		t.Fatalf("embed stage wrote no vectors: %+v", res.Embed)
	}

	// The composed stages share one store: code + doc elements persisted and
	// the embedding pipeline covered every one of them.
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RO)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	elements, err := st.ElementCount()
	if err != nil {
		t.Fatal(err)
	}
	if elements < 3 { // 2 functions + 3 doc headings
		t.Fatalf("element count = %d, want >= 3", elements)
	}
	// The code stage must have produced real function elements, not just the
	// docs stage's headings.
	els, err := st.Elements()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, e := range els {
		if e.ElementType == "function" {
			found[e.Name] = true
		}
	}
	if !found["hello"] || !found["world"] {
		t.Fatalf("indexed functions = %v, want hello and world", found)
	}
	vectors, err := st.VectorCount("deterministic-384")
	if err != nil {
		t.Fatal(err)
	}
	if vectors != elements {
		t.Fatalf("vectors = %d, want one per element (%d)", vectors, elements)
	}

	out := Render(res)
	for _, want := range []string{
		"Indexing code from " + dir + "...\n",
		fmt.Sprintf("Indexed %d code files\n", res.Code.Files),
		"Indexing docs from " + filepath.Join(dir, "docs") + "...\n",
		fmt.Sprintf("Indexed %d documents and %d sections\n", res.DocsResult.Files, res.DocsResult.Elements),
		"Running embed...\n",
		"[watch] Running incremental embed...\n",
		"Refresh complete.\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Render missing %q in:\n%s", want, out)
		}
	}
}

func TestRunIncrementalSkipsAlreadyEmbedded(t *testing.T) {
	isolateEnv(t)
	dir := newProject(t)
	if _, err := Run(context.Background(), Options{Project: dir, Path: dir}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	res, err := Run(context.Background(), Options{Project: dir, Path: dir})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if res.Embed.Embedded != 0 || res.Embed.Skipped == 0 {
		t.Fatalf("second run embedded=%d skipped=%d, want 0 / >0", res.Embed.Embedded, res.Embed.Skipped)
	}
}

func TestRunWithoutDocsDirectory(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n\nfunc only() {}\n")
	res, err := Run(context.Background(), Options{Project: dir, Path: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.DocsIndexed {
		t.Fatalf("docs stage ran without a docs dir: %+v", res)
	}
	if out := Render(res); strings.Contains(out, "Indexing docs from") {
		t.Fatalf("Render mentions docs for a docless project:\n%s", out)
	}
}

func TestRunDefaultPathIsDot(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n\nfunc only() {}\n")
	t.Chdir(dir)
	res, err := Run(context.Background(), Options{Project: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Path != "." {
		t.Fatalf("Path = %q, want the Rust default %q", res.Path, ".")
	}
	if res.Code.Files == 0 {
		t.Fatalf("code stage indexed nothing from the default path: %+v", res.Code)
	}
}

func TestRunRemoteSourceUnsupported(t *testing.T) {
	isolateEnv(t)
	dir := newProject(t)
	_, err := Run(context.Background(), Options{Project: dir, Path: dir, Source: "git+https://example.invalid/repo.git"})
	if err == nil || !strings.Contains(err.Error(), "--source is not supported") {
		t.Fatalf("Source error = %v, want ErrSourceUnsupported", err)
	}
}

func TestRunFailsFastOnBadProvider(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEANKG_EMBED_PROVIDER", "bogus")
	dir := newProject(t)
	_, err := Run(context.Background(), Options{Project: dir, Path: dir})
	if err == nil || !strings.Contains(err.Error(), "unknown LEANKG_EMBED_PROVIDER") {
		t.Fatalf("error = %v, want provider failure", err)
	}
	// Stages before the embed still landed (fail-fast is at stage 3).
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RO)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if n, err := st.ElementCount(); err != nil || n == 0 {
		t.Fatalf("code stage did not persist before the embed failure: n=%d err=%v", n, err)
	}
}
