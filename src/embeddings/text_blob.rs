//! Text-blob construction for embedding.
//!
//! Each CodeElement is converted to a short text blob suitable for embedding
//! with a sentence transformer (BGE-small-en-v1.5, 384-dim, 512-token max).
//! Blobs are deliberately compact: name + qualified_name + doc/signature for
//! code nodes; name + aliases + description for ontology nodes. Source bodies
//! are intentionally excluded — see plan §"What gets embedded".

use crate::db::models::CodeElement;
use sha2::{Digest, Sha256};

/// Maximum text-blob length in characters before truncation. The embedding
/// model's hard limit is 512 BPE tokens; ~1500 ASCII characters is a safe
/// approximation that leaves headroom for tokenization expansion.
///
/// Fast path (`LEANKG_EMBED_FAST=1`) defaults to a tighter cap so batches
/// stay short after `LEANKG_EMBED_MAX_SEQ` — needed for ≥500 vec/s on
/// Apple Silicon. Override with `LEANKG_EMBED_MAX_BLOB_CHARS`.
pub fn max_blob_chars() -> usize {
    if let Ok(v) = std::env::var("LEANKG_EMBED_MAX_BLOB_CHARS") {
        if let Ok(n) = v.parse::<usize>() {
            return n.clamp(64, 8_000);
        }
    }
    if crate::embeddings::runtime::embed_fast_enabled() {
        500
    } else {
        1500
    }
}

pub const MAX_BLOB_CHARS: usize = 1500;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BlobKind {
    Code,
    Ontology,
    Doc,
    Skip,
}

/// Classify a CodeElement into a blob-construction strategy. Returns `Skip`
/// for element types that should not be embedded (e.g. clusters, processes
/// that duplicate the code they group).
/// Perf embed preset for mega cold/full runs (FR-EMBED-TYPES-02).
pub const PERF_TYPE_PRESET: &[&str] = &[
    "function",
    "method",
    "class",
    "interface",
    "file",
    "struct",
    "property",
    "constructor",
    "document",
    "doc_section",
];

pub fn classify(element_type: &str) -> BlobKind {
    match element_type.to_ascii_lowercase().as_str() {
        "file" | "function" | "class" | "module" | "method" | "trait" | "interface" | "struct"
        | "property" | "constructor" => BlobKind::Code,
        "document" | "doc_section" => BlobKind::Doc,
        "domain_entity" | "service" | "api_endpoint" | "data_store" | "environment"
        | "known_issue" | "playbook" | "playbook_step" | "team_knowledge" | "workflow"
        | "workflow_step" | "decision_point" | "failure_mode" => BlobKind::Ontology,
        _ => BlobKind::Skip,
    }
}

/// Build the text blob for a CodeElement. Returns `None` if the element type
/// is in the Skip category or if the resulting blob is empty.
pub fn build_blob(element: &CodeElement) -> Option<String> {
    let kind = classify(&element.element_type);
    let raw = match kind {
        BlobKind::Code => build_code_blob(element),
        BlobKind::Ontology => build_ontology_blob(element),
        BlobKind::Doc => build_doc_blob(element),
        BlobKind::Skip => return None,
    };
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        None
    } else {
        Some(truncate_to_chars(trimmed, max_blob_chars()).to_string())
    }
}

fn build_code_blob(element: &CodeElement) -> String {
    let mut parts: Vec<String> = Vec::with_capacity(4);
    parts.push(element.qualified_name.clone());
    if !element.name.is_empty() && element.name != element.qualified_name {
        parts.push(element.name.clone());
    }
    if let Some(doc) = extract_doc_signature(&element.metadata) {
        parts.push(doc);
    } else {
        // Fallback: keep file path (weak signal but cheap) and try to
        // synthesize a signature-like line from any structured metadata
        // (parameters / return_type) the indexer might have stored.
        if !element.file_path.is_empty() {
            parts.push(element.file_path.clone());
        }
        if let Some(sig) = synthesize_signature(&element.name, &element.metadata) {
            parts.push(sig);
        }
    }
    parts.join("\n")
}

/// Synthesize a Rust/TS-style signature line from structured metadata, when
/// the indexer didn't store a pre-formatted `signature` / `doc_comment`.
///
/// Looks for `parameters` (array of strings, array of `{name, type}` objects,
/// or a single object with named fields) and an optional `return_type`.
/// Returns `None` if neither parameters nor return_type are usable.
fn synthesize_signature(name: &str, metadata: &serde_json::Value) -> Option<String> {
    let params = format_parameters(metadata.get("parameters"));
    let return_type = metadata
        .get("return_type")
        .and_then(|v| v.as_str())
        .filter(|s| !s.trim().is_empty())
        .map(|s| s.trim().to_string());

    if params.is_none() && return_type.is_none() {
        return None;
    }

    let params_str = params.unwrap_or_default();
    let mut line = format!("fn {name}({params_str})");
    if let Some(rt) = return_type {
        line.push_str(" -> ");
        line.push_str(&rt);
    }
    Some(line)
}

/// Convert a `parameters` JSON value into a comma-separated `name: Type` list.
/// Defensive about shape: accepts arrays of strings, arrays of objects with
/// `name`/`type`, or an object mapping name -> type.
fn format_parameters(value: Option<&serde_json::Value>) -> Option<String> {
    let value = value?;
    let items: Vec<String> = if let Some(arr) = value.as_array() {
        if arr.is_empty() {
            return None;
        }
        arr.iter().filter_map(param_to_string).collect()
    } else if let Some(obj) = value.as_object() {
        if obj.is_empty() {
            return None;
        }
        obj.iter()
            .map(|(k, v)| {
                let ty = v.as_str().unwrap_or("");
                if ty.is_empty() {
                    k.clone()
                } else {
                    format!("{k}: {ty}")
                }
            })
            .collect()
    } else {
        return None;
    };

    if items.is_empty() {
        None
    } else {
        Some(items.join(", "))
    }
}

fn param_to_string(v: &serde_json::Value) -> Option<String> {
    if let Some(s) = v.as_str() {
        let trimmed = s.trim();
        if trimmed.is_empty() {
            return None;
        }
        return Some(trimmed.to_string());
    }
    if let Some(obj) = v.as_object() {
        let name = obj.get("name").and_then(|n| n.as_str())?.trim();
        if name.is_empty() {
            return None;
        }
        let ty = obj
            .get("type")
            .and_then(|t| t.as_str())
            .unwrap_or("")
            .trim();
        return Some(if ty.is_empty() {
            name.to_string()
        } else {
            format!("{name}: {ty}")
        });
    }
    None
}

fn build_ontology_blob(element: &CodeElement) -> String {
    let mut parts: Vec<String> = Vec::with_capacity(4);
    parts.push(element.name.clone());
    if let Some(aliases) = element.metadata.get("aliases").and_then(|v| v.as_array()) {
        let alias_str: Vec<String> = aliases
            .iter()
            .filter_map(|v| v.as_str().map(String::from))
            .collect();
        if !alias_str.is_empty() {
            parts.push(alias_str.join(", "));
        }
    }
    if let Some(desc) = element.metadata.get("description").and_then(|v| v.as_str()) {
        if !desc.is_empty() {
            parts.push(desc.to_string());
        }
    }
    parts.push(element.element_type.clone());
    parts.join("\n")
}

fn build_doc_blob(element: &CodeElement) -> String {
    let mut parts: Vec<String> = Vec::with_capacity(3);
    if let Some(title) = element.metadata.get("title").and_then(|v| v.as_str()) {
        parts.push(title.to_string());
    }
    if let Some(heading) = element
        .metadata
        .get("heading_path")
        .and_then(|v| v.as_array())
    {
        let heading_str: Vec<String> = heading
            .iter()
            .filter_map(|v| v.as_str().map(String::from))
            .collect();
        if !heading_str.is_empty() {
            parts.push(heading_str.join(" / "));
        }
    }
    if let Some(body) = element
        .metadata
        .get("first_paragraph")
        .and_then(|v| v.as_str())
    {
        parts.push(body.to_string());
    }
    parts.join("\n")
}

/// Pull a doc comment / signature out of the CodeElement metadata, if the
/// indexer stored one. Different extractor paths use different keys; we
/// accept any of the known ones.
fn extract_doc_signature(metadata: &serde_json::Value) -> Option<String> {
    for key in &[
        "doc_comment",
        "doc",
        "signature",
        "signature_text",
        "description",
        "comment",
        "docstring",
    ] {
        if let Some(s) = metadata.get(key).and_then(|v| v.as_str()) {
            if !s.trim().is_empty() {
                return Some(s.to_string());
            }
        }
    }
    None
}

fn truncate_to_chars(s: &str, max_chars: usize) -> &str {
    if s.len() <= max_chars {
        return s;
    }
    let mut end = max_chars;
    while end > 0 && !s.is_char_boundary(end) {
        end -= 1;
    }
    &s[..end]
}

/// SHA-256 hex digest of the text blob. Stored in `embedding_state.content_hash`
/// to detect content changes between embed runs.
pub fn content_hash_for(blob: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(blob.as_bytes());
    format!("{:x}", hasher.finalize())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_element(element_type: &str, name: &str, qualified_name: &str) -> CodeElement {
        CodeElement {
            element_type: element_type.to_string(),
            name: name.to_string(),
            qualified_name: qualified_name.to_string(),
            ..Default::default()
        }
    }

    #[test]
    fn classify_known_types() {
        assert_eq!(classify("function"), BlobKind::Code);
        assert_eq!(classify("struct"), BlobKind::Code);
        assert_eq!(classify("document"), BlobKind::Doc);
        assert_eq!(classify("File"), BlobKind::Code);
        assert_eq!(classify("workflow"), BlobKind::Ontology);
        assert_eq!(classify("cluster"), BlobKind::Skip);
    }

    #[test]
    fn doc_blob_uses_title_and_paragraph() {
        let mut el = make_element("document", "PRD", "docs/prd.md");
        el.metadata = serde_json::json!({
            "title": "LeanKG PRD",
            "first_paragraph": "Product requirements for embedding."
        });
        let blob = build_blob(&el).unwrap();
        assert!(blob.contains("LeanKG PRD"));
        assert!(blob.contains("Product requirements"));
    }

    #[test]
    fn code_blob_uses_qualified_name_and_doc() {
        let mut el = make_element("function", "do_thing", "src/main.rs::do_thing");
        el.metadata = serde_json::json!({"doc_comment": "Does the thing."});
        let blob = build_blob(&el).unwrap();
        assert!(blob.contains("src/main.rs::do_thing"));
        assert!(blob.contains("do_thing"));
        assert!(blob.contains("Does the thing."));
    }

    #[test]
    fn ontology_blob_includes_aliases_and_description() {
        let mut el = make_element(
            "domain_entity",
            "Refund",
            "ontology://local:checkout:domain_entity:refund:v1",
        );
        el.metadata = serde_json::json!({
            "aliases": ["reversal", "chargeback"],
            "description": "Money returned to a customer after payment capture"
        });
        let blob = build_blob(&el).unwrap();
        assert!(blob.contains("Refund"));
        assert!(blob.contains("reversal"));
        assert!(blob.contains("chargeback"));
        assert!(blob.contains("Money returned"));
        assert!(blob.contains("domain_entity"));
    }

    #[test]
    fn skip_element_types_return_none() {
        let el = make_element("cluster", "cluster1", "cluster://x");
        assert!(build_blob(&el).is_none());
    }

    #[test]
    fn truncation_respects_char_boundaries() {
        let s = "a".repeat(2000);
        let truncated = truncate_to_chars(&s, MAX_BLOB_CHARS);
        assert_eq!(truncated.len(), MAX_BLOB_CHARS);
        // max_blob_chars() may be tighter under LEANKG_EMBED_FAST; constant
        // remains the legacy ceiling.
        assert!(max_blob_chars() <= MAX_BLOB_CHARS);
    }

    #[test]
    fn synthesize_signature_array_of_strings() {
        let meta = serde_json::json!({"parameters": ["x", "y"]});
        let sig = synthesize_signature("add", &meta).unwrap();
        assert_eq!(sig, "fn add(x, y)");
    }

    #[test]
    fn synthesize_signature_array_of_objects_with_name_and_type() {
        let meta = serde_json::json!({
            "parameters": [
                {"name": "x", "type": "i32"},
                {"name": "y", "type": "i32"}
            ],
            "return_type": "i32"
        });
        let sig = synthesize_signature("add", &meta).unwrap();
        assert_eq!(sig, "fn add(x: i32, y: i32) -> i32");
    }

    #[test]
    fn synthesize_signature_return_type_only() {
        let meta = serde_json::json!({"return_type": "void"});
        let sig = synthesize_signature("noop", &meta).unwrap();
        assert_eq!(sig, "fn noop() -> void");
    }

    #[test]
    fn synthesize_signature_empty_params_with_return_type() {
        let meta = serde_json::json!({"parameters": [], "return_type": "Bool"});
        let sig = synthesize_signature("is_ready", &meta).unwrap();
        assert_eq!(sig, "fn is_ready() -> Bool");
    }

    #[test]
    fn synthesize_signature_no_metadata_returns_none() {
        let meta = serde_json::json!({});
        assert!(synthesize_signature("foo", &meta).is_none());
    }

    #[test]
    fn synthesize_signature_object_mapping_params() {
        let meta = serde_json::json!({
            "parameters": {"x": "String", "y": "usize"}
        });
        let sig = synthesize_signature("concat", &meta).unwrap();
        assert_eq!(sig, "fn concat(x: String, y: usize)");
    }

    #[test]
    fn code_blob_falls_back_to_synthesized_signature() {
        let mut el = make_element("function", "detect_root", "src/main.rs::detect_root");
        el.file_path = "./src/main.rs".to_string();
        el.metadata = serde_json::json!({
            "parameters": [{"name": "path", "type": "PathBuf"}],
            "return_type": "Option<PathBuf>"
        });
        let blob = build_blob(&el).unwrap();
        assert!(blob.contains("src/main.rs::detect_root"));
        assert!(blob.contains("fn detect_root(path: PathBuf) -> Option<PathBuf>"));
    }
}
