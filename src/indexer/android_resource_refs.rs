use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static R_REF_RE: OnceLock<Regex> = OnceLock::new();
static RESOURCE_METHOD_RE: OnceLock<Regex> = OnceLock::new();

/// Extractor for Android resource references in Kotlin code
/// Detects R.string.xxx, R.drawable.xxx, R.layout.xxx patterns
pub struct AndroidResourceRefExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> AndroidResourceRefExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = match std::str::from_utf8(self.source) {
            Ok(s) => s,
            Err(_) => {
                eprintln!("warn: non-UTF-8 content in {}, skipping", self.file_path);
                return (Vec::new(), Vec::new());
            }
        };
        let mut relationships = Vec::new();

        // Extract R.xxx.yyy patterns
        let r_refs = self.extract_r_references(content);
        relationships.extend(r_refs);

        // Extract resources.method patterns
        let method_refs = self.extract_resource_methods(content);
        relationships.extend(method_refs);

        // Dedup by (rel_type, target_qualified) — both extractors may match the same reference
        let mut seen = std::collections::HashSet::new();
        relationships.retain(|r| seen.insert((r.rel_type.clone(), r.target_qualified.clone())));

        (Vec::new(), relationships)
    }

    fn extract_r_references(&self, content: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        let re = R_REF_RE.get_or_init(|| Regex::new(r"R\.(\w+)\.(\w+)").unwrap());

        for cap in re.captures_iter(content) {
            if let (Some(type_match), Some(name_match)) = (cap.get(1), cap.get(2)) {
                let res_type = type_match.as_str();
                let res_name = name_match.as_str();

                let rel_type = match res_type {
                    "string" => "uses_string_resource",
                    "drawable" => "uses_drawable_resource",
                    "layout" => "uses_layout_resource",
                    "id" => "references_view_by_id",
                    "color" => "uses_color_resource",
                    "style" => "uses_style_resource",
                    "dimen" => "uses_dimen_resource",
                    "raw" => "uses_raw_resource",
                    "anim" => "uses_anim_resource",
                    "menu" => "uses_menu_resource",
                    "mipmap" => "uses_mipmap_resource",
                    _ => "uses_resource",
                };

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("res/{}/{}", self.resource_dir(res_type), res_name),
                    rel_type: rel_type.to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "resource_type": res_type,
                        "resource_name": res_name,
                    }),
                });
            }
        }

        relationships
    }

    fn extract_resource_methods(&self, content: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        let re = RESOURCE_METHOD_RE.get_or_init(|| {
            Regex::new(r"(?:resources\.)?(?:getString|getText)\s*\(\s*R\.(\w+)\.(\w+)\s*\)")
                .unwrap()
        });

        for cap in re.captures_iter(content) {
            if let (Some(type_match), Some(name_match)) = (cap.get(1), cap.get(2)) {
                let res_type = type_match.as_str();
                let res_name = name_match.as_str();

                if res_type == "string" || res_type == "drawable" || res_type == "color" {
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: self.file_path.to_string(),
                        target_qualified: format!(
                            "res/{}/{}",
                            self.resource_dir(res_type),
                            res_name
                        ),
                        rel_type: format!("uses_{}_resource", res_type),
                        confidence: 1.0,
                        metadata: serde_json::json!({
                            "resource_type": res_type,
                            "resource_name": res_name,
                            "via_method": true,
                        }),
                    });
                }
            }
        }

        relationships
    }

    fn resource_dir(&self, res_type: &str) -> &'static str {
        match res_type {
            "string" => "values/strings.xml",
            "drawable" => "drawable",
            "layout" => "layout",
            "id" => "values/ids.xml",
            "color" => "values/colors.xml",
            "style" => "values/styles.xml",
            "dimen" => "values/dimens.xml",
            "raw" => "raw",
            "anim" => "anim",
            "menu" => "menu",
            "mipmap" => "mipmap",
            _ => "values",
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_string_reference() {
        let source = r#"
            val title = getString(R.string.app_name)
            val desc = resources.getString(R.string.description)
        "#;
        let extractor = AndroidResourceRefExtractor::new(source.as_bytes(), "./Test.kt");
        let (_, relationships) = extractor.extract();

        let string_refs: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "uses_string_resource")
            .collect();
        assert_eq!(
            string_refs.len(),
            2,
            "Expected exactly 2 string refs (one per occurrence), found {}",
            string_refs.len()
        );
        assert!(string_refs
            .iter()
            .any(|r| r.target_qualified.contains("app_name")));
        assert!(string_refs
            .iter()
            .any(|r| r.target_qualified.contains("description")));
    }

    #[test]
    fn test_extract_drawable_reference() {
        let source = r#"
            imageView.setImageResource(R.drawable.ic_launcher)
        "#;
        let extractor = AndroidResourceRefExtractor::new(source.as_bytes(), "./Test.kt");
        let (_, relationships) = extractor.extract();

        let drawable_refs: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "uses_drawable_resource")
            .collect();
        assert_eq!(drawable_refs.len(), 1);
        assert!(drawable_refs[0].target_qualified.contains("ic_launcher"));
    }

    #[test]
    fn test_extract_layout_reference() {
        let source = r#"
            setContentView(R.layout.activity_main)
            val view = layoutInflater.inflate(R.layout.item_row, null)
        "#;
        let extractor = AndroidResourceRefExtractor::new(source.as_bytes(), "./Test.kt");
        let (_, relationships) = extractor.extract();

        let layout_refs: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "uses_layout_resource")
            .collect();
        assert_eq!(layout_refs.len(), 2);
    }

    #[test]
    fn test_extract_id_reference() {
        let source = r#"
            val button = findViewById<Button>(R.id.submit_button)
        "#;
        let extractor = AndroidResourceRefExtractor::new(source.as_bytes(), "./Test.kt");
        let (_, relationships) = extractor.extract();

        let id_refs: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "references_view_by_id")
            .collect();
        assert_eq!(id_refs.len(), 1);
        assert!(id_refs[0].target_qualified.contains("submit_button"));
    }
}
