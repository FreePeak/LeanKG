package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeServerDir drops the fake LSP server on PATH (prepended so the shell
// script keeps finding coreutils) and sets its mode.
func fakeServerDir(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fake-lsp"), []byte(fakeLSP), 0o755); err != nil {
		t.Fatalf("write fake server: %v", err)
	}
	t.Setenv("FAKE_LSP_MODE", mode)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestParseLSPBlockBlockStyle(t *testing.T) {
	cfg, ok := parseLSPBlock([]byte(`
lsp:
  servers:
    go:
      command: gopls
      args: ["serve"]
      extensions: ["go"]
    typescript:
      command: typescript-language-server
      args:
        - --stdio
  workspace_root: /tmp/ws
  timeout_ms: 1234
`))
	if !ok {
		t.Fatal("parseLSPBlock: block not found")
	}
	if cfg.TimeoutMS != 1234 {
		t.Errorf("timeout_ms = %d, want 1234", cfg.TimeoutMS)
	}
	if cfg.WorkspaceRoot != "/tmp/ws" {
		t.Errorf("workspace_root = %q", cfg.WorkspaceRoot)
	}
	goSrv, ok := cfg.Servers["go"]
	if !ok {
		t.Fatalf("go server missing from %v", cfg.Servers)
	}
	if goSrv.Command != "gopls" || len(goSrv.Args) != 1 || goSrv.Args[0] != "serve" {
		t.Errorf("go server = %+v", goSrv)
	}
	if len(goSrv.Extensions) != 1 || goSrv.Extensions[0] != "go" {
		t.Errorf("go extensions = %v", goSrv.Extensions)
	}
	tsSrv, ok := cfg.Servers["typescript"]
	if !ok {
		t.Fatal("typescript server missing")
	}
	if len(tsSrv.Args) != 1 || tsSrv.Args[0] != "--stdio" {
		t.Errorf("block-sequence args = %v", tsSrv.Args)
	}
}

func TestParseLSPBlockFlowStyle(t *testing.T) {
	cfg, ok := parseLSPBlock([]byte(`
# project config
lsp:
  servers:
    go: { command: "gopls", args: ["serve"] }   # inline
    python: { command: pylsp }
`))
	if !ok {
		t.Fatal("parseLSPBlock: flow block not found")
	}
	if cfg.Servers["go"].Command != "gopls" {
		t.Errorf("go = %+v", cfg.Servers["go"])
	}
	if got := cfg.Servers["go"].Args; len(got) != 1 || got[0] != "serve" {
		t.Errorf("go args = %v", got)
	}
	if cfg.Servers["python"].Command != "pylsp" {
		t.Errorf("python = %+v", cfg.Servers["python"])
	}
}

func TestParseLSPBlockOtherBlocksIgnored(t *testing.T) {
	cfg, ok := parseLSPBlock([]byte(`
indexer:
  typed_resolve: go,ts
  include: [src]
lsp:
  servers:
    go:
      command: gopls
`))
	if !ok {
		t.Fatal("lsp block not found alongside other blocks")
	}
	if len(cfg.Servers) != 1 || cfg.Servers["go"].Command != "gopls" {
		t.Errorf("servers = %v", cfg.Servers)
	}
}

func TestParseLSPBlockMissingOrMalformed(t *testing.T) {
	if _, ok := parseLSPBlock([]byte("indexer:\n  typed_resolve: all\n")); ok {
		t.Error("ok = true without an lsp block")
	}
	// Rust parity: an unparsable block degrades to prefab defaults.
	if _, ok := parseLSPBlock([]byte("\tlsp: [unclosed\n")); ok {
		t.Error("ok = true for malformed yaml")
	}
	if _, ok := parseLSPBlock(nil); ok {
		t.Error("ok = true for empty input")
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	if _, ok := LoadConfig(t.TempDir()); ok {
		t.Error("LoadConfig on empty dir = ok, want false")
	}
}

func TestLoadConfigFromDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leankg.yaml", "lsp:\n  servers:\n    go:\n      command: fake-lsp\n")
	cfg, ok := LoadConfig(dir)
	if !ok {
		t.Fatal("LoadConfig = ok false")
	}
	if cfg.Servers["go"].Command != "fake-lsp" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestWithPrefabFallbackFillsAndKeepsUserEntries(t *testing.T) {
	cfg := Config{Servers: map[string]ServerConfig{
		"go": {Command: "my-gopls", Args: []string{"-remote=auto"}},
	}}
	filled, user := cfg.WithPrefabFallback()
	if filled.Servers["go"].Command != "my-gopls" {
		t.Errorf("user entry overwritten: %+v", filled.Servers["go"])
	}
	if !user["go"] {
		t.Error("user set missing go")
	}
	if user["python"] {
		t.Error("python wrongly marked as user-configured")
	}
	if filled.Servers["python"].Command == "" {
		t.Error("python not filled from the catalog")
	}
	if filled.TimeoutMS != defaultTimeoutMS {
		t.Errorf("timeout = %d, want %d", filled.TimeoutMS, defaultTimeoutMS)
	}
}

func TestServerForTiers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leankg.yaml", "lsp:\n  servers:\n    go:\n      command: fake-lsp\n")
	b := FromLeanKGYAMLOrDefault(dir)
	defer b.Shutdown()

	sc, tier, ok := b.ServerFor("go")
	if !ok || tier != TierConfigured || sc.Command != "fake-lsp" {
		t.Errorf("go = %+v tier=%q ok=%v, want configured fake-lsp", sc, tier, ok)
	}
	if sc, tier, ok = b.ServerFor("typescript"); !ok || tier != TierCatalog {
		t.Errorf("typescript = %+v tier=%q ok=%v, want catalog", sc, tier, ok)
	}
	// Go engine tags resolve to the same catalog entry.
	if _, _, ok := b.ServerFor("ts"); !ok {
		t.Error(`ServerFor("ts") not ok`)
	}
	if _, _, ok := b.ServerFor("nonsense-language"); ok {
		t.Error("ServerFor(nonsense) = ok, want false")
	}
}

func TestCatalogLookups(t *testing.T) {
	if spec, ok := ForLanguage("golang"); !ok || spec.Language != "go" {
		t.Errorf("ForLanguage(golang) = %+v ok=%v", spec, ok)
	}
	if spec, ok := ForLanguage("TS"); !ok || spec.Language != "typescript" {
		t.Errorf("ForLanguage(TS) = %+v ok=%v", spec, ok)
	}
	if _, ok := ForLanguage("brainfuck"); ok {
		t.Error("ForLanguage(brainfuck) = ok")
	}
	for path, want := range map[string]string{
		"src/app.ts":     "typescript",
		"a/b/main.go":    "go",
		"pkg/mod.rs":     "rust",
		"x/Widget.swift": "swift",
		"README.md":      "markdown",
	} {
		got, ok := DetectLanguage(path)
		if !ok || got != want {
			t.Errorf("DetectLanguage(%s) = %q,%v want %q", path, got, ok, want)
		}
	}
	if _, ok := DetectLanguage("Makefile"); ok {
		t.Error("DetectLanguage(Makefile) = ok")
	}
}

func TestAutoConfigFiltersByPATH(t *testing.T) {
	// PATH contains nothing at all: no catalog binary resolves.
	t.Setenv("PATH", t.TempDir())
	cfg, missing := AutoConfig(false)
	if len(cfg.Servers) != 0 {
		t.Errorf("AutoConfig(false) included %d servers with no catalog binary on PATH", len(cfg.Servers))
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none when includeMissing=false", missing)
	}
	cfg, missing = AutoConfig(true)
	if len(cfg.Servers) != len(catalog) {
		t.Errorf("AutoConfig(true) servers = %d, want %d", len(cfg.Servers), len(catalog))
	}
	if len(missing) != len(catalog) {
		t.Errorf("missing = %d, want %d", len(missing), len(catalog))
	}
	if cfg.Servers["go"].Command != "gopls" {
		t.Errorf("go = %+v", cfg.Servers["go"])
	}
}
