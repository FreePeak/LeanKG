use crate::db::models::{CodeElement, Relationship};
use tree_sitter::Node;

pub struct KotlinAnnotationExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

#[derive(Debug, Clone)]
pub struct AnnotationInfo {
    pub name: String,
    pub target_element: String,
    pub target_type: String,
    pub arguments: Vec<(String, String)>,
    pub line: u32,
}

impl<'a> KotlinAnnotationExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self, tree: &tree_sitter::Tree) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();
        let mut relationships = Vec::new();
        let mut queue: Vec<(AnnotationInfo, String)> = Vec::new();

        self.visit_node(tree.root_node(), &mut queue);

        for (annotation, target_qn) in queue {
            let ann_qualified = format!(
                "{}::@{}:{}",
                self.file_path, annotation.name, annotation.line
            );

            elements.push(CodeElement {
                qualified_name: ann_qualified.clone(),
                element_type: "annotation".to_string(),
                name: annotation.name.clone(),
                file_path: self.file_path.to_string(),
                line_start: annotation.line,
                line_end: annotation.line,
                language: "kotlin".to_string(),
                parent_qualified: Some(target_qn.clone()),
                metadata: serde_json::json!({
                    "arguments": annotation.arguments,
                    "target_type": annotation.target_type,
                }),
                ..Default::default()
            });

            relationships.push(Relationship {
                id: None,
                source_qualified: ann_qualified,
                target_qualified: target_qn,
                rel_type: "annotates".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({
                    "annotation_name": annotation.name,
                    "target_type": annotation.target_type,
                }),
            });
        }

        (elements, relationships)
    }

    fn visit_node(&self, node: Node, queue: &mut Vec<(AnnotationInfo, String)>) {
        let node_type = node.kind();

        // Handle annotated_expression: grammar wraps annotations+declaration in this node
        // when there are multiple annotations or annotations on constructors
        if node_type == "annotated_expression" {
            let mut accumulated: Vec<AnnotationInfo> = Vec::new();
            self.flatten_annotated_expression(node, &mut accumulated, queue);
            return;
        }

        let target_type = match node_type {
            "class_declaration" => Some("class"),
            "function_declaration" | "function_definition" => Some("function"),
            "property_declaration" => Some("property"),
            "parameter" => Some("parameter"),
            "constructor_declaration" => Some("constructor"),
            "object_declaration" => Some("object"),
            "companion_object" => Some("companion_object"),
            "interface_declaration" => Some("interface"),
            "enum_declaration" => Some("enum"),
            _ => None,
        };

        if let Some(t_type) = target_type {
            if let Some(name) = self.get_node_name(node) {
                let target_qn = format!("{}::{}", self.file_path, name);
                let annotations = self.collect_annotations_from_modifiers(node);
                for mut ann in annotations {
                    ann.target_type = t_type.to_string();
                    queue.push((ann, target_qn.clone()));
                }
            }
        }

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            self.visit_node(child, queue);
        }
    }

    /// Recursively flatten nested annotated_expression nodes, collecting annotations
    /// and queuing them when a declaration target is found.
    fn flatten_annotated_expression(
        &self,
        node: Node,
        accumulated: &mut Vec<AnnotationInfo>,
        queue: &mut Vec<(AnnotationInfo, String)>,
    ) {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            let child_type = child.kind();

            if child_type == "annotation" || child_type == "annotation_entry" {
                if let Some(ann) = self.extract_annotation(child) {
                    accumulated.push(ann);
                }
            } else if child_type == "annotated_expression" {
                self.flatten_annotated_expression(child, accumulated, queue);
            } else {
                // This is the actual declaration or expression being annotated
                let (target_name, t_type) = self.identify_declaration_target(child);
                if let (Some(name), Some(tt)) = (target_name, t_type) {
                    let target_qn = format!("{}::{}", self.file_path, name);
                    for mut ann in accumulated.drain(..) {
                        ann.target_type = tt.to_string();
                        queue.push((ann, target_qn.clone()));
                    }
                    // Also get annotations from the declaration's own modifiers
                    let modifiers_anns = self.collect_annotations_from_modifiers(child);
                    for mut ann in modifiers_anns {
                        ann.target_type = tt.to_string();
                        queue.push((ann, target_qn.clone()));
                    }
                    // Recurse into the declaration's children
                    let mut child_cursor = child.walk();
                    for grandchild in child.children(&mut child_cursor) {
                        self.visit_node(grandchild, queue);
                    }
                } else {
                    // Unknown inner node — still recurse
                    self.visit_node(child, queue);
                }
            }
        }
    }

    /// Determine if a node is a declaration target and return (name, type).
    /// Handles both proper AST nodes and grammar fallback cases (infix_expression for
    /// annotated primary constructors in tree-sitter-kotlin-ng).
    fn identify_declaration_target<'b>(
        &self,
        node: Node<'b>,
    ) -> (Option<String>, Option<&'static str>) {
        match node.kind() {
            "class_declaration" => (self.get_node_name(node), Some("class")),
            "function_declaration" | "function_definition" => {
                (self.get_node_name(node), Some("function"))
            }
            "property_declaration" => (self.get_node_name(node), Some("property")),
            "object_declaration" => (self.get_node_name(node), Some("object")),
            "companion_object" => (self.get_node_name(node), Some("companion_object")),
            "interface_declaration" => (self.get_node_name(node), Some("interface")),
            "enum_declaration" => (self.get_node_name(node), Some("enum")),
            "constructor_declaration" => (self.get_node_name(node), Some("constructor")),
            // Grammar fallback: "@Inject constructor() {}" becomes call_expression
            "call_expression" => {
                let text = self.get_node_text(node).unwrap_or_default();
                if text.starts_with("constructor") {
                    (Some("constructor".to_string()), Some("constructor"))
                } else {
                    (None, None)
                }
            }
            // Grammar fallback: annotated primary constructors cause tree-sitter-kotlin-ng
            // to produce infix_expression("class", Name, ...) instead of class_declaration
            "infix_expression" => {
                let mut cursor = node.walk();
                let children: Vec<Node> = node.children(&mut cursor).collect();
                if children.len() >= 2 {
                    let first = self.get_node_text(children[0]).unwrap_or_default();
                    let t_type = match first.as_str() {
                        "class" => Some("class"),
                        "interface" => Some("interface"),
                        "object" => Some("object"),
                        _ => None,
                    };
                    if let Some(tt) = t_type {
                        let name = self.get_node_text(children[1]);
                        return (name, Some(tt));
                    }
                }
                (None, None)
            }
            _ => (None, None),
        }
    }

    fn collect_annotations_from_modifiers(&self, node: Node) -> Vec<AnnotationInfo> {
        let mut annotations = Vec::new();

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "modifiers" {
                let mut mod_cursor = child.walk();
                for mod_child in child.children(&mut mod_cursor) {
                    match mod_child.kind() {
                        "annotation" | "annotation_entry" => {
                            if let Some(ann) = self.extract_annotation(mod_child) {
                                annotations.push(ann);
                            }
                        }
                        "multiline_annotation" => {
                            let mut ann_cursor = mod_child.walk();
                            for ann_child in mod_child.children(&mut ann_cursor) {
                                if ann_child.kind() == "annotation"
                                    || ann_child.kind() == "annotation_entry"
                                {
                                    if let Some(ann) = self.extract_annotation(ann_child) {
                                        annotations.push(ann);
                                    }
                                }
                            }
                        }
                        _ => {}
                    }
                }
            }
        }

        annotations
    }

    fn extract_annotation(&self, node: Node) -> Option<AnnotationInfo> {
        let line = node.start_position().row as u32 + 1;
        let name = self.get_annotation_name(node)?;
        let arguments = self.get_annotation_arguments(node);

        Some(AnnotationInfo {
            name,
            target_element: String::new(),
            target_type: String::new(),
            arguments,
            line,
        })
    }

    fn get_annotation_name(&self, node: Node) -> Option<String> {
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            match child.kind() {
                "identifier" | "type_identifier" | "simple_identifier" => {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            return Some(s.to_string());
                        }
                    }
                }
                "user_type" | "constructor_invocation" => {
                    return self.get_annotation_name(child);
                }
                _ => {}
            }
        }

        None
    }

    fn get_annotation_arguments(&self, node: Node) -> Vec<(String, String)> {
        let mut args = Vec::new();

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "value_arguments" || child.kind() == "annotation_arguments" {
                let mut arg_cursor = child.walk();
                for arg in child.children(&mut arg_cursor) {
                    if arg.kind() == "value_argument" || arg.kind() == "annotation_argument" {
                        let (key, value) = self.parse_argument(arg);
                        args.push((key, value));
                    }
                }
            }
            // Handle constructor_invocation wrapping value_arguments
            if child.kind() == "constructor_invocation" {
                let mut ci_cursor = child.walk();
                for ci_child in child.children(&mut ci_cursor) {
                    if ci_child.kind() == "value_arguments" {
                        let mut arg_cursor = ci_child.walk();
                        for arg in ci_child.children(&mut arg_cursor) {
                            if arg.kind() == "value_argument" {
                                let (key, value) = self.parse_argument(arg);
                                args.push((key, value));
                            }
                        }
                    }
                }
            }
        }

        args
    }

    fn parse_argument(&self, node: Node) -> (String, String) {
        let mut key = "value".to_string();
        let mut value = String::new();

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "identifier" | "simple_identifier" => {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            key = s.to_string();
                        }
                    }
                }
                "string_literal" | "string_content" => {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            value = s.to_string();
                        }
                    }
                }
                "integer_literal" | "real_literal" | "boolean_literal" => {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            value = s.to_string();
                        }
                    }
                }
                _ => {
                    if value.is_empty() {
                        if let Some(bytes) = self.source.get(child.byte_range()) {
                            if let Ok(s) = std::str::from_utf8(bytes) {
                                if !s.trim().is_empty() && !s.contains('(') {
                                    value = s.trim().to_string();
                                }
                            }
                        }
                    }
                }
            }
        }

        (key, value)
    }

    fn get_node_text(&self, node: Node) -> Option<String> {
        self.source
            .get(node.byte_range())
            .and_then(|b| std::str::from_utf8(b).ok())
            .map(|s| s.to_string())
    }

    fn get_node_name(&self, node: Node) -> Option<String> {
        if let Some(name_node) = node.child_by_field_name("name") {
            if let Some(bytes) = self.source.get(name_node.byte_range()) {
                if let Ok(s) = std::str::from_utf8(bytes) {
                    return Some(s.to_string());
                }
            }
        }

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if matches!(
                child.kind(),
                "identifier" | "type_identifier" | "simple_identifier"
            ) {
                if let Some(bytes) = self.source.get(child.byte_range()) {
                    if let Ok(s) = std::str::from_utf8(bytes) {
                        return Some(s.to_string());
                    }
                }
            }
        }

        None
    }
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
    fn test_extract_class_annotation() {
        let source = r#"
            @Entity(tableName = "channels")
            data class ChannelEntity(val id: Long)
        "#;
        let tree = parse_kotlin(source);
        let extractor = KotlinAnnotationExtractor::new(source.as_bytes(), "./test.kt");
        let (elements, _relationships) = extractor.extract(&tree);

        assert!(!elements.is_empty(), "Should extract annotation elements");
        let entity_ann = elements.iter().find(|e| e.name == "Entity");
        assert!(entity_ann.is_some(), "Should find @Entity annotation");

        let ann = entity_ann.unwrap();
        assert_eq!(ann.element_type, "annotation");

        let args = ann.metadata.get("arguments").unwrap();
        assert!(
            args.to_string().contains("tableName"),
            "Should capture tableName argument"
        );
    }

    #[test]
    fn test_extract_function_annotation() {
        let source = r#"
            @Composable
            fun MyScreen() {
                Text("Hello")
            }
        "#;
        let tree = parse_kotlin(source);
        let extractor = KotlinAnnotationExtractor::new(source.as_bytes(), "./test.kt");
        let (elements, _relationships) = extractor.extract(&tree);

        let composable_ann = elements.iter().find(|e| e.name == "Composable");
        assert!(
            composable_ann.is_some(),
            "Should find @Composable annotation"
        );
    }

    #[test]
    fn test_extract_multiple_annotations() {
        let source = r#"
            @HiltViewModel
            @Singleton
            class MyViewModel @Inject constructor() {}
        "#;
        let tree = parse_kotlin(source);
        let extractor = KotlinAnnotationExtractor::new(source.as_bytes(), "./test.kt");
        let (elements, _) = extractor.extract(&tree);

        assert!(elements.iter().any(|e| e.name == "HiltViewModel"));
        assert!(elements.iter().any(|e| e.name == "Singleton"));
        assert!(elements.iter().any(|e| e.name == "Inject"));
    }

    #[test]
    fn test_annotates_relationship() {
        let source = r#"
            @Dao
            interface ChannelDao {
                @Query("SELECT * FROM channels")
                fun getAll(): List<Channel>
            }
        "#;
        let tree = parse_kotlin(source);
        let extractor = KotlinAnnotationExtractor::new(source.as_bytes(), "./test.kt");
        let (_, relationships) = extractor.extract(&tree);

        let dao_rel = relationships.iter().find(|r| r.rel_type == "annotates");
        assert!(dao_rel.is_some(), "Should create annotates relationship");
        assert!(dao_rel.unwrap().source_qualified.contains("@Dao"));
    }
}
