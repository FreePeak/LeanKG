use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static ROOT_ELEMENT_RE: OnceLock<Regex> = OnceLock::new();

/// Extractor for generic XML files (non-Android specific)
///
/// # Limitations
/// This extractor only captures the root element name from XML files.
/// Child elements, attributes, and the full document structure are not extracted.
/// For Android-specific XML files (AndroidManifest.xml, resources in /res/),
/// use the specialized Android extractors instead.
pub struct GenericXmlExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> GenericXmlExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        // Skip if Android-specific file
        if self.is_android_xml() {
            return (elements, relationships);
        }

        match std::str::from_utf8(self.source) {
            Ok(content) => {
                let root_element = Self::detect_root_element(content);

                if !root_element.is_empty() {
                    elements.push(CodeElement {
                        qualified_name: format!("{}::{}", self.file_path, root_element),
                        element_type: "XMLDocument".to_string(),
                        name: root_element.clone(),
                        file_path: self.file_path.to_string(),
                        ..Default::default()
                    });

                    let mut lines = content.lines();
                    if let Some(first_line) = lines.next() {
                        let first_tag_start = first_line.find('<').unwrap_or(0);
                        let first_tag_end = first_line.rfind('>').unwrap_or(first_line.len());

                        if first_tag_start < first_tag_end && first_tag_end > 0 {
                            relationships.push(Relationship {
                                id: None,
                                source_qualified: format!("{}::{}", self.file_path, root_element),
                                target_qualified: format!(
                                    "{}::{}",
                                    self.file_path,
                                    &first_line[first_tag_start..first_tag_end]
                                ),
                                rel_type: "has_root".to_string(),
                                confidence: 1.0,
                                metadata: serde_json::json!({}),
                            });
                        }
                    }
                }
            }
            Err(_) => {
                return (elements, relationships);
            }
        }

        (elements, relationships)
    }

    fn is_android_xml(&self) -> bool {
        let path_lower = self.file_path.to_lowercase();

        if path_lower.contains("androidmanifest.xml") {
            return true;
        }

        if path_lower.contains("/res/") || path_lower.contains("\\res\\") {
            return true;
        }

        false
    }

    fn detect_root_element(content: &str) -> String {
        let re = ROOT_ELEMENT_RE.get_or_init(|| Regex::new(r"<(\w+)(?:\s|>|/>)").unwrap());

        if let Some(caps) = re.captures(content) {
            if let Some(tag_name) = caps.get(1) {
                return tag_name.as_str().to_string();
            }
        }

        String::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_detect_root_element_simple() {
        let content = r#"<root>content</root>"#;
        let extractor = GenericXmlExtractor::new(content.as_bytes(), "test.xml");

        // Manually call detect_root_element since it's private
        let root = GenericXmlExtractor::detect_root_element(content);
        assert_eq!(root, "root");
    }

    #[test]
    fn test_detect_root_element_with_attributes() {
        let content = r#"<config id="123">content</config>"#;
        let root = GenericXmlExtractor::detect_root_element(content);
        assert_eq!(root, "config");
    }

    #[test]
    fn test_is_android_xml_manifest() {
        let extractor = GenericXmlExtractor::new(b"<manifest/>", "AndroidManifest.xml");
        assert!(extractor.is_android_xml());
    }

    #[test]
    fn test_is_android_xml_lowercase() {
        let extractor = GenericXmlExtractor::new(b"<manifest/>", "androidmanifest.xml");
        assert!(extractor.is_android_xml());
    }

    #[test]
    fn test_is_android_xml_res_directory() {
        let extractor = GenericXmlExtractor::new(b"<layout/>", "/res/layout/activity_main.xml");
        assert!(extractor.is_android_xml());
    }

    #[test]
    fn test_is_not_android_xml_generic() {
        let extractor = GenericXmlExtractor::new(b"<root/>", "config/settings.xml");
        assert!(!extractor.is_android_xml());
    }
}
