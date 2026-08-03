//! Single source of truth for language support: extension ↔ canonical name ↔
//! tree-sitter grammar ↔ node-kind patterns. Replaces the five parallel
//! fixed-size dispatch tables that previously lived in mod.rs / parser.rs /
//! extractor.rs / call_graph.rs.

use tree_sitter::{Language, Parser};

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

const EMPTY: &[&'static str] = &[];

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
        grammar: Some(|| tree_sitter_scala::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_zig::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_solidity::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_lua::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_json::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_yaml::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_css::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_html::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_graphql::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_proto::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_c_sharp::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_haskell::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_elm::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_ocaml::LANGUAGE_OCAML.into()),
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
        grammar: Some(|| tree_sitter_fsharp::LANGUAGE_FSHARP.into()),
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
        grammar: Some(|| tree_sitter_erlang::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_nim::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_powershell::LANGUAGE.into()),
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
        grammar: Some(|| tree_sitter_crystal::LANGUAGE.into()),
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

    /// Every grammar-backed language must parse a representative snippet without
    /// a parse error — guards against grammars that fail to load at runtime.
    #[test]
    fn all_grammar_languages_parse_snippet() {
        let samples: &[(&str, &str)] = &[
            ("c", "int main(void) { return 0; }"),
            ("cpp", "class Foo {}; int main() { return 0; }"),
            ("bash", "greet() { echo hi; }\n"),
            ("ruby", "class User\n  def greet\n  end\nend"),
            ("php", "<?php class Foo {}"),
            ("perl", "package Foo;\nsub bar { return 1; }"),
            ("r", "square <- function(x) x * x\n"),
            ("elixir", "defmodule M do\n  def f, do: 1\nend"),
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
