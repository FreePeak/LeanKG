package projectcfg

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

func parseFixture(t *testing.T, name string) ProjectConfig {
	t.Helper()
	cfg, err := Parse([]byte(readFixture(t, name)))
	if err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return cfg
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// keyPaths returns every mapping key path in a document, so two serializations
// can be compared for shape without depending on formatting or scalar values.
func keyPaths(t *testing.T, doc *yaml.Node, prefix string) []string {
	t.Helper()
	var out []string
	switch doc.Kind {
	case yaml.DocumentNode:
		for _, c := range doc.Content {
			out = append(out, keyPaths(t, c, prefix)...)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(doc.Content); i += 2 {
			path := doc.Content[i].Value
			if prefix != "" {
				path = prefix + "." + path
			}
			out = append(out, path)
			out = append(out, keyPaths(t, doc.Content[i+1], path)...)
		}
	case yaml.SequenceNode, yaml.AliasNode:
		for _, c := range doc.Content {
			out = append(out, keyPaths(t, c, prefix)...)
		}
	}
	sort.Strings(out)
	return out
}

func docOf(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, src)
	}
	return &doc
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultProjectConfig()
	if cfg.Project.Name != "my-project" {
		t.Errorf("project.name = %q, want my-project", cfg.Project.Name)
	}
	if cfg.Project.Root != "." {
		t.Errorf("project.root = %q, want .", cfg.Project.Root)
	}
	if !cfg.MCP.Enabled || cfg.MCP.Port != 3000 {
		t.Errorf("mcp = %+v, want enabled port 3000", cfg.MCP)
	}
	if !cfg.MCP.AutoIndexOnStart || cfg.MCP.AutoIndexThresholdMinutes != 5 {
		t.Errorf("mcp auto-index defaults = %+v", cfg.MCP)
	}
	if cfg.MCP.AutoIndexOnDBWrite {
		t.Error("mcp.auto_index_on_db_write must default false")
	}
	if !cfg.MCP.RequireGitForAutoIndex {
		t.Error("mcp.require_git_for_auto_index must default true")
	}
	if cfg.Indexer.TypedResolve != "off" {
		t.Errorf("indexer.typed_resolve = %q, want off", cfg.Indexer.TypedResolve)
	}
	if !contains(cfg.Indexer.Exclude, "**/node_modules/**") || !contains(cfg.Indexer.Exclude, "**/vendor/**") {
		t.Errorf("indexer.exclude = %v", cfg.Indexer.Exclude)
	}
	for _, want := range []string{"*.go", "*.java", "*.cs"} {
		if !contains(cfg.Indexer.Include, want) {
			t.Errorf("indexer.include missing %s: %v", want, cfg.Indexer.Include)
		}
	}
	if cfg.Documentation.Output != "./docs" || !reflect.DeepEqual(cfg.Documentation.Templates, []string{"agents", "claude"}) {
		t.Errorf("documentation = %+v", cfg.Documentation)
	}
	// Default language list is a superset of every registry-backed language.
	for _, l := range []string{
		"go", "typescript", "python", "java", "kotlin", "rust", "dart", "swift",
		"objc", "c", "cpp", "ruby", "php", "perl", "r", "elixir", "bash", "lua",
		"scala", "zig", "solidity", "csharp",
	} {
		if !contains(cfg.Project.Languages, l) {
			t.Errorf("missing default language %s", l)
		}
	}
	if cfg.Auth.Enabled || cfg.Auth.Provider != AuthProviderStatic {
		t.Errorf("auth = %+v, want disabled static", cfg.Auth)
	}
	if cfg.DB != nil || cfg.LSP != nil || cfg.Source != nil || cfg.Microservice != nil {
		t.Errorf("optional blocks must default nil: %+v", cfg)
	}
	if cfg.Project.ProjectPath != "" {
		t.Errorf("project_path must default empty, got %q", cfg.Project.ProjectPath)
	}
}

// The repo's own leankg.yaml was rendered by the Rust writer, so its key tree
// is the golden shape for this package's default serialization.
func TestDefaultSerializationMatchesRustShape(t *testing.T) {
	out, err := Marshal(DefaultProjectConfig())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := keyPaths(t, docOf(t, string(out)), "")
	want := keyPaths(t, docOf(t, readFixture(t, "full.yaml")), "")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default key tree differs from the Rust-written config\n got: %v\nwant: %v\n--- rendered ---\n%s", got, want, out)
	}
	if strings.Contains(string(out), "db:") {
		t.Errorf("default config must not emit a db: block:\n%s", out)
	}
	if strings.Contains(string(out), "project_path") {
		t.Errorf("nil project_path must not serialize:\n%s", out)
	}
}

func TestParseFullFixture(t *testing.T) {
	cfg := parseFixture(t, "full.yaml")
	if cfg.Project.Name != ".leankg" || cfg.Project.Root != "./src" {
		t.Errorf("project = %+v", cfg.Project)
	}
	if !reflect.DeepEqual(cfg.Project.Languages, []string{"javascript", "rust"}) {
		t.Errorf("languages = %v", cfg.Project.Languages)
	}
	if cfg.Project.ProjectPath != "" {
		t.Errorf("project_path = %q, want empty", cfg.Project.ProjectPath)
	}
	if len(cfg.Indexer.Include) != 28 || cfg.Indexer.Include[0] != "*.go" {
		t.Errorf("include = %v", cfg.Indexer.Include)
	}
	if cfg.Indexer.TypedResolve != "off" {
		t.Errorf("typed_resolve = %q", cfg.Indexer.TypedResolve)
	}
	if !cfg.MCP.Enabled || cfg.MCP.Port != 3000 || cfg.MCP.RequireGitForAutoIndex == false {
		t.Errorf("mcp = %+v", cfg.MCP)
	}
	if cfg.Documentation.Output != "./docs" {
		t.Errorf("documentation = %+v", cfg.Documentation)
	}
	if cfg.Microservice != nil || cfg.DB != nil || cfg.LSP != nil || cfg.Source != nil {
		t.Errorf("optional blocks = %+v", cfg)
	}
	// An explicit empty steer block stays empty but present.
	if len(cfg.Project.Steer.PriorityPaths) != 0 || len(cfg.Project.Steer.Languages.Items) != 0 {
		t.Errorf("steer = %+v", cfg.Project.Steer)
	}
}

// A partial config keeps the built-in defaults for every absent top-level
// block (Rust: container-level #[serde(default)]).
func TestParsePartialFixtureKeepsDefaults(t *testing.T) {
	cfg := parseFixture(t, "partial.yaml")
	def := DefaultProjectConfig()
	if cfg.Project.Name != "demo" || cfg.Project.Root != "./src" {
		t.Errorf("project = %+v", cfg.Project)
	}
	if !reflect.DeepEqual(cfg.Project.Languages, []string{"go"}) {
		t.Errorf("languages = %v", cfg.Project.Languages)
	}
	if !reflect.DeepEqual(cfg.Indexer.Exclude, def.Indexer.Exclude) ||
		!reflect.DeepEqual(cfg.Indexer.Include, def.Indexer.Include) ||
		cfg.Indexer.TypedResolve != def.Indexer.TypedResolve {
		t.Errorf("absent indexer keys must keep defaults: %+v", cfg.Indexer)
	}
	if cfg.MCP != def.MCP {
		t.Errorf("absent mcp block must keep defaults: %+v", cfg.MCP)
	}
	if cfg.Documentation.Output != def.Documentation.Output {
		t.Errorf("absent documentation block must keep defaults: %+v", cfg.Documentation)
	}
	if cfg.Auth.Enabled || cfg.Auth.Provider != def.Auth.Provider || len(cfg.Auth.Tokens) != 0 {
		t.Errorf("absent auth block must keep defaults: %+v", cfg.Auth)
	}
	if cfg.DB == nil {
		t.Fatal("db block must parse")
	}
	if cfg.DB.URL != "postgresql://u:p@host:9999/bar" {
		t.Errorf("db.url = %q", cfg.DB.URL)
	}
	if cfg.DB.PoolSize == nil || *cfg.DB.PoolSize != 12 {
		t.Errorf("db.pool_size = %v", cfg.DB.PoolSize)
	}
	if cfg.DB.Lock == nil || *cfg.DB.Lock {
		t.Errorf("db.lock = %v", cfg.DB.Lock)
	}
}

func TestEmptyDBBlockIsValid(t *testing.T) {
	cfg, err := Parse([]byte("db: {}\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.DB == nil || cfg.DB.URL != "" || cfg.DB.PoolSize != nil || cfg.DB.Lock != nil {
		t.Errorf("db = %+v, want empty block", cfg.DB)
	}
}

// Absent and malformed configs: strict readers error, the unwrap_or_default
// readers fall back to the built-in defaults.
func TestMalformedConfigPostures(t *testing.T) {
	bad := readFixture(t, "malformed.yaml")
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("Parse must reject a malformed document")
	}
	dir := t.TempDir()
	write(t, ConfigPath(dir), bad)
	if _, err := Load(dir); err == nil {
		t.Fatal("Load must report a malformed document")
	}
	if got := LoadOrDefault(dir); !reflect.DeepEqual(got, DefaultProjectConfig()) {
		t.Errorf("LoadOrDefault must fall back to defaults, got %+v", got)
	}
	if got := LoadOrDefault(filepath.Join(dir, "absent")); !reflect.DeepEqual(got, DefaultProjectConfig()) {
		t.Errorf("LoadOrDefault on a missing file must fall back to defaults, got %+v", got)
	}
	if err := EnsureIdentityFields(ConfigPath(dir), "/x"); err != nil {
		t.Errorf("EnsureIdentityFields on malformed config: %v", err)
	}
	if got := DBConfigFromDir(dir); got != nil {
		t.Errorf("DBConfigFromDir on malformed config = %+v, want nil", got)
	}
}

// The first leankg.yaml found is authoritative: a malformed one ends the walk
// instead of falling through to a valid ancestor.
func TestDBConfigFromDirStopsAtFirstConfig(t *testing.T) {
	root := t.TempDir()
	write(t, ConfigPath(root), "db:\n  url: postgresql://root\n")
	child := filepath.Join(root, "child")
	write(t, ConfigPath(child), ":::: not yaml :::")
	if got := DBConfigFromDir(child); got != nil {
		t.Errorf("malformed nearest config must end the walk, got %+v", got)
	}
	if got := DBConfigFromDir(root); got == nil || got.URL != "postgresql://root" {
		t.Errorf("valid config = %+v", got)
	}
}

func TestProjectPathSerializesWhenSet(t *testing.T) {
	cfg := DefaultProjectConfig()
	cfg.Project.ProjectPath = "/host/demo"
	out, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "project_path: /host/demo") {
		t.Errorf("project_path must be written back to yaml:\n%s", out)
	}
}

// ----------------------------------------------------------------------
// N1 (cycle-2 R2a): writers must preserve user fields.
// ----------------------------------------------------------------------

func TestMergePreservesExistingAndFillsMissing(t *testing.T) {
	existing := readFixture(t, "unknown_keys.yaml")
	merged := MergePreservingExisting(existing, DefaultProjectConfig())

	// Existing values win.
	for _, want := range []string{"user-name", "./custom-src", "/host/keep", "**/secret/**"} {
		if !strings.Contains(merged, want) {
			t.Errorf("merged drops existing value %q:\n%s", want, merged)
		}
	}
	// Unmodelled keys survive (a serde round-trip would have dropped them).
	for _, want := range []string{"vendor_probe", "user-custom-value", "custom_indexer_key", "team_identity_probe"} {
		if !strings.Contains(merged, want) {
			t.Errorf("merged drops unmodelled key %q:\n%s", want, merged)
		}
	}
	// Missing keys are filled from the fresh defaults.
	for _, want := range []string{"auto_index_on_start", "typed_resolve", "documentation", "auth_token"} {
		if !strings.Contains(merged, want) {
			t.Errorf("merged does not fill %q:\n%s", want, merged)
		}
	}
	// And the result still parses as a ProjectConfig.
	cfg, err := Parse([]byte(merged))
	if err != nil {
		t.Fatalf("merged output must parse: %v\n%s", err, merged)
	}
	if cfg.Project.Name != "user-name" || cfg.Project.Root != "./custom-src" {
		t.Errorf("parsed merged config = %+v", cfg.Project)
	}
	if cfg.Project.ProjectPath != "/host/keep" {
		t.Errorf("project_path = %q, want /host/keep", cfg.Project.ProjectPath)
	}
	if cfg.Indexer.TypedResolve != "off" {
		t.Errorf("filled typed_resolve = %q", cfg.Indexer.TypedResolve)
	}
	if cfg.MCP.RequireGitForAutoIndex {
		t.Error("existing mcp.require_git_for_auto_index=false must win over the default")
	}
}

func TestMergeIsIdempotentAndNonDestructive(t *testing.T) {
	existing := readFixture(t, "unknown_keys.yaml")
	fresh := DefaultProjectConfig()
	once := MergePreservingExisting(existing, fresh)
	twice := MergePreservingExisting(once, fresh)
	if once != twice {
		t.Errorf("second fill must be a no-op\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
	// A fill over an already-complete document adds nothing.
	before := keyPaths(t, docOf(t, once), "")
	filled := docOf(t, once)
	FillMissingKeys(filled, docOf(t, string(mustMarshal(t, fresh))))
	after := keyPaths(t, docOf(t, mustText(t, filled)), "")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("re-fill changed the document\nbefore: %v\nafter:  %v", before, after)
	}
	for _, want := range []string{"team_identity_probe", "user-custom-value"} {
		if !strings.Contains(twice, want) {
			t.Errorf("second fill dropped %q:\n%s", want, twice)
		}
	}
}

func TestMergeFallsBackToFreshOnUnparseable(t *testing.T) {
	merged := MergePreservingExisting(readFixture(t, "malformed.yaml"), DefaultProjectConfig())
	cfg, err := Parse([]byte(merged))
	if err != nil {
		t.Fatalf("fallback output must be valid yaml: %v\n%s", err, merged)
	}
	if cfg.Project.Name != "my-project" {
		t.Errorf("fallback name = %q, want my-project", cfg.Project.Name)
	}
	if empty := MergePreservingExisting("", DefaultProjectConfig()); !strings.Contains(empty, "my-project") {
		t.Errorf("empty document must fall back to fresh:\n%s", empty)
	}
	if scalar := MergePreservingExisting("42\n", DefaultProjectConfig()); scalar != "42\n" {
		t.Errorf("a scalar document is preserved verbatim, got %q", scalar)
	}
}

func TestWriteConfigPreservingExistingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".leankg", "leankg.yaml")
	write(t, path, "project:\n  name: mine\n  project_path: ./src\n  keep_me: yes\n")

	fresh := DefaultProjectConfig()
	fresh.Project.ProjectPath = "/elsewhere"
	if err := WriteConfigPreservingExisting(path, fresh); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := readFile(t, path)
	for _, want := range []string{"mine", "./src", "keep_me"} {
		if !strings.Contains(out, want) {
			t.Errorf("write dropped %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/elsewhere") {
		t.Errorf("fresh must not override an existing anchor:\n%s", out)
	}

	// A missing file is created with the fresh content, parent dirs included.
	other := filepath.Join(dir, "other", "leankg.yaml")
	if err := WriteConfigPreservingExisting(other, fresh); err != nil {
		t.Fatalf("write missing: %v", err)
	}
	if out := readFile(t, other); !strings.Contains(out, "project_path: /elsewhere") {
		t.Errorf("created config lacks the fresh anchor:\n%s", out)
	}
}

func TestEnsureIdentityFieldsRefillsMissingAnchor(t *testing.T) {
	dir := t.TempDir()
	hint := filepath.Join(dir, "src")
	if err := os.MkdirAll(hint, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, ".leankg", "leankg.yaml")
	write(t, cfg, "project:\n  name: ident-fixture-a\n  root: ./src\n  languages:\n    - rust\nteam_identity_probe: keep-me-through-reindex\n")

	if err := EnsureIdentityFields(cfg, hint); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	out := readFile(t, cfg)
	if !strings.Contains(out, "project_path: "+hint) {
		t.Errorf("anchor not rebuilt from the hint:\n%s", out)
	}
	for _, want := range []string{"keep-me-through-reindex", "ident-fixture-a"} {
		if !strings.Contains(out, want) {
			t.Errorf("heal dropped %q:\n%s", want, out)
		}
	}
}

func TestEnsureIdentityFieldsLeavesExistingAnchorUntouched(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".leankg", "leankg.yaml")
	original := "project:\n  name: p\n  root: .\n  project_path: /elsewhere/anchor\n  custom_probe: 1\n"
	write(t, cfg, original)
	if err := EnsureIdentityFields(cfg, "/somewhere/else"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if out := readFile(t, cfg); out != original {
		t.Errorf("present anchor must not be rewritten:\n%s", out)
	}
}

func TestEnsureIdentityFieldsSkipsMissingFile(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".leankg", "leankg.yaml")
	if err := EnsureIdentityFields(cfg, "/x"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Error("must not create configs on its own")
	}
}

func TestEnsureIdentityFieldsForDBCoversRootAndParentLevels(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "src", ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The repo-root config lost its anchor (the corruption under test).
	write(t, filepath.Join(repo, ".leankg", "leankg.yaml"), "project:\n  name: r\n  root: ./src\nprobe: v\n")

	// `leankg index ./src` anchors the db inside src/; the CLI passes the
	// canonicalized target as the heal hint.
	hint, err := filepath.EvalSymlinks(filepath.Join(repo, "src"))
	if err != nil {
		hint = filepath.Join(repo, "src")
	}
	EnsureIdentityFieldsForDB(filepath.Join(repo, "src", ".leankg"), hint)

	healed := readFile(t, filepath.Join(repo, ".leankg", "leankg.yaml"))
	if !strings.Contains(healed, "project_path: "+hint) {
		t.Errorf("grandparent-level config not healed via one-level walk:\n%s", healed)
	}
	if !strings.Contains(healed, "probe: v") {
		t.Errorf("heal dropped an unmodelled key:\n%s", healed)
	}
}

func TestEnsureIdentityFieldsForDBIsNoopWithoutConfigs(t *testing.T) {
	dir := t.TempDir()
	EnsureIdentityFieldsForDB(filepath.Join(dir, "whatever", ".leankg"), "/x")
	if _, err := os.Stat(filepath.Join(dir, "whatever")); !os.IsNotExist(err) {
		t.Error("must not create directories on its own")
	}
}

// ----------------------------------------------------------------------
// project-path resolution
// ----------------------------------------------------------------------

func TestResolveProjectDBDir(t *testing.T) {
	// 1. A live project_path anchor wins over the local dir.
	repo := t.TempDir()
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(repo, ".leankg")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, ConfigPath(local), "project:\n  name: r\n  root: .\n  project_path: "+other+"\n")
	if got := ResolveProjectDBDir(local); got != filepath.Join(other, ".leankg") {
		t.Errorf("project_path anchor: got %q, want %q", got, filepath.Join(other, ".leankg"))
	}

	// 2. A dangling anchor falls through to a non-"." root that owns .leankg.
	write(t, ConfigPath(local), "project:\n  name: r\n  root: ./src\n  project_path: /nope/missing\n")
	srcDB := filepath.Join(repo, "src", ".leankg")
	if err := os.MkdirAll(srcDB, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ResolveProjectDBDir(local); got != srcDB {
		t.Errorf("project.root: got %q, want %q", got, srcDB)
	}

	// 3. No config, "." root, and a malformed config all leave dbDir alone.
	plain := filepath.Join(t.TempDir(), ".leankg")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ResolveProjectDBDir(plain); got != plain {
		t.Errorf("no config: got %q, want %q", got, plain)
	}
	write(t, ConfigPath(local), "project:\n  name: r\n  root: .\n")
	if got := ResolveProjectDBDir(local); got != local {
		t.Errorf("dot root: got %q, want %q", got, local)
	}
	write(t, ConfigPath(local), ":::: not yaml :::")
	if got := ResolveProjectDBDir(local); got != local {
		t.Errorf("malformed config: got %q, want %q", got, local)
	}
}

func TestFindProjectRootWalksUp(t *testing.T) {
	root := t.TempDir()
	write(t, ConfigPath(root), "project:\n  name: r\n")
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectRoot(deep); !sameFile(t, got, root) {
		t.Errorf("leankg.yaml walk-up: got %q, want %q", got, root)
	}
	// A nearer .leankg marker wins over the further config file.
	near := filepath.Join(root, "a")
	if err := os.MkdirAll(filepath.Join(near, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectRoot(deep); !sameFile(t, got, near) {
		t.Errorf("nearest marker: got %q, want %q", got, near)
	}
	// No marker anywhere: the starting directory is returned unchanged.
	orphan := t.TempDir()
	if got := FindProjectRoot(orphan); !sameFile(t, got, orphan) {
		t.Errorf("no marker: got %q, want %q", got, orphan)
	}
}

func TestDBConfigFromDirWalksUp(t *testing.T) {
	root := t.TempDir()
	write(t, ConfigPath(root), "project:\n  name: r\ndb:\n  url: postgresql://up\n")
	deep := filepath.Join(root, "x", "y")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got := DBConfigFromDir(deep)
	if got == nil || got.URL != "postgresql://up" {
		t.Errorf("walk-up db block = %+v", got)
	}
	// A config without a db block yields nil, not the ancestor's block, and a
	// tree with no config at all yields nil too.
	write(t, ConfigPath(filepath.Join(root, "x")), "project:\n  name: child\n")
	if got := DBConfigFromDir(deep); got != nil {
		t.Errorf("nearest config has no db block: got %+v, want nil", got)
	}
	if got := DBConfigFromDir(t.TempDir()); got != nil {
		t.Errorf("no config: got %+v, want nil", got)
	}
}

func TestResolveProjectRootCanonicalizes(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(root, ".leankg")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, ConfigPath(local), "project:\n  name: r\n  project_path: "+other+"\n")

	want, err := filepath.EvalSymlinks(filepath.Join(other, ".leankg"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ResolveProjectRoot(local); got != want {
		t.Errorf("ResolveProjectRoot: got %q, want %q", got, want)
	}
}

func TestAuthFromDirWalksUpPastUnparsableConfigs(t *testing.T) {
	root := t.TempDir()
	write(t, ConfigPath(root), "auth:\n  enabled: true\n  tokens:\n    - token: t1\n      role: admin\n      client_id: c1\n")
	deep := filepath.Join(root, "a", ".leankg")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// The nearest config is unparsable: the auth walk continues to the
	// ancestor instead of ending there.
	write(t, ConfigPath(deep), ":::: not yaml :::")
	got := AuthFromDir(deep)
	if !got.Enabled || len(got.Tokens) != 1 || got.Tokens[0].Token != "t1" || got.Tokens[0].Role != "admin" || got.Tokens[0].ClientID != "c1" {
		t.Errorf("auth walk-up = %+v, want the ancestor tokens", got)
	}
	// A tree with no config at all yields disabled static auth.
	if got := AuthFromDir(t.TempDir()); got.Enabled || got.Provider != AuthProviderStatic {
		t.Errorf("no config: got %+v, want disabled static", got)
	}
}

// ----------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustMarshal(t *testing.T, cfg ProjectConfig) []byte {
	t.Helper()
	out, err := Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustText(t *testing.T, doc *yaml.Node) string {
	t.Helper()
	out, err := marshalNode(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = b
	}
	return ra == rb
}
