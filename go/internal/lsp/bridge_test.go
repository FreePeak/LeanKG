package lsp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/langs"
)

func TestFindWorkspaceRootFindsManifests(t *testing.T) {
	cases := map[string]string{
		"go":         "go.mod",
		"typescript": "package.json",
		"rust":       "Cargo.toml",
		"python":     "pyproject.toml",
		"java":       "pom.xml",
		"kotlin":     "build.gradle.kts",
		"ruby":       "Gemfile",
		"elixir":     "mix.exs",
		"dart":       "pubspec.yaml",
		"swift":      "Package.swift",
		"csharp":     "Project.toml",
	}
	for lang, manifest := range cases {
		root := t.TempDir()
		writeFile(t, root, manifest, "")
		nested := filepath.Join(root, "service-a", "src")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(nested, "main.ext")
		writeFile(t, nested, "main.ext", "")
		if got := FindWorkspaceRoot(file); got != root {
			t.Errorf("[%s] FindWorkspaceRoot = %q, want %q", lang, got, root)
		}
	}
}

func TestFindWorkspaceRootNearestMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := filepath.Join(root, "svc-a")
	if err := os.MkdirAll(filepath.Join(svc, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(svc, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(svc, "src", "main.go")
	writeFile(t, filepath.Join(svc, "src"), "main.go", "")
	if got := FindWorkspaceRoot(file); got != svc {
		t.Errorf("nearest marker: got %q, want %q", got, svc)
	}

	// Without a service-local marker the walk stops at the outer .git.
	other := filepath.Join(root, "svc-b", "src")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, other, "main.go", "")
	if got := FindWorkspaceRoot(filepath.Join(other, "main.go")); got != root {
		t.Errorf("outer marker: got %q, want %q", got, root)
	}
}

func TestFindWorkspaceRootFallsBackToAbsolutePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "loose.txt")
	writeFile(t, dir, "loose.txt", "")
	got := FindWorkspaceRoot(file)
	if got == "" || !filepath.IsAbs(got) {
		t.Errorf("fallback root = %q, want an absolute path", got)
	}
}

func TestWorkspaceForHonoursExplicitRoot(t *testing.T) {
	explicit := t.TempDir()
	b := NewBridge(Config{WorkspaceRoot: explicit})
	defer b.Shutdown()
	if got := b.WorkspaceFor("/somewhere/else/file.go"); got != explicit {
		t.Errorf("WorkspaceFor = %q, want %q", got, explicit)
	}
}

func TestBridgeResolveWithoutServerIsGraceful(t *testing.T) {
	fakeServerDir(t, "rich")
	dir := t.TempDir()
	writeFile(t, dir, "leankg.yaml", "lsp:\n  servers:\n    go:\n      command: not-a-real-lsp-binary\n")
	b := FromLeanKGYAMLOrDefault(dir)
	defer b.Shutdown()

	res, found, err := b.Resolve(context.Background(), "go", filepath.Join(dir, "a.go"), 0, 0, ReqDefinition)
	if err != nil {
		t.Fatalf("Resolve err = %v, want nil (graceful absence)", err)
	}
	if found {
		t.Error("found = true for a server that is not on PATH")
	}
	if len(res.Locations) != 0 {
		t.Errorf("locations = %v", res.Locations)
	}
}

func TestBridgeResolveDefinitionAndHover(t *testing.T) {
	fakeServerDir(t, "rich")
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module scratch\n")
	file := writeFile(t, root, "a.go", "package scratch\n")

	cfg := Config{Servers: map[string]ServerConfig{
		"go": {Command: "fake-lsp", Args: []string{"--mode=fake"}},
	}}
	b := NewBridge(cfg)
	defer b.Shutdown()

	res, found, err := b.Resolve(context.Background(), "go", file, 0, 0, ReqDefinition)
	if err != nil || !found {
		t.Fatalf("definition: found=%v err=%v", found, err)
	}
	if len(res.Locations) != 0 {
		t.Errorf("null definition decoded to %v, want none", res.Locations)
	}

	res, found, err = b.Resolve(context.Background(), "go", file, 0, 0, ReqHover)
	if err != nil || !found {
		t.Fatalf("hover: found=%v err=%v", found, err)
	}
	if res.Hover != "server hover documentation" {
		t.Errorf("hover = %q", res.Hover)
	}
}

func TestBridgePoolsPerWorkspaceRoot(t *testing.T) {
	fakeServerDir(t, "rich")
	svcA := t.TempDir()
	svcB := t.TempDir()
	writeFile(t, svcA, "go.mod", "module a\n")
	writeFile(t, svcB, "go.mod", "module b\n")
	fileA := writeFile(t, svcA, "a.go", "package a\n")
	fileB := writeFile(t, svcB, "b.go", "package b\n")

	b := NewBridge(Config{Servers: map[string]ServerConfig{"go": {Command: "fake-lsp"}}})
	defer b.Shutdown()
	for _, f := range []string{fileA, fileB} {
		if _, found, err := b.Resolve(context.Background(), "go", f, 0, 0, ReqHover); err != nil || !found {
			t.Fatalf("resolve %s: found=%v err=%v", f, found, err)
		}
	}
	if n := b.manager.Len(); n != 2 {
		t.Errorf("pooled clients = %d, want 2 (one per workspace root)", n)
	}
}

// TestBridgeCoversRegistryLSPSpecs pins cross-registry consistency: every
// language the engine registry equips with an LSP server must resolve
// through the bridge (internal/langs and the LSP catalog are owned by
// different waves).
func TestBridgeCoversRegistryLSPSpecs(t *testing.T) {
	b := NewBridge(Config{})
	defer b.Shutdown()
	for _, p := range langs.Default {
		if p.LSP == nil || len(p.LSP.Commands) == 0 {
			continue
		}
		sc, _, ok := b.ServerFor(string(p.Language))
		if !ok {
			t.Errorf("registry language %q declares LSP %v but the bridge has no server",
				p.Language, p.LSP.Commands)
			continue
		}
		if sc.Command == "" {
			t.Errorf("registry language %q resolved to an empty command", p.Language)
			continue
		}
		// The bridge catalog must agree with the registry's candidate list
		// (drift between the two registries is otherwise invisible).
		if !slices.Contains(p.LSP.Commands, sc.Command) {
			t.Errorf("registry language %q: bridge command %q not among registry commands %v",
				p.Language, sc.Command, p.LSP.Commands)
		}
	}
}

// TestServerForExpandedLanguageTags covers the language-expansion tags: the
// catalog rows ported from the Rust registry, plus graceful absence for the
// languages the Rust catalog never had a server for.
func TestServerForExpandedLanguageTags(t *testing.T) {
	b := NewBridge(Config{})
	defer b.Shutdown()

	for tag, want := range map[string]string{
		"c":          "clangd",
		"cpp":        "clangd",
		"php":        "intelephense",
		"ruby":       "solargraph",
		"scala":      "metals",
		"lua":        "lua-language-server",
		"haskell":    "haskell-language-server-wrapper",
		"elixir":     "elixir-ls",
		"crystal":    "crystalline",
		"elm":        "elm-language-server",
		"erlang":     "erlang_ls",
		"fsharp":     "fsautocomplete",
		"nim":        "nimlangserver",
		"ocaml":      "ocamllsp",
		"sql":        "sqls",
		"powershell": "powershell-es",
		"solidity":   "solidity-ls",
		"zig":        "zls",
	} {
		sc, _, ok := b.ServerFor(tag)
		if !ok || sc.Command != want {
			t.Errorf("ServerFor(%q) = %+v ok=%v, want command %q", tag, sc, ok, want)
		}
	}

	// No Rust catalog row: the bridge reports absence instead of guessing.
	for _, tag := range []string{"csharp", "perl", "cuda", "glsl", "hlsl", "verilog", "systemverilog", "cypher", "qsharp"} {
		if sc, _, ok := b.ServerFor(tag); ok {
			t.Errorf("ServerFor(%q) = %+v, want no server (no catalog row)", tag, sc)
		}
	}
}

// TestTagToCatalogID tables the engine-tag → catalog-id mapping, including
// the tags that diverge from their catalog id and the expanded language sets.
func TestTagToCatalogID(t *testing.T) {
	for tag, want := range map[string]string{
		// Divergent ids.
		"ts": "typescript", "tsx": "typescript",
		"js": "javascript", "jsx": "javascript",
		"py": "python", "md": "markdown", "rs": "rust",
		// Original set.
		"go": "go", "java": "java", "kotlin": "kotlin",
		"swift": "swift", "objc": "objc", "dart": "dart",
		// Wave-1 expansion.
		"c": "c", "cpp": "cpp", "php": "php", "ruby": "ruby", "scala": "scala",
		"lua": "lua", "haskell": "haskell", "elixir": "elixir",
		// Wave-2 expansion.
		"crystal": "crystal", "elm": "elm", "erlang": "erlang", "fsharp": "fsharp",
		"nim": "nim", "ocaml": "ocaml", "sql": "sql", "powershell": "powershell",
		"solidity": "solidity", "zig": "zig",
		// No Rust LSP row: mapped so absence is explicit, not "unknown tag".
		"csharp": "csharp", "perl": "perl", "cuda": "cuda", "cypher": "cypher",
		"glsl": "glsl", "hlsl": "hlsl", "qsharp": "qsharp",
		"systemverilog": "systemverilog", "verilog": "verilog",
		// Case/whitespace tolerance.
		" TS ": "typescript", "Go": "go",
	} {
		got, ok := tagToCatalogID(tag)
		if !ok || got != want {
			t.Errorf("tagToCatalogID(%q) = %q,%v want %q", tag, got, ok, want)
		}
	}
	if got, ok := tagToCatalogID("brainfuck"); ok {
		t.Errorf("tagToCatalogID(brainfuck) = %q, want not ok", got)
	}
}
