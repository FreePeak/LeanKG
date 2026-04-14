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
        "rs" => {
            file_name.ends_with("_test.rs") || path.components().any(|c| c.as_os_str() == "tests")
        }
        "ts" | "js" => {
            file_name.ends_with(".test.ts")
                || file_name.ends_with(".test.js")
                || file_name.ends_with(".spec.ts")
                || file_name.ends_with(".spec.js")
        }
        "java" => {
            file_name.ends_with("Test.java")
                || file_name.ends_with("Tests.java")
                || path.components().any(|c| c.as_os_str() == "test")
        }
        "kt" | "kts" => {
            file_name.ends_with("Test.kt")
                || file_name.ends_with("Tests.kt")
                || file_name.ends_with("Test.kts")
                || path.components().any(|c| c.as_os_str() == "test")
        }
        _ => false,
    }
}

pub fn is_noise_call(name: &str) -> bool {
    matches!(
        name,
        // ── Rust stdlib / common patterns ──
        "println" | "print" | "eprintln" | "format" | "vec"
            | "assert" | "assert_eq" | "assert_ne" | "panic"
            | "unwrap" | "expect" | "clone" | "to_string"
            | "into" | "from" | "len" | "is_empty"
            | "ok" | "err" | "map" | "and_then" | "or_else"
            | "collect" | "iter" | "push" | "pop" | "insert"
            | "get" | "contains" | "drop" | "take" | "skip"
            | "next" | "filter" | "fold" | "Some" | "None"
            | "Ok" | "Err" | "async" | "await" | "new"
            | "with_capacity" | "with_len"
            // ── JavaScript / TypeScript ──
            | "log" | "warn" | "error" | "info" | "debug"         // console methods
            | "keys" | "values" | "entries" | "assign" | "freeze" // Object methods
            | "isArray"                                            // Array methods
            | "stringify"                                          // JSON.stringify
            | "toString" | "valueOf" | "hasOwnProperty"
            | "addEventListener" | "removeEventListener"
            | "setTimeout" | "setInterval" | "clearTimeout" | "clearInterval"
            | "require"
            | "preventDefault" | "stopPropagation"
            // ── Python builtins ──
            | "range" | "enumerate" | "zip" | "sorted" | "reversed"
            | "isinstance" | "issubclass" | "type" | "super"
            | "str" | "int" | "float" | "bool" | "list" | "dict" | "set" | "tuple"
            | "append" | "extend" | "remove" | "join" | "split" | "strip"
            | "startswith" | "endswith" | "replace" | "lower" | "upper"
            // ── Go stdlib / logging ──
            | "Println" | "Printf" | "Sprintf" | "Errorf" | "Fprintf"
            | "Fatal" | "Fatalf" | "Log" | "Logf"
            | "Info" | "Infof" | "Infow" | "Infoln"
            | "Debug" | "Debugf" | "Debugw" | "Debugln"
            | "Warn" | "Warnf" | "Warnw" | "Warnln"
            | "Error" | "Errorw" | "Errorln"
            | "DPanic" | "DPanicf" | "DPanicw"
            | "With" | "WithField" | "WithFields" | "WithError"
            | "make" | "cap" | "close"
            // ── Java stdlib / common patterns ──
            | "charAt" | "compareTo" | "indexOf" | "isEmpty"
            | "length" | "substring" | "toCharArray" | "toLowerCase" | "toUpperCase" | "trim"
            | "add" | "addAll" | "clear" | "containsKey" | "containsValue"
            | "entrySet" | "keySet" | "put" | "putAll" | "size" | "stream"
            | "of" | "ofNullable" | "isPresent" | "ifPresent" | "orElse" | "orElseGet"
            | "getClass" | "notify" | "notifyAll" | "wait"
            // ── Kotlin stdlib / common patterns ──
            | "let" | "run" | "apply" | "also"
            | "listOf" | "setOf" | "mapOf" | "mutableListOf" | "mutableSetOf" | "mutableMapOf"
            | "arrayOf" | "emptyList" | "emptySet" | "emptyMap"
            | "requireNotNull" | "checkNotNull"
            | "TODO" | "lazy"
            // Android logger mappings
            | "v" | "d" | "i" | "w" | "e" | "wtf"
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
        "rs" => {
            if file_name.ends_with("_test.rs") {
                Some(file_name.trim_end_matches("_test.rs").to_string() + ".rs")
            } else {
                None
            }
        }
        "java" => {
            if file_name.ends_with("Test.java") {
                Some(file_name.trim_end_matches("Test.java").to_string() + ".java")
            } else if file_name.ends_with("Tests.java") {
                Some(file_name.trim_end_matches("Tests.java").to_string() + ".java")
            } else {
                None
            }
        }
        "kt" | "kts" => {
            if file_name.ends_with("Test.kt") {
                Some(file_name.trim_end_matches("Test.kt").to_string() + ".kt")
            } else if file_name.ends_with("Tests.kt") {
                Some(file_name.trim_end_matches("Tests.kt").to_string() + ".kt")
            } else if file_name.ends_with("Test.kts") {
                Some(file_name.trim_end_matches("Test.kts").to_string() + ".kts")
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
                    confidence: 1.0,
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
            | "method_definition"
            | "constructor_declaration"
            | "secondary_constructor" => {
                self.extract_function(node, parent, elements, relationships);
            }
            "class_declaration" | "type_declaration" | "class_def" | "struct_item"
            | "class_definition" | "enum_declaration" | "record_declaration"
            | "object_declaration" | "companion_object" => {
                self.extract_class(node, parent, elements, relationships);
            }
            "decorated_definition" => {
                self.extract_decorated_definition(node, parent, elements, relationships);
            }
            "type_spec" => {
                self.extract_type_spec(node, parent, elements, relationships);
            }
            "interface_declaration" | "protocol_declaration" => {
                self.extract_interface(node, parent, elements, relationships);
            }
            "property_declaration" | "field_declaration" | "public_field_definition" => {
                self.extract_property(node, parent, elements, relationships);
            }
            "import_declaration"
            | "import"
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
                        confidence: 1.0,
                        metadata: serde_json::json!({}),
                    });
                }
            }
            "call_expression" | "method_invocation" => {
                self.extract_call(node, parent, elements, relationships);
            }
            "decorator"
            | "decorator_definition"
            | "marker_annotation"
            | "annotation"
            | "annotation_entry" => {
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
                        | "enum_declaration"
                        | "record_declaration"
                        | "constructor_declaration"
                        | "secondary_constructor"
                        | "object_declaration"
                        | "companion_object"
                        | "interface_declaration"
                ) {
                    self.get_node_name(node)
                } else {
                    parent.map(String::from)
                };
                self.visit_node(child, current_parent.as_deref(), elements, relationships);
            }
        }
    }

    fn extract_function(
        &self,
        node: Node,
        parent: Option<&str>,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let is_constructor = matches!(
            node.kind(),
            "constructor_declaration" | "secondary_constructor"
        );
        let name = if is_constructor {
            self.get_node_name(node)
                .or_else(|| parent.map(String::from))
        } else {
            self.get_node_name(node)
        };

        let element_type = if is_constructor
            || name.as_deref() == Some("__init__")
            || name.as_deref() == Some("constructor")
        {
            "constructor"
        } else if parent.is_some() {
            "method"
        } else {
            "function"
        };

        if let Some(name) = name {
            let qualified_name = format!("{}::{}", self.file_path, name);
            let (signature, sig_end) = self.extract_function_signature(node);
            elements.push(CodeElement {
                qualified_name: qualified_name.clone(),
                element_type: element_type.to_string(),
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
                ..Default::default()
            });

            if let Some(p) = parent {
                let p_qualified = format!("{}::{}", self.file_path, p);
                relationships.push(Relationship {
                    id: None,
                    source_qualified: p_qualified,
                    target_qualified: qualified_name.clone(),
                    rel_type: "contains".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });

                if element_type == "constructor" {
                    self.extract_constructor_fields(node, p, elements, relationships);
                }
            } else {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name.clone(),
                    rel_type: "contains".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            }
        }
    }

    fn extract_constructor_fields(
        &self,
        node: Node,
        class_name: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let mut stack = vec![node];
        while let Some(current) = stack.pop() {
            let kind = current.kind();

            if kind == "assignment_expression"
                || kind == "assignment_statement"
                || kind == "assignment"
            {
                if let Some(left) = current.child_by_field_name("left") {
                    self.process_assignment_target(left, class_name, elements, relationships);
                }
            } else if kind == "expression_statement" {
                let mut cursor = current.walk();
                for child in current.children(&mut cursor) {
                    if child.kind() == "assignment_expression" {
                        if let Some(left) = child.child_by_field_name("left") {
                            self.process_assignment_target(
                                left,
                                class_name,
                                elements,
                                relationships,
                            );
                        }
                    }
                }
            }

            let mut cursor = current.walk();
            for child in current.children(&mut cursor) {
                if child.child_count() > 0 {
                    stack.push(child);
                }
            }
        }
    }

    fn process_assignment_target(
        &self,
        left_node: Node,
        class_name: &str,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        let kind = left_node.kind();
        if kind == "member_expression"
            || kind == "attribute"
            || kind == "field_expression"
            || kind == "selector_expression"
        {
            let mut cursor = left_node.walk();
            let mut is_self = false;
            let mut field_name = None;

            for child in left_node.children(&mut cursor) {
                if let Some(bytes) = self.source.get(child.byte_range()) {
                    if let Ok(text) = std::str::from_utf8(bytes) {
                        let inner_kind = child.kind();
                        if inner_kind == "identifier"
                            || inner_kind == "this"
                            || inner_kind == "self"
                        {
                            if text == "this" || text == "self" || text == "cls" {
                                is_self = true;
                            }
                        } else if inner_kind == "property_identifier"
                            || inner_kind == "field_identifier"
                            || inner_kind == "identifier"
                        {
                            field_name = Some(text.to_string());
                        }
                    }
                }
            }

            if is_self {
                if let Some(f_name) = field_name {
                    let qualified_name = format!("{}::{}::{}", self.file_path, class_name, f_name);

                    let already_exists =
                        elements.iter().any(|e| e.qualified_name == qualified_name);

                    if !already_exists {
                        elements.push(CodeElement {
                            qualified_name: qualified_name.clone(),
                            element_type: "property".to_string(),
                            name: f_name.clone(),
                            file_path: self.file_path.to_string(),
                            line_start: left_node.start_position().row as u32 + 1,
                            line_end: left_node.end_position().row as u32 + 1,
                            language: self.language.to_string(),
                            parent_qualified: Some(class_name.to_string()),
                            metadata: serde_json::json!({"inferred_from_constructor": true}),
                            ..Default::default()
                        });

                        relationships.push(Relationship {
                            id: None,
                            source_qualified: format!("{}::{}", self.file_path, class_name),
                            target_qualified: qualified_name,
                            rel_type: "has_property".to_string(),
                            confidence: 1.0,
                            metadata: serde_json::json!({}),
                        });
                    }
                }
            }
        }
    }

    fn extract_property(
        &self,
        node: Node,
        parent: Option<&str>,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        if let Some(name) = self.get_node_name(node) {
            let qualified_name = format!("{}::{}", self.file_path, name);
            elements.push(CodeElement {
                qualified_name: qualified_name.clone(),
                element_type: "property".to_string(),
                name,
                file_path: self.file_path.to_string(),
                line_start: node.start_position().row as u32 + 1,
                line_end: node.end_position().row as u32 + 1,
                language: self.language.to_string(),
                parent_qualified: parent.map(String::from),
                metadata: serde_json::json!({}),
                ..Default::default()
            });

            if let Some(p) = parent {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: format!("{}::{}", self.file_path, p),
                    target_qualified: qualified_name.clone(),
                    rel_type: "has_property".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            }
        }
    }

    fn extract_class(
        &self,
        node: Node,
        parent: Option<&str>,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        if let Some(name) = self.get_node_name(node) {
            let element_type = if node.kind() == "enum_declaration" {
                "enum"
            } else if node.kind() == "record_declaration" {
                "record"
            } else {
                "class"
            };

            let qualified_name = format!("{}::{}", self.file_path, name);

            if let Some(p) = parent {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: format!("{}::{}", self.file_path, p),
                    target_qualified: qualified_name.clone(),
                    rel_type: "contains".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            } else {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name.clone(),
                    rel_type: "contains".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            }

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
                ..Default::default()
            });

            self.extract_class_heritage(node, &qualified_name, relationships);
        }
    }

    fn extract_class_heritage(
        &self,
        node: Node,
        class_qualified: &str,
        relationships: &mut Vec<Relationship>,
    ) {
        let mut cursor = node.walk();
        let mut delegation_index = 0usize;
        for child in node.children(&mut cursor) {
            let kind = child.kind();
            if kind == "class_heritage"
                || kind == "superclass"
                || kind == "super_interfaces"
                || kind == "extends_clause"
                || kind == "implements_clause"
                || kind == "argument_list"
            {
                self.extract_heritage_types(
                    child,
                    class_qualified,
                    kind == "implements_clause" || kind == "super_interfaces",
                    relationships,
                );
            }
            // Kotlin: class AdminUser : User, Authenticatable
            // AST: (class_declaration (delegation_specifiers (delegation_specifier (user_type (identifier)))))
            // delegation_specifiers is the wrapper, delegation_specifier is the child
            if kind == "delegation_specifiers" {
                let mut inner_cursor = child.walk();
                for spec_child in child.children(&mut inner_cursor) {
                    if spec_child.kind() == "delegation_specifier" {
                        let is_first = delegation_index == 0;
                        delegation_index += 1;
                        self.extract_heritage_types(
                            spec_child,
                            class_qualified,
                            !is_first, // first is extends, rest are implements
                            relationships,
                        );
                    }
                }
            }
            // Also handle direct delegation_specifier child (some Kotlin versions)
            if kind == "delegation_specifier" {
                let is_first = delegation_index == 0;
                delegation_index += 1;
                self.extract_heritage_types(
                    child,
                    class_qualified,
                    !is_first, // first is extends, rest are implements
                    relationships,
                );
            }
        }
    }

    fn extract_heritage_types(
        &self,
        node: Node,
        source_qualified: &str,
        is_implements: bool,
        relationships: &mut Vec<Relationship>,
    ) {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            let kind = child.kind();
            if kind == "identifier" || kind == "type_identifier" {
                if let Some(bytes) = self.source.get(child.byte_range()) {
                    if let Ok(target_name) = std::str::from_utf8(bytes) {
                        relationships.push(Relationship {
                            id: None,
                            source_qualified: source_qualified.to_string(),
                            target_qualified: format!("__unresolved__{}", target_name),
                            rel_type: if is_implements {
                                "implements".to_string()
                            } else {
                                "extends".to_string()
                            },
                            confidence: 0.8,
                            metadata: serde_json::json!({ "heritage_name": target_name }),
                        });
                    }
                }
            } else {
                self.extract_heritage_types(
                    child,
                    source_qualified,
                    kind == "implements_clause" || is_implements,
                    relationships,
                );
            }
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
                ..Default::default()
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
                            confidence: 1.0,
                            metadata: serde_json::json!({"embedded": true}),
                        });
                    }
                }
            }
        }
    }

    fn extract_interface(
        &self,
        node: Node,
        parent: Option<&str>,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        if let Some(name) = self.get_node_name(node) {
            let qualified_name = format!("{}::{}", self.file_path, name);
            if let Some(p) = parent {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: format!("{}::{}", self.file_path, p),
                    target_qualified: qualified_name.clone(),
                    rel_type: "contains".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            } else {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name.clone(),
                    rel_type: "contains".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            }

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
                ..Default::default()
            });

            self.extract_class_heritage(node, &qualified_name, relationships);
        }
    }

    fn extract_decorator(&self, node: Node, parent: Option<&str>, elements: &mut Vec<CodeElement>) {
        self.extract_decorator_impl(node, parent, elements, &mut Vec::new())
    }

    fn extract_decorator_impl(
        &self,
        node: Node,
        parent: Option<&str>,
        elements: &mut Vec<CodeElement>,
        mut visited: &mut Vec<usize>,
    ) {
        // Avoid infinite recursion
        let node_ptr = node.id() as usize;
        if visited.contains(&node_ptr) {
            return;
        }
        visited.push(node_ptr);

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "identifier" | "dotted_name" | "simple_identifier" => {
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
                                ..Default::default()
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
                                ..Default::default()
                            });
                        }
                    }
                    return;
                }
                // Kotlin: annotation (constructor_invocation (user_type (identifier)) ...)
                // Kotlin: annotation (user_type (identifier))
                "constructor_invocation" | "user_type" => {
                    self.extract_decorator_impl(child, parent, elements, &mut visited);
                }
                _ => {}
            }
        }
    }

    fn extract_decorated_definition(
        &self,
        node: Node,
        parent: Option<&str>,
        elements: &mut Vec<CodeElement>,
        _relationships: &mut Vec<Relationship>,
    ) {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "decorator" => {
                    self.extract_decorator(child, parent, elements);
                }
                "function_definition" | "function_declaration" => {
                    self.extract_function(child, parent, elements, _relationships);
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
            if kind == "field_expression"
                || kind == "identifier"
                || kind == "scoped_identifier"
                || kind == "selector_expression"
                || kind == "type_identifier"
            {
                let mut found_name = false;
                let mut name_to_use: Option<String> = None;

                let mut last_identifier_name: Option<String> = None;
                let mut first_identifier_name: Option<String> = None;
                let mut is_method_call = false;

                // Handle selector_expression specially (Go: fmt.Println)
                if kind == "selector_expression" {
                    is_method_call = true;
                    let mut field_cursor = child.walk();
                    for inner in child.children(&mut field_cursor) {
                        let inner_kind = inner.kind();
                        if inner_kind == "field_identifier" {
                            if let Some(bytes) = self.source.get(inner.byte_range()) {
                                if let Ok(name) = std::str::from_utf8(bytes) {
                                    last_identifier_name = Some(name.to_string());
                                }
                            }
                        } else if inner_kind == "identifier" || inner_kind == "type_identifier" {
                            if let Some(bytes) = self.source.get(inner.byte_range()) {
                                if let Ok(name) = std::str::from_utf8(bytes) {
                                    if first_identifier_name.is_none() {
                                        first_identifier_name = Some(name.to_string());
                                    }
                                }
                            }
                        }
                    }
                    if let Some(name) = last_identifier_name {
                        if !is_noise_call(&name) {
                            name_to_use = Some(name);
                        }
                    }
                } else {
                    // For scoped_identifier like `Arc::new`, we want the LAST identifier (the function name)
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

                    // For scoped_identifier, field_expression, and identifier, use the last identifier (function/method name)
                    if kind == "scoped_identifier" || kind == "field_expression" {
                        if let Some(name) = last_identifier_name {
                            if !is_noise_call(&name) {
                                name_to_use = Some(name);
                            }
                        }
                    } else if kind == "identifier" || kind == "type_identifier" {
                        // For simple identifier, use it directly
                        if let Some(bytes) = self.source.get(child.byte_range()) {
                            if let Ok(name) = std::str::from_utf8(bytes) {
                                if !is_noise_call(name) {
                                    name_to_use = Some(name.to_string());
                                }
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
                        confidence: 0.5,
                        metadata: serde_json::json!({
                            "bare_name": name,
                            "callee_file_hint": self.file_path,
                            "is_method_call": is_method_call,
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

        // Java/C-style nodes have a 'name' field — use it to avoid
        // picking up the return-type identifier instead of the method name.
        if matches!(
            node_type,
            "method_declaration"
                | "constructor_declaration"
                | "secondary_constructor"
                | "class_declaration"
                | "interface_declaration"
                | "enum_declaration"
                | "record_declaration"
                | "object_declaration"
                | "companion_object"
        ) {
            if let Some(name_node) = node.child_by_field_name("name") {
                return std::str::from_utf8(self.source.get(name_node.byte_range())?)
                    .ok()
                    .map(String::from);
            }
        }

        if node_type == "field_declaration"
            || node_type == "property_declaration"
            || node_type == "public_field_definition"
        {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if child.kind() == "variable_declarator" {
                    if let Some(name_node) = child.child_by_field_name("name") {
                        return std::str::from_utf8(self.source.get(name_node.byte_range())?)
                            .ok()
                            .map(String::from);
                    }
                    let mut inner_cursor = child.walk();
                    for inner in child.children(&mut inner_cursor) {
                        if inner.kind() == "identifier" {
                            return std::str::from_utf8(self.source.get(inner.byte_range())?)
                                .ok()
                                .map(String::from);
                        }
                    }
                } else if child.kind() == "property_identifier"
                    || child.kind() == "field_identifier"
                    || child.kind() == "identifier"
                {
                    return std::str::from_utf8(self.source.get(child.byte_range())?)
                        .ok()
                        .map(String::from);
                }
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

        // Java: import com.example.Foo
        if node_type == "import_declaration" && self.language == "java" {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if child.kind() == "scoped_identifier" {
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

        // Kotlin: import com.example.Foo
        // Kotlin AST uses "import" node with "qualified_identifier" containing multiple "identifier" children
        if node_type == "import" && self.language == "kotlin" {
            let mut parts = Vec::new();
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if child.kind() == "qualified_identifier" {
                    let mut inner_cursor = child.walk();
                    for inner_child in child.children(&mut inner_cursor) {
                        if inner_child.kind() == "identifier"
                            || inner_child.kind() == "simple_identifier"
                        {
                            if let Some(bytes) = self.source.get(inner_child.byte_range()) {
                                if let Ok(s) = std::str::from_utf8(bytes) {
                                    parts.push(s.to_string());
                                }
                            }
                        }
                    }
                }
            }
            if !parts.is_empty() {
                sources.push(parts.join("."));
            }
            return sources;
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

    fn parse_java(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_java::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_kotlin(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_kotlin_ng::LANGUAGE.into();
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
                .filter(|e| e.element_type == "method" && e.name == "myMethod")
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
    fn test_get_tested_file_path_rust() {
        assert_eq!(
            get_tested_file_path("math_test.rs"),
            Some("math.rs".to_string())
        );
        assert_eq!(
            get_tested_file_path("pkg/math_test.rs"),
            Some("pkg/math.rs".to_string())
        );
        assert_eq!(get_tested_file_path("math.rs"), None);
    }

    #[test]
    fn test_is_test_file_rust() {
        assert!(is_test_file("math_test.rs"));
        assert!(is_test_file("pkg/math_test.rs"));
        assert!(is_test_file("tests/integration_test.rs"));
        assert!(is_test_file("src/tests/whatever_test.rs"));
        assert!(!is_test_file("math.rs"));
        assert!(!is_test_file("lib.rs"));
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

    // ── Noise call filter tests per language ──

    #[test]
    fn test_is_noise_call_rust() {
        assert!(is_noise_call("println"));
        assert!(is_noise_call("unwrap"));
        assert!(is_noise_call("clone"));
        assert!(is_noise_call("new"));
        assert!(!is_noise_call("calculate_total"));
        assert!(!is_noise_call("validate_input"));
    }

    #[test]
    fn test_is_noise_call_javascript() {
        assert!(is_noise_call("log"));
        assert!(is_noise_call("warn"));
        assert!(is_noise_call("stringify"));
        assert!(is_noise_call("addEventListener"));
        assert!(is_noise_call("require"));
        assert!(is_noise_call("setTimeout"));
        assert!(!is_noise_call("fetchUserData"));
        assert!(!is_noise_call("renderComponent"));
    }

    #[test]
    fn test_is_noise_call_python() {
        assert!(is_noise_call("range"));
        assert!(is_noise_call("enumerate"));
        assert!(is_noise_call("isinstance"));
        assert!(is_noise_call("append"));
        assert!(is_noise_call("join"));
        assert!(!is_noise_call("process_payment"));
        assert!(!is_noise_call("authenticate_user"));
    }

    #[test]
    fn test_is_noise_call_go() {
        // Standard logging
        assert!(is_noise_call("Println"));
        assert!(is_noise_call("Printf"));
        assert!(is_noise_call("Fatal"));
        assert!(is_noise_call("make"));
        // Structured logging (zap/logrus style)
        assert!(is_noise_call("Info"));
        assert!(is_noise_call("Infof"));
        assert!(is_noise_call("Infow"));
        assert!(is_noise_call("Debug"));
        assert!(is_noise_call("Debugf"));
        assert!(is_noise_call("Warn"));
        assert!(is_noise_call("Warnf"));
        assert!(is_noise_call("Error"));
        assert!(is_noise_call("Errorf"));
        assert!(is_noise_call("DPanic"));
        assert!(is_noise_call("With"));
        assert!(is_noise_call("WithField"));
        assert!(is_noise_call("WithFields"));
        assert!(is_noise_call("WithError"));
        // Legitimate Go functions should NOT be filtered
        assert!(!is_noise_call("HandleRequest"));
        assert!(!is_noise_call("ValidateToken"));
        assert!(!is_noise_call("GetUser"));
        assert!(!is_noise_call("CreateOrder"));
    }

    #[test]
    fn test_is_noise_call_conservative_no_false_positives() {
        // These names could be legitimate functions — should NOT be filtered
        assert!(!is_noise_call("parse"));
        assert!(!is_noise_call("resolve"));
        assert!(!is_noise_call("String"));
    }

    #[test]
    fn test_is_noise_call_short_names() {
        assert!(is_noise_call("a"));
        assert!(is_noise_call("x"));
        assert!(is_noise_call(""));
    }

    #[test]
    fn test_noise_calls_filtered_from_go_extraction() {
        let source =
            b"package main\nimport \"fmt\"\nfunc main() {\n\tfmt.Println(\"hello\")\n\tprocessData()\n}";
        if let Some(tree) = parse_go(source) {
            let extractor = EntityExtractor::new(source, "main.go", "go");
            let (_, relationships) = extractor.extract(&tree);
            let calls: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "calls")
                .collect();
            let call_names: Vec<&str> = calls
                .iter()
                .map(|r| {
                    r.metadata
                        .get("bare_name")
                        .and_then(|v| v.as_str())
                        .unwrap_or("")
                })
                .collect();
            assert!(
                call_names.contains(&"processData"),
                "processData should be extracted"
            );
            assert!(
                !call_names.contains(&"Println"),
                "Println should be filtered as noise"
            );
        }
    }

    #[test]
    fn test_noise_calls_filtered_python_builtins() {
        // Python call extraction uses tree-sitter `call` node (not `call_expression`),
        // so we verify noise filtering works at the is_noise_call level.
        let python_noise = vec![
            "print",
            "range",
            "enumerate",
            "isinstance",
            "append",
            "join",
            "split",
            "strip",
            "lower",
            "upper",
            "sorted",
            "reversed",
        ];
        for name in &python_noise {
            assert!(
                is_noise_call(name),
                "'{}' should be filtered as noise",
                name
            );
        }

        let python_legit = vec![
            "process_data",
            "authenticate_user",
            "validate_input",
            "calculate_total",
            "fetch_records",
        ];
        for name in &python_legit {
            assert!(!is_noise_call(name), "'{}' should NOT be filtered", name);
        }
    }

    // ── Java-specific tests ──

    #[test]
    fn test_extract_java_class() {
        let source = b"public class UserService { }";
        if let Some(tree) = parse_java(source) {
            let extractor = EntityExtractor::new(source, "com/example/UserService.java", "java");
            let (elements, _) = extractor.extract(&tree);
            let classes: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert!(!classes.is_empty(), "Should extract Java class");
            assert_eq!(classes[0].name, "UserService");
            assert_eq!(classes[0].language, "java");
        }
    }

    #[test]
    fn test_extract_java_interface() {
        let source = b"public interface Repository { void save(Object entity); }";
        if let Some(tree) = parse_java(source) {
            let extractor = EntityExtractor::new(source, "com/example/Repository.java", "java");
            let (elements, _) = extractor.extract(&tree);
            let interfaces: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "interface")
                .collect();
            assert!(!interfaces.is_empty(), "Should extract Java interface");
            assert_eq!(interfaces[0].name, "Repository");
        }
    }

    #[test]
    fn test_extract_java_method() {
        let source =
            b"public class Service { public String process(String input) { return input; } }";
        if let Some(tree) = parse_java(source) {
            let extractor = EntityExtractor::new(source, "Service.java", "java");
            let (elements, _) = extractor.extract(&tree);
            let methods: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "method" && e.name == "process")
                .collect();
            assert!(!methods.is_empty(), "Should extract Java method");
        }
    }

    #[test]
    fn test_extract_java_constructor() {
        let source = b"public class User { public User(String name) { this.name = name; } }";
        if let Some(tree) = parse_java(source) {
            let extractor = EntityExtractor::new(source, "User.java", "java");
            let (elements, _) = extractor.extract(&tree);
            let constructors: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "constructor" && e.name == "User")
                .collect();
            assert!(!constructors.is_empty(), "Should extract Java constructor");
        }
    }

    #[test]
    fn test_extract_java_enum() {
        let source = b"public enum Status { ACTIVE, INACTIVE, PENDING }";
        if let Some(tree) = parse_java(source) {
            let extractor = EntityExtractor::new(source, "Status.java", "java");
            let (elements, _) = extractor.extract(&tree);
            let enums: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "enum" && e.name == "Status")
                .collect();
            assert!(!enums.is_empty(), "Should extract Java enum");
        }
    }

    #[test]
    fn test_extract_java_import() {
        let source = b"import com.example.service.UserService;\npublic class Main { }";
        if let Some(tree) = parse_java(source) {
            let extractor = EntityExtractor::new(source, "Main.java", "java");
            let (_, relationships) = extractor.extract(&tree);
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(!imports.is_empty(), "Should extract Java import");
            assert_eq!(
                imports[0].target_qualified,
                "com.example.service.UserService"
            );
        }
    }

    #[test]
    fn test_extract_java_annotation() {
        let source =
            b"public class Service { @Override public String toString() { return \"\"; } }";
        if let Some(tree) = parse_java(source) {
            let extractor = EntityExtractor::new(source, "Service.java", "java");
            let (elements, _) = extractor.extract(&tree);
            let decorators: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "decorator")
                .collect();
            assert!(
                !decorators.is_empty(),
                "Should extract Java annotation as decorator"
            );
            assert_eq!(decorators[0].name, "Override");
        }
    }

    #[test]
    fn test_extract_java_method_invocation() {
        let source = b"public class Main { void run() { processData(); } }";
        if let Some(tree) = parse_java(source) {
            let extractor = EntityExtractor::new(source, "Main.java", "java");
            let (_, relationships) = extractor.extract(&tree);
            let calls: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "calls")
                .collect();
            let call_names: Vec<&str> = calls
                .iter()
                .map(|r| {
                    r.metadata
                        .get("bare_name")
                        .and_then(|v| v.as_str())
                        .unwrap_or("")
                })
                .collect();
            assert!(
                call_names.contains(&"processData"),
                "Should extract Java method invocation: got {:?}",
                call_names
            );
        }
    }

    #[test]
    fn test_is_test_file_java() {
        assert!(is_test_file("UserServiceTest.java"));
        assert!(is_test_file("UserServiceTests.java"));
        assert!(is_test_file("src/test/java/com/example/FooTest.java"));
        assert!(!is_test_file("UserService.java"));
        assert!(!is_test_file("TestHelper.java")); // doesn't end with Test.java
    }

    #[test]
    fn test_get_tested_file_path_java() {
        assert_eq!(
            get_tested_file_path("service/UserServiceTest.java"),
            Some("service/UserService.java".to_string())
        );
        assert_eq!(
            get_tested_file_path("UserServiceTests.java"),
            Some("UserService.java".to_string())
        );
        assert_eq!(get_tested_file_path("UserService.java"), None);
    }

    #[test]
    fn test_is_noise_call_java() {
        // Java stdlib noise
        assert!(is_noise_call("charAt"));
        assert!(is_noise_call("indexOf"));
        assert!(is_noise_call("isEmpty"));
        assert!(is_noise_call("length"));
        assert!(is_noise_call("size"));
        assert!(is_noise_call("stream"));
        assert!(is_noise_call("getClass"));
        assert!(is_noise_call("notify"));
        assert!(is_noise_call("wait"));
        assert!(is_noise_call("of"));
        // Legitimate Java functions should NOT be filtered
        assert!(!is_noise_call("processOrder"));
        assert!(!is_noise_call("findUserById"));
        assert!(!is_noise_call("validateToken"));
        assert!(!is_noise_call("createPayment"));
    }

    #[test]
    fn test_is_noise_call_kotlin() {
        assert!(is_noise_call("let"));
        assert!(is_noise_call("run"));
        assert!(is_noise_call("listOf"));
        assert!(is_noise_call("emptyMap"));
        assert!(is_noise_call("checkNotNull"));
        assert!(is_noise_call("println"));
        // Legitimate Kotlin functions should NOT be filtered
        assert!(!is_noise_call("processOrder"));
        assert!(!is_noise_call("loadUserData"));
    }

    #[test]
    fn test_noise_calls_filtered_from_java_extraction() {
        let source = b"public class Main { void run() { processData(); toString(); } }";
        if let Some(tree) = parse_java(source) {
            let extractor = EntityExtractor::new(source, "Main.java", "java");
            let (_, relationships) = extractor.extract(&tree);
            let calls: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "calls")
                .collect();
            let call_names: Vec<&str> = calls
                .iter()
                .map(|r| {
                    r.metadata
                        .get("bare_name")
                        .and_then(|v| v.as_str())
                        .unwrap_or("")
                })
                .collect();
            assert!(
                call_names.contains(&"processData"),
                "processData should be extracted"
            );
            // toString is in noise list, should be filtered
            assert!(
                !call_names.contains(&"toString"),
                "toString should be filtered as noise"
            );
        }
    }

    #[test]
    fn test_extract_java_creates_tested_by_relationship() {
        let source = b"public class UserServiceTest { void testCreate() {} }";
        if let Some(tree) = parse_java(source) {
            let extractor = EntityExtractor::new(source, "service/UserServiceTest.java", "java");
            let (_, relationships) = extractor.extract(&tree);

            let tested_by: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "tested_by")
                .collect();
            assert_eq!(tested_by.len(), 1);
            assert_eq!(tested_by[0].source_qualified, "service/UserService.java");
            assert_eq!(
                tested_by[0].target_qualified,
                "service/UserServiceTest.java"
            );
        }
    }

    #[test]
    fn test_extract_kotlin_class() {
        let source = br#"
class UserService {
    fun getUser() {}
}

object DatabaseManager {}

class Container {
    companion object {}
}
"#;
        if let Some(tree) = parse_kotlin(source) {
            let extractor = EntityExtractor::new(source, "UserService.kt", "kotlin");
            let (elements, _) = extractor.extract(&tree);

            let class_elements: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert_eq!(class_elements.len(), 3); // UserService, DatabaseManager, Container

            assert!(class_elements.iter().any(|e| e.name == "UserService"));
            assert!(class_elements.iter().any(|e| e.name == "DatabaseManager"));
            assert!(class_elements.iter().any(|e| e.name == "Container"));
        }
    }

    #[test]
    fn test_extract_kotlin_function() {
        let source = br#"
fun calculateInterest() {}

class Account(val id: String) {
    constructor() : this("")

    fun checkBalance() {}
}
"#;
        if let Some(tree) = parse_kotlin(source) {
            let extractor = EntityExtractor::new(source, "Account.kt", "kotlin");
            let (elements, _) = extractor.extract(&tree);

            let func_elements: Vec<_> = elements
                .iter()
                .filter(|e| {
                    matches!(
                        e.element_type.as_str(),
                        "function" | "method" | "constructor"
                    )
                })
                .collect();
            assert_eq!(func_elements.len(), 3);

            assert!(func_elements
                .iter()
                .any(|e| e.name == "calculateInterest" && e.element_type == "function"));
            assert!(func_elements
                .iter()
                .any(|e| e.name == "checkBalance" && e.element_type == "method"));
            assert!(func_elements
                .iter()
                .any(|e| e.name == "Account" && e.element_type == "constructor"));
        }
    }

    #[test]
    fn test_extract_kotlin_creates_tested_by_relationship() {
        let source = br#"
class UserServiceTest {
    fun testCreate() {}
}
"#;
        if let Some(tree) = parse_kotlin(source) {
            let extractor = EntityExtractor::new(source, "service/UserServiceTest.kt", "kotlin");
            let (_, relationships) = extractor.extract(&tree);

            let tested_by: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "tested_by")
                .collect();
            assert_eq!(tested_by.len(), 1);
            assert_eq!(tested_by[0].source_qualified, "service/UserService.kt");
            assert_eq!(tested_by[0].target_qualified, "service/UserServiceTest.kt");
        }
    }

    #[test]
    fn test_extract_typescript_heritage() {
        let source = b"class MyService extends BaseService implements IService, IDisposable { }";
        if let Some(tree) = parse_typescript(source) {
            let extractor = EntityExtractor::new(source, "service.ts", "typescript");
            let (_, relationships) = extractor.extract(&tree);

            let extends: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "extends")
                .collect();
            assert_eq!(extends.len(), 1);
            assert_eq!(extends[0].target_qualified, "__unresolved__BaseService");

            let implements: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "implements")
                .collect();
            assert_eq!(implements.len(), 2);
            assert!(implements
                .iter()
                .any(|r| r.target_qualified == "__unresolved__IService"));
            assert!(implements
                .iter()
                .any(|r| r.target_qualified == "__unresolved__IDisposable"));
        }
    }

    #[test]
    fn test_extract_java_properties() {
        let source = b"public class User { private String name; public int age; }";
        if let Some(tree) = parse_java(source) {
            let extractor = EntityExtractor::new(source, "User.java", "java");
            let (elements, relationships) = extractor.extract(&tree);

            let props: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "property")
                .collect();
            assert_eq!(props.len(), 2);
            assert!(props.iter().any(|e| e.name == "name"));
            assert!(props.iter().any(|e| e.name == "age"));

            let has_prop: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "has_property")
                .collect();
            assert_eq!(has_prop.len(), 2);
            assert!(has_prop
                .iter()
                .any(|r| r.source_qualified == "User.java::User"
                    && r.target_qualified == "User.java::name"));
        }
    }

    #[test]
    fn test_extract_typescript_has_method_and_property() {
        let source = b"class User { name: string; constructor() {} getName(): string { return this.name; } }";
        if let Some(tree) = parse_typescript(source) {
            let extractor = EntityExtractor::new(source, "User.ts", "typescript");
            let (_, relationships) = extractor.extract(&tree);

            // TS now unifies method relationships to 'contains'
            let has_method: Vec<_> = relationships
                .iter()
                .filter(|r| {
                    r.rel_type == "contains"
                        && (r.target_qualified.ends_with("::constructor")
                            || r.target_qualified.ends_with("::getName"))
                })
                .collect();
            assert_eq!(has_method.len(), 2); // constructor and getName

            let has_prop: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "has_property")
                .collect();
            assert_eq!(has_prop.len(), 1);
        }
    }

    // ── Kotlin-specific tests ──

    #[test]
    fn test_extract_kotlin_import() {
        let source = b"import com.example.service.UserService\n\nclass Main { }";
        if let Some(tree) = parse_kotlin(source) {
            let extractor = EntityExtractor::new(source, "Main.kt", "kotlin");
            let (_, relationships) = extractor.extract(&tree);
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(!imports.is_empty(), "Should extract Kotlin import");
            assert!(
                imports
                    .iter()
                    .any(|r| r.target_qualified.contains("UserService")),
                "Import should contain UserService, got: {:?}",
                imports
                    .iter()
                    .map(|r| &r.target_qualified)
                    .collect::<Vec<_>>()
            );
        }
    }

    #[test]
    fn test_extract_kotlin_heritage() {
        let source = b"class AdminUser : User, Authenticatable { }";
        if let Some(tree) = parse_kotlin(source) {
            let extractor = EntityExtractor::new(source, "AdminUser.kt", "kotlin");
            let (_, relationships) = extractor.extract(&tree);

            let extends: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "extends")
                .collect();
            let implements: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "implements")
                .collect();

            assert!(
                !extends.is_empty() || !implements.is_empty(),
                "Should extract heritage relationships, got: {:?}",
                relationships
                    .iter()
                    .map(|r| format!("{}: {}", r.rel_type, r.target_qualified))
                    .collect::<Vec<_>>()
            );
        }
    }

    #[test]
    fn test_extract_kotlin_annotation() {
        let source = br#"
@Deprecated("Use newApi instead")
class OldService {
    @Inject
    fun process() {}
}
"#;
        if let Some(tree) = parse_kotlin(source) {
            let extractor = EntityExtractor::new(source, "OldService.kt", "kotlin");
            let (elements, _) = extractor.extract(&tree);
            let decorators: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "decorator")
                .collect();
            assert!(
                decorators
                    .iter()
                    .any(|d| d.name == "Deprecated" || d.name == "Inject"),
                "Should extract Kotlin annotations, got: {:?}",
                decorators.iter().map(|d| &d.name).collect::<Vec<_>>()
            );
        }
    }
}
