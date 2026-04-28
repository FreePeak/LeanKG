use crate::db::models::Relationship;
use std::collections::{HashMap, HashSet};
use tree_sitter::Node;

/// Two-pass call graph builder for accurate call resolution
pub struct CallGraphBuilder<'a> {
    source: &'a [u8],
    file_path: &'a str,
    defined_functions: HashMap<String, String>,
    // Class name -> set of method names
    class_methods: HashMap<String, HashSet<String>>,
    // Extension functions in this file
    extension_functions: HashSet<String>,
}

#[derive(Debug, Clone)]
pub struct CallInfo {
    pub caller: String,
    pub callee: String,
    pub confidence: f64,
    pub is_resolved: bool,
    pub is_extension: bool,
    pub is_scope_function: bool,
    pub line: u32,
}

impl<'a> CallGraphBuilder<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self {
            source,
            file_path,
            defined_functions: HashMap::new(),
            class_methods: HashMap::new(),
            extension_functions: HashSet::new(),
        }
    }

    /// Pass 1: Collect all function definitions
    pub fn collect_definitions(&mut self, tree: &tree_sitter::Tree) {
        self.visit_for_definitions(tree.root_node(), None);
    }

    /// Pass 2: Build call relationships
    pub fn build_call_graph(&self, tree: &tree_sitter::Tree) -> Vec<Relationship> {
        let mut calls = Vec::new();
        self.visit_for_calls(tree.root_node(), None, None, &mut calls);
        calls
    }

    fn visit_for_definitions(&mut self, node: Node, current_class: Option<&str>) {
        let node_type = node.kind();

        // Extract function/method definitions
        let is_function = matches!(
            node_type,
            "function_declaration" | "function_definition" | "function_item" | "function_def"
        );
        let is_method = matches!(node_type, "method_declaration" | "method_definition");
        let is_constructor = matches!(
            node_type,
            "constructor_declaration" | "secondary_constructor"
        );

        if is_function || is_method || is_constructor {
            if let Some(name) = self.get_node_name(node) {
                let qualified_name = if let Some(class) = current_class {
                    format!("{}::{}::{}", self.file_path, class, name)
                } else {
                    format!("{}::{}", self.file_path, name)
                };

                // Store in appropriate map
                if let Some(class) = current_class {
                    self.class_methods
                        .entry(class.to_string())
                        .or_default()
                        .insert(name.clone());
                } else {
                    self.defined_functions
                        .insert(name.clone(), qualified_name.clone());

                    // Check if it's an extension function
                    if self.is_extension_function(node) {
                        self.extension_functions.insert(name);
                    }
                }
            }
        }

        // Track current class for methods
        let node_name = self.get_node_name(node);
        let new_class = if matches!(
            node_type,
            "class_declaration"
                | "type_declaration"
                | "class_def"
                | "class_definition"
                | "object_declaration"
                | "companion_object"
        ) {
            node_name.as_deref().or(current_class)
        } else {
            current_class
        };

        // Visit children
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            self.visit_for_definitions(child, new_class);
        }
    }

    fn visit_for_calls(
        &self,
        node: Node,
        current_function: Option<&str>,
        current_class: Option<&str>,
        calls: &mut Vec<Relationship>,
    ) {
        let node_type = node.kind();

        // Process call expressions
        if node_type == "call_expression" || node_type == "method_invocation" {
            if let Some(call) = self.extract_call(node, current_function, current_class) {
                calls.push(Relationship {
                    id: None,
                    source_qualified: call.caller,
                    target_qualified: call.callee,
                    rel_type: "calls".to_string(),
                    confidence: call.confidence,
                    metadata: serde_json::json!({
                        "is_resolved": call.is_resolved,
                        "is_extension": call.is_extension,
                        "is_scope_function": call.is_scope_function,
                        "line": call.line,
                    }),
                });
            }
        }

        let is_class = matches!(
            node_type,
            "class_declaration"
                | "type_declaration"
                | "class_def"
                | "class_definition"
                | "object_declaration"
                | "companion_object"
        );

        let is_function = matches!(
            node_type,
            "function_declaration"
                | "function_definition"
                | "function_item"
                | "function_def"
                | "method_declaration"
                | "method_definition"
                | "constructor_declaration"
                | "secondary_constructor"
        );

        let node_name = self.get_node_name(node);
        let new_class = if is_class {
            node_name.as_deref().or(current_class)
        } else {
            current_class
        };
        let new_function = if is_function {
            node_name.as_deref().or(current_function)
        } else {
            current_function
        };

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            self.visit_for_calls(child, new_function, new_class, calls);
        }
    }

    fn extract_call(
        &self,
        node: Node,
        caller: Option<&str>,
        current_class: Option<&str>,
    ) -> Option<CallInfo> {
        let caller_qn = caller.map_or_else(
            || self.file_path.to_string(),
            |f| format!("{}::{}", self.file_path, f),
        );

        let line = node.start_position().row as u32 + 1;

        // Try to resolve the call target
        let (target_name, receiver, is_scope) = self.extract_call_target(node)?;

        // Skip scope functions (let, run, apply, also, with)
        if self.is_scope_function(&target_name) {
            return Some(CallInfo {
                caller: caller_qn,
                callee: target_name,
                confidence: 0.3,
                is_resolved: true,
                is_extension: false,
                is_scope_function: true,
                line,
            });
        }

        // Try to resolve the call
        let (resolved_target, confidence, is_resolved, is_extension) =
            self.resolve_call(&target_name, receiver.as_deref(), current_class);

        Some(CallInfo {
            caller: caller_qn,
            callee: resolved_target,
            confidence,
            is_resolved,
            is_extension,
            is_scope_function: is_scope,
            line,
        })
    }

    fn extract_call_target(&self, node: Node) -> Option<(String, Option<String>, bool)> {
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            let kind = child.kind();

            // Simple identifier call: foo()
            if kind == "identifier" || kind == "simple_identifier" {
                if let Some(name) = self.get_node_text(child) {
                    return Some((name, None, false));
                }
            }

            // Method call on receiver: obj.method()
            if kind == "navigation_expression" || kind == "field_expression" {
                let (name, receiver) = self.extract_navigation_target(child)?;
                return Some((name, Some(receiver), false));
            }

            // Selector expression (Go-style): pkg.Function
            if kind == "selector_expression" {
                if let Some(name) = self.extract_selector_name(child) {
                    return Some((name, None, false));
                }
            }

            // Call with call suffix (Kotlin): expr.callSuffix
            if kind == "call_suffix" {
                // Try to find the function being called
                if let Some(parent) = node.parent() {
                    return self.extract_call_target(parent);
                }
            }
        }

        None
    }

    fn extract_navigation_target(&self, node: Node) -> Option<(String, String)> {
        let mut receiver = None;
        let mut method = None;

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            let kind = child.kind();

            // Receiver is the left side
            if receiver.is_none()
                && (kind == "identifier"
                    || kind == "simple_identifier"
                    || kind == "this_expression")
            {
                receiver = self.get_node_text(child);
            }

            // Method is the right side (after .)
            if kind == "navigation_suffix" || kind == "field_identifier" || kind == "identifier" {
                if let Some(name) = self.get_node_text(child) {
                    method = Some(name);
                }
            }
        }

        match (method, receiver) {
            (Some(m), Some(r)) => Some((m, r)),
            _ => None,
        }
    }

    fn extract_selector_name(&self, node: Node) -> Option<String> {
        // For selector_expression like pkg.Function, return Function
        let mut cursor = node.walk();
        let mut last_identifier = None;

        for child in node.children(&mut cursor) {
            if child.kind() == "field_identifier" || child.kind() == "identifier" {
                last_identifier = self.get_node_text(child);
            }
        }

        last_identifier
    }

    fn resolve_call(
        &self,
        target_name: &str,
        receiver: Option<&str>,
        current_class: Option<&str>,
    ) -> (String, f64, bool, bool) {
        // 1. Check same-class method call (bare call within a class body)
        if receiver.is_none() {
            if let Some(cls) = current_class {
                if let Some(methods) = self.class_methods.get(cls) {
                    if methods.contains(target_name) {
                        let qualified = format!("{}::{}::{}", self.file_path, cls, target_name);
                        return (qualified, 0.95, true, false);
                    }
                }
            }
        }

        // 2. Check explicit receiver
        if let Some(rec) = receiver {
            if self.class_methods.contains_key(rec) {
                let methods = self.class_methods.get(rec).unwrap();
                if methods.contains(target_name) {
                    let qualified = format!("{}::{}::{}", self.file_path, rec, target_name);
                    return (qualified, 0.95, true, false);
                }
            }

            if rec == "this" || rec == "self" {
                for (class, methods) in &self.class_methods {
                    if methods.contains(target_name) {
                        let qualified = format!("{}::{}::{}", self.file_path, class, target_name);
                        return (qualified, 0.95, true, false);
                    }
                }
            }
        }

        // 3. Check top-level function in this file
        if let Some(qualified) = self.defined_functions.get(target_name) {
            let is_ext = self.extension_functions.contains(target_name);
            return (qualified.clone(), 0.90, true, is_ext);
        }

        // 4. Fallback: any class method with this name
        for (class, methods) in &self.class_methods {
            if methods.contains(target_name) {
                let qualified = format!("{}::{}::{}", self.file_path, class, target_name);
                return (qualified, 0.85, true, false);
            }
        }

        // 5. Unresolved
        let unresolved = format!("__unresolved__{}", target_name);
        (unresolved, 0.50, false, false)
    }

    fn is_scope_function(&self, name: &str) -> bool {
        matches!(
            name,
            "let" | "run" | "apply" | "also" | "with" | "takeIf" | "takeUnless"
        )
    }

    fn is_extension_function(&self, node: Node) -> bool {
        // In tree-sitter-kotlin-ng, extension functions have a "." as a direct child
        // of function_declaration between the receiver type and function name.
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "." {
                return true;
            }
        }
        false
    }

    fn get_node_name(&self, node: Node) -> Option<String> {
        // Check for 'name' field first
        if let Some(name_node) = node.child_by_field_name("name") {
            return self.get_node_text(name_node);
        }

        // Walk children for identifier
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "identifier"
                || child.kind() == "type_identifier"
                || child.kind() == "simple_identifier"
            {
                return self.get_node_text(child);
            }
        }

        None
    }

    fn get_node_text(&self, node: Node) -> Option<String> {
        if let Some(bytes) = self.source.get(node.byte_range()) {
            if let Ok(s) = std::str::from_utf8(bytes) {
                return Some(s.to_string());
            }
        }
        None
    }
}

/// Enhanced call extraction that uses the CallGraphBuilder
pub fn extract_calls_with_resolution(
    tree: &tree_sitter::Tree,
    source: &[u8],
    file_path: &str,
    _language: &str,
) -> Vec<Relationship> {
    let mut builder = CallGraphBuilder::new(source, file_path);
    builder.collect_definitions(tree);
    builder.build_call_graph(tree)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn parse_kotlin(source: &str) -> tree_sitter::Tree {
        let mut parser = tree_sitter::Parser::new();
        let lang: tree_sitter::Language = tree_sitter_kotlin_ng::LANGUAGE.into();
        parser.set_language(&lang).unwrap();
        parser.parse(source, None).unwrap()
    }

    #[test]
    fn test_resolve_same_file_function() {
        let source = r#"
            fun helper() {}
            fun main() {
                helper()
            }
        "#;
        let tree = parse_kotlin(source);
        let calls = extract_calls_with_resolution(&tree, source.as_bytes(), "./test.kt", "kotlin");

        assert!(!calls.is_empty(), "Should extract calls");
        let helper_call = calls.iter().find(|c| c.target_qualified.contains("helper"));
        assert!(helper_call.is_some(), "Should find call to helper");
        assert!(
            helper_call.unwrap().confidence >= 0.90,
            "Should have high confidence"
        );
    }

    #[test]
    fn test_resolve_class_method() {
        let source = r#"
            class MyClass {
                fun doSomething() {}
                fun callIt() {
                    doSomething()
                }
            }
        "#;
        let tree = parse_kotlin(source);
        let calls = extract_calls_with_resolution(&tree, source.as_bytes(), "./test.kt", "kotlin");

        let method_call = calls
            .iter()
            .find(|c| c.target_qualified.contains("doSomething"));
        assert!(method_call.is_some(), "Should find method call");
        assert!(
            method_call.unwrap().confidence >= 0.95,
            "Same-class method should have highest confidence"
        );
    }

    #[test]
    fn test_scope_functions_marked() {
        let source = r#"
            fun test(list: List<String>) {
                list.let { println(it) }
            }
        "#;
        let tree = parse_kotlin(source);
        let calls = extract_calls_with_resolution(&tree, source.as_bytes(), "./test.kt", "kotlin");

        let let_call = calls.iter().find(|c| c.target_qualified.contains("let"));
        if let Some(call) = let_call {
            assert!(call
                .metadata
                .get("is_scope_function")
                .unwrap()
                .as_bool()
                .unwrap());
        }
    }

    #[test]
    fn test_extension_function_recognition() {
        let source = r#"
            fun String.extension(): String = this.uppercase()
            fun test() {
                "hello".extension()
            }
        "#;
        let tree = parse_kotlin(source);
        let mut builder = CallGraphBuilder::new(source.as_bytes(), "./test.kt");
        builder.collect_definitions(&tree);

        assert!(
            builder.extension_functions.contains("extension"),
            "Should detect extension function"
        );
    }
}
