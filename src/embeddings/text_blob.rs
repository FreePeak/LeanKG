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
pub fn classify(element_type: &str) -> BlobKind {
    match element_type {
        "file" | "function" | "class" | "module" | "method" | "trait" | "interface" => BlobKind::Code,
        "domain_entity"
        | "service"
        | "api_endpoint"
        | "data_store"
        | "environment"
        | "known_issue"
        | "playbook"
        | "playbook_step"
        | "team_knowledge"
        | "workflow"
        | "workflow_step"
        | "decision_point"
        | "failure_mode" => BlobKind::Ontology,
        // Skip clusters/processes/etc.: they're grouping abstractions whose
        // members already get embedded individually.
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
        Some(truncate_to_chars(trimmed, MAX_BLOB_CHARS).to_string())
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
        // Fallback: file path + language as a weak signature stand-in.
        if !element.file_path.is_empty() {
            parts.push(element.file_path.clone());
        }
    }
    parts.join("\n")
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
    if let Some(body) = element.metadata.get("first_paragraph").and_then(|v| v.as_str()) {
        parts.push(body.to_string());
    }
    parts.join("\n")
}

/// Pull a doc comment / signature out of the CodeElement metadata, if the
/// indexer stored one. Different extractor paths use different keys; we
/// accept any of the known ones.
fn extract_doc_signature(metadata: &serde_json::Value) -> Option<String> {
    for key in &["doc_comment", "doc", "signature", "signature_text"] {
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
        assert_eq!(classify("class"), BlobKind::Code);
        assert_eq!(classify("workflow"), BlobKind::Ontology);
        assert_eq!(classify("domain_entity"), BlobKind::Ontology);
        assert_eq!(classify("cluster"), BlobKind::Skip);
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
    }
}
