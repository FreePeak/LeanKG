//! US-LANG-02 / FR-LANG-02: Swift entity extraction (regex-based).
//!
//! LeanKG doesn't currently bundle a tree-sitter-swift binding, so this
//! extractor uses regex patterns tuned for the most common Swift
//! constructs: classes, structs, enums, protocols, extensions,
//! top-level functions, methods, and properties. The output schema
//! mirrors the tree-sitter-based extractors so agents don't need to
//! special-case Swift sources.
//!
//! Limitations:
//!   - String-literal and comment contexts are not tracked, so a
//!     `class Foo` inside a doc-comment may still be picked up. This
//!     is acceptable for v0 — full Swift parsing can be added later
//!     by swapping in a tree-sitter-swift binding.
use crate::db::models::{CodeElement, Relationship};
use once_cell::sync::Lazy;
use regex::Regex;

static CLASS_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^\s*(?:(?:public|private|internal|fileprivate|open|final)\s+)*class\s+([A-Za-z_][A-Za-z0-9_]*)\b")
        .unwrap()
});
static STRUCT_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^\s*(?:(?:public|private|internal|fileprivate|open)\s+)*struct\s+([A-Za-z_][A-Za-z0-9_]*)\b")
        .unwrap()
});
static ENUM_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^\s*(?:(?:public|private|internal|fileprivate|open)\s+)*enum\s+([A-Za-z_][A-Za-z0-9_]*)\b")
        .unwrap()
});
static PROTOCOL_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^\s*(?:(?:public|private|internal|fileprivate)\s+)*protocol\s+([A-Za-z_][A-Za-z0-9_]*)\b")
        .unwrap()
});
static EXTENSION_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?m)^\s*extension\s+([A-Za-z_][A-Za-z0-9_]*)\b").unwrap());
static FUNC_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^\s*(?:(?:public|private|internal|fileprivate|open|static|class|override)\s+)*func\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(")
        .unwrap()
});
static INIT_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^\s*(?:(?:public|private|internal|fileprivate|open|override)\s+)*init\??\s*\(")
        .unwrap()
});
static PROPERTY_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^\s*(?:(?:public|private|internal|fileprivate|open|static|lazy|weak|unowned)\s+)*(?:var|let)\s+([A-Za-z_][A-Za-z0-9_]*)\s*[:=]")
        .unwrap()
});
static IMPORT_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?m)^\s*import\s+([A-Za-z_][A-Za-z0-9_.]*)").unwrap());

pub struct SwiftExtractor<'a> {
    source: &'a str,
    file_path: &'a str,
}

impl<'a> SwiftExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self {
            source: std::str::from_utf8(source).unwrap_or(""),
            file_path,
        }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements: Vec<CodeElement> = Vec::new();
        let mut relationships: Vec<Relationship> = Vec::new();

        // File-level element so search/find_function can locate the file.
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
            language: "swift".to_string(),
            ..Default::default()
        });

        for cap in CLASS_RE.captures_iter(self.source) {
            let line = self.line_of(&cap[0]);
            self.push_decl(&mut elements, &mut relationships, "class", &cap[1], line);
        }
        for cap in STRUCT_RE.captures_iter(self.source) {
            let line = self.line_of(&cap[0]);
            self.push_decl(&mut elements, &mut relationships, "struct", &cap[1], line);
        }
        for cap in ENUM_RE.captures_iter(self.source) {
            let line = self.line_of(&cap[0]);
            self.push_decl(&mut elements, &mut relationships, "enum", &cap[1], line);
        }
        for cap in PROTOCOL_RE.captures_iter(self.source) {
            let line = self.line_of(&cap[0]);
            self.push_decl(
                &mut elements,
                &mut relationships,
                "interface",
                &cap[1],
                line,
            );
        }
        for cap in EXTENSION_RE.captures_iter(self.source) {
            let line = self.line_of(&cap[0]);
            self.push_decl(
                &mut elements,
                &mut relationships,
                "extension",
                &cap[1],
                line,
            );
        }

        // Functions / methods / initializers: track the most recent
        // enclosing class/struct/enum/protocol/extension so we can
        // emit method vs function and add a `defines` relationship.
        let mut current_parent: Option<String> = None;
        for (idx, line) in self.source.lines().enumerate() {
            let line_num = (idx + 1) as u32;
            if CLASS_RE.is_match(line)
                || STRUCT_RE.is_match(line)
                || ENUM_RE.is_match(line)
                || PROTOCOL_RE.is_match(line)
                || EXTENSION_RE.is_match(line)
            {
                let re = if CLASS_RE.is_match(line) {
                    &CLASS_RE
                } else if STRUCT_RE.is_match(line) {
                    &STRUCT_RE
                } else if ENUM_RE.is_match(line) {
                    &ENUM_RE
                } else if PROTOCOL_RE.is_match(line) {
                    &PROTOCOL_RE
                } else {
                    &EXTENSION_RE
                };
                if let Some(c) = re.captures(line) {
                    current_parent = Some(format!("{}::{}", self.file_path, &c[1]));
                }
                continue;
            }
            if let Some(c) = FUNC_RE.captures(line) {
                let name = c[1].to_string();
                let qn = format!(
                    "{}::{}",
                    current_parent.as_deref().unwrap_or(self.file_path),
                    name
                );
                let element_type = if current_parent.is_some() {
                    "method"
                } else {
                    "function"
                };
                elements.push(CodeElement {
                    qualified_name: qn.clone(),
                    element_type: element_type.to_string(),
                    name,
                    file_path: self.file_path.to_string(),
                    line_start: line_num,
                    line_end: line_num,
                    language: "swift".to_string(),
                    parent_qualified: current_parent.clone(),
                    metadata: serde_json::json!({"resolution_method": "name"}),
                    ..Default::default()
                });
                let container = current_parent
                    .clone()
                    .unwrap_or_else(|| self.file_path.to_string());
                relationships.push(Relationship {
                    id: None,
                    source_qualified: container,
                    target_qualified: qn,
                    rel_type: "defines".to_string(),
                    confidence: 0.8,
                    metadata: serde_json::json!({"resolution_method": "name"}),
                    ..Default::default()
                });
            } else if INIT_RE.is_match(line) {
                let qn = format!(
                    "{}::init",
                    current_parent.as_deref().unwrap_or(self.file_path)
                );
                elements.push(CodeElement {
                    qualified_name: qn.clone(),
                    element_type: "constructor".to_string(),
                    name: "init".to_string(),
                    file_path: self.file_path.to_string(),
                    line_start: line_num,
                    line_end: line_num,
                    language: "swift".to_string(),
                    parent_qualified: current_parent.clone(),
                    metadata: serde_json::json!({"resolution_method": "name"}),
                    ..Default::default()
                });
                let container = current_parent
                    .clone()
                    .unwrap_or_else(|| self.file_path.to_string());
                relationships.push(Relationship {
                    id: None,
                    source_qualified: container,
                    target_qualified: qn,
                    rel_type: "defines".to_string(),
                    confidence: 0.8,
                    metadata: serde_json::json!({"resolution_method": "name"}),
                    ..Default::default()
                });
            } else if let Some(c) = PROPERTY_RE.captures(line) {
                let qn = format!(
                    "{}::{}",
                    current_parent.as_deref().unwrap_or(self.file_path),
                    &c[1]
                );
                elements.push(CodeElement {
                    qualified_name: qn.clone(),
                    element_type: "property".to_string(),
                    name: c[1].to_string(),
                    file_path: self.file_path.to_string(),
                    line_start: line_num,
                    line_end: line_num,
                    language: "swift".to_string(),
                    parent_qualified: current_parent.clone(),
                    ..Default::default()
                });
                if let Some(parent) = &current_parent {
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: parent.clone(),
                        target_qualified: qn,
                        rel_type: "defines".to_string(),
                        confidence: 0.7,
                        metadata: serde_json::json!({"resolution_method": "name"}),
                        ..Default::default()
                    });
                }
            }
        }

        for cap in IMPORT_RE.captures_iter(self.source) {
            relationships.push(Relationship {
                id: None,
                source_qualified: self.file_path.to_string(),
                target_qualified: cap[1].to_string(),
                rel_type: "imports".to_string(),
                confidence: 0.95,
                metadata: serde_json::json!({"resolution_method": "name"}),
                ..Default::default()
            });
        }

        (elements, relationships)
    }

    fn push_decl(
        &self,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
        element_type: &str,
        name: &str,
        line: u32,
    ) {
        let qn = format!("{}::{}", self.file_path, name);
        elements.push(CodeElement {
            qualified_name: qn.clone(),
            element_type: element_type.to_string(),
            name: name.to_string(),
            file_path: self.file_path.to_string(),
            line_start: line,
            line_end: line,
            language: "swift".to_string(),
            ..Default::default()
        });
        relationships.push(Relationship {
            id: None,
            source_qualified: self.file_path.to_string(),
            target_qualified: qn,
            rel_type: "contains".to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({"resolution_method": "name"}),
            ..Default::default()
        });
    }

    fn line_of(&self, matched: &str) -> u32 {
        let prefix = &self.source[..self.source.len() - matched.len().min(self.source.len())];
        // Find the line of the start of the match by counting \n in the
        // original source up to the same offset prefix length.
        let offset = self.source.len() - prefix.len() - matched.len();
        let count = self.source[..offset].matches('\n').count() as u32;
        count + 1
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extracts_swift_class_struct_enum_protocol() {
        let src = r#"
import Foundation

public class Greeter {
    var name = "world"
    func hello() { print("hi") }
}

struct Point {
    var x: Int
    var y: Int
}

enum Direction {
    case north, south
}

protocol Greetable {
    func hello()
}
"#;
        let (elems, rels) = SwiftExtractor::new(src.as_bytes(), "test.swift").extract();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "class" && e.name == "Greeter"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "struct" && e.name == "Point"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "enum" && e.name == "Direction"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "interface" && e.name == "Greetable"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "method" && e.name == "hello"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "property" && e.name == "name"));
        assert!(rels
            .iter()
            .any(|r| r.rel_type == "imports" && r.target_qualified == "Foundation"));
    }

    #[test]
    fn extracts_swift_extension_and_init() {
        let src = r#"
extension Int {
    init?(fromString: String) { self = 0 }
}
"#;
        let (elems, _) = SwiftExtractor::new(src.as_bytes(), "ext.swift").extract();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "extension" && e.name == "Int"));
        assert!(elems.iter().any(|e| e.element_type == "constructor"));
    }
}
