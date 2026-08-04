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

/// Internal row tuple for sorting/rendering: name, file, line, kind,
/// language, element_type, qualified_name, search-pattern.
type TagsRow = (String, String, u32, String, String, String, String, String);

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
///
/// Format (readtags-compatible):
/// ```text
/// {name}\t{file}\t{addr};"\t<pattern>"\tkind:{kind}\tlanguage:{lang}\telement:{type}
/// ```
/// - `addr` is a 1-based line number; the trailing `;"` opens the
///   extended-search field that `readtags` understands.
/// - `<pattern>` is the ctags search-pattern field (regex anchored at
///   line start when read by `^pattern$`); emitted as a second `;"` so
///   `:tag /pattern/` still works in vim.
pub fn render_tags(elements: &[CodeElement]) -> String {
    let mut rows: Vec<TagsRow> = Vec::new();
    for el in elements {
        if el.name.is_empty() {
            continue;
        }
        if el.file_path.is_empty() {
            continue;
        }
        // Skip rows whose name or path would corrupt the tab-delimited
        // format. `readtags` can't survive a literal tab/newline in any
        // of the first three columns.
        if el.name.contains('\t')
            || el.name.contains('\n')
            || el.file_path.contains('\t')
            || el.file_path.contains('\n')
        {
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
        // Search pattern: the symbol name, escaped for ctags regex
        // consumption. We keep it simple — `readtags` re-anchors with
        // `^...$`, so an unescaped `name` is enough for `:tag /name/`.
        rows.push((
            el.name.clone(),
            rel.to_string(),
            el.line_start,
            kind.to_string(),
            language,
            el.element_type.clone(),
            el.qualified_name.clone(),
            escape_pattern(&el.name),
        ));
    }
    rows.sort_by(|a, b| (&a.0, &a.1, &a.2, &a.6).cmp(&(&b.0, &b.1, &b.2, &b.6)));

    let mut out = String::new();
    for (name, file, line, kind, language, element_type, _qn, pattern) in rows {
        // Ex address: `{line};"\t{pattern}"` — two semicolons so vim
        // can do `:tag /pattern/` in addition to `:tag name`.
        let _ = writeln!(
            out,
            "{name}\t{file}\t{line};\t{pattern}\"\tkind:{kind}\tlanguage:{language}\telement:{element_type}"
        );
    }
    out
}

/// Escape a symbol name for use as a ctags search-pattern field. Keeps
/// only ASCII letters/digits/`_` literal; everything else is wrapped in
/// a `\`...`\` rune so `readtags` treats it as a literal character.
fn escape_pattern(name: &str) -> String {
    let mut out = String::with_capacity(name.len() + 2);
    out.push('^');
    for ch in name.chars() {
        if ch.is_ascii_alphanumeric() || ch == '_' {
            out.push(ch);
        } else {
            let _ = write!(out, "\\{}", ch as u32);
        }
    }
    out.push('$');
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
        // `name\tfile\t{addr};"\t{pattern}"\tkind:..\tlanguage:..\telement:..`
        let parts: Vec<&str> = first.split('\t').collect();
        assert!(
            parts.len() >= 5,
            "line should be name\tfile\t{{addr}};\"\t{{pattern}}\"\tfields: {first}"
        );
        assert!(parts[0] == "GraphEngine", "first row sorted by name");
        // Address field ends with the Ex semicolon (`;`) that opens the
        // extended search pattern.
        let addr = parts[2];
        assert!(addr.ends_with(';'), "address must end with `;`: {addr}");
        // The fourth column is the search pattern (terminated by `"`).
        let pattern = parts[3];
        assert!(
            pattern.ends_with('"'),
            "search pattern must be quoted: {pattern}"
        );
        assert!(
            pattern.contains("GraphEngine"),
            "search pattern echoes the symbol name: {pattern}"
        );
        assert!(parts[4].contains("kind:s"), "struct kind");
        assert!(parts.iter().any(|p| p.starts_with("language:rust")));
    }

    #[test]
    fn escape_pattern_anchors_and_letter_safe() {
        assert_eq!(escape_pattern("foo"), "^foo$");
        assert_eq!(escape_pattern("My_Type"), "^My_Type$");
        // Non-ASCII gets rune-escaped.
        let p = escape_pattern("a.b");
        assert!(p.contains('\\'));
    }

    #[test]
    fn skips_rows_with_tabs_or_newlines_in_name_or_path() {
        let mut els = sample_elements();
        els.push(CodeElement {
            qualified_name: "x::bad".into(),
            element_type: "function".into(),
            name: "bad\tname".into(),
            file_path: "src/a.rs".into(),
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
            !out.contains("bad\tname"),
            "name with embedded tab is dropped: {out}"
        );
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
