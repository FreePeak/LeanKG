#![allow(clippy::regex_creation_in_loops)]

use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static BROWSE_FRAGMENT_RE: OnceLock<Regex> = OnceLock::new();
static ITEM_CLICKED_RE: OnceLock<Regex> = OnceLock::new();
static DETAILS_ACTIVITY_RE: OnceLock<Regex> = OnceLock::new();
static FRAGMENT_RE: OnceLock<Regex> = OnceLock::new();

pub struct LeanbackNavExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> LeanbackNavExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = match std::str::from_utf8(self.source) {
            Ok(s) => s,
            Err(_) => return (Vec::new(), Vec::new()),
        };

        let browse_re = BROWSE_FRAGMENT_RE.get_or_init(|| {
            Regex::new(r"class\s+(\w+)\s*:\s*(?:BrowseSupportFragment|BrowseFragment|VerticalGridSupportFragment)\s*\(").unwrap()
        });
        let details_re = DETAILS_ACTIVITY_RE.get_or_init(|| {
            Regex::new(r"startActivity\s*\(\s*Intent\s*\([^,]+,\s*(\w+Activity)::class\.java\s*\)")
                .unwrap()
        });
        let item_clicked_re =
            ITEM_CLICKED_RE.get_or_init(|| Regex::new(r"setOnItemViewClickedListener\b").unwrap());
        let frag_re = FRAGMENT_RE.get_or_init(|| Regex::new(r"(\w+Fragment)\s*\(").unwrap());

        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        for cap in browse_re.captures_iter(content) {
            if let Some(class_match) = cap.get(1) {
                let class_name = class_match.as_str();
                let elem_qn = format!("{}::{}", self.file_path, class_name);

                elements.push(CodeElement {
                    qualified_name: elem_qn.clone(),
                    element_type: "nav_destination".to_string(),
                    name: class_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({
                        "dest_type": "leanback_browse",
                        "class_name": class_name,
                    }),
                    ..Default::default()
                });

                for dcap in details_re.captures_iter(content) {
                    if let Some(activity_match) = dcap.get(1) {
                        let activity_name = activity_match.as_str();
                        relationships.push(Relationship {
                            id: None,
                            source_qualified: elem_qn.clone(),
                            target_qualified: format!("class:{}", activity_name),
                            rel_type: "presents".to_string(),
                            confidence: 0.80,
                            metadata: serde_json::json!({
                                "nav_type": "leanback_browse_to_details",
                                "activity_name": activity_name,
                            }),
                        });
                    }
                }

                if item_clicked_re.is_match(content) {
                    let listener_pos = item_clicked_re
                        .find(content)
                        .map(|m| m.start())
                        .unwrap_or(0);
                    let window =
                        &content[listener_pos..std::cmp::min(listener_pos + 500, content.len())];
                    for fcap in frag_re.captures_iter(window) {
                        if let Some(fm) = fcap.get(1) {
                            let frag_name = fm.as_str();
                            if frag_name != class_name {
                                relationships.push(Relationship {
                                    id: None,
                                    source_qualified: elem_qn.clone(),
                                    target_qualified: format!("class:{}", frag_name),
                                    rel_type: "presents".to_string(),
                                    confidence: 0.75,
                                    metadata: serde_json::json!({
                                        "nav_type": "leanback_item_click",
                                        "fragment_name": frag_name,
                                    }),
                                });
                            }
                        }
                    }
                }
            }
        }

        (elements, relationships)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_browse_fragment_detected() {
        let source = r#"
class MainFragment : BrowseSupportFragment() {
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        setOnItemViewClickedListener { _, item, _, _ ->
            startActivity(Intent(activity, DetailsActivity::class.java))
        }
    }
}
"#;
        let extractor = LeanbackNavExtractor::new(source.as_bytes(), "tv/MainFragment.kt");
        let (elements, relationships) = extractor.extract();

        let browse_elem = elements
            .iter()
            .find(|e| e.element_type == "nav_destination" && e.name.contains("MainFragment"));
        assert!(
            browse_elem.is_some(),
            "Should detect BrowseSupportFragment as nav_destination"
        );

        let nav_rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "presents")
            .collect();
        assert!(!nav_rels.is_empty(), "Should find presents relationship");
        assert!(nav_rels[0].target_qualified.contains("DetailsActivity"));
    }

    #[test]
    fn test_non_leanback_not_detected() {
        let source = r#"
class RegularFragment : Fragment() {
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {}
}
"#;
        let extractor = LeanbackNavExtractor::new(source.as_bytes(), "ui/RegularFragment.kt");
        let (elements, relationships) = extractor.extract();

        assert!(
            elements.is_empty(),
            "Non-leanback fragment should produce no elements"
        );
        assert!(relationships.is_empty());
    }
}
