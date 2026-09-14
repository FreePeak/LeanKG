// Package langs is the language registry: which languages the engine indexes,
// how a codebase declares them (nested repos included), and when they wake up.
//
// Lazy activation is the design contract (user requirement: "everything idle,
// lazy load when user opens the codebase"):
//   - at rest NOTHING is active — no parsers, no LSP processes, no state;
//   - Activate(codebase) walks the tree, detects language markers per repo
//     root (go.mod / Cargo.toml / package.json / pom.xml / pubspec.yaml ...),
//     and enables exactly the languages the codebase uses;
//   - nested repos each activate their own slice;
//   - enrichment tiers are capability-gated, never assumed: the regex
//     extractor is always live, tree-sitter only under the `tstree` build
//     tag (CGO), ast-grep only when the CLI is on PATH, and an LSP server is
//     spawned lazily at QUERY time scoped to the queried directory, idle-
//     terminated after a TTL.
package langs

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/FreePeak/LeanKG/go/internal/astgrep"
)

// Language identifies a supported language.
type Language string

// Default language set (user-specified): go, rust, ts, tsx, js, jsx, py, md,
// java, kotlin, swift, objective-c, flutter (Dart), plus the expanded set
// ported from the Rust indexer registry (src/indexer/lang/registry.rs at
// f7624143^): c, cpp, csharp, php, ruby, scala, perl, lua, haskell, elixir,
// crystal, cuda, cypher, elm, erlang, fsharp, glsl, hlsl, nim, ocaml, sql,
// powershell, qsharp, solidity, systemverilog, verilog, zig.
const (
	Go         Language = "go"
	Rust       Language = "rust"
	TypeScript Language = "ts"
	TSX        Language = "tsx"
	JavaScript Language = "js"
	JSX        Language = "jsx"
	Python     Language = "py"
	Markdown   Language = "md"
	Java       Language = "java"
	Kotlin     Language = "kotlin"
	Swift      Language = "swift"
	ObjC       Language = "objc"
	Dart       Language = "dart" // flutter

	// Expanded set (Rust indexer registry parity).
	C       Language = "c"
	Cpp     Language = "cpp"
	CSharp  Language = "csharp"
	PHP     Language = "php"
	Ruby    Language = "ruby"
	Scala   Language = "scala"
	Perl    Language = "perl"
	Lua     Language = "lua"
	Haskell Language = "haskell"
	Elixir  Language = "elixir"
	// Language-expansion wave 2 (examples/ coverage): extensions and LSP
	// rows mirror the Rust registry (src/indexer/lang/registry.rs) and LSP
	// registry (src/lsp/registry.rs) at f7624143^. Cypher has no Rust row
	// (.cyp graph-query files); sql carries the pgsql/tsql/plsql dialects.
	Crystal       Language = "crystal"
	Cuda          Language = "cuda"
	Cypher        Language = "cypher"
	Elm           Language = "elm"
	Erlang        Language = "erlang"
	FSharp        Language = "fsharp"
	GLSL          Language = "glsl"
	HLSL          Language = "hlsl"
	Nim           Language = "nim"
	OCaml         Language = "ocaml"
	SQL           Language = "sql"
	PowerShell    Language = "powershell"
	QSharp        Language = "qsharp"
	Solidity      Language = "solidity"
	SystemVerilog Language = "systemverilog"
	Verilog       Language = "verilog"
	Zig           Language = "zig"
)

// Tier names an extraction/lookup mechanism, ordered by fidelity.
type Tier string

const (
	TierRegex      Tier = "regex"       // always available, CGO-free
	TierTreeSitter Tier = "tree-sitter" // build tag `tstree` only (CGO)
	TierAstGrep    Tier = "ast-grep"    // requires the ast-grep CLI on PATH
	TierLSP        Tier = "lsp"         // per-language server, lazy at query time
)

// LSPSpec describes how to reach a language server.
type LSPSpec struct {
	// Command candidates, first resolvable on PATH wins (gopls,
	// rust-analyzer, typescript-language-server, pyright-langserver,
	// jdtls, kotlin-language-server, sourcekit-lsp, dart).
	Commands []string
	// RootMarkers optionally restrict serving to dirs containing one of these.
	RootMarkers []string
}

// Profile is a language's registry entry.
type Profile struct {
	Language Language
	// Aliases are accepted in user config (e.g. "txs"->tsx, "flutter"->dart,
	// "object-c"/"objectivec"->objc, "swiftff"->swift).
	Aliases []string
	// Exts this language owns (indexer filter). Each extension has exactly one
	// owner; shared extensions (.h) resolve via HeaderOwner.
	Exts []string
	// HeaderOwner marks languages that may claim .h files; the owner is the
	// activated language with the highest priority (ordered Default list) that
	// lists .h in Exts-or-HeaderExts.
	HeaderExts []string
	// RepoMarkers: files whose presence declares the language at a repo root.
	RepoMarkers []string
	// RepoDirSuffixes: directory-name suffixes (e.g. /Sources, /Classes) used
	// as fallback signals when markers are absent.
	RepoDirSuffixes []string
	// LSP is the language server spec (may be nil).
	LSP *LSPSpec
}

// Default is the activation-priority order of the registry: the 13 default
// languages first (their .h ownership order is load-bearing), then the
// expanded sets ported from the Rust indexer registry.
var Default = []Profile{
	{Language: Go, Aliases: []string{"golang"}, Exts: []string{".go"},
		RepoMarkers: []string{"go.mod"},
		LSP:         &LSPSpec{Commands: []string{"gopls"}, RootMarkers: []string{"go.mod"}}},
	{Language: Rust, Aliases: []string{"rs"}, Exts: []string{".rs"},
		RepoMarkers: []string{"Cargo.toml"},
		LSP:         &LSPSpec{Commands: []string{"rust-analyzer"}, RootMarkers: []string{"Cargo.toml"}}},
	{Language: TypeScript, Aliases: []string{"typescript"}, Exts: []string{".ts"},
		RepoMarkers: []string{"tsconfig.json", "package.json"},
		LSP:         &LSPSpec{Commands: []string{"typescript-language-server", "ts-language-server"}, RootMarkers: []string{"package.json"}}},
	{Language: TSX, Aliases: []string{"tsx", "txs"}, Exts: []string{".tsx"},
		RepoMarkers: []string{"tsconfig.json", "package.json"},
		LSP:         &LSPSpec{Commands: []string{"typescript-language-server"}, RootMarkers: []string{"package.json"}}},
	{Language: JavaScript, Aliases: []string{"javascript"}, Exts: []string{".js"},
		RepoMarkers: []string{"package.json"},
		LSP:         &LSPSpec{Commands: []string{"typescript-language-server", "vscode-langservers-extracted"}, RootMarkers: []string{"package.json"}}},
	{Language: JSX, Aliases: []string{"jsx"}, Exts: []string{".jsx"},
		RepoMarkers: []string{"package.json"},
		LSP:         &LSPSpec{Commands: []string{"typescript-language-server"}, RootMarkers: []string{"package.json"}}},
	{Language: Python, Aliases: []string{"python"}, Exts: []string{".py"},
		RepoMarkers: []string{"pyproject.toml", "requirements.txt", "setup.py", "Pipfile"},
		LSP: &LSPSpec{Commands: []string{"pyright-langserver", "pylsp", "jedi-language-server"},
			RootMarkers: []string{"pyproject.toml", "setup.py", "requirements.txt"}}},
	{Language: Markdown, Aliases: []string{"markdown"}, Exts: []string{".md"},
		// Rust LSP registry parity (registry.rs marksman row).
		LSP: &LSPSpec{Commands: []string{"marksman"}}},
	{Language: Java, Aliases: []string{"java"}, Exts: []string{".java"},
		RepoMarkers: []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle"},
		LSP:         &LSPSpec{Commands: []string{"jdtls", "java-language-server"}, RootMarkers: []string{"pom.xml", "build.gradle", "build.gradle.kts"}}},
	{Language: Kotlin, Aliases: []string{"kotlin", "kt"}, Exts: []string{".kt", ".kts"},
		RepoMarkers: []string{"build.gradle.kts", "build.gradle", "settings.gradle.kts", "pom.xml"},
		LSP:         &LSPSpec{Commands: []string{"kotlin-language-server", "fwdd"}, RootMarkers: []string{"build.gradle", "build.gradle.kts", "settings.gradle.kts"}}},
	{Language: Swift, Aliases: []string{"swift"}, Exts: []string{".swift"},
		HeaderExts:  []string{".h"},
		RepoMarkers: []string{"Package.swift"},
		LSP:         &LSPSpec{Commands: []string{"sourcekit-lsp"}, RootMarkers: []string{"Package.swift"}}},
	{Language: ObjC, Aliases: []string{"object-c", "objectivec", "objective_c", "objc", "obj-c"},
		Exts: []string{".m", ".mm"}, HeaderExts: []string{".h"},
		RepoMarkers: []string{"Podfile"}, RepoDirSuffixes: []string{"/Classes", "/Sources"},
		LSP: &LSPSpec{Commands: []string{"sourcekit-lsp", "clangd"},
			RootMarkers: []string{"Podfile", ".clangd"}}},
	{Language: Dart, Aliases: []string{"flutter", "dart"}, Exts: []string{".dart"},
		RepoMarkers: []string{"pubspec.yaml"},
		// Rust LSP registry parity: dart-language-server is registry.rs's
		// command; the SDK-provided fallbacks (dart/dartls, flutter) stay as
		// later candidates for hosts that only have the SDK on PATH.
		LSP: &LSPSpec{Commands: []string{"dart-language-server", "dart", "dartls", "flutter"},
			RootMarkers: []string{"pubspec.yaml"}}},
	// Expanded set: extensions and config markers mirror the Rust indexer
	// registry (src/indexer/lang/registry.rs at f7624143^); LSP commands mirror
	// the Rust LSP registry (src/lsp/registry.rs at f7624143^). Languages with
	// no Rust LSP row (csharp, perl) get no LSP spec. CMakeLists.txt activates
	// both C and C++ — a CMake project is either; inactive extensions never
	// route files, so over-activation is benign.
	{Language: C, Exts: []string{".c", ".h"},
		RepoMarkers: []string{"CMakeLists.txt"},
		LSP:         &LSPSpec{Commands: []string{"clangd"}}},
	{Language: Cpp, Aliases: []string{"c++", "cxx", "cc"},
		Exts:       []string{".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".h++"},
		HeaderExts: []string{".h"}, RepoMarkers: []string{"CMakeLists.txt"},
		LSP: &LSPSpec{Commands: []string{"clangd"}}},
	{Language: CSharp, Aliases: []string{"c#", "cs", "dotnet"}, Exts: []string{".cs"},
		RepoMarkers: []string{"Project.toml"}},
	{Language: PHP, Aliases: []string{"php"}, Exts: []string{".php", ".phtml"},
		RepoMarkers: []string{"composer.json"},
		LSP:         &LSPSpec{Commands: []string{"intelephense"}}},
	{Language: Ruby, Aliases: []string{"rb"}, Exts: []string{".rb", ".ruby", ".rake", ".gemspec"},
		RepoMarkers: []string{"Gemfile"},
		LSP:         &LSPSpec{Commands: []string{"solargraph"}}},
	{Language: Scala, Aliases: []string{"sc"}, Exts: []string{".scala", ".sc"},
		RepoMarkers: []string{"build.sbt"},
		LSP:         &LSPSpec{Commands: []string{"metals"}}},
	{Language: Perl, Aliases: []string{"pl", "pm"}, Exts: []string{".pl", ".pm", ".t"},
		RepoMarkers: []string{"cpanfile", "Makefile.PL"}},
	{Language: Lua, Aliases: []string{"lua"}, Exts: []string{".lua"},
		LSP: &LSPSpec{Commands: []string{"lua-language-server"}}},
	{Language: Haskell, Aliases: []string{"hs"}, Exts: []string{".hs", ".lhs"},
		RepoMarkers: []string{"stack.yaml", "cabal.project"},
		LSP:         &LSPSpec{Commands: []string{"haskell-language-server-wrapper"}}},
	{Language: Elixir, Aliases: []string{"ex", "exs"}, Exts: []string{".ex", ".exs"},
		RepoMarkers: []string{"mix.exs"},
		LSP:         &LSPSpec{Commands: []string{"elixir-ls"}}},
	// Language-expansion wave 2: extensions, aliases, markers and LSP rows
	// mirror the Rust registries (src/indexer/lang/registry.rs,
	// src/lsp/registry.rs at f7624143^). Rust config_files were empty for
	// these languages (activation fell back to an extension census), so the
	// markers below are the languages' own build manifests — they make
	// activation exact instead of census-guessed. cuda/glsl/hlsl/verilog/
	// systemverilog/cypher/qsharp have no Rust LSP row and get none here.
	{Language: Crystal, Aliases: []string{"cr"}, Exts: []string{".cr"},
		RepoMarkers: []string{"shard.yml"},
		LSP:         &LSPSpec{Commands: []string{"crystalline"}, RootMarkers: []string{"shard.yml"}}},
	{Language: Cuda, Aliases: []string{"cu"}, Exts: []string{".cu", ".cuh"}},
	{Language: Cypher, Aliases: []string{"neo4j"}, Exts: []string{".cyp"}},
	{Language: Elm, Exts: []string{".elm"},
		RepoMarkers: []string{"elm.json"},
		LSP:         &LSPSpec{Commands: []string{"elm-language-server"}, RootMarkers: []string{"elm.json"}}},
	{Language: Erlang, Aliases: []string{"erl", "hrl"}, Exts: []string{".erl", ".hrl"},
		RepoMarkers: []string{"rebar.config", "rebar.lock"},
		LSP:         &LSPSpec{Commands: []string{"erlang_ls", "erlang-language-server"}}},
	{Language: FSharp, Aliases: []string{"f#", "fs"}, Exts: []string{".fs", ".fsi", ".fsx"},
		LSP: &LSPSpec{Commands: []string{"fsautocomplete"}}},
	{Language: GLSL, Aliases: []string{"shader"},
		Exts: []string{".glsl", ".vert", ".frag", ".geom", ".tesc", ".tese", ".comp"}},
	{Language: HLSL, Aliases: []string{"hlsli"}, Exts: []string{".hlsl", ".fx", ".fxh", ".hlsli"}},
	{Language: Nim, Exts: []string{".nim", ".nims"},
		LSP: &LSPSpec{Commands: []string{"nimlangserver"}}},
	{Language: OCaml, Aliases: []string{"ml"}, Exts: []string{".ml", ".mli"},
		RepoMarkers: []string{"dune-project"},
		LSP:         &LSPSpec{Commands: []string{"ocamllsp"}, RootMarkers: []string{"dune-project"}}},
	// One language id for all three SQL dialects: pgsql (.sql), tsql (.sql)
	// and plsql (.pls) share the extractor and differ only by dialect
	// syntax the regex tier treats uniformly.
	{Language: SQL, Aliases: []string{"plsql", "pgsql", "tsql", "postgres"},
		Exts: []string{".sql", ".pls"},
		LSP:  &LSPSpec{Commands: []string{"sqls"}}},
	{Language: PowerShell, Aliases: []string{"ps1", "pwsh"}, Exts: []string{".ps1", ".psm1", ".psd1"},
		LSP: &LSPSpec{Commands: []string{"powershell-es"}}},
	{Language: QSharp, Aliases: []string{"q#", "qsharp"}, Exts: []string{".qs"}},
	{Language: Solidity, Aliases: []string{"sol"}, Exts: []string{".sol"},
		RepoMarkers: []string{"foundry.toml", "hardhat.config.js", "hardhat.config.ts", "truffle-config.js"},
		LSP:         &LSPSpec{Commands: []string{"solidity-ls"}}},
	{Language: SystemVerilog, Aliases: []string{"sv"}, Exts: []string{".sv", ".svh"}},
	{Language: Verilog, Exts: []string{".v", ".vh"}},
	{Language: Zig, Exts: []string{".zig"},
		RepoMarkers: []string{"build.zig", "build.zig.zon"},
		LSP:         &LSPSpec{Commands: []string{"zls"}, RootMarkers: []string{"build.zig"}}},
}

// Registry holds profiles at REST (all of them) and the set currently ACTIVE
// for an opened codebase. Nothing parses or spawns until activation.
type Registry struct {
	mu        sync.RWMutex
	profiles  map[Language]Profile
	order     []Language // priority order from Default
	active    map[Language]bool
	codebase  string
	astgrepFn func() bool // injected availability probe (exec LookPath)
}

// NewRegistry builds the registry over the Default profiles.
func NewRegistry() *Registry {
	r := &Registry{
		profiles: map[Language]Profile{},
		active:   map[Language]bool{},
	}
	for _, p := range Default {
		r.profiles[p.Language] = p
		r.order = append(r.order, p.Language)
	}
	r.astgrepFn = astGrepPresent
	return r
}

var (
	builtinsMu sync.Mutex
	builtins   *Registry // process-wide default registry for the indexer
)

// DefaultRegistry is the process-wide registry used when no explicit one is
// supplied (cmd layers, index tests). Lazily built.
func DefaultRegistry() *Registry {
	builtinsMu.Lock()
	defer builtinsMu.Unlock()
	if builtins == nil {
		builtins = NewRegistry()
	}
	return builtins
}

// astGrepPresent reports the ast-grep tier live only when the CLI really is
// ast-grep: bare `sg` on PATH is util-linux's set-group command on Linux, and
// counting it as the alias would advertise a tier that cannot answer.
// Resolution (and its identity check) lives in astgrep.New.
func astGrepPresent() bool {
	_, err := astgrep.New()
	return err == nil
}

// Lookup resolves a user-supplied language name (case-insensitive, aliases
// accepted: "golang", "txs", "flutter", "object-c", ...).
func (r *Registry) Lookup(name string) (Profile, bool) {
	n := Language(strings.ToLower(strings.TrimSpace(name)))
	// exact language id
	if p, ok := r.profiles[n]; ok {
		return p, true
	}
	for _, p := range r.profiles {
		for _, a := range p.Aliases {
			if Language(strings.ToLower(a)) == n {
				return p, true
			}
		}
	}
	return Profile{}, false
}

// Activate detects the languages of codebase (including NESTED repos) and
// marks exactly those active. Returns the per-repo-root mapping. Detection:
//  1. walk to depth limit, honoring skipDirs;
//  2. any directory containing a known repo marker file is a repo root;
//  3. a root activates the languages whose marker it holds;
//  4. fallback by extension census when no markers found at the top dir
//     (a loose directory of sources still activates its languages);
//  5. header ownership: .h belongs to the highest-priority activated language
//     listing it in HeaderExts.
func (r *Registry) Activate(codebase string) (map[string][]Language, error) {
	roots, err := detectRoots(codebase)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codebase = codebase
	r.active = map[Language]bool{}
	out := map[string][]Language{}
	for root, langs := range roots {
		out[root] = langs
		for _, l := range langs {
			r.active[l] = true
		}
	}
	// Census ALWAYS supplements markers: a polyglot tree with one marker
	// (e.g. pubspec.yaml) must not silence stray sources of other languages
	// (stray .go files). Only census-hit languages activate here.
	cens := censusExts(codebase)
	for _, l := range r.order {
		if r.active[l] {
			continue
		}
		p := r.profiles[l]
		for _, e := range append(append([]string{}, p.Exts...), p.HeaderExts...) {
			if cens[e] > 0 {
				r.active[l] = true
				out[codebase] = append(out[codebase], l)
				break
			}
		}
	}
	// dedupe out[codebase]
	if lst, ok := out[codebase]; ok {
		set := map[Language]bool{}
		var uniq []Language
		for _, l := range lst {
			if !set[l] {
				set[l] = true
				uniq = append(uniq, l)
			}
		}
		out[codebase] = uniq
	}
	return out, nil
}

// Deactivate returns the registry to the idle state (nothing active).
func (r *Registry) Deactivate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = map[Language]bool{}
	r.codebase = ""
}

// Active reports the languages currently enabled for the opened codebase.
func (r *Registry) Active() []Language {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Language
	for _, l := range r.order {
		if r.active[l] {
			out = append(out, l)
		}
	}
	return out
}

// IsActive reports whether a language is turned on.
func (r *Registry) IsActive(l Language) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active[l]
}

// Codebase is the directory Activate was last called with.
func (r *Registry) Codebase() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.codebase
}

// ExtOwner maps a file extension to its ACTIVE language, honoring lazy
// activation and header ownership. ok=false ⇒ extension not active, skip file.
func (r *Registry) ExtOwner(ext string) (Language, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, l := range r.order {
		if !r.active[l] {
			continue
		}
		p := r.profiles[l]
		for _, e := range p.Exts {
			if e == ext {
				return l, true
			}
		}
	}
	if ext == ".h" {
		for _, l := range r.order {
			if !r.active[l] {
				continue
			}
			for _, e := range r.profiles[l].HeaderExts {
				if e == ".h" {
					return l, true
				}
			}
		}
	}
	return "", false
}

// LSPSpec returns the language server spec for l (nil when the language has
// no LSP tier).
func (r *Registry) LSPSpec(l Language) *LSPSpec {
	p, ok := r.profiles[l]
	if !ok {
		return nil
	}
	return p.LSP
}

// Tiers reports, per ACTIVE language, which extraction/lookup tiers are live
// right now given the build + host toolchain (regex always; tree-sitter only
// under the tstree tag; ast-grep only when the CLI exists; lsp only when a
// server binary resolves on PATH).
func (r *Registry) Tiers() map[Language][]Tier {
	r.mu.RLock()
	order := r.order
	active := map[Language]bool{}
	for l, v := range r.active {
		active[l] = v
	}
	aGrep := r.astgrepFn != nil && r.astgrepFn()
	r.mu.RUnlock()

	out := map[Language][]Tier{}
	for _, l := range order {
		if !active[l] {
			continue
		}
		ts := []Tier{TierRegex}
		if TreeSitterEnabled(l) {
			ts = append(ts, TierTreeSitter)
		}
		if aGrep && astGrepLang(l) != "" {
			ts = append(ts, TierAstGrep)
		}
		if spec := r.LSPSpec(l); spec != nil && anyResolvable(spec.Commands) {
			ts = append(ts, TierLSP)
		}
		out[l] = ts
	}
	return out
}

// ---------------------------------------------------------------------------
// detection helpers
// ---------------------------------------------------------------------------

var skipDirs = map[string]bool{
	".git": true, "target": true, "node_modules": true, "vendor": true,
	".leankg": true, "dist": true, "build": true, ".worktrees": true, ".worktree": true,
}

func markerToLangs() map[string][]Language {
	m := map[string][]Language{}
	for _, p := range Default {
		for _, mk := range p.RepoMarkers {
			m[mk] = append(m[mk], p.Language)
		}
	}
	return m
}

// detectRoots walks codebase (bounded depth) and returns repo roots and the
// languages each declares.
func detectRoots(codebase string) (map[string][]Language, error) {
	markers := markerToLangs()
	exts := map[string]Language{}
	for _, p := range Default {
		for _, e := range p.Exts {
			if _, dup := exts[e]; !dup {
				exts[e] = p.Language
			}
		}
	}
	out := map[string][]Language{}
	abs, err := filepath.Abs(codebase)
	if err != nil {
		return nil, err
	}
	baseDepth := strings.Count(abs, string(os.PathSeparator))
	maxDepth := baseDepth + 4 // bounded walk: nested repos live shallow

	filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtrees: skip, never abort detection
		}
		if d.IsDir() {
			name := d.Name()
			if path != abs && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			depth := strings.Count(path, string(os.PathSeparator))
			if depth > maxDepth {
				return filepath.SkipDir
			}
			var found []Language
			for mk, langs := range markers {
				if st, err := os.Stat(filepath.Join(path, mk)); err == nil && !st.IsDir() {
					found = append(found, langs...)
				}
			}
			if len(found) > 0 {
				set := map[Language]bool{}
				var uniq []Language
				for _, l := range found {
					if !set[l] {
						set[l] = true
						uniq = append(uniq, l)
					}
				}
				sort.Slice(uniq, func(i, j int) bool {
					return langIndex(uniq[i]) < langIndex(uniq[j])
				})
				out[path] = uniq
			}
			return nil
		}
		return nil
	})
	return out, nil
}

// censusExts counts known extensions in a bounded subtree below dir
// (marker-less trees). The walk honors skipDirs and dot-dirs and is depth-
// bounded like detectRoots (baseDepth+4): a marker-less tree whose sources
// sit several directories down still activates its languages. Files deeper
// than the bound stay unseen — a repo marker is the exact signal for those.
func censusExts(dir string) map[string]int {
	counts := map[string]int{}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return counts
	}
	baseDepth := strings.Count(abs, string(os.PathSeparator))
	filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtrees: skip, never abort detection
		}
		if d.IsDir() {
			if path != abs && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if strings.Count(path, string(os.PathSeparator)) > baseDepth+4 {
				return filepath.SkipDir
			}
			return nil
		}
		counts[filepath.Ext(d.Name())]++
		return nil
	})
	return counts
}

func langIndex(l Language) int {
	for i, p := range Default {
		if p.Language == l {
			return i
		}
	}
	return len(Default)
}

// anyResolvable reports whether one of the candidate commands is on PATH.
func anyResolvable(cmds []string) bool {
	for _, c := range cmds {
		if _, err := execLookPath(c); err == nil {
			return true
		}
	}
	return false
}
