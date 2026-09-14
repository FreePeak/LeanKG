package registry

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// isolateHome points $HOME at a temp dir so the real ~/.leankg/registry.json is
// never read or written.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// resetEngineEnv keeps Status on the sqlite engine regardless of ambient env.
func resetEngineEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
}

func TestLoadDefaultWhenMissing(t *testing.T) {
	home := isolateHome(t)
	reg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Version != RegistryVersion {
		t.Fatalf("version = %d, want %d", reg.Version, RegistryVersion)
	}
	if len(reg.Repos) != 0 {
		t.Fatalf("repos = %v, want empty", reg.Repos)
	}
	if want := filepath.Join(home, ".leankg", "registry.json"); Path() != want {
		t.Fatalf("Path() = %s, want %s", Path(), want)
	}
}

// The on-disk schema is the Rust contract: {"version":1,"repos":{"name":
// {"path":…,"last_indexed":null,"element_count":null}}} — the Option fields are
// present as JSON null, not omitted.
func TestRegisterResolvesRelativePathAndWritesRustSchema(t *testing.T) {
	isolateHome(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := Register("demo", filepath.Join("sub", "repo")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var doc struct {
		Version int                                   `json:"version"`
		Repos   map[string]map[string]json.RawMessage `json:"repos"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Version != 1 {
		t.Fatalf("version = %d, want 1", doc.Version)
	}
	entry, ok := doc.Repos["demo"]
	if !ok {
		t.Fatalf("repos.demo missing: %s", raw)
	}
	if string(entry["element_count"]) != "null" || string(entry["last_indexed"]) != "null" {
		t.Fatalf("bookkeeping fields = %s / %s, want JSON null each",
			entry["last_indexed"], entry["element_count"])
	}
	if got, want := string(entry["path"]), `"`+filepath.Join(cwd, "sub", "repo")+`"`; got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
}

func TestRegisterAbsolutePathIsKeptAndListingSorted(t *testing.T) {
	isolateHome(t)
	for _, name := range []string{"zeta", "alpha", "mid"} {
		if err := Register(name, t.TempDir()); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	entries, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Name)
	}
	if strings.Join(got, ",") != "alpha,mid,zeta" {
		t.Fatalf("names = %v, want [alpha mid zeta]", got)
	}
}

func TestUnregisterReportsPresence(t *testing.T) {
	isolateHome(t)
	if err := Register("r1", t.TempDir()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	was, err := Unregister("r1")
	if err != nil || !was {
		t.Fatalf("Unregister(r1) = %v, %v; want true, nil", was, err)
	}
	entries, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("after unregister: %v", entries)
	}
	was, err = Unregister("r1")
	if err != nil || was {
		t.Fatalf("second Unregister(r1) = %v, %v; want false, nil", was, err)
	}
}

func TestUpdateLastIndexedStampsRegisteredRepoOnly(t *testing.T) {
	isolateHome(t)
	if err := Register("r1", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := UpdateLastIndexed("r1", "2026-09-12T00:00:00Z", 42); err != nil {
		t.Fatalf("UpdateLastIndexed: %v", err)
	}
	e, err := Get("r1")
	if err != nil {
		t.Fatal(err)
	}
	if e.LastIndexed == nil || *e.LastIndexed != "2026-09-12T00:00:00Z" {
		t.Fatalf("LastIndexed = %v", e.LastIndexed)
	}
	if e.ElementCount == nil || *e.ElementCount != 42 {
		t.Fatalf("ElementCount = %v", e.ElementCount)
	}
	// Unknown name: no-op, no error (Rust if-let on get_mut).
	if err := UpdateLastIndexed("ghost", "x", 1); err != nil {
		t.Fatalf("UpdateLastIndexed(ghost): %v", err)
	}
}

func TestStatusReportsNotIndexedRepo(t *testing.T) {
	isolateHome(t)
	resetEngineEnv(t)
	dir := t.TempDir() // no .leankg directory
	if err := Register("cold", dir); err != nil {
		t.Fatal(err)
	}
	s, err := Status("cold")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if s.Indexed || s.CurrentElements != nil || s.CurrentRelationships != nil {
		t.Fatalf("cold repo reported live state: %+v", s)
	}
	out := s.Render()
	for _, want := range []string{
		"Repository: cold\n",
		"  Path: " + dir + "\n",
		"  Last indexed: None\n",
		"  Element count: None\n",
		"  Status: Not indexed (no .leankg directory found)\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Render missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Current elements") {
		t.Fatalf("Render printed live counts for an unindexed repo:\n%s", out)
	}
}

func TestStatusReadsLiveCountsForIndexedRepo(t *testing.T) {
	isolateHome(t)
	resetEngineEnv(t)
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertElements([]store.Element{
		{QualifiedName: "a::one", ElementType: "function", Name: "one", FilePath: "a.go"},
		{QualifiedName: "a::two", ElementType: "function", Name: "two", FilePath: "a.go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRelationships([]store.Relationship{
		{Source: "a::one", Target: "a::two", RelType: "calls", Confidence: 0.9},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Register("warm", dir); err != nil {
		t.Fatal(err)
	}
	s, err := Status("warm")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !s.Indexed {
		t.Fatalf("Indexed = false; want true")
	}
	if s.CurrentElements == nil || *s.CurrentElements != 2 {
		t.Fatalf("CurrentElements = %v, want 2", s.CurrentElements)
	}
	if s.CurrentRelationships == nil || *s.CurrentRelationships != 1 {
		t.Fatalf("CurrentRelationships = %v, want 1", s.CurrentRelationships)
	}
	out := s.Render()
	if !strings.Contains(out, "  Current elements: 2\n") || !strings.Contains(out, "  Current relationships: 1\n") {
		t.Fatalf("Render missing live counts:\n%s", out)
	}
	if strings.Contains(out, "Not indexed") {
		t.Fatalf("Render said not indexed for an indexed repo:\n%s", out)
	}
}

func TestStatusUnknownNameIsErrNotFound(t *testing.T) {
	isolateHome(t)
	if _, err := Status("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Status(nope) error = %v, want ErrNotFound", err)
	}
}

func TestRenderListAndConfirmations(t *testing.T) {
	isolateHome(t)
	if out := RenderList(nil); out != "No repositories registered. Run 'leankg register <name>' to add one.\n" {
		t.Fatalf("empty list render = %q", out)
	}
	if err := Register("demo", "/tmp/demo"); err != nil {
		t.Fatal(err)
	}
	entries, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if out := RenderList(entries); out != "Registered repositories:\n  - demo: /tmp/demo (indexed: None)\n" {
		t.Fatalf("list render = %q", out)
	}
	if out := ConfirmRegister("demo", "/tmp/demo"); out != "Registered repository 'demo' at /tmp/demo\n" {
		t.Fatalf("register render = %q", out)
	}
	if out := ConfirmUnregister("demo", true); out != "Unregistered repository 'demo'\n" {
		t.Fatalf("unregister render = %q", out)
	}
	if out := ConfirmUnregister("demo", false); out != "Repository 'demo' not found in registry\n" {
		t.Fatalf("missing unregister render = %q", out)
	}
}

func TestLoadMalformedRegistryIsAnError(t *testing.T) {
	home := isolateHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load() = nil error for malformed registry")
	}
}
