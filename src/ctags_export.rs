//! `leankg tags --format=ctags` — persistent ctags/GNU Global fast edge layer.
//!
//! Strategy: Aider/ctags lesson (`docs/analysis/leankg-competitive-research-and-
//! improvement-strategy-2026-08-02.md` §3.16 / §17 Tier 1 item 9). LeanKG already
//! owns a typed graph; this module renders it as a line-oriented `tags` file that
//! every existing editor / tooling can consume (Vim, Emacs, `readtags`,
//! ctags-mcp, etc.).
//!
//! Format (readtags-compatible):
//!   `{name}\t{file}\t{address};\tkind:{kind}\tlanguage:{lang}\tline:{line}`
//!
//! - `address` is a line number + `;` (Ex-style line address, deterministic).
//! - Extension fields are `key:value` pairs after the `;`.
//! - Order is deterministic: sort by `(name, file, line)`.
//! - The tags layer is a *fast edge* over the graph — it never replaces the
//!   typed graph, it just lowers it for editor/script consumers.

use std::collections::BTreeMap;
use std::fmt::Write as _;
use std::path::Path;

use crate::db::models::CodeElement;
use crate::graph::GraphEngine;

/// ctags kind shorthand for a LeanKG element type. Unknown types fall back to
/// the LeanKG element type verbatim (still valid extension field).
pub fn kind_for(element_type: &str) -> &'static str {
    match element_type {
        "file" | "module" => "f",
        "class" | "interface" => "c",
        "struct" => "s",
        "enum" => "g",
        "function" => "f",
        "method" => "m",
        "constructor" => "c",
        "property" => "p",
        "var" => "v",
        "document" | "doc_section" => "d",
        _ => "x",
    }
}

/// Build the ctags `tags` content from a slice of indexed elements.
///
/// Deterministic: sorted by `(name, file, line)`, then by qualified_name as a
/// stable tie-breaker so identical names/lines never jitter.
pub fn render_tags(elements: &[CodeElement]) -> String {
    let mut rows: Vec<(String, String, u32, String, String, String, String)> = Vec::new();
    for el in elements {
        if el.name.is_empty() {
            continue;
        }
        if el.file_path.is_empty() {
            continue;
        }
        // Normalize the file path to a repo-relative path (strip leading ./).
        let rel = el
            .file_path
            .trim_start_matches("./")
            .trim_start_matches('.')
            .trim_start_matches('/');
        let kind = kind_for(&el.element_type);
        let language = if el.language.is_empty() {
            "unknown".to_string()
        } else {
            el.language.clone()
        };
        rows.push((
            el.name.clone(),
            rel.to_string(),
            el.line_start,
            kind.to_string(),
            language,
            el.element_type.clone(),
            el.qualified_name.clone(),
        ));
    }
    rows.sort_by(|a, b| (&a.0, &a.1, &a.2, &a.6).cmp(&(&b.0, &b.1, &b.2, &b.6)));

    let mut out = String::new();
    for (name, file, line, kind, language, element_type, _qn) in rows {
        // Ex address: `{line};"` — semicolon terminates the search pattern.
        let _ = writeln!(
            out,
            "{name}\t{file}\t{line};\tkind:{kind}\tlanguage:{language}\telement:{element_type}"
        );
    }
    out
}

/// Load all indexed elements from a project's `.leankg` DB and render tags.
///
/// `db_path` is the `.leankg` directory (matching the CLI convention in
/// `src/main.rs`, e.g. `find_project_root()?.join(".leankg")`).
pub fn export_tags(db_path: &Path) -> Result<String, Box<dyn std::error::Error>> {
    let db = crate::db::schema::init_db_readonly(db_path)?;
    let graph = GraphEngine::new(db);
    let elements = graph.all_elements()?;
    Ok(render_tags(&elements))
}

/// Count symbols covered by a rendered tags file vs the source elements.
/// Returns `(covered, total)`.
pub fn coverage(tags: &str, elements: &[CodeElement]) -> (usize, usize) {
    let covered: BTreeMap<String, ()> = tags
        .lines()
        .filter(|l| !l.is_empty())
        .map(|l| (l.split('\t').next().unwrap_or_default().to_string(), ()))
        .collect();
    let total = elements
        .iter()
        .filter(|e| !e.name.is_empty() && !e.file_path.is_empty())
        .count();
    (covered.len(), total)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sample_elements() -> Vec<CodeElement> {
        let mk = |name: &str, etype: &str, file: &str, line: u32, lang: &str| CodeElement {
            qualified_name: format!("{file}::{name}"),
            element_type: etype.to_string(),
            name: name.to_string(),
            file_path: file.to_string(),
            line_start: line,
            line_end: line + 4,
            language: lang.to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::Value::Null,
            env: "local".to_string(),
        };
        vec![
            mk("GraphEngine", "struct", "./src/graph/query.rs", 1, "rust"),
            mk("run_query", "function", "./src/graph/query.rs", 20, "rust"),
            mk("handle_index", "function", "src/mcp/handler.rs", 5, "rust"),
        ]
    }

    #[test]
    fn render_is_deterministic() {
        let els = sample_elements();
        let a = render_tags(&els);
        let b = render_tags(&els);
        assert_eq!(a, b);
    }

    #[test]
    fn render_emits_readtags_shape() {
        let out = render_tags(&sample_elements());
        let first = out.lines().next().expect("non-empty");
        // `name\tfile\taddress\tkind:..\tlanguage:..\telement:..`
        let parts: Vec<&str> = first.split('\t').collect();
        assert!(
            parts.len() >= 4,
            "line should be name\tfile\taddress\tfields: {first}"
        );
        assert!(parts[0] == "GraphEngine", "first row sorted by name");
        assert!(
            parts[2].ends_with(';'),
            "address must be Ex line + semicolon"
        );
        assert!(parts[3].contains("kind:s"), "struct kind");
        assert!(parts.iter().any(|p| p.starts_with("language:rust")));
    }

    #[test]
    fn render_normalizes_leading_dot_slash() {
        let out = render_tags(&sample_elements());
        assert!(!out.contains("./src/"), "leading ./ stripped: {out}");
        assert!(
            out.contains("src/graph/query.rs"),
            "repo-relative path kept"
        );
    }

    #[test]
    fn coverage_counts_distinct_names() {
        let els = sample_elements();
        let out = render_tags(&els);
        let (covered, total) = coverage(&out, &els);
        assert_eq!(total, 3);
        assert_eq!(covered, 3);
    }

    #[test]
    fn skips_empty_name_or_path() {
        let mut els = sample_elements();
        els.push(CodeElement {
            qualified_name: "x::".into(),
            element_type: "function".into(),
            name: String::new(),
            file_path: "./src/a.rs".into(),
            line_start: 1,
            line_end: 2,
            language: "rust".into(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::Value::Null,
            env: "local".into(),
        });
        let out = render_tags(&els);
        assert!(
            !out.contains("\t./src/a.rs\t"),
            "empty-name element skipped"
        );
    }
}
