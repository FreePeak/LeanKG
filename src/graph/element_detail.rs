//! Element detail shaping for the lightweight Graph View (`GET /api/element`).
//!
//! Pure helpers live here so ui-lite can TDD without spinning Axum.

use crate::db::models::{BusinessLogic, CodeElement, Relationship};
use serde::{Deserialize, Serialize};

/// Default max neighbors kept per direction (out / in).
pub const DEFAULT_NEIGHBOR_CAP: usize = 20;

/// Default pad lines around `line_start..=line_end` for code snippets.
pub const DEFAULT_SNIPPET_PAD: u32 = 3;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct NeighborEntry {
    pub direction: String,
    pub rel_type: String,
    pub peer: String,
    pub confidence_label: String,
    pub confidence: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ElementDetail {
    pub element: CodeElement,
    pub neighbors: Vec<NeighborEntry>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub annotation: Option<BusinessLogic>,
}

/// Build capped neighbor rows from outbound (source=self) and inbound (target=self) edges.
pub fn collect_neighbors(
    outbound: &[Relationship],
    inbound: &[Relationship],
    self_qn: &str,
    cap_per_direction: usize,
) -> Vec<NeighborEntry> {
    let mut out: Vec<NeighborEntry> = outbound
        .iter()
        .filter(|r| r.source_qualified == self_qn)
        .take(cap_per_direction)
        .map(|r| NeighborEntry {
            direction: "out".to_string(),
            rel_type: r.rel_type.clone(),
            peer: r.target_qualified.clone(),
            confidence_label: r.confidence_label().to_string(),
            confidence: r.confidence,
        })
        .collect();

    let inn: Vec<NeighborEntry> = inbound
        .iter()
        .filter(|r| r.target_qualified == self_qn)
        .take(cap_per_direction)
        .map(|r| NeighborEntry {
            direction: "in".to_string(),
            rel_type: r.rel_type.clone(),
            peer: r.source_qualified.clone(),
            confidence_label: r.confidence_label().to_string(),
            confidence: r.confidence,
        })
        .collect();

    out.extend(inn);
    out
}

/// Inclusive 1-based line range clipped to `[1, total_lines]` with pad.
pub fn clip_snippet_range(
    line_start: u32,
    line_end: u32,
    pad: u32,
    total_lines: u32,
) -> (u32, u32) {
    if total_lines == 0 {
        return (0, 0);
    }
    let start = line_start.max(1).saturating_sub(pad).max(1);
    let end = line_end.saturating_add(pad).min(total_lines).max(start);
    (start, end)
}

/// Pull `metadata.signature` when present and non-empty.
pub fn extract_signature(metadata: &serde_json::Value) -> Option<String> {
    metadata
        .get("signature")
        .and_then(|v| v.as_str())
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(str::to_string)
}

/// Assemble the detail payload from already-fetched DB rows.
pub fn build_element_detail(
    element: CodeElement,
    outbound: &[Relationship],
    inbound: &[Relationship],
    annotation: Option<BusinessLogic>,
    cap_per_direction: usize,
) -> ElementDetail {
    let qn = element.qualified_name.clone();
    ElementDetail {
        neighbors: collect_neighbors(outbound, inbound, &qn, cap_per_direction),
        element,
        annotation,
    }
}

/// Load element detail from the graph engine (keyed lookups only — mega-safe).
pub fn fetch_element_detail(
    engine: &crate::graph::GraphEngine,
    qn: &str,
    cap_per_direction: usize,
) -> Result<Option<ElementDetail>, Box<dyn std::error::Error>> {
    let Some(element) = engine.find_element(qn)? else {
        return Ok(None);
    };
    let outbound = engine.get_relationships(qn)?;
    let inbound = engine.get_relationships_for_target(qn)?;
    let annotation = crate::db::get_business_logic(engine.db(), qn)?;
    Ok(Some(build_element_detail(
        element,
        &outbound,
        &inbound,
        annotation,
        cap_per_direction,
    )))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::models::Relationship;

    fn rel(source: &str, target: &str, rel_type: &str, confidence: f64) -> Relationship {
        Relationship {
            source_qualified: source.to_string(),
            target_qualified: target.to_string(),
            rel_type: rel_type.to_string(),
            confidence,
            metadata: serde_json::json!({}),
            ..Default::default()
        }
    }

    #[test]
    fn collect_neighbors_includes_outbound_and_inbound_with_labels() {
        let self_qn = "src/a.rs::foo";
        let outbound = vec![rel(self_qn, "src/a.rs::bar", "calls", 1.0)];
        let inbound = vec![rel("src/a.rs::baz", self_qn, "calls", 0.4)];

        let neighbors = collect_neighbors(&outbound, &inbound, self_qn, 20);
        assert_eq!(neighbors.len(), 2);

        let out = neighbors
            .iter()
            .find(|n| n.direction == "out")
            .expect("outbound neighbor");
        assert_eq!(out.peer, "src/a.rs::bar");
        assert_eq!(out.rel_type, "calls");
        assert_eq!(out.confidence_label, "EXTRACTED");

        let inn = neighbors
            .iter()
            .find(|n| n.direction == "in")
            .expect("inbound neighbor");
        assert_eq!(inn.peer, "src/a.rs::baz");
        assert_eq!(inn.confidence_label, "AMBIGUOUS");
    }

    #[test]
    fn collect_neighbors_caps_each_direction() {
        let self_qn = "src/a.rs::hub";
        let outbound: Vec<_> = (0..30)
            .map(|i| rel(self_qn, &format!("src/a.rs::t{i}"), "calls", 1.0))
            .collect();
        let inbound: Vec<_> = (0..25)
            .map(|i| rel(&format!("src/a.rs::s{i}"), self_qn, "imports", 1.0))
            .collect();

        let neighbors = collect_neighbors(&outbound, &inbound, self_qn, 5);
        let out_count = neighbors.iter().filter(|n| n.direction == "out").count();
        let in_count = neighbors.iter().filter(|n| n.direction == "in").count();
        assert_eq!(out_count, 5);
        assert_eq!(in_count, 5);
    }

    #[test]
    fn clip_snippet_range_applies_pad_and_bounds() {
        assert_eq!(clip_snippet_range(10, 12, 3, 100), (7, 15));
        assert_eq!(clip_snippet_range(1, 2, 3, 50), (1, 5));
        assert_eq!(clip_snippet_range(48, 50, 3, 50), (45, 50));
        assert_eq!(clip_snippet_range(5, 5, 0, 10), (5, 5));
    }

    #[test]
    fn extract_signature_reads_metadata() {
        assert_eq!(
            extract_signature(&serde_json::json!({"signature": "fn foo() -> i32"})),
            Some("fn foo() -> i32".to_string())
        );
        assert_eq!(extract_signature(&serde_json::json!({})), None);
        assert_eq!(
            extract_signature(&serde_json::json!({"signature": ""})),
            None
        );
    }

    #[test]
    fn build_element_detail_includes_annotation_when_present() {
        let element = CodeElement {
            qualified_name: "src/a.rs::foo".to_string(),
            element_type: "function".to_string(),
            name: "foo".to_string(),
            file_path: "src/a.rs".to_string(),
            line_start: 1,
            line_end: 5,
            language: "rust".to_string(),
            metadata: serde_json::json!({"signature": "fn foo()"}),
            ..Default::default()
        };
        let annotation = Some(BusinessLogic {
            id: None,
            element_qualified: "src/a.rs::foo".to_string(),
            description: "entry point".to_string(),
            user_story_id: Some("US-1".to_string()),
            feature_id: None,
        });
        let detail = build_element_detail(element, &[], &[], annotation, 20);
        assert_eq!(
            detail.annotation.as_ref().unwrap().description,
            "entry point"
        );
        assert_eq!(
            extract_signature(&detail.element.metadata).as_deref(),
            Some("fn foo()")
        );
    }

    fn make_test_engine() -> (crate::graph::GraphEngine, tempfile::TempDir) {
        let tmp = tempfile::TempDir::new().unwrap();
        let db_path = tmp.path().join("test.db");
        let db = crate::db::backend::init_db(&db_path).unwrap();
        (crate::graph::GraphEngine::new(db), tmp)
    }

    #[test]
    fn fetch_element_detail_returns_none_for_missing() {
        let (engine, _tmp) = make_test_engine();
        let detail = fetch_element_detail(&engine, "missing::qn", 20).unwrap();
        assert!(detail.is_none());
    }

    #[test]
    fn fetch_element_detail_loads_element_neighbors_and_annotation() {
        let (engine, _tmp) = make_test_engine();
        let foo = CodeElement {
            qualified_name: "src/a.rs::foo".to_string(),
            element_type: "function".to_string(),
            name: "foo".to_string(),
            file_path: "src/a.rs".to_string(),
            line_start: 10,
            line_end: 20,
            language: "rust".to_string(),
            metadata: serde_json::json!({"signature": "fn foo()"}),
            ..Default::default()
        };
        let bar = CodeElement {
            qualified_name: "src/a.rs::bar".to_string(),
            element_type: "function".to_string(),
            name: "bar".to_string(),
            file_path: "src/a.rs".to_string(),
            line_start: 30,
            line_end: 40,
            language: "rust".to_string(),
            ..Default::default()
        };
        engine.insert_element(&foo).unwrap();
        engine.insert_element(&bar).unwrap();
        engine
            .insert_relationships(&[rel("src/a.rs::foo", "src/a.rs::bar", "calls", 1.0)])
            .unwrap();
        crate::db::create_business_logic(
            engine.db(),
            "src/a.rs::foo",
            "does work",
            Some("US-42"),
            None,
        )
        .unwrap();

        let detail = fetch_element_detail(&engine, "src/a.rs::foo", 20)
            .unwrap()
            .expect("detail");
        assert_eq!(detail.element.name, "foo");
        assert_eq!(detail.element.line_start, 10);
        assert!(
            detail
                .neighbors
                .iter()
                .any(|n| n.direction == "out" && n.peer == "src/a.rs::bar"),
            "expected outbound call to bar"
        );
        assert_eq!(
            detail.annotation.as_ref().unwrap().user_story_id.as_deref(),
            Some("US-42")
        );
    }
}
