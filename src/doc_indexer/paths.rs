//! Path normalization and alias resolution for doc↔code joins (FR-DOCJOIN-01/02).

use crate::graph::GraphEngine;
use std::collections::HashSet;
use std::path::Path;

/// Normalize slashes and strip a leading `./`.
pub fn slash_normalize(path: &str) -> String {
    let trimmed = path.trim();
    let stripped = trimmed.strip_prefix("./").unwrap_or(trimmed);
    stripped.replace('\\', "/")
}

/// Strip `#anchor` fragments from a path reference.
pub fn strip_anchor(path: &str) -> String {
    path.split('#').next().unwrap_or(path).to_string()
}

fn push_unique(out: &mut Vec<String>, seen: &mut HashSet<String>, value: String) {
    if value.is_empty() {
        return;
    }
    if seen.insert(value.clone()) {
        out.push(value);
    }
}

/// Candidate document keys for `get_files_for_doc` (canonical `docs/…` first).
pub fn doc_key_candidates(doc_arg: &str) -> Vec<String> {
    let mut out = Vec::new();
    let mut seen = HashSet::new();
    let base = slash_normalize(doc_arg);
    let no_anchor = strip_anchor(&base);
    let normalized = slash_normalize(&no_anchor);

    push_unique(&mut out, &mut seen, normalized.clone());

    if let Some(stripped) = normalized.strip_prefix("docs/") {
        push_unique(&mut out, &mut seen, stripped.to_string());
    } else {
        push_unique(&mut out, &mut seen, format!("docs/{normalized}"));
    }

    if normalized.starts_with("docs/") {
        let without_docs = normalized
            .trim_start_matches("docs/")
            .trim_start_matches('/');
        push_unique(&mut out, &mut seen, without_docs.to_string());
    }

    if let Some(name) = normalized.rsplit('/').next() {
        if !name.is_empty() && !name.contains("docs") {
            push_unique(&mut out, &mut seen, format!("docs/{name}"));
        }
    }

    out
}

/// Candidate file keys for `find_related_docs`.
pub fn file_key_candidates(file_arg: &str) -> Vec<String> {
    let mut out = Vec::new();
    let mut seen = HashSet::new();
    let base = slash_normalize(file_arg);
    let no_anchor = strip_anchor(&base);
    let normalized = slash_normalize(&no_anchor);

    push_unique(&mut out, &mut seen, normalized.clone());
    push_unique(&mut out, &mut seen, format!("./{normalized}"));

    if normalized.starts_with("./") {
        let without = normalized.trim_start_matches("./");
        push_unique(&mut out, &mut seen, without.to_string());
    }

    if let Some(name) = normalized.rsplit('/').next() {
        if !name.is_empty() {
            push_unique(&mut out, &mut seen, name.to_string());
        }
    }

    out
}

/// Path variants used when resolving an extracted markdown code reference.
pub fn code_ref_path_candidates(raw_ref: &str) -> Vec<String> {
    let mut out = Vec::new();
    let mut seen = HashSet::new();
    let base = slash_normalize(&strip_anchor(raw_ref));

    push_unique(&mut out, &mut seen, base.clone());
    push_unique(&mut out, &mut seen, format!("./{base}"));

    if base.starts_with("./") {
        let without = base.trim_start_matches("./");
        push_unique(&mut out, &mut seen, without.to_string());
    }

    out
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct KeyResolveResult {
    pub resolved: Option<String>,
    pub tried: Vec<String>,
}

/// Resolve a document argument to an indexed `document` element key.
pub fn resolve_doc_key(graph: &GraphEngine, doc_arg: &str) -> KeyResolveResult {
    let tried = doc_key_candidates(doc_arg);
    for key in &tried {
        if let Ok(Some(elem)) = graph.find_element(key) {
            if elem.element_type == "document" {
                return KeyResolveResult {
                    resolved: Some(key.clone()),
                    tried,
                };
            }
        }
    }
    KeyResolveResult {
        resolved: None,
        tried,
    }
}

/// Resolve a file argument to an indexed `file` element key (outgoing join queries).
pub fn resolve_file_key(graph: &GraphEngine, file_arg: &str) -> KeyResolveResult {
    let tried = file_key_candidates(file_arg);
    for key in &tried {
        if let Ok(Some(elem)) = graph.find_element(key) {
            if elem.element_type == "file" {
                return KeyResolveResult {
                    resolved: Some(key.clone()),
                    tried,
                };
            }
        }
    }

    for key in &tried {
        if let Some(file_qn) = lookup_file_by_path(graph, key) {
            return KeyResolveResult {
                resolved: Some(file_qn),
                tried,
            };
        }
    }

    KeyResolveResult {
        resolved: None,
        tried,
    }
}

/// Resolve an extracted markdown code reference to a file-level element key.
pub fn resolve_code_ref(graph: &GraphEngine, raw_ref: &str) -> Option<String> {
    let candidates = code_ref_path_candidates(raw_ref);

    for path in &candidates {
        if let Ok(Some(elem)) = graph.find_element(path) {
            if elem.element_type == "file" {
                return Some(elem.qualified_name);
            }
        }
        if let Some(file_qn) = lookup_file_by_path(graph, path) {
            return Some(file_qn);
        }
    }

    if !raw_ref.contains('/') {
        return resolve_basename_file(graph, raw_ref);
    }

    let normalized = slash_normalize(&strip_anchor(raw_ref));
    if let Some(file_path) = lookup_file_by_symbol_suffix(graph, &normalized) {
        return Some(file_path);
    }

    if normalized.contains('/') {
        if let Ok(matches) = graph.find_elements_by_file_path_prefix(&normalized, 20) {
            if !matches.is_empty() {
                let unique_paths: HashSet<_> =
                    matches.iter().map(|e| e.file_path.clone()).collect();
                if unique_paths.len() == 1 {
                    return Some(matches[0].file_path.clone());
                }
            }
        }
    }

    None
}

fn path_match_endings(normalized: &str) -> Vec<String> {
    vec![
        normalized.to_string(),
        format!("./{normalized}"),
        format!("/{normalized}"),
    ]
}

fn unique_file_path_from_elements(elements: &[crate::db::models::CodeElement]) -> Option<String> {
    let unique_paths: HashSet<_> = elements.iter().map(|e| e.file_path.clone()).collect();
    if unique_paths.len() == 1 {
        elements.first().map(|e| e.file_path.clone())
    } else {
        None
    }
}

fn lookup_file_by_symbol_suffix(graph: &GraphEngine, normalized: &str) -> Option<String> {
    let stem = Path::new(normalized)
        .file_stem()
        .and_then(|s| s.to_str())
        .filter(|s| !s.is_empty())?;
    let endings = path_match_endings(normalized);
    let elems = graph
        .find_elements_by_name_exact(stem, Some("function"))
        .ok()?;
    let matching: Vec<_> = elems
        .into_iter()
        .filter(|e| {
            endings
                .iter()
                .any(|end| e.file_path == *end || e.file_path.ends_with(end))
        })
        .collect();
    unique_file_path_from_elements(&matching)
}

fn lookup_file_by_path(graph: &GraphEngine, path: &str) -> Option<String> {
    let normalized = slash_normalize(&strip_anchor(path));
    for fp in path_match_endings(&normalized) {
        if let Ok(elements) = graph.get_elements_by_file(&fp) {
            if let Some(file_elem) = elements.iter().find(|e| e.element_type == "file") {
                return Some(file_elem.qualified_name.clone());
            }
            if let Some(file_path) = unique_file_path_from_elements(&elements) {
                return Some(file_path);
            }
        }
    }

    if let Ok(matches) = graph.find_elements_by_file_path_prefix(&normalized, 20) {
        if let Some(file_path) = unique_file_path_from_elements(&matches) {
            return Some(file_path);
        }
    }

    lookup_file_by_symbol_suffix(graph, &normalized)
}

fn resolve_basename_file(graph: &GraphEngine, basename: &str) -> Option<String> {
    let name = slash_normalize(&strip_anchor(basename));
    if name.is_empty() || name.contains('/') {
        return None;
    }

    if let Ok(files) = graph.find_elements_by_name_exact(&name, Some("file")) {
        let unique_paths: HashSet<_> = files.iter().map(|e| e.file_path.clone()).collect();
        if unique_paths.len() == 1 {
            return Some(files[0].qualified_name.clone());
        }
    }

    if let Some(file_path) = lookup_file_by_symbol_suffix(graph, &name) {
        return Some(file_path);
    }
    if let Ok(matches) = graph.find_elements_by_file_path_prefix(&name, 50) {
        if let Some(file_path) = unique_file_path_from_elements(&matches) {
            return Some(file_path);
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::schema::init_db;
    use crate::graph::GraphEngine;
    use tempfile::TempDir;

    fn graph_with_doc_and_file() -> (GraphEngine, TempDir) {
        let tmp = TempDir::new().unwrap();
        let db = init_db(&tmp.path().join("leankg.db")).unwrap();
        let elements = r#"
        ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] <-
        [
            ["./src/widget.rs", "file", "widget.rs", "./src/widget.rs", 1, 1, "rust", null, null, null, "{}", "local"],
            ["docs/guide.md", "document", "Guide", "docs/guide.md", 1, 5, "markdown", null, null, null, "{}", "local"]
        ]
        :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env}
        "#;
        crate::db::schema::run_script(&db, elements, Default::default()).unwrap();
        (GraphEngine::new(db), tmp)
    }

    #[test]
    fn doc_key_candidates_cover_aliases() {
        let tried = doc_key_candidates("./docs/guide.md");
        assert!(tried.contains(&"docs/guide.md".to_string()));
        assert!(
            tried.contains(&"guide.md".to_string()) || tried.contains(&"docs/guide.md".to_string())
        );

        let prd = doc_key_candidates("prd.md");
        assert!(prd.iter().any(|k| k == "docs/prd.md"));
    }

    #[test]
    fn resolve_doc_key_hits_canonical_and_alias() {
        let (graph, _tmp) = graph_with_doc_and_file();
        for arg in ["docs/guide.md", "./docs/guide.md", "guide.md"] {
            let result = resolve_doc_key(&graph, arg);
            assert_eq!(
                result.resolved.as_deref(),
                Some("docs/guide.md"),
                "failed for {arg}"
            );
            assert!(!result.tried.is_empty());
        }
    }

    #[test]
    fn resolve_doc_key_miss_lists_tried() {
        let (graph, _tmp) = graph_with_doc_and_file();
        let result = resolve_doc_key(&graph, "missing.md");
        assert!(result.resolved.is_none());
        assert!(result.tried.iter().any(|k| k.contains("missing")));
    }

    #[test]
    fn resolve_code_ref_relative_and_basename() {
        let (graph, _tmp) = graph_with_doc_and_file();
        assert_eq!(
            resolve_code_ref(&graph, "src/widget.rs").as_deref(),
            Some("./src/widget.rs")
        );
        assert_eq!(
            resolve_code_ref(&graph, "./src/widget.rs").as_deref(),
            Some("./src/widget.rs")
        );
        assert_eq!(
            resolve_code_ref(&graph, "widget.rs").as_deref(),
            Some("./src/widget.rs")
        );
        assert!(resolve_code_ref(&graph, "nope.rs").is_none());
    }

    #[test]
    fn resolve_file_key_for_query_tools() {
        let (graph, _tmp) = graph_with_doc_and_file();
        let result = resolve_file_key(&graph, "src/widget.rs");
        assert_eq!(result.resolved.as_deref(), Some("./src/widget.rs"));
    }
}
