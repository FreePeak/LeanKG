package langs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fixture builds a tree with the given marker files at the given relative
// repo-root directories, plus source files for extension census.
func fixture(t *testing.T, markers map[string][]string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for root, mks := range markers {
		for _, mk := range mks {
			p := filepath.Join(dir, filepath.FromSlash(root), mk)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestActivateNestedRepos(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t,
		map[string][]string{
			".":                       {"go.mod"},
			"services/mobile-app":     {"pubspec.yaml"},
			"services/ios-app":        {"Package.swift"},
			"services/android-client": {"build.gradle.kts"},
			"vendor-lib":              {"Cargo.toml"},
		},
		map[string]string{
			"main.go":                                         "package main",
			"services/mobile-app/lib/main.dart":               "void main() {}",
			"services/ios-app/Sources/App.swift":              "let x = 1",
			"services/ios-app/Sources/bridge.h":               "",
			"services/android-client/app/src/MainActivity.kt": "class A",
			"services/android-client/app/src/Util.java":       "class U",
			"services/ts-frontend/src/index.ts":               "let a = 1",
			"vendor-lib/src/lib.rs":                           "fn a() {}",
		})

	roots, err := r.Activate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 5 {
		t.Fatalf("roots = %d, want 5: %v", len(roots), roots)
	}
	active := r.Active()
	// Markers activate go/rust/java/kotlin/swift/dart. The census ALWAYS
	// supplements markers (a polyglot tree must not silence stray sources),
	// so the sources below the marker roots that sit inside the census bound
	// add ts (services/ts-frontend/src/index.ts) and the header-capable
	// c/cpp/objc for services/ios-app/Sources/bridge.h. Order follows the
	// registry's activation priority.
	want := []Language{Go, Rust, TypeScript, Java, Kotlin, Swift, ObjC, Dart, C, Cpp}
	if !reflect.DeepEqual(active, want) {
		t.Fatalf("active = %v, want %v", active, want)
	}
	// lazy: languages with no marker and no census hit stay off
	if r.IsActive(Markdown) || r.IsActive(Python) {
		t.Fatal("undetected languages must stay idle")
	}
	// extension ownership honors activation: .dart routes to Dart only when active
	if l, ok := r.ExtOwner(".dart"); !ok || l != Dart {
		t.Fatalf(".dart owner: %v %v", l, ok)
	}
	// header ownership: C lists .h in Exts, so the active C beats the
	// Swift/ObjC HeaderExts claim (priority order + Exts-first lookup).
	if l, ok := r.ExtOwner(".h"); !ok || l != C {
		t.Fatalf(".h owner: %v %v, want c", l, ok)
	}
}

func TestActivateMarkerlessCensus(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t, nil, map[string]string{
		"app/main.py":  "def a(): pass",
		"app/util.py":  "def b(): pass",
		"web/index.js": "let x = 1",
	})
	if _, err := r.Activate(dir); err != nil {
		t.Fatal(err)
	}
	if !r.IsActive(Python) || !r.IsActive(JavaScript) {
		t.Fatalf("census must activate py+js, got %v", r.Active())
	}
	if r.IsActive(Go) {
		t.Fatal("go must stay idle in a py/js tree")
	}
}

// TestActivateMarkerlessCensusDeep pins the census depth: a marker-less tree
// whose sources sit several directories down must still activate its
// languages. The census used to count only the top level plus one level of
// nesting, so such trees activated nothing and the indexer skipped every
// file without a counter.
func TestActivateMarkerlessCensusDeep(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t, nil, map[string]string{
		"src/a/a.go":           "package a\n",
		"src/b/c/d/d.py":       "def d(): pass",
		"src/b/c/d/e/deep.zig": "pub fn f() void {}",
	})
	if _, err := r.Activate(dir); err != nil {
		t.Fatal(err)
	}
	if !r.IsActive(Go) {
		t.Fatalf("go three directories down must activate via census, got %v", r.Active())
	}
	if !r.IsActive(Python) {
		t.Fatalf("py in a directory at the census bound must activate, got %v", r.Active())
	}
	if l, ok := r.ExtOwner(".go"); !ok || l != Go {
		t.Fatalf("ExtOwner(.go) = %v ok=%v, want go", l, ok)
	}
	// One directory deeper than the bound stays unseen: a repo marker is the
	// exact signal for those trees (ceiling documented on censusExts).
	if _, ok := r.ExtOwner(".zig"); ok {
		t.Fatalf("zig past the census bound must stay unowned, got %v", r.Active())
	}
}

func TestDeactivateReturnsToIdle(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t, map[string][]string{".": {"go.mod"}}, map[string]string{"a.go": "package a"})
	if _, err := r.Activate(dir); err != nil {
		t.Fatal(err)
	}
	if !r.IsActive(Go) {
		t.Fatal("go should be active")
	}
	r.Deactivate()
	if len(r.Active()) != 0 {
		t.Fatalf("deactivate must return to idle, got %v", r.Active())
	}
}

func TestLookupAliases(t *testing.T) {
	r := NewRegistry()
	for name, want := range map[string]Language{
		"golang": Go, "txs": TSX, "flutter": Dart, "object-c": ObjC,
		"objectivec": ObjC, "swift": Swift, "kotlin": Kotlin, "kt": Kotlin,
		"python": Python, "GO": Go, "Rust": Rust,
	} {
		got, ok := r.Lookup(name)
		if !ok || got.Language != want {
			t.Fatalf("lookup %q = %v ok=%v, want %v", name, got.Language, ok, want)
		}
	}
	if _, ok := r.Lookup("cobol"); ok {
		t.Fatal("cobol must not resolve")
	}
}

func TestTiersCapabilityGated(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t, map[string][]string{".": {"go.mod", "pubspec.yaml"}}, nil)
	if _, err := r.Activate(dir); err != nil {
		t.Fatal(err)
	}
	tiers := r.Tiers()
	// regex always on
	for _, l := range []Language{Go, Dart} {
		if len(tiers[l]) == 0 || tiers[l][0] != TierRegex {
			t.Fatalf("%s tiers: %v", l, tiers[l])
		}
	}
	// tree-sitter is present ONLY for bundled grammars under the tstree tag
	// (go yes, dart no grammar in the set), absent entirely in the default build.
	for _, tier := range tiers[Go] {
		if tier == TierTreeSitter && !TreeSitterEnabled(Go) {
			t.Fatalf("tree-sitter tier claimed while disabled: %v", tiers[Go])
		}
	}
	if !TreeSitterEnabled(Go) {
		for _, tier := range tiers[Go] {
			if tier == TierTreeSitter {
				t.Fatalf("tree-sitter must be absent in default build: %v", tiers[Go])
			}
		}
	} else {
		found := false
		for _, tier := range tiers[Go] {
			if tier == TierTreeSitter {
				found = true
			}
		}
		if !found {
			t.Fatalf("tstree build must report tree-sitter for go: %v", tiers[Go])
		}
	}
	// objc and dart ship vendored grammars: for every language active in this
	// fixture, the tree-sitter tier claim must track TreeSitterEnabled.
	for _, l := range []Language{ObjC, Dart} {
		ts, ok := tiers[l]
		if !ok {
			continue // not active in this fixture
		}
		claimed := false
		for _, tier := range ts {
			if tier == TierTreeSitter {
				claimed = true
			}
		}
		if claimed != TreeSitterEnabled(l) {
			t.Fatalf("%s tree-sitter claim %v does not match TreeSitterEnabled %v: %v", l, claimed, TreeSitterEnabled(l), ts)
		}
	}
	// markdown has no AST tier even when active
	r2 := NewRegistry()
	d2 := fixture(t, nil, map[string]string{"README.md": "# x"})
	if _, err := r2.Activate(d2); err != nil {
		t.Fatal(err)
	}
	for _, tier := range r2.Tiers()[Markdown] {
		if tier == TierAstGrep || tier == TierTreeSitter {
			t.Fatalf("markdown must not gain AST tiers: %v", r2.Tiers()[Markdown])
		}
	}
}

func TestExtOwnerInactiveSkips(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t, nil, map[string]string{"x.dart": "void main(){}"})
	if _, err := r.Activate(dir); err != nil {
		t.Fatal(err)
	}
	if !r.IsActive(Dart) {
		t.Fatal("census must activate dart")
	}
	r.Deactivate()
	if _, ok := r.ExtOwner(".dart"); ok {
		t.Fatal("inactive language must not own extensions")
	}
}

// TestAstGrepPresentIgnoresSgAlias pins the tier side of the Linux name
// collision: a bare `sg` on PATH is util-linux's set-group command there
// (upstream deprecated the alias), so it must not light up the ast-grep tier.
// Resolution goes through astgrep.New, which probes only `ast-grep`.
func TestAstGrepPresentIgnoresSgAlias(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sg"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if astGrepPresent() {
		t.Fatal("astGrepPresent = true with only util-linux sg on PATH, want false")
	}
}

// TestDefaultRegistryExpandedSet pins the registry size after both expanded
// sets landed: 13 defaults + c, cpp, csharp, php, ruby, scala, perl, lua,
// haskell, elixir + crystal, cuda, cypher, elm, erlang, fsharp, glsl, hlsl,
// nim, ocaml, sql, powershell, qsharp, solidity, systemverilog, verilog, zig
// (Rust indexer registry parity; every examples/ directory is covered).
func TestDefaultRegistryExpandedSet(t *testing.T) {
	if len(Default) != 40 {
		t.Fatalf("len(Default) = %d, want 40", len(Default))
	}
	r := NewRegistry()
	for _, id := range []Language{
		C, Cpp, CSharp, PHP, Ruby, Scala, Perl, Lua, Haskell, Elixir,
		Crystal, Cuda, Cypher, Elm, Erlang, FSharp, GLSL, HLSL, Nim, OCaml,
		SQL, PowerShell, QSharp, Solidity, SystemVerilog, Verilog, Zig,
	} {
		if p, ok := r.Lookup(string(id)); !ok || p.Language != id {
			t.Fatalf("lookup id %q failed", id)
		}
	}
}

// TestLookupExpandedAliases pins alias resolution for both expanded sets.
func TestLookupExpandedAliases(t *testing.T) {
	r := NewRegistry()
	for name, want := range map[string]Language{
		"c++": Cpp, "cxx": Cpp, "c#": CSharp, "cs": CSharp, "dotnet": CSharp,
		"rb": Ruby, "pl": Perl, "pm": Perl, "hs": Haskell, "ex": Elixir,
		"CPP": Cpp, "RUBY": Ruby,
		"cr": Crystal, "cu": Cuda, "erl": Erlang, "f#": FSharp, "fs": FSharp,
		"ml": OCaml, "plsql": SQL, "pgsql": SQL, "tsql": SQL, "ps1": PowerShell,
		"pwsh": PowerShell, "q#": QSharp, "sol": Solidity, "sv": SystemVerilog,
		"Crystal": Crystal, "ZIG": Zig,
	} {
		got, ok := r.Lookup(name)
		if !ok || got.Language != want {
			t.Fatalf("lookup %q = %v ok=%v, want %v", name, got.Language, ok, want)
		}
	}
}

// TestActivateExpandedMarkers pins lazy activation via the expanded set's
// repo markers (Rust config_files parity + standard build manifests).
func TestActivateExpandedMarkers(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t,
		map[string][]string{
			".":        {"mix.exs", "composer.json", "Gemfile", "build.sbt"},
			"clib/":    {"CMakeLists.txt"},
			"crystal/": {"shard.yml"},
			"elm/":     {"elm.json"},
			"erlang/":  {"rebar.config"},
			"ocaml/":   {"dune-project"},
			"sol/":     {"foundry.toml"},
			"zig/":     {"build.zig"},
		},
		map[string]string{
			"lib/my_app.ex":       "defmodule MyApp, do: nil",
			"src/Foo.php":         "<?php class Foo {}",
			"app/main.rb":         "puts 1",
			"src/Main.scala":      "object Main",
			"crystal/src/user.cr": "class User; end",
			"elm/src/Counter.elm": "module Counter",
			"erlang/src/math.erl": "-module(math).",
			"ocaml/src/math.ml":   "let f x = x",
			"sol/contracts/C.sol": "contract C {}",
			"zig/src/main.zig":    "pub fn main() void {}",
		})
	roots, err := r.Activate(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []Language{Elixir, PHP, Ruby, Scala, C, Cpp,
		Crystal, Elm, Erlang, OCaml, Solidity, Zig} {
		if !r.IsActive(l) {
			t.Fatalf("%s must be active via markers, got %v", l, r.Active())
		}
	}
	for _, ext := range []string{".ex", ".php", ".rb", ".scala", ".c", ".h",
		".cr", ".elm", ".erl", ".ml", ".sol", ".zig"} {
		if _, ok := r.ExtOwner(ext); !ok {
			t.Fatalf("ext %s must be owned while its language is active", ext)
		}
	}
	if len(roots) != 8 {
		t.Fatalf("roots = %d, want 8 (top + CMake + 6 wave-2 roots): %v", len(roots), roots)
	}
}

// TestExtOwnerHeaderClaim pins .h ownership: C lists .h in Exts (Rust
// registry parity), so an active C beats the Swift/ObjC HeaderExts claim;
// with C idle the claim stays with Swift.
func TestExtOwnerHeaderClaim(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t, map[string][]string{".": {"Package.swift"}},
		map[string]string{"Sources/App.swift": "let x = 1", "bridge.c": "int f(void) { return 0; }", "bridge.h": ""})
	if _, err := r.Activate(dir); err != nil {
		t.Fatal(err)
	}
	if !r.IsActive(C) || !r.IsActive(Swift) {
		t.Fatalf("census+marker must activate C and Swift, got %v", r.Active())
	}
	if l, ok := r.ExtOwner(".h"); !ok || l != C {
		t.Fatalf(".h owner = %v ok=%v, want c (C active, .h in Exts)", l, ok)
	}
	r2 := NewRegistry()
	d2 := fixture(t, map[string][]string{".": {"Package.swift"}},
		map[string]string{"Sources/App.swift": "let x = 1"})
	if _, err := r2.Activate(d2); err != nil {
		t.Fatal(err)
	}
	if l, ok := r2.ExtOwner(".h"); !ok || l != Swift {
		t.Fatalf(".h owner = %v ok=%v, want swift (C idle)", l, ok)
	}
}

// TestAstGrepLangExpandedSet pins the ast-grep tier mapping: verified against
// the ast-grep CLI (0.45.3) — c, cpp, csharp (as "cs"), php, ruby, scala,
// lua and solidity are supported; perl, haskell, elixir and every other
// wave-2 language are rejected by `ast-grep run --lang <id>`.
func TestAstGrepLangExpandedSet(t *testing.T) {
	want := map[Language]string{
		C: "c", Cpp: "cpp", CSharp: "cs", PHP: "php", Ruby: "ruby",
		Scala: "scala", Lua: "lua", Perl: "", Haskell: "", Elixir: "",
		Solidity: "solidity",
		Crystal:  "", Cuda: "", Cypher: "", Elm: "", Erlang: "", FSharp: "",
		GLSL: "", HLSL: "", Nim: "", OCaml: "", SQL: "", PowerShell: "",
		QSharp: "", SystemVerilog: "", Verilog: "", Zig: "",
	}
	for l, id := range want {
		if got := astGrepLang(l); got != id {
			t.Fatalf("astGrepLang(%s) = %q, want %q", l, got, id)
		}
	}
}

// TestExpandedWave2Profiles pins the second expansion wave's registry
// surface: extensions exactly as the Rust registry declares them, plus the
// LSP rows that exist there. cuda, cypher, glsl, hlsl, qsharp, verilog and
// systemverilog have no Rust LSP server and must keep a nil spec.
func TestExpandedWave2Profiles(t *testing.T) {
	want := map[Language][]string{
		Crystal: {".cr"}, Cuda: {".cu", ".cuh"}, Cypher: {".cyp"},
		Elm: {".elm"}, Erlang: {".erl", ".hrl"},
		FSharp: {".fs", ".fsi", ".fsx"},
		GLSL:   {".glsl", ".vert", ".frag", ".geom", ".tesc", ".tese", ".comp"},
		HLSL:   {".hlsl", ".fx", ".fxh", ".hlsli"},
		Nim:    {".nim", ".nims"}, OCaml: {".ml", ".mli"},
		SQL:        {".sql", ".pls"},
		PowerShell: {".ps1", ".psm1", ".psd1"}, QSharp: {".qs"},
		Solidity: {".sol"}, SystemVerilog: {".sv", ".svh"},
		Verilog: {".v", ".vh"}, Zig: {".zig"},
	}
	r := NewRegistry()
	for l, exts := range want {
		p, ok := r.Lookup(string(l))
		if !ok {
			t.Fatalf("language %q missing from the registry", l)
		}
		if !reflect.DeepEqual(p.Exts, exts) {
			t.Fatalf("%s exts = %v, want %v", l, p.Exts, exts)
		}
	}
	lsp := map[Language]string{
		Crystal: "crystalline", Elm: "elm-language-server", Erlang: "erlang_ls",
		FSharp: "fsautocomplete", Nim: "nimlangserver", OCaml: "ocamllsp",
		SQL: "sqls", PowerShell: "powershell-es", Solidity: "solidity-ls",
		Zig: "zls",
	}
	for l, cmd := range lsp {
		p, _ := r.Lookup(string(l))
		if p.LSP == nil || len(p.LSP.Commands) == 0 || p.LSP.Commands[0] != cmd {
			t.Fatalf("%s LSP = %+v, want first command %q", l, p.LSP, cmd)
		}
	}
	for _, l := range []Language{Cuda, Cypher, GLSL, HLSL, QSharp, SystemVerilog, Verilog} {
		if p, _ := r.Lookup(string(l)); p.LSP != nil {
			t.Fatalf("%s must have no LSP spec (no Rust LSP row)", l)
		}
	}
}

// TestActivateWave2Census pins extension-census activation (marker-less
// trees) and lazy ExtOwner routing for the wave-2 languages with no repo
// marker, including the .v (verilog) and .pls (sql) ownership.
func TestActivateWave2Census(t *testing.T) {
	r := NewRegistry()
	dir := fixture(t, map[string][]string{},
		map[string]string{
			"src/kernel.cu":   "__global__ void k() {}",
			"src/shader.vert": "void main() {}",
			"src/light.hlsl":  "float4 f() { return 0; }",
			"src/query.cyp":   "MATCH (n:Person) RETURN n;",
			"src/math.nim":    "proc f() = discard",
			"src/Math.fs":     "module Math",
			"src/User.ps1":    "function Get-User {}",
			"src/bell.qs":     "namespace Q {}",
			"src/packet.sv":   "class packet; endclass",
			"src/counter.v":   "module counter; endmodule",
			"db/schema.sql":   "CREATE TABLE t (id INT);",
			"db/pkg.pls":      "CREATE PACKAGE pkg AS END pkg;",
		})
	if _, err := r.Activate(dir); err != nil {
		t.Fatal(err)
	}
	for _, l := range []Language{Cuda, GLSL, HLSL, Cypher, Nim, FSharp,
		PowerShell, QSharp, SystemVerilog, Verilog, SQL} {
		if !r.IsActive(l) {
			t.Fatalf("%s must activate by extension census, got %v", l, r.Active())
		}
	}
	for ext, want := range map[string]Language{
		".v": Verilog, ".pls": SQL, ".cu": Cuda, ".qs": QSharp,
		".vert": GLSL, ".cyp": Cypher, ".ps1": PowerShell,
	} {
		if l, ok := r.ExtOwner(ext); !ok || l != want {
			t.Fatalf("ExtOwner(%s) = %v ok=%v, want %v", ext, l, ok, want)
		}
	}
	if _, ok := r.ExtOwner(".zig"); ok {
		t.Fatalf(".zig must stay unowned while zig is inactive")
	}
}
