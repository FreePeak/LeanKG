//! Cross-alias entity resolution (US-GE-03 / FR-GE-03).
//!
//! Given a mention/alias such as `"Handler"`, `"handler.rs::handle_tool_call"`
//! or `"mcp handler"`, resolve it to the best-matching element using only
//! data already in the graph (qualified_name, name, file_path) — no LLM.
//!
//! Deterministic ranking, exact first:
//!   1. exact `name` (or file basename) match
//!   2. case-insensitive `name` match
//!   3. exact `file_path` / basename match
//!   4. prefix match on `name`
//!
//! Ambiguous aliases return a ranked list instead of a silent single pick.
use crate::db::models::CodeElement;

/// Number of matches `resolve` may return when the alias is ambiguous.
pub const MAX_MATCHES: usize = 20;

/// A resolved candidate with a deterministic score (lower is better).
///
/// `tie_break` distinguishes candidates that ranked identically: identical
/// values are returned in input order, so resolution is deterministic even
/// for collisions.
#[derive(Debug, Clone)]
pub struct Match {
    pub score: u32,
    pub tie_break: u32,
    pub element: CodeElement,
}

/// Element-type ranking: more specific symbols win over files/directories
/// when an alias collides (mirrors `rank_element_type` in query.rs).
fn type_rank(element_type: &str) -> u32 {
    match element_type {
        "function" | "method" | "constructor" => 0,
        "class" | "struct" | "interface" | "enum" | "trait" => 1,
        "route" | "module" | "property" | "field" => 2,
        "file" => 3,
        "directory" | "folder" => 4,
        _ => 5,
    }
}

/// Basename of a path (suffix after the last `/`), empty when none.
fn basename(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}

/// Extensionless basename: `"src/mcp/handler.rs"` -> `"handler"`.
fn basename_stem(path: &str) -> &str {
    let base = basename(path);
    base.rsplit_once('.').map(|(stem, _)| stem).unwrap_or(base)
}

/// Case-insensitive prefix match.
fn is_prefix(haystack: &str, needle: &str) -> bool {
    haystack.len() >= needle.len() && haystack[..needle.len()].eq_ignore_ascii_case(needle)
}

/// Rank one candidate against a parsed alias. Lower score = better match.
fn score_candidate(element: &CodeElement, alias: &str) -> Option<u32> {
    // 0: exact name
    if element.name == alias {
        return Some(0);
    }
    // 1: case-insensitive name
    if element.name.eq_ignore_ascii_case(alias) {
        return Some(1);
    }
    // 2: exact file path
    if element.file_path == alias {
        return Some(2);
    }
    // 3: file basename (element name is often the basename for file nodes)
    if basename(&element.file_path) == alias {
        return Some(3);
    }
    // 4: element lives in a file whose extensionless basename equals the
    // alias ("handler" → src/mcp/handler.rs::handle_tool_call)
    if basename_stem(&element.file_path) == alias {
        return Some(4);
    }
    // 5: prefix of name (min length 3 — "mcp handler" etc.)
    if alias.len() >= 3 && is_prefix(&element.name, alias) {
        return Some(5);
    }
    None
}

/// Rank one candidate against a file-qualified alias (`path::symbol`).
///
/// A full path half (`src/mcp/handler.rs::X`) means the exact file; the
/// basename fallback only applies when the path half is itself a basename
/// (`handler.rs::X` or `handler.rs/X`).
fn score_qualified(element: &CodeElement, path: &str, symbol: &str) -> Option<u32> {
    if element.file_path == path {
        if element.name == symbol {
            return Some(0); // exact path + symbol
        }
        if element.name.eq_ignore_ascii_case(symbol) {
            return Some(1); // exact path, case-insensitive symbol
        }
        if symbol.len() >= 3 && is_prefix(&element.name, symbol) {
            return Some(2); // exact path, symbol prefix
        }
        return None;
    }
    if !path.contains('/') && basename(&element.file_path) == path && element.name == symbol {
        return Some(3); // basename path half + exact symbol
    }
    None
}

/// Internal candidate with its deterministic tie-break slot.
struct Scored {
    score: u32,
    order: u32,
    element: CodeElement,
}

/// Resolve an alias to its best-matching elements.
///
/// - `path::symbol` aliases match against `file_path` + `name` directly.
/// - Plain aliases match exact name first, then case-insensitive, then file
///   path/basename, then prefix. Deterministic ranking — never a silent
///   arbitrary pick: ties stay in input order (see [`Match::tie_break`]).
/// - Unknown aliases return an empty vec.
pub fn resolve(elements: &[CodeElement], alias: &str, max_matches: usize) -> Vec<Match> {
    let alias = alias.trim();
    if alias.is_empty() || elements.is_empty() {
        return Vec::new();
    }
    let max_matches = max_matches.max(1);

    // `path::symbol` aliases (or `path/symbol` file handles).
    let qualified: Option<(&str, &str)> = alias
        .split_once("::")
        .or_else(|| alias.split_once('/'))
        .map(|(p, s)| (p.trim(), s.trim()))
        .filter(|(p, s)| !p.is_empty() && !s.is_empty());

    let mut scored: Vec<Scored> = Vec::new();
    for (order, element) in elements.iter().enumerate() {
        let score = match qualified {
            Some((path, symbol)) => score_qualified(element, path, symbol),
            None => score_candidate(element, alias),
        };
        if let Some(score) = score {
            scored.push(Scored {
                score,
                order: order as u32,
                element: element.clone(),
            });
        }
    }

    // Sort by score, then type rank (symbols over files), then input order.
    scored.sort_by_key(|s| (s.score, type_rank(&s.element.element_type), s.order));

    scored
        .into_iter()
        .take(max_matches)
        .map(|s| Match {
            score: s.score,
            tie_break: s.order,
            element: s.element,
        })
        .collect()
}

/// Best single match (score 0 — exact `name` or exact `path::symbol`).
/// Returns None when no exact match exists; use [`resolve`] for fuzzy cases.
pub fn resolve_exact(elements: &[CodeElement], alias: &str) -> Option<CodeElement> {
    resolve(elements, alias, MAX_MATCHES)
        .into_iter()
        .find(|m| m.score == 0)
        .map(|m| m.element)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn element(name: &str, file_path: &str, element_type: &str) -> CodeElement {
        let qualified_name = if element_type == "file" {
            file_path.to_string() // indexer convention: files key by path
        } else {
            format!("{}::{}", file_path, name)
        };
        CodeElement {
            qualified_name,
            element_type: element_type.to_string(),
            name: name.to_string(),
            file_path: file_path.to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            ..Default::default()
        }
    }

    fn scores(matches: &[Match]) -> Vec<u32> {
        matches.iter().map(|m| m.score).collect()
    }

    fn names(matches: &[Match]) -> Vec<&str> {
        matches.iter().map(|m| m.element.name.as_str()).collect()
    }

    #[test]
    fn alias_handler_resolves_to_handler_module_symbols() {
        let elements = vec![
            element("Handler", "src/mcp/handler.rs", "struct"),
            element("handle_tool_call", "src/mcp/handler.rs", "function"),
            element("handler", "src/other/handler.rs", "module"),
            element("Handler", "src/other/handler.rs", "struct"),
        ];
        let matches = resolve(&elements, "handler", 10);
        // exact name first (score 0), then case-insensitive (score 1),
        // then file-stem residency (score 4 — symbols living in handler.rs).
        assert_eq!(
            matches[0].element.qualified_name,
            "src/other/handler.rs::handler"
        );
        assert_eq!(matches[0].score, 0);
        assert_eq!(matches[1].element.name, "Handler");
        assert_eq!(matches[1].score, 1);
        assert_eq!(matches[2].element.name, "Handler");
        assert_eq!(matches[2].score, 1);
        assert_eq!(matches[3].element.name, "handle_tool_call");
        assert_eq!(matches[3].score, 4);
        assert!(matches[3].element.file_path.contains("src/mcp/"));
    }

    #[test]
    fn alias_handler_rs_symbol_resolves_by_path_and_name() {
        let elements = vec![
            element("handle_tool_call", "src/mcp/handler.rs", "function"),
            element("handle_tool_call", "src/other/handler.rs", "function"),
        ];
        let matches = resolve(&elements, "src/mcp/handler.rs::handle_tool_call", 10);
        assert_eq!(matches.len(), 1);
        assert_eq!(
            matches[0].element.qualified_name,
            "src/mcp/handler.rs::handle_tool_call"
        );
        assert_eq!(matches[0].score, 0);
    }

    #[test]
    fn ambiguous_alias_returns_ranked_list_not_silent_pick() {
        let elements = vec![
            element("Handler", "src/a/handler.rs", "struct"),
            element("Handler", "src/b/handler.rs", "struct"),
            element("handler", "src/c/handler.rs", "module"),
            element("handle_tool_call", "src/d/handler.rs", "function"),
        ];
        let matches = resolve(&elements, "Handler", 10);
        assert_eq!(matches.len(), 3);
        // Both exact-name matches; deterministic order = input order.
        assert_eq!(
            matches[0].element.qualified_name,
            "src/a/handler.rs::Handler"
        );
        assert_eq!(
            matches[1].element.qualified_name,
            "src/b/handler.rs::Handler"
        );
        assert_eq!(matches[0].score, matches[1].score);
        assert!(matches[0].tie_break < matches[1].tie_break);
        // Case-insensitive "handler" (score 1) plus file-stem "handle_tool_call"
        // (score 4) also rank, ahead of any silent single pick.
        assert_eq!(matches[2].element.name, "handler");
        assert_eq!(matches[2].score, 1);
    }

    #[test]
    fn unknown_alias_returns_empty() {
        let elements = vec![
            element("handle_tool_call", "src/mcp/handler.rs", "function"),
            element("get_dependencies", "src/graph/query.rs", "function"),
        ];
        assert!(resolve(&elements, "nope_does_not_exist", 10).is_empty());
        assert!(resolve(&elements, "", 10).is_empty());
    }

    #[test]
    fn resolve_is_deterministic() {
        let elements = vec![
            element("Handler", "src/a/handler.rs", "struct"),
            element("handler", "src/b/handler.rs", "module"),
            element("handle_tool_call", "src/c/handler.rs", "function"),
        ];
        let a = resolve(&elements, "handler", 10);
        let b = resolve(&elements, "handler", 10);
        assert_eq!(a.len(), b.len());
        for (x, y) in a.iter().zip(b.iter()) {
            assert_eq!(x.score, y.score);
            assert_eq!(x.element.qualified_name, y.element.qualified_name);
            assert_eq!(x.tie_break, y.tie_break);
        }
    }

    #[test]
    fn case_insensitive_fallback_before_prefix() {
        let elements = vec![
            element("Handler", "src/a/handler.rs", "struct"),
            element("HandleToolCall", "src/b/handler.rs", "function"),
        ];
        let matches = resolve(&elements, "handler", 10);
        assert_eq!(matches.len(), 2);
        assert_eq!(matches[0].element.name, "Handler"); // score 1 ci
        assert_eq!(matches[1].element.name, "HandleToolCall"); // score 4 stem
        assert_eq!(scores(&matches), vec![1, 4]);
    }

    #[test]
    fn basename_alias_resolves_to_file_element() {
        let elements = vec![
            element("handler.rs", "src/mcp/handler.rs", "file"),
            element("handle_tool_call", "src/mcp/handler.rs", "function"),
        ];
        let matches = resolve(&elements, "handler.rs", 10);
        assert_eq!(matches.len(), 2);
        // File element matches its own basename exactly (score 0); the
        // function living in that file also ranks (basename residency, score 3).
        assert_eq!(matches[0].element.element_type, "file");
        assert_eq!(matches[0].element.qualified_name, "src/mcp/handler.rs");
        assert_eq!(matches[0].score, 0);
        assert_eq!(matches[1].element.name, "handle_tool_call");
        assert_eq!(matches[1].score, 3);
    }

    #[test]
    fn slash_qualified_alias_matches_basename() {
        let elements = vec![
            element("handle_tool_call", "src/mcp/handler.rs", "function"),
            element("handle_tool_call", "src/other/handler.rs", "function"),
        ];
        let matches = resolve(&elements, "handler.rs/handle_tool_call", 10);
        assert_eq!(matches.len(), 2);
        // Basename path half matches both files (score 3); full-path alias
        // (with `src/`) instead pins exactly one file.
        assert_eq!(
            matches[0].element.qualified_name,
            "src/mcp/handler.rs::handle_tool_call"
        );
        assert_eq!(matches[0].score, 3);
        assert_eq!(
            matches[1].element.qualified_name,
            "src/other/handler.rs::handle_tool_call"
        );
    }

    #[test]
    fn type_rank_breaks_collisions_deterministically() {
        let elements = vec![
            element("Handler", "src/a/handler.rs", "file"),
            element("Handler", "src/b/handler.rs", "struct"),
        ];
        let matches = resolve(&elements, "Handler", 10);
        assert_eq!(matches.len(), 2);
        // Both score 0; struct (rank 1) beats file (rank 3).
        assert_eq!(matches[0].element.element_type, "struct");
        assert_eq!(matches[1].element.element_type, "file");
    }

    #[test]
    fn empty_input_returns_empty() {
        assert!(resolve(&[], "anything", 10).is_empty());
    }

    #[test]
    fn resolve_exact_returns_none_for_fuzzy_only() {
        let elements = vec![
            element("handle_tool_call", "src/mcp/handler.rs", "function"),
            element("Handler", "src/mcp/handler.rs", "struct"),
        ];
        // No element named exactly "handl" — fuzzy match only.
        assert!(resolve_exact(&elements, "handl").is_none());
        // Exact struct hit.
        let exact = resolve_exact(&elements, "Handler").unwrap();
        assert_eq!(exact.qualified_name, "src/mcp/handler.rs::Handler");
    }
}
