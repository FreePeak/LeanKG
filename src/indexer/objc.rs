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
//!   - Method argument types / selector fragments are not parsed
//!   - Protocol conformance (<Proto1,Proto2>) is not extracted as edges
//!   - String-literal / comment false positives possible (regex limitation)
//!   - .h files may be double-counted when included from .m files (v0)
use crate::db::models::{CodeElement, Relationship};
use once_cell::sync::Lazy;
use regex::Regex;

static INTERFACE_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?m)^\s*@interface\s+(\w+)(?:\s*:\s*(\w+))?\b").unwrap());
static CATEGORY_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?m)^\s*@interface\s+(\w+)\s*\((\w*)\)").unwrap());
static IMPLEMENTATION_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?m)^\s*@implementation\s+(\w+)").unwrap());
static PROTOCOL_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"(?m)^\s*@protocol\s+(\w+)").unwrap());
static PROPERTY_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?m)^\s*@property\s*\([^)]*\)\s*.*?(\w+)\s*;").unwrap());
static METHOD_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?m)^\s*[-+]\s*\([^)]*\)\s*(\w+)").unwrap());
static IMPORT_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(?m)^\s*(?:#import\s+["<]([^">]+)[">]|@import\s+(\w+))"#).unwrap());

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

            // @interface ClassName [: SuperClass]
            if let Some(cap) = INTERFACE_RE.captures(line) {
                let name = &cap[1];
                self.push_decl(&mut elements, &mut relationships, "class", name, line_num);
                current_parent = Some(format!("{}::{}", self.file_path, name));
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

            // Methods: - (type)name or + (type)name
            if let Some(cap) = METHOD_RE.captures(line) {
                let name = &cap[1];
                let qn = format!(
                    "{}::{}",
                    current_parent.as_deref().unwrap_or(self.file_path),
                    name
                );
                elements.push(CodeElement {
                    qualified_name: qn.clone(),
                    element_type: "method".to_string(),
                    name: name.to_string(),
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
            language: "objc".to_string(),
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
            .any(|e| e.element_type == "method" && e.name == "compute"));
    }
}
