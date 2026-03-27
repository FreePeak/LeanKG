use crate::db::models::{CodeElement, Relationship};
use std::path::Path;
use tree_sitter::{Node, Tree};

pub struct EntityExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
    language: &'a str,
}

pub fn is_test_file(file_path: &str) -> bool {
    let path = Path::new(file_path);
    let file_name = path.file_name().and_then(|n| n.to_str()).unwrap_or("");

    match path.extension().and_then(|e| e.to_str()).unwrap_or("") {
        "go" => file_name.ends_with("_test.go"),
        "py" => file_name.starts_with("test_") || file_name.ends_with("_test.py"),
        "rb" => file_name.ends_with("_spec.rb"),
        "ts" | "js" => {
            file_name.ends_with(".test.ts")
                || file_name.ends_with(".test.js")
                || file_name.ends_with(".spec.ts")
                || file_name.ends_with(".spec.js")
        }
        _ => false,
    }
}

pub fn is_noise_call(name: &str) -> bool {
    matches!(
        name,
        "println"
            | "print"
            | "eprintln"
            | "format"
            | "vec"
            | "assert"
            | "assert_eq"
            | "assert_ne"
            | "panic"
            | "unwrap"
            | "expect"
            | "clone"
            | "to_string"
            | "into"
            | "from"
            | "len"
            | "is_empty"
            | "ok"
            | "err"
            | "map"
            | "and_then"
            | "or_else"
            | "collect"
            | "iter"
            | "push"
            | "pop"
            | "insert"
            | "get"
            | "contains"
            | "drop"
            | "take"
            | "skip"
            | "next"
            | "filter"
            | "fold"
            | "Some"
            | "None"
            | "Ok"
            | "Err"
            | "async"
            | "await"
            | "new"
            | "with_capacity"
            | "with_len"
    ) || name.len() < 2
}

pub fn get_tested_file_path(file_path: &str) -> Option<String> {
    let path = Path::new(file_path);
    let file_name = path.file_name()?.to_str()?;
    let parent = path.parent()?.to_string_lossy().to_string();

    let tested_name = match path.extension().and_then(|e| e.to_str()).unwrap_or("") {
        "go" => {
            if file_name.ends_with("_test.go") {
                Some(file_name.trim_end_matches("_test.go").to_string() + ".go")
            } else {
                None
            }
        }
        "py" => {
            if file_name.starts_with("test_") {
                Some(file_name.strip_prefix("test_").unwrap().to_string())
            } else if file_name.ends_with("_test.py") {
                Some(file_name.trim_end_matches("_test.py").to_string() + ".py")
            } else {
                None
            }
        }
        "rb" => {
            if file_name.ends_with("_spec.rb") {
                Some(file_name.trim_end_matches("_spec.rb").to_string() + ".rb")
            } else {
                None
            }
        }
        "ts" | "js" => {
            if file_name.ends_with(".test.ts") || file_name.ends_with(".test.js") {
                Some(file_name.replace(".test.", "."))
            } else if file_name.ends_with(".spec.ts") || file_name.ends_with(".spec.js") {
                Some(file_name.replace(".spec.", "."))
            } else {
                None
            }
        }
        _ => None,
    }?;

    if parent.is_empty() || parent == "." {
        Some(tested_name)
    } else {
        Some(format!("{}/{}", parent, tested_name))
    }
}

impl<'a> EntityExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str, language: &'a str) -> Self {
        Self {
            source,
            file_path,
            language,
        }
    }

    fn find_body_start_line(&self, node: Node) -> Option<u32> {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "block" || child.kind() == "statement_block" {
                return Some(child.start_position().row as u32);
            }
        }
        None
    }

    fn extract_function_signature(&self, node: Node) -> (String, u32) {
        let start = node.start_position().row;
        let body_start = self.find_body_start_line(node);
        let end_row = body_start
            .unwrap_or(node.end_position().row as u32)
            .saturating_sub(1);

        let mut signature_lines = Vec::new();
        let mut current_row = start as u32;

        let source_str = std::str::from_utf8(self.source).unwrap_or("");
        for line in source_str.lines() {
            if current_row > end_row {
                break;
            }
            if current_row == start as u32 || signature_lines.is_empty() {
                signature_lines.push(line.to_string());
            } else if current_row <= end_row {
                signature_lines.push(line.to_string());
            }
            current_row += 1;
        }

        let signature = signature_lines.join("\n");
        let sig_end = if signature_lines.len() > 1 {
            start as u32 + signature_lines.len() as u32 - 1
        } else {
            start as u32
        };

        (signature, sig_end)
    }

    pub fn extract(&self, tree: &Tree) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();
        let mut relationships = Vec::new();
        self.visit_node(tree.root_node(), None, &mut elements, &mut relationships);

        if is_test_file(self.file_path) {
            if let Some(tested_path) = get_tested_file_path(self.file_path) {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: tested_path,
                    target_qualified: self.file_path.to_string(),
                    rel_type: "tested_by".to_string(),
                    metadata: serde_json::json!({}),
                });
            }
        }

        (elements, relationships)
    }

    fn visit_node(
        &self,
        node: Node,
        parent: Option<&str>,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let node_type = node.kind();

        match node_type {
            "function_declaration"
            | "function_definition"
            | "function_item"
            | "function_def"
            | "method_declaration"
            | "method_definition" => {
                self.extract_function(node, parent, elements);
            }
            "class_declaration" | "type_declaration" | "class_def" | "struct_item"
            | "class_definition" => {
                self.extract_class(node, parent, elements);
            }
            "decorated_definition" => {
                self.extract_decorated_definition(node, parent, elements, relationships);
            }
            "type_spec" => {
                self.extract_type_spec(node, parent, elements, relationships);
            }
            "interface_declaration" => {
                self.extract_interface(node, parent, elements);
            }
            "import_declaration"
            | "import_specifier"
            | "import_statement"
            | "import_from_statement"
            | "use_declaration" => {
                for source in self.get_import_sources(node, node_type) {
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: self.file_path.to_string(),
                        target_qualified: source,
                        rel_type: "imports".to_string(),
                        metadata: serde_json::json!({}),
                    });
                }
            }
            "call_expression" => {
                self.extract_call(node, parent, elements, relationships);
            }
            "decorator" | "decorator_definition" => {
                self.extract_decorator(node, parent, elements);
            }
            _ => {}
        }

        for i in 0..node.child_count() {
            if let Some(child) = node.child(i) {
                let current_parent = if matches!(
                    node_type,
                    "function_declaration"
                        | "function_definition"
                        | "function_item"
                        | "function_def"
                        | "method_declaration"
                        | "method_definition"
                        | "class_declaration"
                        | "type_declaration"
                        | "class_def"
                        | "class_definition"
                        | "type_spec"
                        | "struct_item"
                ) {
                    self.get_node_name(node)
                } else {
                    parent.map(String::from)
                };
                self.visit_node(child, current_parent.as_deref(), elements, relationships);
            }
        }
    }

    fn extract_function(&self, node: Node, parent: Option<&str>, elements: &mut Vec<CodeElement>) {
        if let Some(name) = self.get_node_name(node) {
            let qualified_name = format!("{}::{}", self.file_path, name);
            let (signature, sig_end) = self.extract_function_signature(node);
            elements.push(CodeElement {
                qualified_name: qualified_name.clone(),
                element_type: "function".to_string(),
                name,
                file_path: self.file_path.to_string(),
                line_start: node.start_position().row as u32 + 1,
                line_end: node.end_position().row as u32 + 1,
                language: self.language.to_string(),
                parent_qualified: parent.map(String::from),
                metadata: serde_json::json!({
                    "signature": signature,
                    "signature_line_end": sig_end + 1,
                }),
            });
        }
    }

    fn extract_class(&self, node: Node, parent: Option<&str>, elements: &mut Vec<CodeElement>) {
        if let Some(name) = self.get_node_name(node) {
            let qualified_name = format!("{}::{}", self.file_path, name);
            elements.push(CodeElement {
                qualified_name: qualified_name.clone(),
                element_type: "class".to_string(),
                name,
                file_path: self.file_path.to_string(),
                line_start: node.start_position().row as u32 + 1,
                line_end: node.end_position().row as u32 + 1,
                language: self.language.to_string(),
                parent_qualified: parent.map(String::from),
                metadata: serde_json::json!({}),
            });
        }
    }

    fn extract_type_spec(
        &self,
        node: Node,
        parent: Option<&str>,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        if let Some(name) = self.get_node_name(node) {
            let is_interface = self.check_if_interface(node);
            let element_type = if is_interface { "interface" } else { "struct" };

            let qualified_name = format!("{}::{}", self.file_path, name);
            elements.push(CodeElement {
                qualified_name: qualified_name.clone(),
                element_type: element_type.to_string(),
                name,
                file_path: self.file_path.to_string(),
                line_start: node.start_position().row as u32 + 1,
                line_end: node.end_position().row as u32 + 1,
                language: self.language.to_string(),
                parent_qualified: parent.map(String::from),
                metadata: serde_json::json!({}),
            });

            if !is_interface {
                self.extract_go_implementations(node, qualified_name, relationships);
            }
        }
    }

    fn check_if_interface(&self, node: Node) -> bool {
        if node.kind() == "interface_type" {
            return true;
        }
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "method_set"
                || child.kind() == "method_elem"
                || child.kind() == "interface_type"
            {
                return true;
            }
        }
        false
    }

    fn extract_go_implementations(
        &self,
        node: Node,
        struct_qualified: String,
        relationships: &mut Vec<Relationship>,
    ) {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() != "field_declaration_list" {
                continue;
            }
            let mut field_cursor = child.walk();
            for field in child.children(&mut field_cursor) {
                if field.kind() != "field_declaration" {
                    continue;
                }
                let has_name = field.child_by_field_name("name").is_some();
                if has_name {
                    continue;
                }
                if let Some(type_node) = field.child_by_field_name("type") {
                    let type_str =
                        std::str::from_utf8(self.source.get(type_node.byte_range()).unwrap_or(&[]))
                            .unwrap_or("")
                            .trim_start_matches('*');

                    if !type_str.is_empty() && !type_str.contains(' ') {
                        relationships.push(Relationship {
                            id: None,
                            source_qualified: struct_qualified.clone(),
                            target_qualified: format!(
                                "{}::{}",
                                self.file_path
                                    .rsplit('/')
                                    .next()
                                    .unwrap_or("")
                                    .trim_end_matches(".go"),
                                type_str
                            ),
                            rel_type: "implements".to_string(),
                            metadata: serde_json::json!({"embedded": true}),
                        });
                    }
                }
            }
        }
    }

    fn extract_interface(&self, node: Node, parent: Option<&str>, elements: &mut Vec<CodeElement>) {
        if let Some(name) = self.get_node_name(node) {
            let qualified_name = format!("{}::{}", self.file_path, name);
            elements.push(CodeElement {
                qualified_name: qualified_name.clone(),
                element_type: "interface".to_string(),
                name,
                file_path: self.file_path.to_string(),
                line_start: node.start_position().row as u32 + 1,
                line_end: node.end_position().row as u32 + 1,
                language: self.language.to_string(),
                parent_qualified: parent.map(String::from),
                metadata: serde_json::json!({}),
            });
        }
    }

    fn extract_decorator(&self, node: Node, parent: Option<&str>, elements: &mut Vec<CodeElement>) {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "identifier" | "dotted_name" => {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(name) = std::str::from_utf8(bytes) {
                            let qualified_name = format!("{}::@{}", self.file_path, name);
                            elements.push(CodeElement {
                                qualified_name: qualified_name.clone(),
                                element_type: "decorator".to_string(),
                                name: name.to_string(),
                                file_path: self.file_path.to_string(),
                                line_start: node.start_position().row as u32 + 1,
                                line_end: node.end_position().row as u32 + 1,
                                language: self.language.to_string(),
                                parent_qualified: parent.map(String::from),
                                metadata: serde_json::json!({}),
                            });
                        }
                    }
                    return;
                }
                "attribute" => {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(name) = std::str::from_utf8(bytes) {
                            let qualified_name = format!("{}::@{}", self.file_path, name);
                            elements.push(CodeElement {
                                qualified_name: qualified_name.clone(),
                                element_type: "decorator".to_string(),
                                name: name.to_string(),
                                file_path: self.file_path.to_string(),
                                line_start: node.start_position().row as u32 + 1,
                                line_end: node.end_position().row as u32 + 1,
                                language: self.language.to_string(),
                                parent_qualified: parent.map(String::from),
                                metadata: serde_json::json!({}),
                            });
                        }
                    }
                    return;
                }
                _ => {}
            }
            if child.kind() == "attribute" {
                if let Some(bytes) = self.source.get(child.byte_range()) {
                    if let Ok(name) = std::str::from_utf8(bytes) {
                        let qualified_name = format!("{}::@{}", self.file_path, name);
                        elements.push(CodeElement {
                            qualified_name: qualified_name.clone(),
                            element_type: "decorator".to_string(),
                            name: name.to_string(),
                            file_path: self.file_path.to_string(),
                            line_start: node.start_position().row as u32 + 1,
                            line_end: node.end_position().row as u32 + 1,
                            language: self.language.to_string(),
                            parent_qualified: parent.map(String::from),
                            metadata: serde_json::json!({}),
                        });
                    }
                }
                break;
            }
        }
    }

    fn extract_decorated_definition(
        &self,
        node: Node,
        parent: Option<&str>,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "decorator" => {
                    self.extract_decorator(child, parent, elements);
                }
                "function_definition" | "function_declaration" => {
                    self.extract_function(child, parent, elements);
                }
                _ => {}
            }
        }
    }

    fn extract_call(
        &self,
        node: Node,
        parent: Option<&str>,
        _elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            let kind = child.kind();
            if kind == "field_expression" || kind == "identifier" || kind == "scoped_identifier" {
                let mut found_name = false;
                let mut name_to_use: Option<String> = None;

                // For scoped_identifier like `Arc::new`, we want the LAST identifier (the function name)
                let mut last_identifier_name: Option<String> = None;
                let mut first_identifier_name: Option<String> = None;

                let mut field_cursor = child.walk();
                for inner in child.children(&mut field_cursor) {
                    let inner_kind = inner.kind();
                    if inner_kind == "field_identifier" || inner_kind == "identifier" {
                        if let Some(bytes) = self.source.get(inner.byte_range()) {
                            if let Ok(name) = std::str::from_utf8(bytes) {
                                if first_identifier_name.is_none() {
                                    first_identifier_name = Some(name.to_string());
                                }
                                last_identifier_name = Some(name.to_string());
                            }
                        }
                    }
                }

                // For scoped_identifier like `Type::func()`, skip if first part is uppercase (it's a type, not module)
                if kind == "scoped_identifier" {
                    if let Some(first) = first_identifier_name {
                        if first
                            .chars()
                            .next()
                            .map(|c| c.is_uppercase())
                            .unwrap_or(false)
                        {
                            // Skip - first part is uppercase (likely a type constructor like Arc::new)
                            continue;
                        }
                    }
                }

                // For scoped_identifier and field_expression, use the last identifier (function/method name)
                if kind == "scoped_identifier" || kind == "field_expression" {
                    if let Some(name) = last_identifier_name {
                        if !is_noise_call(&name) {
                            name_to_use = Some(name);
                        }
                    }
                } else {
                    // For simple identifier, use it directly
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(name) = std::str::from_utf8(bytes) {
                            if !is_noise_call(name) {
                                name_to_use = Some(name.to_string());
                            }
                        }
                    }
                }

                if let Some(name) = name_to_use {
                    let parent_name = parent.unwrap_or("");
                    let source = if parent_name.is_empty() {
                        self.file_path.to_string()
                    } else {
                        format!("{}::{}", self.file_path, parent_name)
                    };
                    let target_qualified = format!("__unresolved__{}", name);
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: source,
                        target_qualified: target_qualified.clone(),
                        rel_type: "calls".to_string(),
                        metadata: serde_json::json!({
                            "bare_name": name,
                            "callee_file_hint": self.file_path,
                        }),
                    });
                    found_name = true;
                }

                if found_name {
                    break;
                }
            }
        }
    }

    fn get_node_name(&self, node: Node) -> Option<String> {
        let node_type = node.kind();

        if node_type == "type_spec" {
            if let Some(name_node) = node.child_by_field_name("name") {
                return std::str::from_utf8(self.source.get(name_node.byte_range())?)
                    .ok()
                    .map(String::from);
            }
        }

        if node_type == "import_from_statement" {
            if let Some(module_node) = node.child_by_field_name("module_name") {
                return std::str::from_utf8(self.source.get(module_node.byte_range())?)
                    .ok()
                    .map(String::from);
            }
        }

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "identifier"
                || child.kind() == "type_identifier"
                || child.kind() == "property_identifier"
                || child.kind() == "field_identifier"
            {
                return std::str::from_utf8(self.source.get(child.byte_range())?)
                    .ok()
                    .map(String::from);
            }
        }
        None
    }

    fn get_import_sources(&self, node: Node, node_type: &str) -> Vec<String> {
        let mut sources = Vec::new();

        // Python: from X import Y
        if node_type == "import_from_statement" {
            if let Some(module_node) = node.child_by_field_name("module_name") {
                if let Some(bytes) = self.source.get(module_node.byte_range()) {
                    if let Ok(s) = std::str::from_utf8(bytes) {
                        sources.push(s.to_string());
                    }
                }
            }
            return sources;
        }

        // Python: import X
        if node_type == "import_statement" {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if child.kind() == "dotted_name" || child.kind() == "identifier" {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            sources.push(s.to_string());
                        }
                    }
                    return sources;
                }
            }
            return sources;
        }

        // Rust: use X::Y
        if node_type == "use_declaration" {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if child.kind() == "identifier"
                    || child.kind() == "scoped_identifier"
                    || child.kind() == "dotted_identifier"
                {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            sources.push(s.to_string());
                        }
                    }
                    return sources;
                }
            }
        }

        // Go and JS/TS: walk all children to find string literals and import_specifiers
        let mut stack = vec![node];
        while let Some(current) = stack.pop() {
            let mut cursor = current.walk();
            for child in current.children(&mut cursor) {
                match child.kind() {
                    "interpreted_string_literal" | "raw_string_literal" | "string" => {
                        if let Some(bytes) = self.source.get(child.byte_range()) {
                            if let Ok(s) = std::str::from_utf8(bytes) {
                                let trimmed = s.trim_matches('"').trim_matches('`').to_string();
                                if !trimmed.is_empty() {
                                    sources.push(trimmed);
                                }
                            }
                        }
                    }
                    "import_specifier" => {
                        if let Some(name_node) = child.child_by_field_name("name") {
                            if let Some(bytes) = self.source.get(name_node.byte_range()) {
                                if let Ok(s) = std::str::from_utf8(bytes) {
                                    sources.push(s.to_string());
                                }
                            }
                        }
                    }
                    _ => {
                        if child.child_count() > 0 {
                            stack.push(child);
                        }
                    }
                }
            }
        }
        sources
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tree_sitter::Parser;

    fn parse_go(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_go::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_python(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_python::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_typescript(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    #[test]
    fn test_extractor_new() {
        let source = b"func foo() {}";
        let extractor = EntityExtractor::new(source, "test.go", "go");
        assert_eq!(extractor.language, "go");
    }

    #[test]
    fn test_extract_go_function() {
        let source = b"package main\nfunc add(a int, b int) int { return a + b }";
        if let Some(tree) = parse_go(source) {
            let extractor = EntityExtractor::new(source, "pkg/math.go", "go");
            let (elements, _) = extractor.extract(&tree);
            assert!(!elements.is_empty());
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(!funcs.is_empty());
            assert_eq!(funcs[0].name, "add");
        }
    }

    #[test]
    fn test_extract_go_struct() {
        let source = b"package main\ntype Person struct { name string }";
        if let Some(tree) = parse_go(source) {
            let extractor = EntityExtractor::new(source, "pkg/person.go", "go");
            let (elements, _) = extractor.extract(&tree);
            let structs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "struct")
                .collect();
            assert!(!structs.is_empty());
            assert_eq!(structs[0].name, "Person");
        }
    }

    #[test]
    fn test_extract_go_interface() {
        let source = b"package main\ntype Reader interface { Read(p []byte) }";
        if let Some(tree) = parse_go(source) {
            let extractor = EntityExtractor::new(source, "pkg/io.go", "go");
            let (elements, _) = extractor.extract(&tree);
            let interfaces: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "interface")
                .collect();
            assert!(!interfaces.is_empty());
            assert_eq!(interfaces[0].name, "Reader");
        }
    }

    #[test]
    fn test_extract_python_function() {
        let source = b"def greet(name):\n    return f'Hello {name}'";
        if let Some(tree) = parse_python(source) {
            let extractor = EntityExtractor::new(source, "main.py", "python");
            let (elements, _) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(!funcs.is_empty());
            assert_eq!(funcs[0].name, "greet");
        }
    }

    #[test]
    fn test_extract_python_class() {
        let source = b"class MyClass:\n    def __init__(self):\n        pass";
        if let Some(tree) = parse_python(source) {
            let extractor = EntityExtractor::new(source, "main.py", "python");
            let (elements, _) = extractor.extract(&tree);
            let classes: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert!(!classes.is_empty());
            assert_eq!(classes[0].name, "MyClass");
        }
    }

    #[test]
    fn test_extract_python_decorator() {
        let source = b"@pytest.fixture\ndef my_fixture():\n    pass";
        if let Some(tree) = parse_python(source) {
            let extractor = EntityExtractor::new(source, "conftest.py", "python");
            let (elements, _) = extractor.extract(&tree);
            let decorators: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "decorator")
                .collect();
            assert!(!decorators.is_empty());
            assert_eq!(decorators[0].name, "pytest.fixture");
        }
    }

    #[test]
    fn test_extract_python_import() {
        let source = b"import os\nfrom pathlib import Path";
        if let Some(tree) = parse_python(source) {
            let extractor = EntityExtractor::new(source, "main.py", "python");
            let (_elements, relationships) = extractor.extract(&tree);
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(!imports.is_empty());
        }
    }

    #[test]
    fn test_extract_typescript_function() {
        let source = b"function greet(name: string): string { return `Hello ${name}`; }";
        if let Some(tree) = parse_typescript(source) {
            let extractor = EntityExtractor::new(source, "main.ts", "typescript");
            let (elements, _) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(!funcs.is_empty());
            assert_eq!(funcs[0].name, "greet");
        }
    }

    #[test]
    fn test_extract_typescript_class() {
        let source = b"class MyClass { private value: number; }";
        if let Some(tree) = parse_typescript(source) {
            let extractor = EntityExtractor::new(source, "main.ts", "typescript");
            let (elements, _) = extractor.extract(&tree);
            let classes: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert!(!classes.is_empty());
            assert_eq!(classes[0].name, "MyClass");
        }
    }

    #[test]
    fn test_extract_typescript_interface() {
        let source = b"interface Person { name: string; age: number; }";
        if let Some(tree) = parse_typescript(source) {
            let extractor = EntityExtractor::new(source, "types.ts", "typescript");
            let (elements, _) = extractor.extract(&tree);
            let interfaces: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "interface")
                .collect();
            assert!(!interfaces.is_empty());
            assert_eq!(interfaces[0].name, "Person");
        }
    }

    #[test]
    fn test_extract_typescript_method() {
        let source = b"class MyClass { myMethod(): void { } }";
        if let Some(tree) = parse_typescript(source) {
            let extractor = EntityExtractor::new(source, "main.ts", "typescript");
            let (elements, _) = extractor.extract(&tree);
            let methods: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function" && e.name == "myMethod")
                .collect();
            assert!(!methods.is_empty());
        }
    }

    #[test]
    fn test_extract_file_path_preserved() {
        let source = b"package p\nfunc f() {}";
        if let Some(tree) = parse_go(source) {
            let extractor = EntityExtractor::new(source, "src/pkg/f.go", "go");
            let (elements, _) = extractor.extract(&tree);
            assert!(!elements.is_empty());
            assert_eq!(elements[0].file_path, "src/pkg/f.go");
        }
    }

    #[test]
    fn test_is_test_file_go() {
        assert!(is_test_file("pkg/math_test.go"));
        assert!(is_test_file("math_test.go"));
        assert!(!is_test_file("pkg/math.go"));
        assert!(!is_test_file("pkg/math_wrong.go"));
    }

    #[test]
    fn test_is_test_file_python() {
        assert!(is_test_file("test_math.py"));
        assert!(is_test_file("math_test.py"));
        assert!(!is_test_file("math.py"));
        assert!(!is_test_file("testmath.py"));
    }

    #[test]
    fn test_is_test_file_ruby() {
        assert!(is_test_file("math_spec.rb"));
        assert!(!is_test_file("math.rb"));
    }

    #[test]
    fn test_is_test_file_typescript() {
        assert!(is_test_file("math.test.ts"));
        assert!(is_test_file("math.spec.ts"));
        assert!(is_test_file("math.test.js"));
        assert!(is_test_file("math.spec.js"));
        assert!(!is_test_file("math.ts"));
    }

    #[test]
    fn test_get_tested_file_path_go() {
        assert_eq!(
            get_tested_file_path("pkg/math_test.go"),
            Some("pkg/math.go".to_string())
        );
        assert_eq!(
            get_tested_file_path("math_test.go"),
            Some("math.go".to_string())
        );
        assert_eq!(get_tested_file_path("pkg/math.go"), None);
    }

    #[test]
    fn test_get_tested_file_path_python() {
        assert_eq!(
            get_tested_file_path("test_math.py"),
            Some("math.py".to_string())
        );
        assert_eq!(
            get_tested_file_path("math_test.py"),
            Some("math.py".to_string())
        );
        assert_eq!(get_tested_file_path("math.py"), None);
    }

    #[test]
    fn test_get_tested_file_path_ruby() {
        assert_eq!(
            get_tested_file_path("math_spec.rb"),
            Some("math.rb".to_string())
        );
        assert_eq!(get_tested_file_path("math.rb"), None);
    }

    #[test]
    fn test_get_tested_file_path_typescript() {
        assert_eq!(
            get_tested_file_path("math.test.ts"),
            Some("math.ts".to_string())
        );
        assert_eq!(
            get_tested_file_path("math.spec.ts"),
            Some("math.ts".to_string())
        );
        assert_eq!(
            get_tested_file_path("math.test.js"),
            Some("math.js".to_string())
        );
        assert_eq!(get_tested_file_path("math.ts"), None);
    }

    #[test]
    fn test_extract_creates_tested_by_relationship() {
        let source = b"package main\nfunc add(a int, b int) int { return a + b }";
        if let Some(tree) = parse_go(source) {
            let extractor = EntityExtractor::new(source, "pkg/math_test.go", "go");
            let (_elements, relationships) = extractor.extract(&tree);

            let tested_by: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "tested_by")
                .collect();
            assert_eq!(tested_by.len(), 1);
            assert_eq!(tested_by[0].source_qualified, "pkg/math.go");
            assert_eq!(tested_by[0].target_qualified, "pkg/math_test.go");
        }
    }

    #[test]
    fn test_extract_non_test_file_no_tested_by() {
        let source = b"package main\nfunc add(a int, b int) int { return a + b }";
        if let Some(tree) = parse_go(source) {
            let extractor = EntityExtractor::new(source, "pkg/math.go", "go");
            let (_elements, relationships) = extractor.extract(&tree);

            let tested_by: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "tested_by")
                .collect();
            assert!(tested_by.is_empty());
        }
    }
}
