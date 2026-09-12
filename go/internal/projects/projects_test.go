package projects

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

func TestParseDirs(t *testing.T) {
	// Rust parse_project_dirs parity: comma split, trim, drop empty,
	// sort, dedup.
	got := ParseDirs(" /b/repo , /a/repo,,  /b/repo ,   ")
	want := []string{"/a/repo", "/b/repo"}
	if len(got) != len(want) {
		t.Fatalf("ParseDirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseDirs = %v, want %v", got, want)
		}
	}
	if out := ParseDirs(""); out != nil {
		t.Fatalf("empty env must yield no dirs, got %v", out)
	}
}

func TestResolveSelectors(t *testing.T) {
	base := t.TempDir()
	defaultDir := filepath.Join(base, "primary")
	dirA := filepath.Join(base, "alpha")
	dirB := filepath.Join(base, "beta")
	for _, d := range []string{defaultDir, dirA, dirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRouter(defaultDir, Config{ExtraDirs: []string{dirA, dirB}})

	if got := r.Default(); got != canonical(defaultDir) {
		t.Fatalf("Default = %s, want %s", got, canonical(defaultDir))
	}
	// Empty selector = default (the serving process's own project).
	if got, err := r.resolve(""); err != nil || got != canonical(defaultDir) {
		t.Fatalf("resolve(\"\") = %s,%v", got, err)
	}
	// Exact directory path.
	if got, err := r.resolve(dirA); err != nil || got != canonical(dirA) {
		t.Fatalf("resolve(dirA) = %s,%v", got, err)
	}
	// Directory name.
	if got, err := r.resolve("beta"); err != nil || got != canonical(dirB) {
		t.Fatalf("resolve(beta) = %s,%v", got, err)
	}
	// A file inside a registered project routes to that project (Rust
	// find_leankg_for_path ancestor walk).
	inner := filepath.Join(dirB, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(inner), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inner, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := r.resolve(inner); err != nil || got != canonical(dirB) {
		t.Fatalf("resolve(inner file) = %s,%v want %s", got, err, canonical(dirB))
	}
	// FR-ZCP-02: an explicit unknown key never falls back to the default.
	got, err := r.resolve("nope")
	if err == nil {
		t.Fatalf("resolve(nope) = %s, want error", got)
	}
	if !strings.Contains(err.Error(), "unknown project") ||
		!strings.Contains(err.Error(), canonical(dirA)) {
		t.Fatalf("error must name the known projects, got %q", err)
	}
}

func TestResolveAmbiguousName(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "x", "same")
	b := filepath.Join(base, "y", "same")
	if err := os.MkdirAll(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRouter(t.TempDir(), Config{ExtraDirs: []string{a, b}})
	if _, err := r.resolve("same"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous name must error, got %v", err)
	}
	// The full path still resolves unambiguously.
	if got, err := r.resolve(a); err != nil || got != canonical(a) {
		t.Fatalf("resolve(a) = %s,%v", got, err)
	}
}

func TestLazyOpenSeedAndClose(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	defaultDir := filepath.Join(base, "primary")
	dirA := filepath.Join(base, "alpha")
	if err := os.MkdirAll(defaultDir, 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRouter(defaultDir, Config{ExtraDirs: []string{dirA}})
	// Seed the default with an externally-opened engine: routing to it
	// must hand back the SAME engine (one store handle per project).
	st, err := store.OpenBackend(ctx, defaultDir, "sqlite", "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	defEngine := core.New(st, nil, nil)
	defEngine.SetProjectDir(defaultDir)
	r.SeedDefault(&Project{Dir: defaultDir, Name: "primary", Store: st, Engine: defEngine})

	got, err := r.EngineFor(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != defEngine {
		t.Fatal("default routing must return the seeded engine, not a second handle")
	}

	// The extra project opens lazily: its .leankg/leankg.db does not
	// exist until the first routed request.
	dbPath := filepath.Join(dirA, ".leankg", "leankg.db")
	if _, err := os.Stat(dbPath); err == nil {
		t.Fatal("extra project must not be opened before first use")
	}
	first, err := r.EngineFor(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("routed open must create the store: %v", err)
	}
	second, err := r.EngineFor(ctx, dirA)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("repeated routing must reuse the cached engine")
	}

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Open(ctx, "alpha"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Open after Close = %v, want closed error", err)
	}
	// Close must not have closed the seeded (externally owned) store.
	if _, err := st.ElementCount(); err != nil {
		t.Fatalf("seeded store must survive router.Close: %v", err)
	}
}

// TestResolveDoesNotOpenStore pins the read-only contract of Resolve: the
// dashboard's switch endpoint calls it on every POST, and Open would run
// Migrate() (a write), open memory and activate languages on the target — a
// UI action must not mutate a project it merely asks about.
func TestResolveDoesNotOpenStore(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	r := NewRouter(a, Config{ExtraDirs: []string{b}})
	defer r.Close()

	dir, err := r.Resolve(b)
	if err != nil {
		t.Fatalf("Resolve(%q) = %v", b, err)
	}
	if dir == "" {
		t.Fatal("Resolve returned an empty dir")
	}
	if _, err := os.Stat(filepath.Join(b, ".leankg")); !os.IsNotExist(err) {
		t.Fatalf("Resolve created a store under %s (stat err = %v); it must not open", b, err)
	}
	if _, err := r.Resolve(filepath.Join(t.TempDir(), "unknown")); err == nil {
		t.Fatal("Resolve accepted an unregistered path")
	}
}
