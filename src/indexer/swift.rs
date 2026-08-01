//! US-LANG-02 / FR-LANG-02: Swift entity extraction (regex-based).
//!
//! LeanKG doesn't currently bundle a tree-sitter-swift binding, so this
//! extractor uses regex patterns tuned for the most common Swift
//! constructs: classes, structs, enums, protocols, extensions,
//! top-level functions, methods, and properties. The output schema
//! mirrors the tree-sitter-based extractors so agents don't need to
//! special-case Swift sources.
//!
//! Wired into both the bulk index walk and incremental `index_file_sync`
//! / MCP watcher paths. Heritage (`extends`/`implements`) edges are emitted
//! from type inheritance clauses; call-graph edges land in a later phase.
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
    Regex::new(r"(?m)^\s*(?:(?:public|private|internal|fileprivate|open|final)\s+)*class\s+([A-Za-z_][A-Za-z0-9_]*)\b(?:\s*:\s*([^{/\n]+))?")
        .unwrap()
});
static STRUCT_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^\s*(?:(?:public|private|internal|fileprivate|open)\s+)*struct\s+([A-Za-z_][A-Za-z0-9_]*)\b(?:\s*:\s*([^{/\n]+))?")
        .unwrap()
});
static ENUM_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^\s*(?:(?:public|private|internal|fileprivate|open)\s+)*enum\s+([A-Za-z_][A-Za-z0-9_]*)\b(?:\s*:\s*([^{/\n]+))?")
        .unwrap()
});
static PROTOCOL_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^\s*(?:(?:public|private|internal|fileprivate)\s+)*protocol\s+([A-Za-z_][A-Za-z0-9_]*)\b(?:\s*:\s*([^{/\n]+))?")
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
static TYPE_TOKEN_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"[A-Za-z_][A-Za-z0-9_]*").unwrap());

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
            let qn = self.push_decl(&mut elements, &mut relationships, "class", &cap[1], line);
            self.push_heritage(
                &mut relationships,
                &qn,
                cap.get(2).map(|m| m.as_str()),
                HeritageKind::Class,
            );
        }
        for cap in STRUCT_RE.captures_iter(self.source) {
            let line = self.line_of(&cap[0]);
            let qn = self.push_decl(&mut elements, &mut relationships, "struct", &cap[1], line);
            self.push_heritage(
                &mut relationships,
                &qn,
                cap.get(2).map(|m| m.as_str()),
                HeritageKind::ConformancesOnly,
            );
        }
        for cap in ENUM_RE.captures_iter(self.source) {
            let line = self.line_of(&cap[0]);
            let qn = self.push_decl(&mut elements, &mut relationships, "enum", &cap[1], line);
            self.push_heritage(
                &mut relationships,
                &qn,
                cap.get(2).map(|m| m.as_str()),
                HeritageKind::ConformancesOnly,
            );
        }
        for cap in PROTOCOL_RE.captures_iter(self.source) {
            let line = self.line_of(&cap[0]);
            let qn = self.push_decl(
                &mut elements,
                &mut relationships,
                "interface",
                &cap[1],
                line,
            );
            self.push_heritage(
                &mut relationships,
                &qn,
                cap.get(2).map(|m| m.as_str()),
                HeritageKind::ProtocolInheritance,
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

    /// Regex entity extraction plus tree-sitter call edges (Phase 2).
    pub fn extract_with_calls(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let (elements, mut relationships) = self.extract();
        if let Some(tree) = parse_swift(self.source.as_bytes()) {
            let calls = crate::indexer::call_graph::extract_calls_with_resolution(
                &tree,
                self.source.as_bytes(),
                self.file_path,
                "swift",
            );
            relationships.extend(calls);
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
    ) -> String {
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
            target_qualified: qn.clone(),
            rel_type: "contains".to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({"resolution_method": "name"}),
            ..Default::default()
        });
        qn
    }

    fn push_heritage(
        &self,
        relationships: &mut Vec<Relationship>,
        source_qn: &str,
        heritage: Option<&str>,
        kind: HeritageKind,
    ) {
        let Some(raw) = heritage else {
            return;
        };
        let types = parse_swift_heritage_types(raw);
        if types.is_empty() {
            return;
        }
        match kind {
            HeritageKind::Class => {
                // First type is the superclass; remaining are protocols.
                relationships.push(Relationship {
                    id: None,
                    source_qualified: source_qn.to_string(),
                    target_qualified: types[0].clone(),
                    rel_type: "extends".to_string(),
                    confidence: 0.85,
                    metadata: serde_json::json!({"resolution_method": "name"}),
                    ..Default::default()
                });
                for proto in types.iter().skip(1) {
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: source_qn.to_string(),
                        target_qualified: proto.clone(),
                        rel_type: "implements".to_string(),
                        confidence: 0.85,
                        metadata: serde_json::json!({"resolution_method": "name"}),
                        ..Default::default()
                    });
                }
            }
            HeritageKind::ConformancesOnly => {
                for proto in types {
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: source_qn.to_string(),
                        target_qualified: proto,
                        rel_type: "implements".to_string(),
                        confidence: 0.85,
                        metadata: serde_json::json!({"resolution_method": "name"}),
                        ..Default::default()
                    });
                }
            }
            HeritageKind::ProtocolInheritance => {
                for proto in types {
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: source_qn.to_string(),
                        target_qualified: proto,
                        rel_type: "extends".to_string(),
                        confidence: 0.85,
                        metadata: serde_json::json!({"resolution_method": "name"}),
                        ..Default::default()
                    });
                }
            }
        }
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

enum HeritageKind {
    Class,
    ConformancesOnly,
    ProtocolInheritance,
}

/// Split a Swift heritage clause (`NSObject, Foo<Bar>, Baz`) into type names.
fn parse_swift_heritage_types(raw: &str) -> Vec<String> {
    let mut types = Vec::new();
    let mut depth = 0i32;
    let mut current = String::new();
    for ch in raw.chars() {
        match ch {
            '<' => {
                depth += 1;
                current.push(ch);
            }
            '>' => {
                depth -= 1;
                current.push(ch);
            }
            ',' if depth == 0 => {
                if let Some(name) = first_type_token(&current) {
                    types.push(name);
                }
                current.clear();
            }
            _ => current.push(ch),
        }
    }
    if let Some(name) = first_type_token(&current) {
        types.push(name);
    }
    types
}

fn first_type_token(segment: &str) -> Option<String> {
    TYPE_TOKEN_RE
        .find(segment.trim())
        .map(|m| m.as_str().to_string())
}

fn parse_swift(source: &[u8]) -> Option<tree_sitter::Tree> {
    let mut parser = tree_sitter::Parser::new();
    let lang: tree_sitter::Language = tree_sitter_swift::LANGUAGE.into();
    parser.set_language(&lang).ok()?;
    parser.parse(source, None)
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

    #[test]
    fn extracts_swift_extends_and_implements() {
        let src = r#"
public class Session: NSObject, Authenticating, Resettable {
    func authenticate() {}
}

struct Point: Codable, Equatable {
    var x: Int
}

protocol Authenticating: AnyObject {
    func authenticate()
}
"#;
        let (_elems, rels) = SwiftExtractor::new(src.as_bytes(), "heritage.swift").extract();

        let extends: Vec<_> = rels
            .iter()
            .filter(|r| r.rel_type == "extends")
            .map(|r| (r.source_qualified.as_str(), r.target_qualified.as_str()))
            .collect();
        let implements: Vec<_> = rels
            .iter()
            .filter(|r| r.rel_type == "implements")
            .map(|r| (r.source_qualified.as_str(), r.target_qualified.as_str()))
            .collect();

        assert!(
            extends
                .iter()
                .any(|(s, t)| s.ends_with("::Session") && *t == "NSObject"),
            "Session should extend NSObject, got {:?}",
            extends
        );
        assert!(
            implements
                .iter()
                .any(|(s, t)| s.ends_with("::Session") && *t == "Authenticating"),
            "Session should implement Authenticating, got {:?}",
            implements
        );
        assert!(
            implements
                .iter()
                .any(|(s, t)| s.ends_with("::Point") && *t == "Codable"),
            "Point should implement Codable, got {:?}",
            implements
        );
        assert!(
            extends
                .iter()
                .any(|(s, t)| s.ends_with("::Authenticating") && *t == "AnyObject"),
            "Authenticating should extend AnyObject, got {:?}",
            extends
        );
    }

    #[test]
    fn extracts_swift_calls_via_tree_sitter() {
        let src = r#"
class Session {
    func authenticate() {}
    func start() {
        authenticate()
        helper.doWork()
    }
}
"#;
        let (_elems, rels) =
            SwiftExtractor::new(src.as_bytes(), "calls.swift").extract_with_calls();
        let calls: Vec<_> = rels
            .iter()
            .filter(|r| r.rel_type == "calls")
            .map(|r| (r.source_qualified.as_str(), r.target_qualified.as_str()))
            .collect();
        assert!(
            calls
                .iter()
                .any(|(s, t)| s.contains("start") && t.contains("authenticate")),
            "start should call authenticate, got {:?}",
            calls
        );
        assert!(
            calls.iter().any(|(_, t)| t.contains("doWork")),
            "should see doWork call, got {:?}",
            calls
        );
    }

    #[test]
    fn extracts_demo_swift_fixture_from_disk() {
        let path = concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/tests/fixtures/swift/Session.swift"
        );
        let src = std::fs::read(path).expect("Session.swift fixture");
        let (elems, rels) = SwiftExtractor::new(&src, "Session.swift").extract_with_calls();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "class" && e.name == "Session"));
        assert!(rels.iter().any(|r| {
            r.rel_type == "extends"
                && r.source_qualified.ends_with("::Session")
                && r.target_qualified == "NSObject"
        }));
        assert!(rels
            .iter()
            .any(|r| r.rel_type == "calls" && r.target_qualified.contains("authenticate")));
    }
}
