package lsp

// Server registry (Rust parity: src/lsp/registry.rs). The catalog maps a
// language id to its canonical LSP server (command + args + extensions +
// aliases) so a project gets typed enrichment without writing an lsp: block.
// Ordering matters: ForLanguage returns the first entry whose language or
// alias matches.
//
// Deliberately not ported from the Rust registry: InstallMethod and the
// `leankg lsp-install` CLI (the Go engine ships no package-installer
import (
	"os/exec"
	"path/filepath"
	"strings"
)

// ServerSpec is one canonical LSP server entry.
type ServerSpec struct {
	Language   string   // canonical id: "go", "typescript", "python", ...
	Command    string   // executable resolved on PATH
	Args       []string // flags the server requires (--stdio, serve, ...)
	Extensions []string // file extensions handled (no dot)
	Aliases    []string // alternate ids accepted by ForLanguage
}

// catalog mirrors Rust ALL_LSP_SERVERS one-for-one.
var catalog = []ServerSpec{
	// === Systems ===
	{Language: "go", Command: "gopls", Args: []string{"serve"},
		Extensions: []string{"go"}, Aliases: []string{"golang"}},
	{Language: "rust", Command: "rust-analyzer",
		Extensions: []string{"rs"}, Aliases: []string{"rs"}},
	{Language: "c", Command: "clangd", Args: []string{"--background-index"},
		Extensions: []string{"c", "h"}},
	{Language: "cpp", Command: "clangd", Args: []string{"--background-index"},
		Extensions: []string{"cpp", "cxx", "cc", "c++", "hpp", "hxx", "hh", "h"},
		Aliases:    []string{"c++", "cxx"}},
	{Language: "zig", Command: "zls", Extensions: []string{"zig"}},
	{Language: "nim", Command: "nimlangserver", Extensions: []string{"nim"}},
	{Language: "crystal", Command: "crystalline", Extensions: []string{"cr"}},
	// === JVM ===
	{Language: "java", Command: "jdtls", Extensions: []string{"java"}},
	{Language: "kotlin", Command: "kotlin-language-server",
		Extensions: []string{"kt", "kts"}},
	{Language: "scala", Command: "metals", Extensions: []string{"scala", "sbt"}},
	{Language: "clojure", Command: "clojure-lsp",
		Extensions: []string{"clj", "cljs", "cljc", "edn"}, Aliases: []string{"clj"}},
	// === Web / Scripting ===
	{Language: "typescript", Command: "typescript-language-server", Args: []string{"--stdio"},
		Extensions: []string{"ts", "tsx", "mts", "cts"}, Aliases: []string{"ts"}},
	{Language: "javascript", Command: "typescript-language-server", Args: []string{"--stdio"},
		Extensions: []string{"js", "jsx", "mjs", "cjs"}, Aliases: []string{"js"}},
	{Language: "vue", Command: "vue-language-server", Args: []string{"--stdio"},
		Extensions: []string{"vue"}},
	{Language: "svelte", Command: "svelteserver", Args: []string{"--stdio"},
		Extensions: []string{"svelte"}},
	{Language: "html", Command: "vscode-html-language-server", Args: []string{"--stdio"},
		Extensions: []string{"html", "htm"}},
	{Language: "css", Command: "vscode-css-language-server", Args: []string{"--stdio"},
		Extensions: []string{"css", "scss", "less"}, Aliases: []string{"scss", "less"}},
	{Language: "json", Command: "vscode-json-language-server", Args: []string{"--stdio"},
		Extensions: []string{"json", "jsonc"}, Aliases: []string{"jsonc"}},
	{Language: "yaml", Command: "yaml-language-server", Args: []string{"--stdio"},
		Extensions: []string{"yaml", "yml"}, Aliases: []string{"yml"}},
	{Language: "xml", Command: "lemminx",
		Extensions: []string{"xml", "xsl", "xslt", "svg"},
		Aliases:    []string{"xsl", "xslt", "svg"}},
	{Language: "python", Command: "pylsp",
		Extensions: []string{"py", "pyi"}, Aliases: []string{"py"}},
	{Language: "ruby", Command: "solargraph",
		Extensions: []string{"rb", "erb", "rake"}, Aliases: []string{"rb"}},
	{Language: "php", Command: "intelephense", Args: []string{"--stdio"},
		Extensions: []string{"php", "phtml"}},
	{Language: "lua", Command: "lua-language-server", Extensions: []string{"lua"}},
	{Language: "bash", Command: "bash-language-server", Args: []string{"start"},
		Extensions: []string{"sh", "bash", "zsh"}, Aliases: []string{"sh", "zsh"}},
	{Language: "powershell", Command: "powershell-es",
		Extensions: []string{"ps1", "psm1"}, Aliases: []string{"ps1"}},
	{Language: "haskell", Command: "haskell-language-server-wrapper", Args: []string{"--lsp"},
		Extensions: []string{"hs"}, Aliases: []string{"hs"}},
	{Language: "elm", Command: "elm-language-server", Extensions: []string{"elm"}},
	{Language: "ocaml", Command: "ocamllsp",
		Extensions: []string{"ml", "mli"}, Aliases: []string{"ml"}},
	{Language: "fsharp", Command: "fsautocomplete",
		Extensions: []string{"fs", "fsx", "fsi"}, Aliases: []string{"fs"}},
	{Language: "elixir", Command: "elixir-ls",
		Extensions: []string{"ex", "exs"}, Aliases: []string{"ex"}},
	{Language: "erlang", Command: "erlang_ls",
		Extensions: []string{"erl", "hrl"}, Aliases: []string{"erl"}},
	{Language: "sql", Command: "sqls", Extensions: []string{"sql"}},
	{Language: "r", Command: "languageserver", Extensions: []string{"r", "R"}},
	{Language: "swift", Command: "sourcekit-lsp", Extensions: []string{"swift"}},
	{Language: "objc", Command: "clangd", Args: []string{"--background-index"},
		Extensions: []string{"m", "mm"},
		Aliases:    []string{"objective-c", "objectivec"}},
	{Language: "dart", Command: "dart-language-server", Args: []string{"--protocol=lsp"},
		Extensions: []string{"dart"}},
	{Language: "kotlin-android", Command: "kotlin-language-server",
		Aliases: []string{"android-kotlin"}},
	{Language: "markdown", Command: "marksman", Args: []string{"server"},
		Extensions: []string{"md", "markdown"}, Aliases: []string{"md"}},
	{Language: "toml", Command: "taplo", Args: []string{"lsp", "stdio"},
		Extensions: []string{"toml"}},
	{Language: "graphql", Command: "graphql-lsp", Args: []string{"server", "-m", "stream"},
		Extensions: []string{"graphql", "gql"}, Aliases: []string{"gql"}},
	{Language: "terraform", Command: "terraform-ls", Args: []string{"serve"},
		Extensions: []string{"tf", "hcl"}, Aliases: []string{"hcl", "tf"}},
	{Language: "dockerfile", Command: "docker-langserver", Args: []string{"--stdio"},
		Extensions: []string{"dockerfile", "Dockerfile"}},
	{Language: "protobuf", Command: "buf", Args: []string{"beta", "lsp"},
		Extensions: []string{"proto"}, Aliases: []string{"proto"}},
	{Language: "solidity", Command: "solidity-ls", Args: []string{"--stdio"},
		Extensions: []string{"sol"}, Aliases: []string{"sol"}},
}

// ForLanguage looks up a spec by canonical language id OR alias (Rust parity:
// LspServerSpec::for_language, case-insensitive).
func ForLanguage(lang string) (ServerSpec, bool) {
	needle := strings.ToLower(strings.TrimSpace(lang))
	if needle == "" {
		return ServerSpec{}, false
	}
	for _, s := range catalog {
		if s.Language == needle {
			return s, true
		}
		for _, a := range s.Aliases {
			if a == needle {
				return s, true
			}
		}
	}
	return ServerSpec{}, false
}

// DetectLanguage maps a file path to the canonical language id by extension
// (Rust parity: detect_language). ok=false for unknown extensions.
func DetectLanguage(path string) (string, bool) {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	return DetectLanguageByExtension(ext)
}

// DetectLanguageByExtension is DetectLanguage over a bare extension.
func DetectLanguageByExtension(ext string) (string, bool) {
	if ext == "" {
		return "", false
	}
	for _, s := range catalog {
		for _, e := range s.Extensions {
			if e == ext {
				return s.Language, true
			}
		}
	}
	return "", false
}

// ExtensionTable maps every known extension to its canonical language id
// (Rust parity: extension_table).
func ExtensionTable() map[string]string {
	out := make(map[string]string, len(catalog)*3)
	for _, s := range catalog {
		for _, e := range s.Extensions {
			out[e] = s.Language
		}
	}
	return out
}

// AutoConfig builds a Config from the catalog: languages whose command is
// already on PATH are always included; missing ones are added only when
// includeMissing is set (Rust parity: auto_config). Returns the ids whose
// command is missing.
func AutoConfig(includeMissing bool) (Config, []string) {
	cfg := Config{Servers: map[string]ServerConfig{}, TimeoutMS: defaultTimeoutMS}
	var missing []string
	for _, spec := range catalog {
		if !commandOnPath(spec.Command) {
			if !includeMissing {
				continue
			}
			missing = append(missing, spec.Language)
		}
		cfg.Servers[spec.Language] = ServerConfig{
			Command:    spec.Command,
			Args:       append([]string(nil), spec.Args...),
			Extensions: append([]string(nil), spec.Extensions...),
		}
	}
	return cfg, missing
}

// commandOnPath reports whether an executable resolves on the current PATH.
func commandOnPath(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}
