//! Single source of truth for language support: extension ↔ canonical name ↔
//! tree-sitter grammar ↔ node-kind patterns. Replaces the five parallel
//! fixed-size dispatch tables that previously lived in mod.rs / parser.rs /
//! extractor.rs / call_graph.rs.

use tree_sitter::{Language, Parser};
#[cfg(feature = "lang-extras")]
use tree_sitter_cuda;
#[cfg(feature = "lang-extras")]
use tree_sitter_glsl;
#[cfg(feature = "lang-extras")]
use tree_sitter_hlsl;
#[cfg(feature = "lang-extras")]
use tree_sitter_qsharp;
#[cfg(feature = "lang-extras")]
use tree_sitter_systemverilog;
#[cfg(feature = "lang-extras")]
use tree_sitter_verilog;

/// Grammar for a `lang-extras` language. Compiles to `Some(producer)` when the
/// feature is on, `None` when off (--no-default-features, the slim Docker core
/// build). The spec row stays in LANG_SPECS either way; only the grammar is
/// dropped, so `language_for_path` still maps the extension but no parser loads.
///
/// `$prod` is a plain fn turning the crate's `LANGUAGE` const into a
/// `tree_sitter::Language`; `$wrap` is the const fn referenced from the static
/// LANG_SPECS `grammar:` field (statics only allow const calls, hence the
/// `const fn` — a closure would also fail here).
macro_rules! lang_extras_grammar {
    ($prod:ident, $wrap:ident, $lang_expr:path) => {
        #[cfg(feature = "lang-extras")]
        fn $prod() -> Language {
            $lang_expr.into()
        }
        #[cfg(feature = "lang-extras")]
        const fn $wrap() -> Option<fn() -> Language> {
            Some($prod)
        }
        #[cfg(not(feature = "lang-extras"))]
        const fn $wrap() -> Option<fn() -> Language> {
            None
        }
    };
}

lang_extras_grammar!(scala_lang, scala_grammar, tree_sitter_scala::LANGUAGE);
lang_extras_grammar!(zig_lang, zig_grammar, tree_sitter_zig::LANGUAGE);
lang_extras_grammar!(
    solidity_lang,
    solidity_grammar,
    tree_sitter_solidity::LANGUAGE
);
lang_extras_grammar!(lua_lang, lua_grammar, tree_sitter_lua::LANGUAGE);
lang_extras_grammar!(json_lang, json_grammar, tree_sitter_json::LANGUAGE);
lang_extras_grammar!(yaml_lang, yaml_grammar, tree_sitter_yaml::LANGUAGE);
lang_extras_grammar!(css_lang, css_grammar, tree_sitter_css::LANGUAGE);
lang_extras_grammar!(html_lang, html_grammar, tree_sitter_html::LANGUAGE);
lang_extras_grammar!(graphql_lang, graphql_grammar, tree_sitter_graphql::LANGUAGE);
lang_extras_grammar!(proto_lang, proto_grammar, tree_sitter_proto::LANGUAGE);
lang_extras_grammar!(csharp_lang, csharp_grammar, tree_sitter_c_sharp::LANGUAGE);
lang_extras_grammar!(haskell_lang, haskell_grammar, tree_sitter_haskell::LANGUAGE);
lang_extras_grammar!(elm_lang, elm_grammar, tree_sitter_elm::LANGUAGE);
lang_extras_grammar!(ocaml_lang, ocaml_grammar, tree_sitter_ocaml::LANGUAGE_OCAML);
lang_extras_grammar!(
    fsharp_lang,
    fsharp_grammar,
    tree_sitter_fsharp::LANGUAGE_FSHARP
);
lang_extras_grammar!(erlang_lang, erlang_grammar, tree_sitter_erlang::LANGUAGE);
lang_extras_grammar!(nim_lang, nim_grammar, tree_sitter_nim::LANGUAGE);
lang_extras_grammar!(
    powershell_lang,
    powershell_grammar,
    tree_sitter_powershell::LANGUAGE
);
lang_extras_grammar!(crystal_lang, crystal_grammar, tree_sitter_crystal::LANGUAGE);
lang_extras_grammar!(cuda_lang, cuda_grammar, tree_sitter_cuda::LANGUAGE);
lang_extras_grammar!(hlsl_lang, hlsl_grammar, tree_sitter_hlsl::LANGUAGE_HLSL);
lang_extras_grammar!(glsl_lang, glsl_grammar, tree_sitter_glsl::LANGUAGE_GLSL);
lang_extras_grammar!(verilog_lang, verilog_grammar, tree_sitter_verilog::LANGUAGE);
lang_extras_grammar!(
    systemverilog_lang,
    systemverilog_grammar,
    tree_sitter_systemverilog::LANGUAGE
);
lang_extras_grammar!(qsharp_lang, qsharp_grammar, tree_sitter_qsharp::LANGUAGE);

/// Extraction fidelity tier.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Tier {
    /// Functions/classes/interfaces/properties/imports + calls.
    Full,
    /// Functions + classes; imports via generic string-literal scan.
    Decl,
    /// File-level element + generic import scan (config/data languages).
    Minimal,
}

/// Per-language tree-sitter node-kind sets. Each language names its own set so
/// shared node kinds (e.g. `type_declaration`, `class`) can mean different
/// things in different grammars.
#[derive(Debug, Clone, Copy)]
pub struct NodeKinds {
    pub functions: &'static [&'static str],
    pub classes: &'static [&'static str],
    pub interfaces: &'static [&'static str],
    pub properties: &'static [&'static str],
    pub imports: &'static [&'static str],
    pub calls: &'static [&'static str],
}

const EMPTY: &[&str] = &[];

/// Static spec for one language. One row drives every dispatch point.
pub struct LanguageSpec {
    pub name: &'static str,
    pub extensions: &'static [&'static str],
    pub config_files: &'static [&'static str],
    pub tier: Tier,
    pub kinds: NodeKinds,
    pub grammar: Option<fn() -> Language>,
}

const FN_DEF: NodeKinds = NodeKinds {
    functions: &["function_definition"],
    classes: &[
        "class_specifier",
        "struct_specifier",
        "enum_specifier",
        "union_specifier",
        "type_definition",
    ],
    interfaces: &["struct_specifier"],
    properties: &["field_declaration"],
    imports: &["preproc_include"],
    calls: &["call_expression"],
};

/// All supported languages. Order is not significant.
pub static LANG_SPECS: &[LanguageSpec] = &[
    LanguageSpec {
        name: "c",
        extensions: &["c", "h"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: FN_DEF,
        grammar: Some(|| tree_sitter_c::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "cpp",
        extensions: &["cpp", "cc", "cxx", "hpp", "hh", "hxx", "h++"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition"],
            classes: &[
                "class_specifier",
                "struct_specifier",
                "enum_specifier",
                "union_specifier",
                "type_definition",
            ],
            interfaces: &["class_specifier"],
            properties: &["field_declaration"],
            imports: &[
                "preproc_include",
                "using_declaration",
                "namespace_definition",
            ],
            calls: &["call_expression"],
        },
        grammar: Some(|| tree_sitter_cpp::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "bash",
        extensions: &["sh", "bash", "zsh", "bashrc", "zshrc"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition", "function"],
            classes: EMPTY,
            interfaces: EMPTY,
            properties: &["variable_assignment"],
            imports: EMPTY,
            calls: &["command", "command_name"],
        },
        grammar: Some(|| tree_sitter_bash::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "ruby",
        extensions: &["rb", "ruby", "rake", "gemspec"],
        config_files: &["Gemfile"],
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["method", "singleton_method"],
            classes: &["class", "module"],
            interfaces: EMPTY,
            properties: &["assignment"],
            imports: &["require", "require_relative"],
            calls: &["method_call", "call", "command", "command_call"],
        },
        grammar: Some(|| tree_sitter_ruby::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "php",
        extensions: &["php", "phtml"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition", "method_declaration"],
            classes: &[
                "class_declaration",
                "interface_declaration",
                "trait_declaration",
                "enum_declaration",
                "class",
            ],
            interfaces: &["interface_declaration"],
            properties: &["property_declaration"],
            imports: &[
                "namespace_use_declaration",
                "namespace_use_clause",
                "require",
                "require_once",
                "require_expression",
            ],
            calls: &["function_call_expression"],
        },
        grammar: Some(|| tree_sitter_php::LANGUAGE_PHP.into()),
    },
    LanguageSpec {
        name: "perl",
        extensions: &["pl", "pm", "t"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &[
                "function_definition",
                "function_definition_without_sub",
                "sub",
            ],
            classes: &["package_statement", "package"],
            interfaces: EMPTY,
            properties: &["variable_declaration"],
            imports: &[
                "require",
                "require_statement",
                "use_statement",
                "use_no_subs_statement",
            ],
            calls: &[
                "method_invocation",
                "call_expression_with_args_with_brackets",
                "call_expression_with_bareword",
            ],
        },
        grammar: Some(|| tree_sitter_perl::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "r",
        extensions: &["r", "R", "rdata"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition"],
            classes: EMPTY,
            interfaces: EMPTY,
            properties: &["assignment"],
            imports: &["library", "require"],
            calls: &["call"],
        },
        grammar: Some(|| tree_sitter_r::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "elixir",
        extensions: &["ex", "exs"],
        config_files: &["mix.exs"],
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["def", "defp", "defmacro", "defmacrop", "defguard"],
            classes: &["module", "defmodule", "defprotocol", "defimpl"],
            interfaces: EMPTY,
            properties: EMPTY,
            imports: &["require", "import", "alias", "use"],
            calls: &["call"],
        },
        grammar: Some(|| tree_sitter_elixir::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "scala",
        extensions: &["scala", "sc"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition", "function_declaration", "def"],
            classes: &[
                "class_definition",
                "object_definition",
                "trait_definition",
                "enum_definition",
                "extension_definition",
                "given_definition",
                "class",
                "object",
                "trait",
            ],
            interfaces: &["trait_definition"],
            properties: &["class_parameter"],
            imports: &["import_declaration", "import"],
            calls: &["generic_function", "function_definition"],
        },
        grammar: scala_grammar(),
    },
    LanguageSpec {
        name: "zig",
        extensions: &["zig"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &[
                "function_declaration",
                "function_signature",
                "fn",
                "test_declaration",
            ],
            classes: &["struct_declaration", "enum_declaration", "struct", "enum"],
            interfaces: EMPTY,
            properties: EMPTY,
            imports: &["using_namespace_declaration", "usingnamespace"],
            calls: &["builtin_function", "function_call"],
        },
        grammar: zig_grammar(),
    },
    LanguageSpec {
        name: "solidity",
        extensions: &["sol"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition", "constructor_definition"],
            classes: &[
                "contract_declaration",
                "interface_declaration",
                "library_declaration",
                "struct_declaration",
                "enum_declaration",
                "contract",
                "interface",
                "library",
                "struct",
            ],
            interfaces: &["interface_declaration"],
            properties: &["state_variable_declaration"],
            imports: &["import_directive", "import"],
            calls: &["function_call_expression", "call"],
        },
        grammar: solidity_grammar(),
    },
    LanguageSpec {
        name: "lua",
        extensions: &["lua"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition", "function_declaration"],
            classes: EMPTY,
            interfaces: EMPTY,
            properties: &["assignment"],
            imports: &["require"],
            calls: &["function_call", "function"],
        },
        grammar: lua_grammar(),
    },
    LanguageSpec {
        name: "json",
        extensions: &["json", "jsonc"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: json_grammar(),
    },
    LanguageSpec {
        name: "toml",
        extensions: &["toml"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        // No grammar: tree-sitter-toml v0.20 pins an incompatible tree-sitter.
        grammar: None,
    },
    LanguageSpec {
        name: "yaml",
        extensions: &["yaml", "yml"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: yaml_grammar(),
    },
    LanguageSpec {
        name: "css",
        extensions: &["css", "scss"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: css_grammar(),
    },
    LanguageSpec {
        name: "html",
        extensions: &["html", "htm"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: html_grammar(),
    },
    LanguageSpec {
        name: "graphql",
        extensions: &["graphql", "gql"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: graphql_grammar(),
    },
    LanguageSpec {
        name: "protobuf",
        extensions: &["proto"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: proto_grammar(),
    },
    LanguageSpec {
        name: "dockerfile",
        extensions: &["dockerfile", "Dockerfile"],
        config_files: &["Dockerfile"],
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        // No grammar: tree-sitter-dockerfile v0.2 pins an incompatible tree-sitter.
        grammar: None,
    },
    LanguageSpec {
        name: "csharp",
        extensions: &["cs"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: &["method_declaration", "local_function_statement"],
            classes: &[
                "class_declaration",
                "interface_declaration",
                "struct_declaration",
                "record_declaration",
            ],
            interfaces: &["interface_declaration"],
            properties: &["property_declaration"],
            imports: &["using_directive"],
            calls: &["invocation_expression", "object_creation_expression"],
        },
        grammar: csharp_grammar(),
    },
    LanguageSpec {
        name: "haskell",
        extensions: &["hs", "lhs"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function", "function_signature"],
            classes: &["class_decl", "data_type", "data_constructor"],
            interfaces: EMPTY,
            properties: &["constructor"],
            imports: &["import"],
            calls: &["function_call_expression"],
        },
        grammar: haskell_grammar(),
    },
    LanguageSpec {
        name: "elm",
        extensions: &["elm"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_declaration_left"],
            classes: &[
                "type_declaration",
                "type_alias_declaration",
                "module_declaration",
            ],
            interfaces: EMPTY,
            properties: EMPTY,
            imports: &["import_clause", "import"],
            calls: &["function_call_expr"],
        },
        grammar: elm_grammar(),
    },
    LanguageSpec {
        name: "ocaml",
        extensions: &["ml", "mli"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["value_definition", "external", "function_expression"],
            classes: &[
                "class_definition",
                "class_binding",
                "module_binding",
                "module_definition",
            ],
            interfaces: EMPTY,
            properties: &["field_definition"],
            imports: &["open", "include", "module_binding"],
            calls: &["application_expression", "call_expression"],
        },
        grammar: ocaml_grammar(),
    },
    LanguageSpec {
        name: "fsharp",
        extensions: &["fs", "fsi", "fsx"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_declaration_left", "value_definition"],
            classes: &[
                "type_definition",
                "class_definition",
                "module_definition",
                "module",
            ],
            interfaces: EMPTY,
            properties: &["member_definition"],
            imports: &["open", "open_declaration", "import"],
            calls: &["function_call_expression"],
        },
        grammar: fsharp_grammar(),
    },
    LanguageSpec {
        name: "erlang",
        extensions: &["erl", "hrl"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["fun_decl", "function_clause"],
            classes: &["module", "module_attribute"],
            interfaces: EMPTY,
            properties: EMPTY,
            imports: &[
                "include_attribute",
                "include_lib_attribute",
                "import_attribute",
                "import",
            ],
            calls: &["call", "external_fun"],
        },
        grammar: erlang_grammar(),
    },
    LanguageSpec {
        name: "nim",
        extensions: &["nim", "nims"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["func_declaration", "proc_declaration", "func", "proc"],
            classes: &["type_declaration", "type"],
            interfaces: EMPTY,
            properties: &["let_declaration"],
            imports: &["import_declaration", "import"],
            calls: &["call"],
        },
        grammar: nim_grammar(),
    },
    LanguageSpec {
        name: "powershell",
        extensions: &["ps1", "psm1", "psd1"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_statement", "class_method_definition"],
            classes: &["class_statement"],
            interfaces: EMPTY,
            properties: &["class_property_definition"],
            imports: &["using_statement", "import_module"],
            calls: &["command", "call_expression"],
        },
        grammar: powershell_grammar(),
    },
    LanguageSpec {
        name: "crystal",
        extensions: &["cr"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["method_definition", "def", "fun"],
            classes: &["class_declaration", "class"],
            interfaces: EMPTY,
            properties: &["instance_var", "class_var"],
            imports: &["require", "import"],
            calls: &["call"],
        },
        grammar: crystal_grammar(),
    },
    LanguageSpec {
        name: "toml",
        extensions: &["toml"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        // No grammar: tree-sitter-toml v0.20 returns an incompatible tree-sitter
        // Language type. File-level element + regex import scan still apply.
        grammar: None,
    },
    LanguageSpec {
        name: "dockerfile",
        extensions: &["dockerfile", "Dockerfile"],
        config_files: &["Dockerfile"],
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        // No grammar: tree-sitter-dockerfile v0.2 returns an incompatible
        // tree-sitter Language type. File-level element + regex import scan.
        grammar: None,
    },
    LanguageSpec {
        name: "v",
        extensions: &["v", "vsh", "vv"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "odin",
        extensions: &["odin"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "gleam",
        extensions: &["gleam"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "agda",
        extensions: &["agda", "lagda"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "fortran",
        extensions: &["f", "for", "f77", "f90", "f95", "f03", "f08"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "ada",
        extensions: &["adb", "ads"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "julia",
        extensions: &["jl"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "matlab",
        extensions: &["m", "mlx"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "sas",
        extensions: &["sas"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "cmake",
        extensions: &["cmake"],
        config_files: &["CMakeLists.txt"],
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "make",
        extensions: &["mk", "mak"],
        config_files: &["Makefile", "GNUmakefile"],
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "starlark",
        extensions: &["star", "bzl"],
        config_files: &["BUILD", "BUILD.bazel", "WORKSPACE", "MODULE.bazel"],
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "groovy",
        extensions: &["groovy", "gradle"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "jinja",
        extensions: &["jinja", "jinja2", "j2"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "scss",
        extensions: &["scss"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "cuda",
        extensions: &["cu", "cuh"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition"],
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: &["preproc_include", "using_declaration"],
            calls: &["call_expression"],
        },
        grammar: cuda_grammar(),
    },
    LanguageSpec {
        name: "hlsl",
        extensions: &["hlsl", "fx", "fxh", "hlsli"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition"],
            classes: &["class_specifier", "struct_specifier"],
            interfaces: EMPTY,
            properties: EMPTY,
            imports: &["preproc_include"],
            calls: &["call_expression"],
        },
        grammar: hlsl_grammar(),
    },
    LanguageSpec {
        name: "glsl",
        extensions: &["glsl", "vert", "frag", "geom", "tesc", "tese", "comp"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition"],
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: &["call_expression"],
        },
        grammar: glsl_grammar(),
    },
    LanguageSpec {
        name: "verilog",
        extensions: &["v", "vh"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["module_declaration"],
            classes: &["module_declaration"],
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: verilog_grammar(),
    },
    LanguageSpec {
        name: "systemverilog",
        extensions: &["sv", "svh"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["module_declaration", "function_declaration"],
            classes: &["class_declaration"],
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: systemverilog_grammar(),
    },
    LanguageSpec {
        name: "qsharp",
        extensions: &["qs"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["callable_decl"],
            classes: &["namespace_decl"],
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: qsharp_grammar(),
    },
    LanguageSpec {
        name: "vyper",
        extensions: &["vy"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "move",
        extensions: &["move"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "sway",
        extensions: &["sw"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "tact",
        extensions: &["tact"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "fe",
        extensions: &["fe"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "cobol",
        extensions: &["cob", "cbl", "cpy"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "abap",
        extensions: &["abap"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "pl_i",
        extensions: &["pli", "pl1"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "rpg",
        extensions: &["rpg", "rpgle"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "jcl",
        extensions: &["jcl", "cntl", "proc", "job"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "rexx",
        extensions: &["rex", "rexx"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "hlasm",
        extensions: &["asm", "hlasm"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "commonlisp",
        extensions: &["lisp", "lsp", "cl"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "scheme",
        extensions: &["scm", "ss"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "racket",
        extensions: &["rkt"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "elisp",
        extensions: &["el"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "purescript",
        extensions: &["purs"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "idris2",
        extensions: &["idr"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "lean",
        extensions: &["lean"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "coq",
        extensions: &["v"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "msl",
        extensions: &["metal"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "wgsl",
        extensions: &["wgsl"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "vhdl",
        extensions: &["vhdl", "vhd"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "yul",
        extensions: &["yul"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "wasm",
        extensions: &["wat", "wast"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "less",
        extensions: &["less"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "stylus",
        extensions: &["styl", "stylus"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "sass",
        extensions: &["sass"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "handlebars",
        extensions: &["hbs", "handlebars"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "pug",
        extensions: &["pug", "jade"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "slim",
        extensions: &["slim"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "haml",
        extensions: &["haml"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "erb",
        extensions: &["erb"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "ejs",
        extensions: &["ejs"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "liquid",
        extensions: &["liquid"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "twig",
        extensions: &["twig"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "blade",
        extensions: &["blade.php"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "astro",
        extensions: &["astro"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "mdx",
        extensions: &["mdx"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "vue",
        extensions: &["vue"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "svelte",
        extensions: &["svelte"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "python",
        extensions: &["py", "pyi", "pyw"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition"],
            classes: &["class_definition"],
            interfaces: EMPTY,
            properties: &["assignment"],
            imports: &["import_statement"],
            calls: &["call"],
        },
        grammar: Some(|| tree_sitter_python::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "javascript",
        extensions: &["js", "mjs", "cjs"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "typescript",
        extensions: &["ts", "mts", "cts"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition", "function_declaration"],
            classes: &["class_declaration"],
            interfaces: &["interface_declaration"],
            properties: EMPTY,
            imports: &["import_statement"],
            calls: &["call_expression"],
        },
        grammar: Some(|| tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into()),
    },
    LanguageSpec {
        name: "go",
        extensions: &["go"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_declaration", "method_declaration"],
            classes: &["type_declaration"],
            interfaces: &["interface_type"],
            properties: EMPTY,
            imports: &["import_specifier"],
            calls: &["call_expression"],
        },
        grammar: Some(|| tree_sitter_go::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "rust",
        extensions: &["rs"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_item"],
            classes: &["struct_item", "enum_item", "impl_item", "trait_item"],
            interfaces: &["trait_item"],
            properties: &["field_declaration"],
            imports: &["use_declaration"],
            calls: &["call_expression"],
        },
        grammar: Some(|| tree_sitter_rust::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "java",
        extensions: &["java"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["method_declaration"],
            classes: &["class_declaration"],
            interfaces: &["interface_declaration"],
            properties: &["field_declaration"],
            imports: &["import_declaration"],
            calls: &["method_invocation"],
        },
        grammar: Some(|| tree_sitter_java::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "kotlin",
        extensions: &["kt", "kts"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_declaration"],
            classes: &["class_declaration"],
            interfaces: &["interface_declaration"],
            properties: &["property_declaration"],
            imports: &["import"],
            calls: &["call_expression"],
        },
        grammar: Some(|| tree_sitter_kotlin_ng::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "dart",
        extensions: &["dart"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_declaration"],
            classes: &["class_declaration", "mixin_declaration"],
            interfaces: &["class_declaration"],
            properties: &["field_declaration"],
            imports: &["import"],
            calls: &["function_expression"],
        },
        grammar: Some(|| tree_sitter_dart::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "swift",
        extensions: &["swift"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_declaration"],
            classes: &["class_declaration", "struct_declaration"],
            interfaces: &["protocol_declaration"],
            properties: &["property_declaration"],
            imports: &["import_declaration"],
            calls: &["call_expression"],
        },
        grammar: Some(|| tree_sitter_swift::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "objc",
        extensions: &["m", "mm", "h"],
        config_files: EMPTY,
        tier: Tier::Full,
        kinds: NodeKinds {
            functions: &["function_definition"],
            classes: &["interface_declaration", "implementation_declaration"],
            interfaces: EMPTY,
            properties: &["property_declaration"],
            imports: &["preproc_include"],
            calls: &["message_send"],
        },
        grammar: Some(|| tree_sitter_objc::LANGUAGE.into()),
    },
    LanguageSpec {
        name: "clojure",
        extensions: &["clj", "cljs", "cljc", "edn"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "vb",
        extensions: &["vb", "vbnet"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "hare",
        extensions: &["ha", "hare"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "haxe",
        extensions: &["hx", "hxml"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "carbon",
        extensions: &["carbon"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "mojo",
        extensions: &["mojo"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "jai",
        extensions: &["jai"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "vim",
        extensions: &["vim"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "lisp",
        extensions: &["lisp", "lsp"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "pascal",
        extensions: &["pas", "pp", "lpr", "dpr"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "d",
        extensions: &["d"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "vlang",
        extensions: &["v", "vsh"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "arduino",
        extensions: &["ino"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "nix",
        extensions: &["nix"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "sql",
        extensions: &["sql"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "octave",
        extensions: &["m"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "nushell",
        extensions: &["nu"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "brainfuck",
        extensions: &["bf", "b"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "wat",
        extensions: &["wat", "wast"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "clojurescript",
        extensions: &["cljs"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "fish",
        extensions: &["fish"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "fennel",
        extensions: &["fnl"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "awk",
        extensions: &["awk"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "sed",
        extensions: &["sed"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "coffeescript",
        extensions: &["coffee"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "xonsh",
        extensions: &["xsh"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "elvish",
        extensions: &["elv"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "janet",
        extensions: &["janet"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "unison",
        extensions: &["u"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "idl",
        extensions: &["pro"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "igor",
        extensions: &["ipf"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "scilab",
        extensions: &["sci"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "maxima",
        extensions: &["mac"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "eviews",
        extensions: &["prg"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "mplus",
        extensions: &["inp"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "qiskit",
        extensions: &["qisk"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "cirq",
        extensions: &["cirq"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
    LanguageSpec {
        name: "silq",
        extensions: &["silq"],
        config_files: EMPTY,
        tier: Tier::Minimal,
        kinds: NodeKinds {
            functions: EMPTY,
            classes: EMPTY,
            interfaces: EMPTY,
            properties: EMPTY,
            imports: EMPTY,
            calls: EMPTY,
        },
        grammar: None,
    },
];

pub fn language_spec(name: &str) -> Option<&'static LanguageSpec> {
    LANG_SPECS.iter().find(|s| s.name == name)
}

/// Map a file path to its canonical language spec via extension.
pub fn language_for_path(path: &str) -> Option<&'static LanguageSpec> {
    let ext = std::path::Path::new(path)
        .extension()
        .and_then(|e| e.to_str())?;
    let ext = ext.to_lowercase();
    LANG_SPECS
        .iter()
        .find(|s| s.extensions.iter().any(|e| e.eq_ignore_ascii_case(&ext)))
}

/// Build a parser for one language, or None if the grammar can't be loaded.
pub fn parser_for(name: &str) -> Option<Parser> {
    let spec = language_spec(name)?;
    let grammar = spec.grammar?;
    let mut p = Parser::new();
    p.set_language(&grammar()).ok()?;
    Some(p)
}

/// Init parsers for every language that bundles a grammar.
pub fn init_parsers() -> std::collections::HashMap<String, Parser> {
    LANG_SPECS
        .iter()
        .filter_map(|s| s.grammar.map(|_| s.name))
        .filter_map(|name| parser_for(name).map(|p| (name.to_string(), p)))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn language_for_path_c_extension() {
        let spec = language_for_path("src/main.c").expect("c spec");
        assert_eq!(spec.name, "c");
        assert_eq!(spec.tier, Tier::Full);
    }

    #[test]
    fn language_for_path_unknown_ext_is_none() {
        assert!(language_for_path("src/main.xyz").is_none());
    }

    #[test]
    fn language_spec_by_name() {
        assert_eq!(language_spec("c").unwrap().name, "c");
        assert!(language_spec("does-not-exist").is_none());
    }

    #[test]
    fn c_grammar_loads() {
        let mut parser = parser_for("c").expect("c parser");
        let tree = parser
            .parse("int main() { return 0; }", None)
            .expect("parse");
        assert!(!tree.root_node().has_error());
    }

    #[test]
    fn init_parsers_contains_c() {
        let parsers = init_parsers();
        assert!(parsers.contains_key("c"));
    }

    /// lang-extras feature gates the 25 non-core grammars. When disabled
    /// (--no-default-features, the Docker core build), those languages keep
    /// their LANG_SPECS row but report no grammar; core grammars stay loaded.
    #[cfg(not(feature = "lang-extras"))]
    #[test]
    fn lang_extras_grammars_absent_when_disabled() {
        assert!(language_spec("scala").unwrap().grammar.is_none());
        assert!(language_spec("cuda").unwrap().grammar.is_none());
        assert!(language_spec("qsharp").unwrap().grammar.is_none());
        // Core languages still carry a grammar in the slim build.
        assert!(language_spec("rust").unwrap().grammar.is_some());
        assert!(language_spec("go").unwrap().grammar.is_some());
        assert!(language_spec("objc").unwrap().grammar.is_some());
        assert!(language_spec("dart").unwrap().grammar.is_some());
    }

    #[cfg(feature = "lang-extras")]
    #[test]
    fn lang_extras_grammars_present_when_enabled() {
        assert!(language_spec("scala").unwrap().grammar.is_some());
        assert!(language_spec("cuda").unwrap().grammar.is_some());
    }

    /// Every core grammar-backed language must parse a representative snippet
    /// without a parse error — guards against grammars that fail to load.
    #[test]
    fn all_core_grammar_languages_parse_snippet() {
        let samples: &[(&str, &str)] = &[
            ("c", "int main(void) { return 0; }"),
            ("cpp", "class Foo {}; int main() { return 0; }"),
            ("bash", "greet() { echo hi; }\n"),
            ("ruby", "class User\n  def greet\n  end\nend"),
            ("php", "<?php class Foo {}"),
            ("perl", "package Foo;\nsub bar { return 1; }"),
            ("r", "square <- function(x) x * x\n"),
            ("elixir", "defmodule M do\n  def f, do: 1\nend"),
        ];
        for (lang, src) in samples {
            let spec = language_spec(lang).expect("spec");
            assert!(spec.grammar.is_some(), "{} missing grammar", lang);
            let mut parser = parser_for(lang).expect("parser");
            let tree = parser
                .parse(src, None)
                .unwrap_or_else(|| panic!("{} parse failed", lang));
            assert!(!tree.root_node().has_error(), "{} parse has errors", lang);
        }
    }

    /// lang-extras grammar-backed languages (only present when the feature is on).
    #[cfg(feature = "lang-extras")]
    #[test]
    fn lang_extras_grammar_languages_parse_snippet() {
        let samples: &[(&str, &str)] = &[
            ("scala", "class User\nobject Main { def main() = () }"),
            ("zig", "fn add(a: i32) i32 { return a; }"),
            ("solidity", "contract C { function f() public {} }"),
            ("lua", "function f() end\n"),
            ("json", "{\"a\": 1}"),
            ("yaml", "a: 1\n"),
            ("csharp", "class Foo {}"),
            (
                "haskell",
                "module M where\nimport Data.List (sort)\ndouble x = x * 2",
            ),
            (
                "elm",
                "module M exposing (main)\nimport Html\ntype Msg = A\ndouble x = x",
            ),
            (
                "ocaml",
                "open List\nlet double x = x * 2\nmodule M = struct\n  let add a b = a + b\nend",
            ),
            ("fsharp", "module Math\nlet double x = x * 2"),
            (
                "erlang",
                "-module(math).\n-export([double/1]).\ndouble(X) -> X * 2.",
            ),
            (
                "nim",
                "import std/strutils\nproc double(x: int): int =\n  x * 2",
            ),
            (
                "powershell",
                "function Get-User {\n  param($id)\n  return $id\n}",
            ),
            ("crystal", "class User\n  def greet\n  end\nend"),
        ];
        for (lang, src) in samples {
            let spec = language_spec(lang).expect("spec");
            assert!(spec.grammar.is_some(), "{} missing grammar", lang);
            let mut parser = parser_for(lang).expect("parser");
            let tree = parser
                .parse(src, None)
                .unwrap_or_else(|| panic!("{} parse failed", lang));
            assert!(!tree.root_node().has_error(), "{} parse has errors", lang);
        }
    }
}
