//! Comprehensive LSP server registry.
//!
//! Maps language → canonical LSP server command + args + extensions +
//! install instructions. Used by the bridge so that as long as a
//! language has a known LSP server, the user gets typed-resolve for
//! it without writing a `lsp:` block in leankg.yaml.
//!
//! ## Adding a new language
//!
//! Drop a [`LspServerSpec`] entry into [`ALL_LSP_SERVERS`] and the
//! bridge will pick it up automatically. The auto-detection from
//! file extension (see [`detect_language`]) covers ~70 file
//! extensions out of the box.
//!
//! ## Install commands
//!
//! Each entry carries one or more [`InstallMethod`]s so the
//! `leankg lsp-install <lang>` CLI can install on the user's host.
//! Detection is best-effort: we try `npm`, `pip`, `cargo`, `brew`,
//! and a manual fallback in that order.

use std::collections::HashMap;
use std::path::Path;

/// One canonical LSP server entry.
#[derive(Debug, Clone)]
pub struct LspServerSpec {
    /// Canonical language id (matches `IndexerConfig.typed_resolve`
    /// keys: `go`, `typescript`, `python`, `rust`, …).
    pub language: &'static str,
    /// Executable name as resolved by `which`. For npm packages this
    /// is the shim name (`typescript-language-server`,
    /// `vscode-langservers-extracted`'s shims, etc.).
    pub command: &'static str,
    /// Args passed to the command. For LSP servers that need
    /// `--stdio` or `serve`, those go here.
    pub args: &'static [&'static str],
    /// File extensions handled (without leading dot). Used by
    /// [`detect_language`].
    pub extensions: &'static [&'static str],
    /// Optional fallback language ids. e.g. `javascript` is the same
    /// server as `typescript` in many setups, so its entry lists
    /// `["javascript"]` aliases.
    pub aliases: &'static [&'static str],
    /// How to install this server. Ordered: most-preferred first.
    pub install: &'static [InstallMethod],
}

#[derive(Debug, Clone)]
pub enum InstallMethod {
    /// `npm install -g <package>`
    Npm { package: &'static str },
    /// `pip install <package>` or `pipx install <package>`
    Pip { package: &'static str },
    /// `cargo install <crate>`
    Cargo { crate_name: &'static str },
    /// `brew install <formula>`
    Brew { formula: &'static str },
    /// `go install <pkg>@latest`
    GoInstall { pkg: &'static str },
    /// `gem install <gem>`
    Gem { gem: &'static str },
    /// `opam install <pkg>`
    Opam { pkg: &'static str },
    /// `dotnet tool install -g <tool>`
    Dotnet { tool: &'static str },
    /// Manual instruction printed to user (no automatic install).
    Manual {
        url: &'static str,
        note: &'static str,
    },
}

/// Comprehensive LSP server catalog. One entry per canonical language.
///
/// IMPORTANT: ordering matters — the bridge picks the first entry
/// whose `language` or `aliases` matches the requested id.
pub const ALL_LSP_SERVERS: &[LspServerSpec] = &[
    // === Systems ===
    LspServerSpec {
        language: "go",
        command: "gopls",
        args: &["serve"],
        extensions: &["go"],
        aliases: &["golang"],
        install: &[
            InstallMethod::GoInstall {
                pkg: "golang.org/x/tools/gopls@latest",
            },
            InstallMethod::Brew { formula: "gopls" },
            InstallMethod::Npm { package: "gopls" },
        ],
    },
    LspServerSpec {
        language: "rust",
        command: "rust-analyzer",
        args: &[],
        extensions: &["rs"],
        aliases: &["rs"],
        install: &[
            InstallMethod::Brew {
                formula: "rust-analyzer",
            },
            InstallMethod::Cargo {
                crate_name: "rust-analyzer",
            },
            InstallMethod::Npm {
                package: "@rust-lang/rust-analyzer",
            },
        ],
    },
    LspServerSpec {
        language: "c",
        command: "clangd",
        args: &["--background-index"],
        extensions: &["c", "h"],
        aliases: &[],
        install: &[
            InstallMethod::Brew { formula: "clangd" },
            InstallMethod::Npm { package: "clangd" },
        ],
    },
    LspServerSpec {
        language: "cpp",
        command: "clangd",
        args: &["--background-index"],
        extensions: &["cpp", "cxx", "cc", "c++", "hpp", "hxx", "hh", "h"],
        aliases: &["c++", "cxx"],
        install: &[
            InstallMethod::Brew { formula: "clangd" },
            InstallMethod::Npm { package: "clangd" },
        ],
    },
    LspServerSpec {
        language: "zig",
        command: "zls",
        args: &[],
        extensions: &["zig"],
        aliases: &[],
        install: &[
            InstallMethod::Brew { formula: "zls" },
            InstallMethod::Manual {
                url: "https://github.com/zigtools/zls",
                note: "Install ZLS via your package manager",
            },
        ],
    },
    LspServerSpec {
        language: "nim",
        command: "nimlangserver",
        args: &[],
        extensions: &["nim"],
        aliases: &[],
        install: &[
            InstallMethod::Npm {
                package: "nimlangserver",
            },
            InstallMethod::Brew {
                formula: "nimlangserver",
            },
        ],
    },
    LspServerSpec {
        language: "crystal",
        command: "crystalline",
        args: &[],
        extensions: &["cr"],
        aliases: &[],
        install: &[InstallMethod::Manual {
            url: "https://github.com/elbywan/crystalline",
            note: "crystalline is distributed as a binary release",
        }],
    },
    // === JVM ===
    LspServerSpec {
        language: "java",
        command: "jdtls",
        args: &[],
        extensions: &["java"],
        aliases: &[],
        install: &[
            InstallMethod::Brew { formula: "jdtls" },
            InstallMethod::Npm {
                package: "java-language-server",
            },
        ],
    },
    LspServerSpec {
        language: "kotlin",
        command: "kotlin-language-server",
        args: &[],
        extensions: &["kt", "kts"],
        aliases: &[],
        install: &[
            InstallMethod::Brew {
                formula: "kotlin-language-server",
            },
            InstallMethod::Npm {
                package: "kotlin-language-server",
            },
        ],
    },
    LspServerSpec {
        language: "scala",
        command: "metals",
        args: &[],
        extensions: &["scala", "sbt"],
        aliases: &[],
        install: &[
            InstallMethod::Brew { formula: "metals" },
            InstallMethod::Manual {
                url: "https://scalameta.org/metals/docs/editors/install.html",
                note: "Install Metals via Coursier: cs install metals",
            },
        ],
    },
    LspServerSpec {
        language: "clojure",
        command: "clojure-lsp",
        args: &[],
        extensions: &["clj", "cljs", "cljc", "edn"],
        aliases: &["clj"],
        install: &[
            InstallMethod::Brew {
                formula: "clojure-lsp/brew/clojure-lsp",
            },
            InstallMethod::Npm {
                package: "clojure-lsp",
            },
        ],
    },
    // === Web / Scripting ===
    LspServerSpec {
        language: "typescript",
        command: "typescript-language-server",
        args: &["--stdio"],
        extensions: &["ts", "tsx", "mts", "cts"],
        aliases: &["ts"],
        install: &[
            InstallMethod::Npm {
                package: "typescript-language-server",
            },
            InstallMethod::Brew {
                formula: "typescript-language-server",
            },
        ],
    },
    LspServerSpec {
        language: "javascript",
        command: "typescript-language-server",
        args: &["--stdio"],
        extensions: &["js", "jsx", "mjs", "cjs"],
        aliases: &["js"],
        install: &[
            InstallMethod::Npm {
                package: "typescript-language-server",
            },
            InstallMethod::Brew {
                formula: "typescript-language-server",
            },
        ],
    },
    LspServerSpec {
        language: "vue",
        command: "vue-language-server",
        args: &["--stdio"],
        extensions: &["vue"],
        aliases: &[],
        install: &[InstallMethod::Npm {
            package: "@vue/language-server",
        }],
    },
    LspServerSpec {
        language: "svelte",
        command: "svelteserver",
        args: &["--stdio"],
        extensions: &["svelte"],
        aliases: &[],
        install: &[InstallMethod::Npm {
            package: "svelte-language-server",
        }],
    },
    LspServerSpec {
        language: "html",
        command: "vscode-html-language-server",
        args: &["--stdio"],
        extensions: &["html", "htm"],
        aliases: &[],
        install: &[InstallMethod::Npm {
            package: "vscode-langservers-extracted",
        }],
    },
    LspServerSpec {
        language: "css",
        command: "vscode-css-language-server",
        args: &["--stdio"],
        extensions: &["css", "scss", "less"],
        aliases: &["scss", "less"],
        install: &[InstallMethod::Npm {
            package: "vscode-langservers-extracted",
        }],
    },
    LspServerSpec {
        language: "json",
        command: "vscode-json-language-server",
        args: &["--stdio"],
        extensions: &["json", "jsonc"],
        aliases: &["jsonc"],
        install: &[InstallMethod::Npm {
            package: "vscode-langservers-extracted",
        }],
    },
    LspServerSpec {
        language: "yaml",
        command: "yaml-language-server",
        args: &["--stdio"],
        extensions: &["yaml", "yml"],
        aliases: &["yml"],
        install: &[InstallMethod::Npm {
            package: "yaml-language-server",
        }],
    },
    LspServerSpec {
        language: "xml",
        command: "lemminx",
        args: &[],
        extensions: &["xml", "xsl", "xslt", "svg"],
        aliases: &["xsl", "xslt", "svg"],
        install: &[
            InstallMethod::Brew { formula: "lemminx" },
            InstallMethod::Npm { package: "lemminx" },
        ],
    },
    // === Scripting ===
    LspServerSpec {
        language: "python",
        command: "pylsp",
        args: &[],
        extensions: &["py", "pyi"],
        aliases: &["py"],
        install: &[
            InstallMethod::Pip {
                package: "python-lsp-server",
            },
            InstallMethod::Brew {
                formula: "python-lsp-server",
            },
        ],
    },
    LspServerSpec {
        language: "ruby",
        command: "solargraph",
        args: &[],
        extensions: &["rb", "erb", "rake"],
        aliases: &["rb"],
        install: &[
            InstallMethod::Brew {
                formula: "solargraph",
            },
            InstallMethod::Gem { gem: "solargraph" },
        ],
    },
    LspServerSpec {
        language: "php",
        command: "intelephense",
        args: &["--stdio"],
        extensions: &["php", "phtml"],
        aliases: &[],
        install: &[InstallMethod::Npm {
            package: "intelephense",
        }],
    },
    LspServerSpec {
        language: "lua",
        command: "lua-language-server",
        args: &[],
        extensions: &["lua"],
        aliases: &[],
        install: &[
            InstallMethod::Brew {
                formula: "lua-language-server",
            },
            InstallMethod::Npm {
                package: "lua-language-server",
            },
        ],
    },
    LspServerSpec {
        language: "bash",
        command: "bash-language-server",
        args: &["start"],
        extensions: &["sh", "bash", "zsh"],
        aliases: &["sh", "zsh"],
        install: &[InstallMethod::Npm {
            package: "bash-language-server",
        }],
    },
    LspServerSpec {
        language: "powershell",
        command: "powershell-es",
        args: &[],
        extensions: &["ps1", "psm1"],
        aliases: &["ps1"],
        install: &[InstallMethod::Npm {
            package: "powershell-editor-services",
        }],
    },
    // === Functional ===
    LspServerSpec {
        language: "haskell",
        command: "haskell-language-server-wrapper",
        args: &["--lsp"],
        extensions: &["hs"],
        aliases: &["hs"],
        install: &[
            InstallMethod::Brew {
                formula: "haskell-language-server",
            },
            InstallMethod::Manual {
                url: "https://haskell-language-server.readthedocs.io",
                note: "Install via ghcup / stack",
            },
        ],
    },
    LspServerSpec {
        language: "elm",
        command: "elm-language-server",
        args: &[],
        extensions: &["elm"],
        aliases: &[],
        install: &[InstallMethod::Npm {
            package: "elm-language-server",
        }],
    },
    LspServerSpec {
        language: "ocaml",
        command: "ocamllsp",
        args: &[],
        extensions: &["ml", "mli"],
        aliases: &["ml"],
        install: &[
            InstallMethod::Brew {
                formula: "ocaml-lsp",
            },
            InstallMethod::Opam {
                pkg: "ocaml-lsp-server",
            },
        ],
    },
    LspServerSpec {
        language: "fsharp",
        command: "fsautocomplete",
        args: &[],
        extensions: &["fs", "fsx", "fsi"],
        aliases: &["fs"],
        install: &[InstallMethod::Dotnet {
            tool: "fsautocomplete",
        }],
    },
    LspServerSpec {
        language: "elixir",
        command: "elixir-ls",
        args: &[],
        extensions: &["ex", "exs"],
        aliases: &["ex"],
        install: &[
            InstallMethod::Brew {
                formula: "elixir-ls",
            },
            InstallMethod::Manual {
                url: "https://github.com/elixir-lsp/elixir-ls",
                note: "Install via mix archive",
            },
        ],
    },
    LspServerSpec {
        language: "erlang",
        command: "erlang_ls",
        args: &[],
        extensions: &["erl", "hrl"],
        aliases: &["erl"],
        install: &[InstallMethod::Brew {
            formula: "erlang-language-server",
        }],
    },
    // === Data / DB ===
    LspServerSpec {
        language: "sql",
        command: "sqls",
        args: &[],
        extensions: &["sql"],
        aliases: &[],
        install: &[
            InstallMethod::Brew { formula: "sqls" },
            InstallMethod::GoInstall {
                pkg: "github.com/sqls-server/sqls@latest",
            },
        ],
    },
    LspServerSpec {
        language: "r",
        command: "languageserver",
        args: &[],
        extensions: &["r", "R"],
        aliases: &[],
        install: &[InstallMethod::Manual {
            url: "https://github.com/REditorSupport/languageserver",
            note: "R: install.packages('languageserver')",
        }],
    },
    // === Mobile ===
    LspServerSpec {
        language: "swift",
        command: "sourcekit-lsp",
        args: &[],
        extensions: &["swift"],
        aliases: &[],
        install: &[InstallMethod::Brew {
            formula: "sourcekit-lsp",
        }],
    },
    LspServerSpec {
        language: "dart",
        command: "dart-language-server",
        args: &["--protocol=lsp"],
        extensions: &["dart"],
        aliases: &[],
        install: &[InstallMethod::Brew { formula: "dart" }],
    },
    LspServerSpec {
        language: "kotlin-android",
        command: "kotlin-language-server",
        args: &[],
        extensions: &[],
        aliases: &["android-kotlin"],
        install: &[InstallMethod::Brew {
            formula: "kotlin-language-server",
        }],
    },
    // === Other ===
    LspServerSpec {
        language: "markdown",
        command: "marksman",
        args: &["server"],
        extensions: &["md", "markdown"],
        aliases: &["md"],
        install: &[
            InstallMethod::Brew {
                formula: "marksman",
            },
            InstallMethod::Manual {
                url: "https://github.com/artempyanykh/marksman",
                note: "Install marksman binary",
            },
        ],
    },
    LspServerSpec {
        language: "toml",
        command: "taplo",
        args: &["lsp", "stdio"],
        extensions: &["toml"],
        aliases: &[],
        install: &[
            InstallMethod::Brew { formula: "taplo" },
            InstallMethod::Cargo {
                crate_name: "taplo-lsp",
            },
        ],
    },
    LspServerSpec {
        language: "graphql",
        command: "graphql-lsp",
        args: &["server", "-m", "stream"],
        extensions: &["graphql", "gql"],
        aliases: &["gql"],
        install: &[InstallMethod::Npm {
            package: "graphql-lsp",
        }],
    },
    LspServerSpec {
        language: "terraform",
        command: "terraform-ls",
        args: &["serve"],
        extensions: &["tf", "hcl"],
        aliases: &["hcl", "tf"],
        install: &[InstallMethod::Brew {
            formula: "hashicorp/tap/terraform-ls",
        }],
    },
    LspServerSpec {
        language: "dockerfile",
        command: "docker-langserver",
        args: &["--stdio"],
        extensions: &["dockerfile", "Dockerfile"],
        aliases: &[],
        install: &[InstallMethod::Npm {
            package: "dockerfile-language-server",
        }],
    },
    LspServerSpec {
        language: "protobuf",
        command: "buf",
        args: &["beta", "lsp"],
        extensions: &["proto"],
        aliases: &["proto"],
        install: &[InstallMethod::Brew { formula: "buf" }],
    },
    LspServerSpec {
        language: "solidity",
        command: "solidity-ls",
        args: &["--stdio"],
        extensions: &["sol"],
        aliases: &["sol"],
        install: &[InstallMethod::Npm {
            package: "solidity-ls",
        }],
    },
];

/// Extra install methods we accept even though they aren't in
/// `InstallMethod` (kept separate so the registry stays small but
/// still practical).
#[derive(Debug, Clone)]
#[allow(dead_code)]
pub enum ExtraInstallMethod {
    Gem { gem: &'static str },
    Opam { pkg: &'static str },
    Dotnet { tool: &'static str },
}

impl InstallMethod {
    /// Hint string for `leankg lsp-install <lang>`.
    pub fn hint(&self) -> String {
        match self {
            InstallMethod::Npm { package } => format!("npm install -g {package}"),
            InstallMethod::Pip { package } => format!("pip install {package}"),
            InstallMethod::Cargo { crate_name } => format!("cargo install {crate_name}"),
            InstallMethod::Brew { formula } => format!("brew install {formula}"),
            InstallMethod::GoInstall { pkg } => format!("go install {pkg}"),
            InstallMethod::Gem { gem } => format!("gem install {gem}"),
            InstallMethod::Opam { pkg } => format!("opam install {pkg}"),
            InstallMethod::Dotnet { tool } => format!("dotnet tool install -g {tool}"),
            InstallMethod::Manual { url, note } => format!("{note} ({url})"),
        }
    }

    /// Whether this method can run silently on the user's machine
    /// without interactive prompts.
    pub fn is_automatic(&self) -> bool {
        !matches!(self, InstallMethod::Manual { .. })
    }
}

impl LspServerSpec {
    /// Look up a spec by canonical language id OR by an alias.
    pub fn for_language(lang: &str) -> Option<&'static LspServerSpec> {
        let needle = lang.to_lowercase();
        ALL_LSP_SERVERS
            .iter()
            .find(|s| s.language == needle || s.aliases.iter().any(|a| *a == needle))
    }
}

/// Best-effort language detection from a file path. Returns the
/// canonical language id (e.g. "typescript") suitable for
/// [`LspServerSpec::for_language`].
///
/// Returns `None` for files whose extension we don't recognize; the
/// bridge falls back to tree-sitter in that case.
pub fn detect_language(path: &Path) -> Option<&'static str> {
    let ext = path.extension()?.to_string_lossy().to_lowercase();
    detect_language_by_extension(&ext)
}

pub fn detect_language_by_extension(ext: &str) -> Option<&'static str> {
    for spec in ALL_LSP_SERVERS {
        if spec.extensions.contains(&ext) {
            return Some(spec.language);
        }
    }
    // Special-case filenames where the extension isn't enough.
    if ext.is_empty() {
        return None;
    }
    None
}

/// Returns all (language, command) pairs the registry knows about.
/// Used by `leankg lsp-install all` and the docs page.
pub fn all_languages() -> Vec<(&'static str, &'static str)> {
    ALL_LSP_SERVERS
        .iter()
        .map(|s| (s.language, s.command))
        .collect()
}

/// Build a `LspServerConfig` for `language` using the registry
/// defaults. Returns `None` when the language is not in the catalog.
pub fn default_server_config(language: &str) -> Option<crate::lsp::config::LspServerConfig> {
    let spec = LspServerSpec::for_language(language)?;
    Some(crate::lsp::config::LspServerConfig {
        command: spec.command.to_string(),
        args: spec.args.iter().map(|s| s.to_string()).collect(),
        extensions: spec.extensions.iter().map(|s| s.to_string()).collect(),
        initialization_options: None,
    })
}

/// Map a list of supported languages into an `LspConfig` keyed by
/// language. Languages whose command is already available on the
/// PATH are included; missing ones are skipped with a warning
/// printed to stderr unless `include_missing` is true.
pub fn auto_config(include_missing: bool) -> (crate::lsp::config::LspConfig, Vec<String>) {
    let mut cfg = crate::lsp::config::LspConfig::default();
    let mut missing = Vec::new();
    for spec in ALL_LSP_SERVERS {
        if command_on_path(spec.command) {
            if let Some(server_cfg) = default_server_config(spec.language) {
                cfg.servers.insert(spec.language.to_string(), server_cfg);
            }
        } else if include_missing {
            if let Some(server_cfg) = default_server_config(spec.language) {
                cfg.servers.insert(spec.language.to_string(), server_cfg);
            }
            missing.push(spec.language.to_string());
        }
    }
    (cfg, missing)
}

fn command_on_path(cmd: &str) -> bool {
    if let Ok(paths) = std::env::var("PATH") {
        for dir in paths.split(':') {
            if dir.is_empty() {
                continue;
            }
            let candidate = std::path::Path::new(dir).join(cmd);
            if candidate.is_file() {
                return true;
            }
        }
    }
    false
}

/// Build an `LspConfig` map keyed by *extension* for fast lookup from
/// file paths. This is what `auto_detect_for_path` uses.
pub fn extension_table() -> HashMap<String, &'static str> {
    let mut out = HashMap::new();
    for spec in ALL_LSP_SERVERS {
        for ext in spec.extensions {
            out.insert((*ext).to_string(), spec.language);
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lookup_by_canonical_name() {
        let s = LspServerSpec::for_language("go").unwrap();
        assert_eq!(s.command, "gopls");
    }

    #[test]
    fn lookup_by_alias() {
        let s = LspServerSpec::for_language("ts").unwrap();
        assert_eq!(s.language, "typescript");
    }

    #[test]
    fn lookup_unknown_language_returns_none() {
        assert!(LspServerSpec::for_language("klingon").is_none());
    }

    #[test]
    fn detect_language_from_extension() {
        assert_eq!(detect_language_by_extension("go"), Some("go"));
        assert_eq!(detect_language_by_extension("ts"), Some("typescript"));
        assert_eq!(detect_language_by_extension("tsx"), Some("typescript"));
        assert_eq!(detect_language_by_extension("rs"), Some("rust"));
        assert_eq!(detect_language_by_extension("kt"), Some("kotlin"));
        assert_eq!(detect_language_by_extension("py"), Some("python"));
        assert_eq!(detect_language_by_extension("vue"), Some("vue"));
        assert_eq!(detect_language_by_extension("svelte"), Some("svelte"));
        assert_eq!(detect_language_by_extension("sol"), Some("solidity"));
    }

    #[test]
    fn detect_unknown_extension_returns_none() {
        assert_eq!(detect_language_by_extension("unknownext"), None);
    }

    #[test]
    fn detect_language_from_path() {
        let p = std::path::Path::new("/foo/bar/baz.tsx");
        assert_eq!(detect_language(p), Some("typescript"));
    }

    #[test]
    fn catalog_includes_common_languages() {
        // Sanity-check that the catalog covers the languages our
        // existing integration tests exercise.
        for lang in &["go", "typescript", "python", "rust", "kotlin"] {
            assert!(
                LspServerSpec::for_language(lang).is_some(),
                "catalog missing {}",
                lang
            );
        }
    }

    #[test]
    fn install_methods_are_non_empty() {
        for spec in ALL_LSP_SERVERS {
            assert!(
                !spec.install.is_empty(),
                "spec {} has no install methods",
                spec.language
            );
        }
    }

    #[test]
    fn default_server_config_has_extensions() {
        let cfg = default_server_config("go").unwrap();
        assert_eq!(cfg.command, "gopls");
        assert!(cfg.extensions.contains(&"go".to_string()));
    }

    #[test]
    fn auto_config_runs_without_panic() {
        // We can't assert what's on PATH in CI, just that the call
        // doesn't panic and returns a valid config.
        let (cfg, missing) = auto_config(false);
        assert!(cfg.timeout_ms > 0);
        let _ = missing;
    }

    #[test]
    fn extension_table_has_known_entries() {
        let t = extension_table();
        assert_eq!(t.get("go").copied(), Some("go"));
        assert_eq!(t.get("ts").copied(), Some("typescript"));
        assert_eq!(t.get("rs").copied(), Some("rust"));
    }

    #[test]
    fn all_languages_lists_every_spec() {
        let v = all_languages();
        assert!(v.iter().any(|(l, _)| *l == "go"));
        assert!(v.iter().any(|(l, _)| *l == "rust"));
        assert!(v.iter().any(|(l, _)| *l == "typescript"));
        assert!(v.len() >= 30, "catalog should cover many languages");
    }

    #[test]
    fn install_hint_contains_package_name() {
        let m = InstallMethod::Npm { package: "gopls" };
        assert!(m.hint().contains("gopls"));
        let m = InstallMethod::Brew { formula: "gopls" };
        assert!(m.hint().contains("gopls"));
    }

    #[test]
    fn manual_install_is_not_automatic() {
        let m = InstallMethod::Manual {
            url: "https://example.com",
            note: "manual",
        };
        assert!(!m.is_automatic());
    }
}
