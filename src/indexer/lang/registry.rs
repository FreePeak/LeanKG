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
}
