use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static FRAGMENT_REPLACE_RE: OnceLock<Regex> = OnceLock::new();
static BACKSTACK_RE: OnceLock<Regex> = OnceLock::new();
static START_ACTIVITY_RE: OnceLock<Regex> = OnceLock::new();
static NAV_CONTROLLER_RE: OnceLock<Regex> = OnceLock::new();

pub struct FragmentNavExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> FragmentNavExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = match std::str::from_utf8(self.source) {
            Ok(s) => s,
            Err(_) => return (Vec::new(), Vec::new()),
        };

        let mut relationships = Vec::new();

        let replace_re = FRAGMENT_REPLACE_RE.get_or_init(|| {
            Regex::new(r"\.(?:replace|add)\s*\(\s*[^,]+,\s*(\w+Fragment)\s*\(").unwrap()
        });

        let backstack_re = BACKSTACK_RE
            .get_or_init(|| Regex::new(r#"\.addToBackStack\s*\(\s*"([^"]+)"\s*\)"#).unwrap());

        for cap in replace_re.captures_iter(content) {
            if let Some(frag_match) = cap.get(1) {
                let fragment_name = frag_match.as_str();
                let target_qn = format!("class:{}", fragment_name);

                let pos = cap.get(0).map(|m| m.start()).unwrap_or(0);
                let window = &content[pos..std::cmp::min(pos + 300, content.len())];
                let backstack_tag = backstack_re
                    .captures(window)
                    .and_then(|bc| bc.get(1))
                    .map(|m| m.as_str().to_string());

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: target_qn,
                    rel_type: "navigates_to".to_string(),
                    confidence: 0.85,
                    metadata: serde_json::json!({
                        "nav_type": "fragment_manager",
                        "fragment_name": fragment_name,
                        "backstack_tag": backstack_tag,
                    }),
                });
            }
        }

        let start_activity_re = START_ACTIVITY_RE.get_or_init(|| {
            Regex::new(r"startActivity\s*\(\s*Intent\s*\([^,]+,\s*(\w+Activity)::class\.java\s*\)")
                .unwrap()
        });

        for cap in start_activity_re.captures_iter(content) {
            if let Some(activity_match) = cap.get(1) {
                let activity_name = activity_match.as_str();
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("class:{}", activity_name),
                    rel_type: "navigates_to".to_string(),
                    confidence: 0.90,
                    metadata: serde_json::json!({
                        "nav_type": "start_activity",
                        "activity_name": activity_name,
                    }),
                });
            }
        }

        let nav_controller_re = NAV_CONTROLLER_RE.get_or_init(|| {
            Regex::new(r#"(?:findNavController|navController)\s*\(\s*\)\s*\.navigate\s*\(\s*(?:R\.id\.([\w_]+)|"([^"]+)")"#).unwrap()
        });

        for cap in nav_controller_re.captures_iter(content) {
            let action_or_route = cap
                .get(1)
                .or_else(|| cap.get(2))
                .map(|m| m.as_str().to_string())
                .unwrap_or_default();

            if !action_or_route.is_empty() {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("nav_action:{}", action_or_route),
                    rel_type: "navigates_to".to_string(),
                    confidence: 0.85,
                    metadata: serde_json::json!({
                        "nav_type": "nav_controller",
                        "action_or_route": action_or_route,
                    }),
                });
            }
        }

        (Vec::new(), relationships)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_fragment_replace_extraction() {
        let source = r#"
fun navigate() {
    supportFragmentManager.beginTransaction()
        .replace(R.id.container, DetailFragment())
        .addToBackStack("detail")
        .commit()
}
"#;
        let extractor = FragmentNavExtractor::new(source.as_bytes(), "ui/MainActivity.kt");
        let (_, relationships) = extractor.extract();

        let nav_rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "navigates_to")
            .collect();
        assert!(
            !nav_rels.is_empty(),
            "Should find navigates_to relationship"
        );
        assert!(
            nav_rels[0].target_qualified.contains("DetailFragment"),
            "Target should be DetailFragment"
        );
        assert_eq!(
            nav_rels[0]
                .metadata
                .get("backstack_tag")
                .and_then(|v| v.as_str()),
            Some("detail"),
            "Should capture backstack tag"
        );
    }

    #[test]
    fn test_start_activity_extraction() {
        let source = r#"
fun openProfile() {
    startActivity(Intent(this, ProfileActivity::class.java))
}
"#;
        let extractor = FragmentNavExtractor::new(source.as_bytes(), "ui/HomeFragment.kt");
        let (_, relationships) = extractor.extract();

        let nav_rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "navigates_to")
            .collect();
        assert!(
            !nav_rels.is_empty(),
            "Should find navigates_to from startActivity"
        );
        assert!(nav_rels[0].target_qualified.contains("ProfileActivity"));
    }

    #[test]
    fn test_nav_controller_navigate() {
        let source = r#"
fun goToDetail() {
    findNavController().navigate(R.id.action_home_to_detail)
}
"#;
        let extractor = FragmentNavExtractor::new(source.as_bytes(), "ui/HomeFragment.kt");
        let (_, relationships) = extractor.extract();

        let nav_rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "navigates_to")
            .collect();
        assert!(
            !nav_rels.is_empty(),
            "Should find navigates_to from NavController"
        );
        assert!(nav_rels[0]
            .target_qualified
            .contains("action_home_to_detail"));
    }
}
