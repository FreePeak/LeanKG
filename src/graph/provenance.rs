//! ENT-9: Single source of truth for edge provenance labels and element
//! synthetic markers across ALL graph responses.
//!
//! Every tool response that contains relationships/edges must carry a
//! `confidence_label ∈ {EXTRACTED, INFERRED, AMBIGUOUS}` per edge, and
//! elements that are not extractor-derived code (synthetic / ontology /
//! event rows) are flagged so agents can calibrate trust. The thresholds
//! here are canonical — `Relationship::confidence_label()` delegates to
//! [`confidence_label_for`], and serializers call it directly when they
//! only have raw confidence + metadata at hand.

use crate::db::models::confidence_labels;

/// ENT-9: Derive the canonical confidence label from a relationship's
/// confidence score and its resolver's `resolution_method`.
///
/// Exact semantics (must stay in sync with historical behaviour):
/// - `resolution_method = "typed"`             -> EXTRACTED (AST-verified)
/// - `resolution_method = "name"`, conf >= 0.8 -> EXTRACTED
/// - `resolution_method = "name_file_hint"`    -> INFERRED when conf >= 0.6,
///   otherwise falls through to the threshold ladder
/// - `resolution_method = "name"`              -> INFERRED otherwise
/// - `resolution_method = "unresolved"`        -> AMBIGUOUS always
/// - no method: conf >= 0.8 -> EXTRACTED; conf >= 0.5 -> INFERRED; else AMBIGUOUS
pub fn confidence_label_for(confidence: f64, resolution_method: Option<&str>) -> &'static str {
    match resolution_method {
        Some("typed") => confidence_labels::EXTRACTED,
        Some("name") if confidence >= 0.8 => confidence_labels::EXTRACTED,
        Some("name_file_hint") if confidence >= 0.6 => confidence_labels::INFERRED,
        Some("name") => confidence_labels::INFERRED,
        Some("unresolved") => confidence_labels::AMBIGUOUS,
        _ if confidence >= 0.8 => confidence_labels::EXTRACTED,
        _ if confidence >= 0.5 => confidence_labels::INFERRED,
        _ => confidence_labels::AMBIGUOUS,
    }
}

/// ENT-9: Convenience wrapper that reads `resolution_method` out of a
/// relationship's metadata blob before delegating to [`confidence_label_for`].
pub fn confidence_label_for_metadata(
    confidence: f64,
    metadata: &serde_json::Value,
) -> &'static str {
    let method = metadata.get("resolution_method").and_then(|v| v.as_str());
    confidence_label_for(confidence, method)
}

/// ENT-9: Element provenance source — `"extracted"`, `"synthetic"` or
/// `"ontology"` depending on how the row entered the graph.
pub fn provenance_source(element_type: &str, file_path: &str) -> &'static str {
    if file_path.starts_with("ontology://") {
        return "ontology";
    }
    if is_synthetic_element(element_type, file_path) {
        return "synthetic";
    }
    "extracted"
}

/// ENT-9: True when the element row is not extractor-derived code —
/// synthetic/summary/event element types, or URI-style paths minted by the
/// ontology/event pipelines (`ontology://`, `event://`).
pub fn is_synthetic_element(element_type: &str, file_path: &str) -> bool {
    matches!(element_type, "synthetic" | "summary" | "event")
        || file_path.starts_with("ontology://")
        || file_path.starts_with("event://")
}

#[cfg(test)]
mod tests {
    use super::*;

    // ---- threshold ladder (no resolution_method) --------------------------

    #[test]
    fn perfect_confidence_is_extracted() {
        assert_eq!(confidence_label_for(1.0, None), "EXTRACTED");
    }

    #[test]
    fn high_confidence_boundary_is_extracted() {
        assert_eq!(confidence_label_for(0.8, None), "EXTRACTED");
        assert_eq!(confidence_label_for(0.95, None), "EXTRACTED");
    }

    #[test]
    fn mid_confidence_is_inferred() {
        assert_eq!(confidence_label_for(0.65, None), "INFERRED");
        assert_eq!(confidence_label_for(0.5, None), "INFERRED");
        assert_eq!(confidence_label_for(0.79, None), "INFERRED");
    }

    #[test]
    fn low_or_absent_confidence_is_ambiguous() {
        assert_eq!(confidence_label_for(0.3, None), "AMBIGUOUS");
        assert_eq!(confidence_label_for(0.49, None), "AMBIGUOUS");
        assert_eq!(confidence_label_for(0.0, None), "AMBIGUOUS");
    }

    // ---- resolution_method overrides --------------------------------------

    #[test]
    fn typed_resolution_always_extracted() {
        assert_eq!(confidence_label_for(1.0, Some("typed")), "EXTRACTED");
        assert_eq!(confidence_label_for(0.2, Some("typed")), "EXTRACTED");
    }

    #[test]
    fn name_resolution_threshold_at_eighty() {
        assert_eq!(confidence_label_for(0.85, Some("name")), "EXTRACTED");
        assert_eq!(confidence_label_for(0.7, Some("name")), "INFERRED");
    }

    #[test]
    fn name_file_hint_inferred_at_sixty() {
        assert_eq!(
            confidence_label_for(0.6, Some("name_file_hint")),
            "INFERRED"
        );
        // Below 0.6 it falls through to the threshold ladder -> AMBIGUOUS.
        assert_eq!(
            confidence_label_for(0.4, Some("name_file_hint")),
            "AMBIGUOUS"
        );
    }

    #[test]
    fn unresolved_is_always_ambiguous() {
        assert_eq!(confidence_label_for(1.0, Some("unresolved")), "AMBIGUOUS");
        assert_eq!(confidence_label_for(0.0, Some("unresolved")), "AMBIGUOUS");
    }

    #[test]
    fn unknown_method_falls_through_to_ladder() {
        assert_eq!(
            confidence_label_for(0.9, Some("future_method")),
            "EXTRACTED"
        );
        assert_eq!(
            confidence_label_for(0.55, Some("future_method")),
            "INFERRED"
        );
    }

    // ---- metadata wrapper --------------------------------------------------

    #[test]
    fn metadata_wrapper_reads_resolution_method() {
        let md = serde_json::json!({"resolution_method": "typed"});
        assert_eq!(confidence_label_for_metadata(0.1, &md), "EXTRACTED");

        let empty = serde_json::json!({});
        assert_eq!(confidence_label_for_metadata(0.6, &empty), "INFERRED");
        assert_eq!(confidence_label_for_metadata(0.85, &empty), "EXTRACTED");
    }

    // ---- element provenance ------------------------------------------------

    #[test]
    fn plain_code_element_is_extracted() {
        assert_eq!(provenance_source("function", "src/main.rs"), "extracted");
        assert!(!is_synthetic_element("function", "src/main.rs"));
    }

    #[test]
    fn synthetic_summary_and_event_types_flagged() {
        for t in ["synthetic", "summary", "event"] {
            assert!(is_synthetic_element(t, "anywhere"), "type {t}");
            assert_eq!(provenance_source(t, "anywhere"), "synthetic");
        }
    }

    #[test]
    fn ontology_uri_paths_flagged_as_ontology() {
        assert!(is_synthetic_element(
            "domain_entity",
            "ontology://local:checkout:domain_entity:refund:v1"
        ));
        assert_eq!(
            provenance_source("domain_entity", "ontology://local:x:v1"),
            "ontology",
            "ontology:// wins over generic synthetic"
        );
    }

    #[test]
    fn event_uri_paths_flagged() {
        assert!(is_synthetic_element("event", "event://deploy"));
        assert_eq!(provenance_source("file", "event://deploy"), "synthetic");
    }
}
