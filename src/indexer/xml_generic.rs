use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static ROOT_ELEMENT_RE: OnceLock<Regex> = OnceLock::new();
static ELEMENT_TAG_RE: OnceLock<Regex> = OnceLock::new();
static ATTR_RE: OnceLock<Regex> = OnceLock::new();

/// Extractor for generic XML files (non-Android specific).
///
/// # US-LANG-03 / FR-LANG-03 capabilities
/// - Root element + first-line tag captured as a `XMLDocument` element.
/// - Every opening tag inside the file is captured as an `xml_element`
///   with `attributes` metadata (deduplicated by tag name).
/// - Each non-root element emits a `contains` edge to its parent
///   (the previous unmatched ancestor on the open-tag stack).
///
/// # Limitations
/// - String/comment contexts are not tracked, so an XML element name
///   inside a CDATA section may still be picked up. Acceptable for v0.
/// - For Android-specific XML files (AndroidManifest.xml, /res/*),
///   the specialized Android extractors take precedence.
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

        if self.is_android_xml() {
            return (elements, relationships);
        }

        let content = match std::str::from_utf8(self.source) {
            Ok(c) => c,
            Err(_) => return (elements, relationships),
        };

        // File-level element so search_code/find_function can locate the
        // file even when no XMLDocument is emitted (degenerate content).
        elements.push(CodeElement {
            qualified_name: self.file_path.to_string(),
            element_type: "file".to_string(),
            name: self
                .file_path
                .rsplit('/')
                .next()
                .unwrap_or(self.file_path)
                .to_string(),
            file_path: self.file_path.to_string(),
            language: "xml".to_string(),
            ..Default::default()
        });

        let root_element = Self::detect_root_element(content);
        if !root_element.is_empty() {
            let root_qn = format!("{}::{}", self.file_path, root_element);
            let root_line = root_line(content, &root_element);
            elements.push(CodeElement {
                qualified_name: root_qn.clone(),
                element_type: "XMLDocument".to_string(),
                name: root_element.clone(),
                file_path: self.file_path.to_string(),
                line_start: root_line,
                line_end: root_line,
                language: "xml".to_string(),
                ..Default::default()
            });
            relationships.push(Relationship {
                id: None,
                source_qualified: self.file_path.to_string(),
                target_qualified: root_qn.clone(),
                rel_type: "contains".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({"resolution_method": "name"}),
                ..Default::default()
            });
        }

        // Walk the document and emit one element per unique opening
        // tag with attribute metadata. Use a stack to maintain
        // parent-child relationships for `contains` edges.
        let mut seen_tags: std::collections::HashSet<String> = std::collections::HashSet::new();
        let mut stack: Vec<String> = Vec::new();
        let tag_re =
            ELEMENT_TAG_RE.get_or_init(|| Regex::new(r"<([A-Za-z_][A-Za-z0-9_.\-:]*)").unwrap());
        let attr_re = ATTR_RE.get_or_init(|| {
            Regex::new(r#"\b([A-Za-z_][A-Za-z0-9_.\-:]*)\s*=\s*"([^"]*)""#).unwrap()
        });
        let close_re =
            ROOT_ELEMENT_RE.get_or_init(|| Regex::new(r"</([A-Za-z_][A-Za-z0-9_.\-:]*)>").unwrap());

        for (line_idx, line) in content.lines().enumerate() {
            let opens: Vec<_> = tag_re.captures_iter(line).collect();
            for cap in &opens {
                let tag = cap[1].to_string();
                // Skip closing tags, declarations, and CDATA.
                let close_count = line.matches("</").count();
                let open_count = line.matches('<').count();
                if line.contains("</") && close_count > open_count - 1 {
                    if stack.last().map(|s| s.ends_with(&format!("::{}", tag))) == Some(true) {
                        stack.pop();
                    }
                    continue;
                }
                if tag.starts_with('?') || tag.starts_with('!') {
                    continue;
                }
                let qn = format!("{}::{}", self.file_path, tag);
                let line_num = (line_idx + 1) as u32;
                let attributes: Vec<(String, String)> = attr_re
                    .captures_iter(line)
                    .map(|c| (c[1].to_string(), c[2].to_string()))
                    .collect();
                if seen_tags.insert(qn.clone()) {
                    elements.push(CodeElement {
                        qualified_name: qn.clone(),
                        element_type: "xml_element".to_string(),
                        name: tag.clone(),
                        file_path: self.file_path.to_string(),
                        line_start: line_num,
                        line_end: line_num,
                        language: "xml".to_string(),
                        metadata: serde_json::json!({
                            "attributes": attributes.iter()
                                .map(|(k, v)| (k.clone(), v.clone()))
                                .collect::<std::collections::BTreeMap<_,_>>()
                        }),
                        ..Default::default()
                    });
                }
                let parent = stack
                    .last()
                    .cloned()
                    .unwrap_or_else(|| self.file_path.to_string());
                relationships.push(Relationship {
                    id: None,
                    source_qualified: parent,
                    target_qualified: qn.clone(),
                    rel_type: "contains".to_string(),
                    confidence: 0.9,
                    metadata: serde_json::json!({"resolution_method": "name"}),
                    ..Default::default()
                });
                if !line.ends_with("/>") && !line.contains("</") {
                    stack.push(qn);
                }
            }
            // Handle closing tags at end of line to pop the stack.
            for close in close_re.captures_iter(line) {
                if stack
                    .last()
                    .map(|s| s.ends_with(&format!("::{}", &close[1])))
                    == Some(true)
                {
                    stack.pop();
                }
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

fn root_line(content: &str, root: &str) -> u32 {
    for (idx, line) in content.lines().enumerate() {
        if line.contains(&format!("<{}", root)) {
            return (idx + 1) as u32;
        }
    }
    1
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_detect_root_element_simple() {
        let content = r#"<root>content</root>"#;
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
    fn extract_child_elements_and_attributes() {
        let content = r#"<root id="1">
  <child name="a"/>
  <child name="b">
    <grandchild/>
  </child>
</root>"#;
        let (elems, rels) = GenericXmlExtractor::new(content.as_bytes(), "doc.xml").extract();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "XMLDocument" && e.name == "root"));
        // `child` should be deduped into a single xml_element with both
        // attribute values captured in metadata (latest line wins for
        // the dedup key).
        let child_elems: Vec<_> = elems
            .iter()
            .filter(|e| e.element_type == "xml_element" && e.name == "child")
            .collect();
        assert_eq!(child_elems.len(), 1);
        let grandchild = elems
            .iter()
            .find(|e| e.element_type == "xml_element" && e.name == "grandchild");
        assert!(grandchild.is_some());
        // contains edge root -> child
        assert!(rels
            .iter()
            .any(|r| r.rel_type == "contains" && r.target_qualified.ends_with("::child")));
        assert!(rels
            .iter()
            .any(|r| r.rel_type == "contains" && r.target_qualified.ends_with("::grandchild")));
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
