//! Deterministic file/module **summary nodes** for GraphRAG-style embedding
//! on large codebases — without an LLM.
//!
//! ## Why
//! The default embed granularity is one vector per function. On a large
//! codebase that is (a) a lot of inference and (b) each function blob is just
//! `qualified_name + name + signature` — which carries very little semantic
//! signal (a `fn parse(s: &str)` blob is nearly empty). Summary nodes flip
//! the granularity: one vector per file (and per module) whose blob is a
//! dense "table of contents" — file path + module decl + exported types +
//! public function signatures. That is the single most information-dense,
//! token-efficient description of a file's purpose, and it costs ~N-files
//! inferences instead of ~N-functions.
//!
//! ## How
//! Summary nodes are pure functions of the already-extracted `CodeElement`
//! set — no source re-read, no LLM call. They reuse the existing `"file"`
//! and `"module"` element types (already on the embed allowlist in
//! [`crate::embeddings::text_blob::classify`] and already registered as
//! upper seeds in [`crate::retrieval::ontology_traversal::UPPER_TYPES`]).
//! The retrieval layer already walks `"file"` → `contains` → functions
//! ([`crate::retrieval::ontology_traversal::downward_rule_for`]), so a
//! file-summary hit auto-expands to its member functions with **zero**
//! retrieval changes.
//!
//! ## Token budget
//! [`FILE_SUMMARY_MAX_CHARS`] caps the composed TOC at ~400 characters to
//! fit the BGE-small-en-v1.5 fast-path (`LEANKG_EMBED_FAST=1` pins
//! `max_seq=128`; ~400 ASCII chars ≈ 100–120 BPE tokens, leaving headroom
//! for the qualified_name line that the blob builder prepends).
//!
//! See `docs/prd.md` § "Summary-node embedding (FR-EMBED-SUMMARY)".

use crate::db::models::{CodeElement, Relationship};

/// Maximum length (in characters) of the composed `summary` field stored in
/// a summary node's metadata. Sized to fit the 128-token fast-path budget
/// after the blob builder prepends the qualified name. Override with
/// `LEANKG_EMBED_SUMMARY_CHARS` (clamped 120–1500).
pub fn file_summary_max_chars() -> usize {
    if let Ok(v) = std::env::var("LEANKG_EMBED_SUMMARY_CHARS") {
        if let Ok(n) = v.parse::<usize>() {
            return n.clamp(120, 1500);
        }
    }
    400
}

/// Element types we treat as exported "type" declarations for the TOC.
const TYPE_ELEMENTS: &[&str] = &["class", "struct", "interface", "trait", "enum", "record", "union"];

/// Element types we treat as function-like for the TOC.
const FUNCTION_ELEMENTS: &[&str] = &["function", "method", "constructor"];

/// Qualified-name convention for the per-file summary node: the raw file
/// path. This matches the `source_qualified` the generic extractor already
/// uses for its `file → element` `contains` edges
/// (`src/indexer/extractor.rs`, `else` branch of `extract_function`), so the
/// existing downward-traversal rule finds the children with no extra wiring.
pub fn file_summary_qualified_name(file_path: &str) -> String {
    file_path.to_string()
}

/// Build (or enrich) a per-file summary node from a file's element set.
///
/// - If `existing_file_node` is `Some`, its metadata is enriched with the
///   composed `summary` (swift/objc already emit a bare `"file"` node).
/// - Otherwise a new `"file"` node is constructed.
///
/// `file_elements` should be all elements whose `file_path` matches, **minus**
/// any pre-existing `"file"`/`"document"` node (pass that in as
/// `existing_file_node` instead, so its counts aren't double-counted).
pub fn build_file_summary(
    file_path: &str,
    language: &str,
    file_elements: &[&CodeElement],
    existing_file_node: Option<&CodeElement>,
) -> CodeElement {
    let (summary, fn_count, type_count, line_count) = compose_toc(file_path, file_elements);

    let name = std::path::Path::new(file_path)
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or(file_path)
        .to_string();

    let mut element = existing_file_node.cloned().unwrap_or(CodeElement {
        qualified_name: file_summary_qualified_name(file_path),
        element_type: "file".to_string(),
        name: name.clone(),
        file_path: file_path.to_string(),
        line_start: 1,
        line_end: line_count.unwrap_or(1),
        language: language.to_string(),
        ..Default::default()
    });

    // Ensure the node name reflects the basename even if the existing node
    // had an empty/stale name.
    if element.name.is_empty() {
        element.name = name;
    }
    if let Some(lines) = line_count {
        element.line_end = element.line_end.max(lines);
    }

    element.metadata = serde_json::json!({
        "summary": summary,
        "summary_kind": "toc",
        "fn_count": fn_count,
        "type_count": type_count,
        "line_count": line_count.unwrap_or(0),
    });
    element
}

/// Compose the deterministic table-of-contents string for a set of elements.
///
/// Priority order (each section only added if the previous sections leave
/// room within [`file_summary_max_chars`]):
/// 1. File path
/// 2. Module / package declaration (first `module` element's name)
/// 3. Exported type names (`class | struct | interface | trait | enum | …`)
/// 4. Public function signatures (`metadata.signature`, top-level first)
/// 5. Filler counts (`"N functions, M types"`)
///
/// Returns `(summary_string, fn_count, type_count, line_count)`.
fn compose_toc(
    file_path: &str,
    elements: &[&CodeElement],
) -> (String, usize, usize, Option<u32>) {
    compose_toc_with_cap(file_path, elements, file_summary_max_chars())
}

/// Same as [`compose_toc`] but with an explicit char cap. Used by tests so
/// they don't depend on the process-global `LEANKG_EMBED_SUMMARY_CHARS` env
/// var (which races under parallel test execution).
fn compose_toc_with_cap(
    file_path: &str,
    elements: &[&CodeElement],
    cap: usize,
) -> (String, usize, usize, Option<u32>) {
    let mut line: String = String::with_capacity(cap);
    let mut fn_count = 0usize;
    let mut type_count = 0usize;
    let mut max_line: Option<u32> = None;

    // Section 1: file path (always).
    push_section(&mut line, cap, file_path);

    // Collect module decls, types, and functions while scanning line span.
    let mut modules: Vec<&str> = Vec::new();
    let mut types: Vec<&str> = Vec::new();
    let mut func_sigs: Vec<String> = Vec::new();

    for el in elements {
        if el.line_end > 0 {
            max_line = Some(max_line.map_or(el.line_end, |m| m.max(el.line_end)));
        }
        let et = el.element_type.as_str();
        if et == "module" || et == "package" {
            if !el.name.is_empty() && !modules.contains(&el.name.as_str()) {
                modules.push(el.name.as_str());
            }
        } else if TYPE_ELEMENTS.contains(&et) {
            type_count += 1;
            if !el.name.is_empty() && types.len() < 24 {
                types.push(el.name.as_str());
            }
        } else if FUNCTION_ELEMENTS.contains(&et) {
            fn_count += 1;
            if func_sigs.len() < 24 {
                if let Some(sig) = short_signature(el) {
                    func_sigs.push(sig);
                } else if !el.name.is_empty() {
                    func_sigs.push(el.name.clone());
                }
            }
        }
    }

    // Section 2: module declaration.
    if !modules.is_empty() {
        let mods = modules.join(", ");
        push_section(&mut line, cap, &format!("module: {mods}"));
    }

    // Section 3: exported types (compact list).
    if !types.is_empty() {
        let t = types.join(", ");
        push_section(&mut line, cap, &format!("types: {t}"));
    }

    // Section 4: public function signatures (most semantic signal per token).
    for sig in &func_sigs {
        if !push_section(&mut line, cap, sig) {
            break;
        }
    }

    // Section 5: filler counts — only if there's clear room.
    if line.len() + 40 < cap {
        let counts = format!("{fn_count} functions, {type_count} types");
        push_section(&mut line, cap, &counts);
    }

    (line, fn_count, type_count, max_line)
}

/// Try to append `section` (separated by ` | ` from prior content) without
/// exceeding `cap`. Returns `false` if it would not fit at all (caller stops).
fn push_section(line: &mut String, cap: usize, section: &str) -> bool {
    if section.trim().is_empty() {
        return true;
    }
    let sep = if line.is_empty() { 0 } else { 3 }; // " | "
    if line.len() + sep >= cap {
        return false;
    }
    let remaining = cap - line.len() - sep;
    let section = if section.len() <= remaining {
        section.to_string()
    } else {
        // Truncate on a char boundary.
        let mut end = remaining;
        while end > 0 && !section.is_char_boundary(end) {
            end -= 1;
        }
        if end < 3 {
            return false;
        }
        section[..end].to_string()
    };
    if line.is_empty() {
        line.push_str(&section);
    } else {
        line.push_str(" | ");
        line.push_str(&section);
    }
    true
}

/// Extract a compact single-line signature from a function element.
///
/// Prefers `metadata.signature` (the raw source slice up to the body) but
/// collapses it to a single line and strips the body/brace. Falls back to
/// `metadata.doc_comment` if no signature is stored.
fn short_signature(el: &CodeElement) -> Option<String> {
    if let Some(sig) = el.metadata.get("signature").and_then(|v| v.as_str()) {
        let collapsed = collapse_signature(sig);
        if !collapsed.is_empty() {
            return Some(collapsed);
        }
    }
    for key in &["signature_text", "doc_comment", "doc"] {
        if let Some(s) = el.metadata.get(key).and_then(|v| v.as_str()) {
            let collapsed = collapse_signature(s);
            if !collapsed.is_empty() {
                return Some(collapsed);
            }
        }
    }
    None
}

/// Collapse a (possibly multi-line) signature to a single line, trimming
/// trailing `{`, `=>`, and whitespace so the TOC stays compact.
fn collapse_signature(sig: &str) -> String {
    let first_line = sig.lines().next().unwrap_or("").trim();
    let mut s = first_line.trim_end_matches('{').trim_end().to_string();
    // Strip a trailing `=> ...` body arrow to keep the signature tight.
    if let Some(idx) = s.find("=>") {
        let before = s[..idx].trim_end();
        if !before.is_empty() {
            s = before.to_string();
        }
    }
    s
}

/// Build `contains` edges from the file-summary node to each top-level
/// (no `parent_qualified`) function/method/type element in the file. These
/// are the bridge edges the retrieval downward-traversal walks; the generic
/// extractor already emits `file → element` contains edges, so this is a
/// no-op when those exist, but guarantees connectivity for languages whose
/// extractor does not emit them (e.g. when a summary node is created fresh
/// for a file whose functions all have a class parent).
pub fn file_summary_contains_edges(
    file_summary_qn: &str,
    file_elements: &[&CodeElement],
) -> Vec<Relationship> {
    let mut edges = Vec::new();
    let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
    for el in file_elements {
        if el.qualified_name == file_summary_qn {
            continue;
        }
        // Only bridge to top-level declarations (functions, types, modules).
        let et = el.element_type.as_str();
        let is_target = FUNCTION_ELEMENTS.contains(&et)
            || TYPE_ELEMENTS.contains(&et)
            || et == "module"
            || et == "package";
        if !is_target {
            continue;
        }
        if el.parent_qualified.is_some() {
            continue;
        }
        if !seen.insert(el.qualified_name.clone()) {
            continue;
        }
        edges.push(Relationship {
            source_qualified: file_summary_qn.to_string(),
            target_qualified: el.qualified_name.clone(),
            rel_type: "contains".to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({"source": "file_summary"}),
            ..Default::default()
        });
    }
    edges
}

/// Build a cross-file module-level summary node, aggregating the exported
/// symbols of every file that declares the same module/package.
///
/// Returns `None` if `file_summaries` is empty or no module name is given.
/// The qualified name is `{first_file}::module::{module_name}` so it never
/// collides with a per-file module node.
pub fn build_module_summary(
    module_name: &str,
    language: &str,
    file_summaries: &[&CodeElement],
) -> Option<CodeElement> {
    if file_summaries.is_empty() || module_name.trim().is_empty() {
        return None;
    }

    let cap = file_summary_max_chars();
    let mut line = String::with_capacity(cap);
    push_section(&mut line, cap, &format!("module: {module_name}"));

    // Aggregate exported-type and function counts across files (we only have
    // the per-file summary nodes here, not their individual children).
    let mut total_fn = 0usize;
    let mut total_types = 0usize;
    for fs in file_summaries {
        let t = fs.metadata.get("type_count").and_then(|v| v.as_u64()).unwrap_or(0) as usize;
        let f = fs.metadata.get("fn_count").and_then(|v| v.as_u64()).unwrap_or(0) as usize;
        total_types += t;
        total_fn += f;
    }

    // Re-scan each file summary's sibling elements is not possible here (we
    // only have the summary nodes), so the module TOC lists file basenames
    // + counts — still far richer than an empty module blob.
    let mut files: Vec<String> = Vec::new();
    for fs in file_summaries {
        let basename = std::path::Path::new(&fs.file_path)
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or(&fs.file_path)
            .to_string();
        if !files.contains(&basename) {
            files.push(basename);
        }
        if files.len() >= 16 {
            break;
        }
    }
    if !files.is_empty() {
        push_section(&mut line, cap, &format!("files: {}", files.join(", ")));
    }
    push_section(&mut line, cap, &format!("{total_fn} functions, {total_types} types"));

    let first = file_summaries.first()?;
    let qn = format!("{}::module::{}", first.file_path, module_name);

    Some(CodeElement {
        qualified_name: qn,
        element_type: "module".to_string(),
        name: module_name.to_string(),
        file_path: first.file_path.clone(),
        line_start: 1,
        line_end: 1,
        language: language.to_string(),
        metadata: serde_json::json!({
            "summary": line,
            "summary_kind": "module_toc",
            "fn_count": total_fn,
            "type_count": total_types,
            "file_count": file_summaries.len(),
        }),
        ..Default::default()
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn el(et: &str, name: &str, file: &str) -> CodeElement {
        CodeElement {
            element_type: et.to_string(),
            name: name.to_string(),
            qualified_name: format!("{file}::{name}"),
            file_path: file.to_string(),
            ..Default::default()
        }
    }

    fn fn_el(name: &str, sig: &str, file: &str) -> CodeElement {
        let mut e = el("function", name, file);
        e.metadata = serde_json::json!({"signature": sig});
        e
    }

    #[test]
    fn composes_path_module_types_and_signatures() {
        let file = "src/parser.rs";
        let elements: Vec<CodeElement> = vec![
            CodeElement {
                element_type: "module".to_string(),
                name: "parser".to_string(),
                qualified_name: "src/parser.rs::parser".to_string(),
                file_path: file.to_string(),
                ..Default::default()
            },
            el("struct", "Ast", file),
            el("trait", "Visitor", file),
            fn_el("parse", "pub fn parse(src: &str) -> Ast", file),
            fn_el("tokenize", "fn tokenize(src: &str) -> Vec<Token>", file),
        ];
        let refs: Vec<&CodeElement> = elements.iter().collect();
        let (summary, fn_count, type_count, lines) = compose_toc(file, &refs);
        assert!(summary.starts_with(file));
        assert!(summary.contains("module: parser"));
        assert!(summary.contains("types: Ast, Visitor"));
        assert!(summary.contains("pub fn parse(src: &str) -> Ast"));
        assert_eq!(fn_count, 2);
        assert_eq!(type_count, 2);
        assert!(lines.is_none()); // no line_end set
    }

    #[test]
    fn respects_char_cap() {
        // Use the explicit-cap variant so the test doesn't depend on the
        // process-global LEANKG_EMBED_SUMMARY_CHARS env var (which races
        // under parallel test execution).
        let file = "x.rs";
        let elements: Vec<CodeElement> = (0..50)
            .map(|i| fn_el(&format!("f{i}"), &format!("fn f{i}() -> i32"), file))
            .collect();
        let refs: Vec<&CodeElement> = elements.iter().collect();
        let (summary, _, _, _) = compose_toc_with_cap(file, &refs, 60);
        assert!(summary.len() <= 60, "len {} > 60: {summary}", summary.len());
    }

    #[test]
    fn collapses_multiline_signature_to_first_line() {
        assert_eq!(collapse_signature("pub fn x(\n  a: i32,\n) -> i32 {"), "pub fn x(");
        assert_eq!(collapse_signature("fn y() => 1"), "fn y()");
        assert_eq!(collapse_signature("  fn z()  "), "fn z()");
    }

    #[test]
    fn build_file_summary_enriches_existing_node() {
        let file = "src/main.rs";
        let existing = CodeElement {
            qualified_name: file.to_string(),
            element_type: "file".to_string(),
            name: "main.rs".to_string(),
            file_path: file.to_string(),
            ..Default::default()
        };
        let fns: Vec<CodeElement> = vec![fn_el("main", "fn main()", file)];
        let refs: Vec<&CodeElement> = fns.iter().collect();
        let summary = build_file_summary(file, "rust", &refs, Some(&existing));
        assert_eq!(summary.qualified_name, file);
        assert_eq!(summary.element_type, "file");
        assert_eq!(summary.name, "main.rs");
        assert!(summary.metadata["summary"].as_str().unwrap().contains("fn main()"));
        assert_eq!(summary.metadata["fn_count"].as_u64().unwrap(), 1);
        assert_eq!(summary.metadata["summary_kind"].as_str().unwrap(), "toc");
    }

    #[test]
    fn build_file_summary_creates_new_node_when_none_exists() {
        let file = "lib.go";
        let fns: Vec<CodeElement> = vec![fn_el("Handle", "func Handle(w http.ResponseWriter)", file)];
        let refs: Vec<&CodeElement> = fns.iter().collect();
        let summary = build_file_summary(file, "go", &refs, None);
        assert_eq!(summary.qualified_name, file);
        assert_eq!(summary.element_type, "file");
        assert!(summary.metadata["summary"].as_str().unwrap().contains("Handle"));
    }

    #[test]
    fn contains_edges_bridge_top_level_only() {
        let file = "src/a.rs";
        let top = fn_el("alpha", "fn alpha()", file);
        let mut child = fn_el("beta", "fn beta()", file);
        child.parent_qualified = Some(format!("{file}::Impl"));
        let typ = el("struct", "Impl", file);
        let refs: Vec<&CodeElement> = vec![&top, &child, &typ];
        let edges = file_summary_contains_edges(file, &refs);
        let targets: Vec<&str> = edges.iter().map(|e| e.target_qualified.as_str()).collect();
        assert!(targets.contains(&"src/a.rs::alpha"));
        assert!(targets.contains(&"src/a.rs::Impl"));
        assert!(
            !targets.contains(&"src/a.rs::beta"),
            "child with parent should not get a direct bridge"
        );
    }

    #[test]
    fn build_module_summary_aggregates_files() {
        let file = "pkg/handler.rs";
        let fs1 = build_file_summary(file, "rust", &[&fn_el("get", "fn get()", file)], None);
        let fs2 = build_file_summary("pkg/post.rs", "rust", &[&fn_el("post", "fn post()", "pkg/post.rs")], None);
        let refs: Vec<&CodeElement> = vec![&fs1, &fs2];
        let module = build_module_summary("handler", "rust", &refs).unwrap();
        assert_eq!(module.element_type, "module");
        assert!(module.qualified_name.ends_with("::module::handler"));
        assert_eq!(module.metadata["fn_count"].as_u64().unwrap(), 2);
        assert_eq!(module.metadata["file_count"].as_u64().unwrap(), 2);
    }

    #[test]
    fn build_module_summary_returns_none_for_empty() {
        assert!(build_module_summary("x", "rust", &[]).is_none());
        assert!(build_module_summary("", "rust", &[]).is_none());
    }
}
