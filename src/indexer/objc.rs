//! US-LANG-04 / FR-LANG-04: Objective-C entity extraction (regex-based v0).
//!
//! Note: US-LANG-03 is XML (DONE). ObjC uses US-LANG-04.
//!
//! LeanKG doesn't currently bundle tree-sitter-objc, so this extractor uses
//! regex patterns for the most common ObjC constructs: @interface,
//! @implementation, @protocol, @property, instance/class methods, categories,
//! and imports. The output schema mirrors the tree-sitter-based extractors so
//! agents don't need to special-case ObjC sources.
//!
//! Wired into both the bulk index walk and incremental `index_file_sync`
//! / MCP watcher paths (`.m`, `.mm`, `.h`).
//!
//! Limitations:
//!   - C functions, blocks, typedef, #define macros are not extracted
//!   - String-literal / comment false positives possible (regex limitation)
//!   - .h files may be double-counted when included from .m files (v0)
use crate::db::models::{CodeElement, Relationship};
use once_cell::sync::Lazy;
use regex::Regex;

static INTERFACE_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^\s*@interface\s+(\w+)(?:\s*:\s*(\w+))?(?:\s*<([^>]+)>)?").unwrap()
});
static CATEGORY_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?m)^\s*@interface\s+(\w+)\s*\((\w*)\)").unwrap());
static IMPLEMENTATION_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?m)^\s*@implementation\s+(\w+)").unwrap());
static PROTOCOL_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"(?m)^\s*@protocol\s+(\w+)").unwrap());
static PROPERTY_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?m)^\s*@property\s*\([^)]*\)\s*.*?(\w+)\s*;").unwrap());
static METHOD_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?m)^\s*[-+]\s*\([^)]*\)\s*(.+)").unwrap());
static IMPORT_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(?m)^\s*(?:#import\s+["<]([^">]+)[">]|@import\s+(\w+))"#).unwrap());
static SELECTOR_PART_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"(\w+)\s*:").unwrap());
static BARE_METHOD_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"^(\w+)\b").unwrap());

pub struct ObjCExtractor<'a> {
    source: &'a str,
    file_path: &'a str,
}

impl<'a> ObjCExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self {
            source: std::str::from_utf8(source).unwrap_or(""),
            file_path,
        }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements: Vec<CodeElement> = Vec::new();
        let mut relationships: Vec<Relationship> = Vec::new();

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
            language: "objc".to_string(),
            ..Default::default()
        });

        let mut current_parent: Option<String> = None;

        for (idx, line) in self.source.lines().enumerate() {
            let line_num = (idx + 1) as u32;

            if line.trim() == "@end" {
                current_parent = None;
                continue;
            }

            // Category: @interface ClassName (CatName) — check before INTERFACE_RE
            // since INTERFACE_RE also matches the class name prefix.
            if CATEGORY_RE.is_match(line) {
                if let Some(cap) = CATEGORY_RE.captures(line) {
                    let cls = &cap[1];
                    let cat = cap.get(2).map(|m| m.as_str()).unwrap_or("");
                    let disp = if cat.is_empty() {
                        format!("{}()", cls)
                    } else {
                        format!("{}({})", cls, cat)
                    };
                    self.push_decl(
                        &mut elements,
                        &mut relationships,
                        "category",
                        &disp,
                        line_num,
                    );
                    current_parent = Some(format!("{}::{}", self.file_path, disp));
                }
                continue;
            }

            // @interface ClassName [: SuperClass] [<Proto, ...>]
            if let Some(cap) = INTERFACE_RE.captures(line) {
                let name = &cap[1];
                let qn = self.push_decl(&mut elements, &mut relationships, "class", name, line_num);
                if let Some(super_cap) = cap.get(2) {
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: qn.clone(),
                        target_qualified: super_cap.as_str().to_string(),
                        rel_type: "extends".to_string(),
                        confidence: 0.9,
                        metadata: serde_json::json!({"resolution_method": "name"}),
                        ..Default::default()
                    });
                }
                if let Some(protos) = cap.get(3) {
                    for proto in protos.as_str().split(',') {
                        let proto = proto.trim();
                        if proto.is_empty() {
                            continue;
                        }
                        relationships.push(Relationship {
                            id: None,
                            source_qualified: qn.clone(),
                            target_qualified: proto.to_string(),
                            rel_type: "implements".to_string(),
                            confidence: 0.9,
                            metadata: serde_json::json!({"resolution_method": "name"}),
                            ..Default::default()
                        });
                    }
                }
                current_parent = Some(qn);
                continue;
            }

            // @implementation ClassName
            if let Some(cap) = IMPLEMENTATION_RE.captures(line) {
                let name = &cap[1];
                // Avoid duplicating class element if @interface already registered it.
                let already = elements
                    .iter()
                    .any(|e| e.element_type == "class" && e.name == name);
                if !already {
                    self.push_decl(&mut elements, &mut relationships, "class", name, line_num);
                }
                current_parent = Some(format!("{}::{}", self.file_path, name));
                continue;
            }

            // @protocol ProtocolName
            if let Some(cap) = PROTOCOL_RE.captures(line) {
                let name = &cap[1];
                self.push_decl(
                    &mut elements,
                    &mut relationships,
                    "interface",
                    name,
                    line_num,
                );
                current_parent = Some(format!("{}::{}", self.file_path, name));
                continue;
            }

            // @property
            if let Some(cap) = PROPERTY_RE.captures(line) {
                let name = &cap[1];
                let qn = format!(
                    "{}::{}",
                    current_parent.as_deref().unwrap_or(self.file_path),
                    name
                );
                elements.push(CodeElement {
                    qualified_name: qn.clone(),
                    element_type: "property".to_string(),
                    name: name.to_string(),
                    file_path: self.file_path.to_string(),
                    line_start: line_num,
                    line_end: line_num,
                    language: "objc".to_string(),
                    parent_qualified: current_parent.clone(),
                    ..Default::default()
                });
                if let Some(ref parent) = current_parent {
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
                continue;
            }

            // Methods: - (type)name or + (type)name: / multi-arg selectors
            if let Some(cap) = METHOD_RE.captures(line) {
                let Some(name) = parse_objc_selector(cap[1].trim()) else {
                    continue;
                };
                let qn = format!(
                    "{}::{}",
                    current_parent.as_deref().unwrap_or(self.file_path),
                    name
                );
                elements.push(CodeElement {
                    qualified_name: qn.clone(),
                    element_type: "method".to_string(),
                    name,
                    file_path: self.file_path.to_string(),
                    line_start: line_num,
                    line_end: line_num,
                    language: "objc".to_string(),
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
            }
        }

        for cap in IMPORT_RE.captures_iter(self.source) {
            let target = cap
                .get(1)
                .or_else(|| cap.get(2))
                .map(|m| m.as_str())
                .unwrap_or("");
            if !target.is_empty() {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: target.to_string(),
                    rel_type: "imports".to_string(),
                    confidence: 0.95,
                    metadata: serde_json::json!({"resolution_method": "name"}),
                    ..Default::default()
                });
            }
        }

        (elements, relationships)
    }

    /// Regex entities plus tree-sitter-objc message-send call edges.
    pub fn extract_with_calls(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let (elements, mut relationships) = self.extract();
        if let Some(tree) = parse_objc(self.source.as_bytes()) {
            relationships.extend(extract_objc_message_calls(
                &tree,
                self.source.as_bytes(),
                self.file_path,
            ));
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
            language: "objc".to_string(),
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
}

/// Build an ObjC selector from the text after the return-type parentheses.
/// Examples: `sayHello` → `sayHello`; `setName:(NSString *)name age:(NSInteger)age` → `setName:age:`
fn parse_objc_selector(after_rettype: &str) -> Option<String> {
    let trimmed = after_rettype
        .trim_end_matches(|c: char| c == ';' || c == '{' || c.is_whitespace())
        .trim();
    if trimmed.is_empty() {
        return None;
    }
    let parts: Vec<_> = SELECTOR_PART_RE
        .captures_iter(trimmed)
        .map(|c| format!("{}:", &c[1]))
        .collect();
    if !parts.is_empty() {
        return Some(parts.join(""));
    }
    BARE_METHOD_RE.captures(trimmed).map(|c| c[1].to_string())
}

/// Heuristic: treat a `.h` as Objective-C when ObjC markers are present.
pub fn looks_like_objc(source: &str) -> bool {
    source.contains("@interface")
        || source.contains("@implementation")
        || source.contains("@protocol")
        || source.contains("@class")
        || source.contains("#import")
        || source.contains("@property")
}

fn parse_objc(source: &[u8]) -> Option<tree_sitter::Tree> {
    let mut parser = tree_sitter::Parser::new();
    let lang: tree_sitter::Language = tree_sitter_objc::LANGUAGE.into();
    parser.set_language(&lang).ok()?;
    parser.parse(source, None)
}

fn extract_objc_message_calls(
    tree: &tree_sitter::Tree,
    source: &[u8],
    file_path: &str,
) -> Vec<Relationship> {
    let mut calls = Vec::new();
    let mut stack = vec![(tree.root_node(), None::<String>)];

    while let Some((node, current_method)) = stack.pop() {
        let kind = node.kind();
        let mut method_ctx = current_method.clone();

        if kind == "method_definition" {
            // Prefer identifier after method_type as the selector base; rebuild
            // keyword parts from sibling identifiers followed by ':'.
            if let Some(sel) = objc_method_definition_selector(node, source) {
                method_ctx = Some(format!("{}::{}", file_path, sel));
            }
        }

        if kind == "message_expression" {
            if let Some(sel) = objc_message_selector(node, source) {
                let caller = method_ctx.clone().unwrap_or_else(|| file_path.to_string());
                calls.push(Relationship {
                    id: None,
                    source_qualified: caller,
                    target_qualified: format!("{}::{}", file_path, sel),
                    rel_type: "calls".to_string(),
                    confidence: 0.7,
                    metadata: serde_json::json!({
                        "resolution_method": "name",
                        "line": node.start_position().row as u32 + 1
                    }),
                    ..Default::default()
                });
            }
        }

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            stack.push((child, method_ctx.clone()));
        }
    }

    calls
}

fn node_text<'a>(source: &'a [u8], node: tree_sitter::Node) -> Option<&'a str> {
    std::str::from_utf8(&source[node.byte_range()]).ok()
}

fn objc_method_definition_selector(node: tree_sitter::Node, source: &[u8]) -> Option<String> {
    // Collect identifier / identifier: parts in order.
    let mut parts = Vec::new();
    let mut cursor = node.walk();
    let children: Vec<_> = node.children(&mut cursor).collect();
    let mut i = 0;
    while i < children.len() {
        let child = children[i];
        if child.kind() == "identifier" {
            let name = node_text(source, child)?.to_string();
            if i + 1 < children.len() && children[i + 1].kind() == ":" {
                parts.push(format!("{}:", name));
                i += 2;
                continue;
            }
            if parts.is_empty() {
                // Bare method name (no args) — take first identifier after types.
                // Skip if this looks like we're still inside method_type.
                if child.start_byte() > 0 {
                    parts.push(name);
                    break;
                }
            }
        }
        i += 1;
    }
    if parts.is_empty() {
        None
    } else {
        Some(parts.join(""))
    }
}

fn objc_message_selector(node: tree_sitter::Node, source: &[u8]) -> Option<String> {
    // message_expression: [recv sel] or [recv sel:arg other:arg2]
    // Skip first identifier (receiver); subsequent identifier / identifier: form selector.
    let mut cursor = node.walk();
    let children: Vec<_> = node
        .children(&mut cursor)
        .filter(|c| c.kind() != "[" && c.kind() != "]")
        .collect();
    if children.is_empty() {
        return None;
    }
    // Receiver is first non-bracket child.
    let mut parts = Vec::new();
    let mut i = 1; // skip receiver
    while i < children.len() {
        let child = children[i];
        if child.kind() == "identifier" {
            let name = node_text(source, child)?.to_string();
            if i + 1 < children.len() && children[i + 1].kind() == ":" {
                parts.push(format!("{}:", name));
                i += 2;
                // skip argument expression(s) until next identifier or end
                while i < children.len()
                    && children[i].kind() != "identifier"
                    && children[i].kind() != ":"
                {
                    i += 1;
                }
                continue;
            }
            if parts.is_empty() {
                parts.push(name);
                break;
            }
        }
        i += 1;
    }
    if parts.is_empty() {
        None
    } else {
        Some(parts.join(""))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extracts_objc_interface_and_protocol() {
        let src = r#"
#import <Foundation/Foundation.h>

@interface Greeter : NSObject
@property (nonatomic, strong) NSString *name;
- (void)sayHello;
- (NSString *)greeting;
+ (instancetype)shared;
@end

@protocol Greetable
- (void)greet;
@end
"#;
        let (elems, rels) = ObjCExtractor::new(src.as_bytes(), "test.m").extract();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "class" && e.name == "Greeter"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "interface" && e.name == "Greetable"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "property" && e.name == "name"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "method" && e.name == "sayHello"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "method" && e.name == "greeting"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "method" && e.name == "shared"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "method" && e.name == "greet"));
        assert!(rels
            .iter()
            .any(|r| r.rel_type == "imports" && r.target_qualified == "Foundation/Foundation.h"));
    }

    #[test]
    fn extracts_objc_category() {
        let src = r#"
@interface NSString (MyCategory)
- (NSString *)reversed;
@end
"#;
        let (elems, _) = ObjCExtractor::new(src.as_bytes(), "cat.m").extract();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "category" && e.name == "NSString(MyCategory)"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "method" && e.name == "reversed"));
    }

    #[test]
    fn extracts_objc_implementation() {
        let src = r#"
#import "Greeter.h"

@implementation Greeter
- (void)sayHello {
    NSLog(@"hi");
}
@end
"#;
        let (elems, rels) = ObjCExtractor::new(src.as_bytes(), "greeter.m").extract();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "class" && e.name == "Greeter"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "method" && e.name == "sayHello"));
        assert!(rels
            .iter()
            .any(|r| r.rel_type == "imports" && r.target_qualified == "Greeter.h"));
    }

    #[test]
    fn extracts_objc_header() {
        let src = r#"
#import <Foundation/Foundation.h>

@interface MyClass : NSObject
@property (readonly) NSInteger count;
- (NSInteger)compute:(NSString *)input;
@end
"#;
        let (elems, _) = ObjCExtractor::new(src.as_bytes(), "MyClass.h").extract();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "class" && e.name == "MyClass"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "property" && e.name == "count"));
        assert!(elems
            .iter()
            .any(|e| e.element_type == "method" && e.name == "compute:"));
    }

    #[test]
    fn extracts_objc_extends_implements_and_selectors() {
        let src = r#"
@interface Greeter : NSObject <Greetable, Logging>
- (void)setName:(NSString *)name age:(NSInteger)age;
- (void)sayHello;
@end
"#;
        let (elems, rels) = ObjCExtractor::new(src.as_bytes(), "Greeter.h").extract();

        assert!(
            elems
                .iter()
                .any(|e| e.element_type == "method" && e.name == "setName:age:"),
            "expected multi-arg selector setName:age:, got {:?}",
            elems
                .iter()
                .filter(|e| e.element_type == "method")
                .map(|e| &e.name)
                .collect::<Vec<_>>()
        );
        assert!(elems
            .iter()
            .any(|e| e.element_type == "method" && e.name == "sayHello"));

        assert!(
            rels.iter().any(|r| {
                r.rel_type == "extends"
                    && r.source_qualified.ends_with("::Greeter")
                    && r.target_qualified == "NSObject"
            }),
            "Greeter should extend NSObject, got {:?}",
            rels.iter()
                .filter(|r| r.rel_type == "extends")
                .collect::<Vec<_>>()
        );
        assert!(
            rels.iter().any(|r| {
                r.rel_type == "implements"
                    && r.source_qualified.ends_with("::Greeter")
                    && r.target_qualified == "Greetable"
            }),
            "Greeter should implement Greetable"
        );
        assert!(
            rels.iter().any(|r| {
                r.rel_type == "implements"
                    && r.source_qualified.ends_with("::Greeter")
                    && r.target_qualified == "Logging"
            }),
            "Greeter should implement Logging"
        );
    }

    #[test]
    fn extracts_objc_message_sends_as_calls() {
        let src = r#"
@implementation Greeter
- (void)sayHello {
    [self setup];
    [logger log:@"hi" level:1];
}
- (void)setup {}
@end
"#;
        let (_elems, rels) = ObjCExtractor::new(src.as_bytes(), "Greeter.m").extract_with_calls();
        let calls: Vec<_> = rels
            .iter()
            .filter(|r| r.rel_type == "calls")
            .map(|r| r.target_qualified.as_str())
            .collect();
        assert!(
            calls.iter().any(|t| t.contains("setup")),
            "expected call to setup, got {:?}",
            calls
        );
        assert!(
            calls.iter().any(|t| t.contains("log:level:")),
            "expected call to log:level:, got {:?}",
            calls
        );
    }

    #[test]
    fn looks_like_objc_detects_headers() {
        assert!(looks_like_objc(
            "#import <Foundation/Foundation.h>\n@interface Foo : NSObject\n@end\n"
        ));
        assert!(!looks_like_objc(
            "#ifndef FOO_H\n#define FOO_H\nstruct Foo { int x; };\n#endif\n"
        ));
    }
}
