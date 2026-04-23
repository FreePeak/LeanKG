use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static SET_CONTENT_VIEW_RE: OnceLock<Regex> = OnceLock::new();
static INFLATE_RE: OnceLock<Regex> = OnceLock::new();
static VIEWBINDING_INFLATE_RE: OnceLock<Regex> = OnceLock::new();
static CLICK_HANDLER_RE: OnceLock<Regex> = OnceLock::new();
static FIND_VIEW_BY_ID_CLICK_RE: OnceLock<Regex> = OnceLock::new();

/// Enhanced resource linking for Android Kotlin
/// Links Activities/Fragments to layouts, view bindings, click handlers
pub struct AndroidResourceLinker<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> AndroidResourceLinker<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = match std::str::from_utf8(self.source) {
            Ok(s) => s,
            Err(_) => return (Vec::new(), Vec::new()),
        };

        let mut relationships = Vec::new();

        // Extract layout inflation patterns
        let inflation_rels = self.extract_layout_inflation(content);
        relationships.extend(inflation_rels);

        // Extract ViewBinding usage
        let binding_rels = self.extract_viewbinding_usage(content);
        relationships.extend(binding_rels);

        // Extract click handlers
        let click_rels = self.extract_click_handlers(content);
        relationships.extend(click_rels);

        (Vec::new(), relationships)
    }

    fn extract_layout_inflation(&self, content: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        // setContentView(R.layout.xxx)
        let set_cv_re = SET_CONTENT_VIEW_RE
            .get_or_init(|| Regex::new(r"setContentView\s*\(\s*R\.layout\.(\w+)\s*\)").unwrap());

        for cap in set_cv_re.captures_iter(content) {
            if let Some(layout_match) = cap.get(1) {
                let layout_name = layout_match.as_str();
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("res/layout/{}.xml", layout_name),
                    rel_type: "inflates_layout".to_string(),
                    confidence: 0.95,
                    metadata: serde_json::json!({
                        "method": "setContentView",
                        "layout_name": layout_name,
                    }),
                });
            }
        }

        // LayoutInflater.inflate(R.layout.xxx, ...)
        let inflate_re =
            INFLATE_RE.get_or_init(|| Regex::new(r"inflate\s*\(\s*R\.layout\.(\w+)").unwrap());

        for cap in inflate_re.captures_iter(content) {
            if let Some(layout_match) = cap.get(1) {
                let layout_name = layout_match.as_str();
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("res/layout/{}.xml", layout_name),
                    rel_type: "inflates_layout".to_string(),
                    confidence: 0.90,
                    metadata: serde_json::json!({
                        "method": "inflate",
                        "layout_name": layout_name,
                    }),
                });
            }
        }

        relationships
    }

    fn extract_viewbinding_usage(&self, content: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        // ActivityMainBinding.inflate(layoutInflater)
        // val binding = ActivityMainBinding.inflate(...)
        let binding_re = VIEWBINDING_INFLATE_RE
            .get_or_init(|| Regex::new(r"(\w+Binding)\.(inflate|bind)\s*\(").unwrap());

        for cap in binding_re.captures_iter(content) {
            if let Some(binding_match) = cap.get(1) {
                let binding_name = binding_match.as_str();
                let method = cap.get(2).map(|m| m.as_str()).unwrap_or("inflate");

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("generated/{}.java", binding_name),
                    rel_type: "uses_viewbinding".to_string(),
                    confidence: 0.95,
                    metadata: serde_json::json!({
                        "binding_class": binding_name,
                        "method": method,
                    }),
                });

                // Try to infer layout name from binding
                // ActivityMainBinding -> activity_main
                let layout_name = self.binding_to_layout(binding_name);
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("res/layout/{}.xml", layout_name),
                    rel_type: "inflates_layout".to_string(),
                    confidence: 0.85,
                    metadata: serde_json::json!({
                        "inferred_from_binding": binding_name,
                        "layout_name": layout_name,
                    }),
                });
            }
        }

        relationships
    }

    fn extract_click_handlers(&self, content: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        // xxx.setOnClickListener { ... }
        // xxx.setOnClickListener(this)
        let click_re = CLICK_HANDLER_RE
            .get_or_init(|| Regex::new(r"(\w+)\.setOnClickListener\s*\{\s*([^}]+)\}").unwrap());

        for cap in click_re.captures_iter(content) {
            if let Some(view_match) = cap.get(1) {
                let view_name = view_match.as_str();
                let handler_body = cap.get(2).map(|m| m.as_str()).unwrap_or("");

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("__view__/{}", view_name),
                    rel_type: "on_click_handler".to_string(),
                    confidence: 0.80,
                    metadata: serde_json::json!({
                        "view_id": view_name,
                        "handler_type": "lambda",
                        "handler_body_snippet": handler_body.chars().take(50).collect::<String>(),
                    }),
                });
            }
        }

        // findViewById<...>(R.id.xxx).setOnClickListener { ... }
        let find_click_re = FIND_VIEW_BY_ID_CLICK_RE.get_or_init(|| {
            Regex::new(r"findViewById(?:<[^>]+>)?\s*\(\s*R\.id\.(\w+)\s*\)\.setOnClickListener")
                .unwrap()
        });

        for cap in find_click_re.captures_iter(content) {
            if let Some(id_match) = cap.get(1) {
                let view_id = id_match.as_str();
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("res/layout/__unknown__/@+id/{}", view_id),
                    rel_type: "on_click_handler".to_string(),
                    confidence: 0.85,
                    metadata: serde_json::json!({
                        "view_id": view_id,
                        "method": "findViewById",
                        "handler_type": "lambda",
                    }),
                });
            }
        }

        relationships
    }

    fn binding_to_layout(&self, binding_name: &str) -> String {
        // Convert CamelCase to snake_case
        // ActivityMainBinding -> activity_main
        let mut result = String::new();
        let mut prev_lowercase = false;

        for (i, c) in binding_name.char_indices() {
            // Skip "Binding" suffix
            if binding_name[i..].to_lowercase() == *"binding" {
                break;
            }

            if c.is_uppercase() && i > 0 && prev_lowercase {
                result.push('_');
            }
            result.push(c.to_lowercase().next().unwrap_or(c));
            prev_lowercase = c.is_lowercase();
        }

        result.trim_end_matches("_binding").to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_set_content_view() {
        let source = r#"
            class MainActivity : AppCompatActivity() {
                override fun onCreate(savedInstanceState: Bundle?) {
                    super.onCreate(savedInstanceState)
                    setContentView(R.layout.activity_main)
                }
            }
        "#;
        let linker = AndroidResourceLinker::new(source.as_bytes(), "./MainActivity.kt");
        let (_, relationships) = linker.extract();

        let inflates: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "inflates_layout")
            .collect();
        assert!(!inflates.is_empty(), "Should find setContentView");
        assert!(inflates
            .iter()
            .any(|r| r.target_qualified.contains("activity_main")));
    }

    #[test]
    fn test_viewbinding_usage() {
        let source = r#"
            val binding = ActivityMainBinding.inflate(layoutInflater)
            binding.submitButton.setOnClickListener { ... }
        "#;
        let linker = AndroidResourceLinker::new(source.as_bytes(), "./Test.kt");
        let (_, relationships) = linker.extract();

        let bindings: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "uses_viewbinding")
            .collect();
        assert!(!bindings.is_empty(), "Should detect ViewBinding");
        assert!(bindings
            .iter()
            .any(|r| r.metadata.get("binding_class").unwrap() == "ActivityMainBinding"));
    }

    #[test]
    fn test_click_handler() {
        let source = r#"
            submitButton.setOnClickListener {
                handleSubmit()
            }
        "#;
        let linker = AndroidResourceLinker::new(source.as_bytes(), "./Test.kt");
        let (_, relationships) = linker.extract();

        let handlers: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "on_click_handler")
            .collect();
        assert!(!handlers.is_empty(), "Should find click handler");
    }

    #[test]
    fn test_binding_to_layout_conversion() {
        let linker = AndroidResourceLinker::new(b"", "./test.kt");

        assert_eq!(
            linker.binding_to_layout("ActivityMainBinding"),
            "activity_main"
        );
        assert_eq!(linker.binding_to_layout("ItemRowBinding"), "item_row");
        assert_eq!(
            linker.binding_to_layout("FragmentHomeBinding"),
            "fragment_home"
        );
    }
}
