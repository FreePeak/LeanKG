use crate::db::models::{CodeElement, Relationship};
use crate::indexer::regex_cache::{KOTLIN_SYNTHETIC_IMPORT, VIEWBINDING_VAR};
use regex::Regex;
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
        "dart" => {
            file_name.ends_with("_test.dart")
                || file_name.ends_with("_widget_test.dart")
                || path.components().any(|c| c.as_os_str() == "test")
        }
        "vue" => file_name.ends_with(".spec.vue") || file_name.ends_with(".test.vue"),
        "svelte" => file_name.ends_with(".spec.svelte") || file_name.ends_with(".test.svelte"),
        "php" => file_name.ends_with("Test.php") || file_name.ends_with("_test.php"),
        "pl" | "pm" => file_name.ends_with(".t") || file_name.ends_with("_test.pl"),
        "ex" | "exs" => {
            file_name.ends_with("_test.exs") || path.components().any(|c| c.as_os_str() == "test")
        }
        "r" => file_name.ends_with("_test.R") || file_name.ends_with("test_that.R"),
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
            // ── Dart / Flutter built-ins ──
            | "setState" | "initState" | "dispose" | "build"
            | "context" | "mounted" | "widget"
            | "debugPrint"
            | "maybeOf"
            // Dart null safety & keywords
            | "late" | "required" | "abstract" | "override"
            | "extends" | "with" | "implements" | "mixin" | "extension"
            | "static" | "final" | "const" | "var"
            // Flutter test functions
            | "group" | "testWidgets" | "test" | "setUp" | "tearDown"
            | "setUpAll" | "tearDownAll"
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
        "dart" => {
            if file_name.ends_with("_test.dart") {
                Some(file_name.trim_end_matches("_test.dart").to_string() + ".dart")
            } else if file_name.ends_with("_widget_test.dart") {
                Some(file_name.trim_end_matches("_widget_test.dart").to_string() + ".dart")
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

        let source_str = std::str::from_utf8(self.source).unwrap_or("");
        for (current_row, line) in (start as u32..).zip(source_str.lines()) {
            if current_row > end_row {
                break;
            }
            if current_row == start as u32 || signature_lines.is_empty() || current_row <= end_row {
                signature_lines.push(line.to_string());
            }
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
                    ..Default::default()
                });
            }
        }

        // Phase 1: Extract HTTP routes from Go and TS/JS files
        if self.language == "go"
            || self.language == "typescript"
            || self.language == "javascript"
            || self.language == "tsx"
            || self.language == "jsx"
        {
            let routes = crate::indexer::route_extractor::RouteExtractor::extract_routes(
                self.source,
                tree,
                self.file_path,
                self.language,
            );
            let (route_elements, route_rels) =
                crate::indexer::route_extractor::RouteExtractor::routes_to_elements_and_rels(
                    &routes,
                );
            elements.extend(route_elements);
            relationships.extend(route_rels);
        }

        if self.language == "kotlin" || self.language == "java" {
            self.extract_android_bindings(&mut relationships);
        }

        // Ruby/Elixir/R imports are generic `call` nodes in their grammars, so
        // regex-scan the source for the common require/import forms (mirrors the
        // swift/objc regex-extractor pattern for grammars without import nodes).
        if self.language == "ruby"
            || self.language == "elixir"
            || self.language == "r"
            || self.language == "perl"
            || self.language == "lua"
            || self.language == "nim"
            || self.language == "crystal"
        {
            self.extract_script_imports(&mut relationships);
        }

        // Elixir's bundled grammar (v0.3.5) has no `defmodule`/`def` node types —
        // they parse as generic `call`s. Regex-extract module + function elements.
        if self.language == "elixir" {
            self.extract_elixir_definitions(&mut elements);
        }

        // MINIMAL-tier languages (json/toml/yaml/css/html/graphql/protobuf/
        // dockerfile): emit a file-level document element so the file is indexed
        // even when the grammar has no function/class node kinds.
        if elements.is_empty()
            && relationships.iter().all(|r| r.rel_type != "imports")
            && crate::indexer::lang::registry::language_spec(self.language)
                .map(|s| s.tier == crate::indexer::lang::registry::Tier::Minimal)
                .unwrap_or(false)
        {
            let file_name = std::path::Path::new(self.file_path)
                .file_name()
                .and_then(|n| n.to_str())
                .unwrap_or(self.file_path);
            elements.push(CodeElement {
                qualified_name: format!("{}::<document>", self.file_path),
                element_type: "document".to_string(),
                name: format!("{}: <document>", file_name),
                file_path: self.file_path.to_string(),
                line_start: 1,
                line_end: 1,
                language: self.language.to_string(),
                ..Default::default()
            });
        }

        (elements, relationships)
    }

    /// Elixir grammar lacks defmodule/def nodes; regex-extract them as elements.
    fn extract_elixir_definitions(&self, elements: &mut Vec<CodeElement>) {
        let content = std::str::from_utf8(self.source).unwrap_or("");
        let source_path = self.file_path.to_string();
        let module_re = match regex::Regex::new(r"^\s*defmodule\s+([\w.]+)") {
            Ok(r) => r,
            Err(_) => return,
        };
        let def_re = match regex::Regex::new(
            r"(?m)^\s*def(p|macro|macrop|guard)?\s+([a-zA-Z_]\w*)(?:\(|\s|$)",
        ) {
            Ok(r) => r,
            Err(_) => return,
        };
        for cap in module_re.captures_iter(content) {
            if let Some(name) = cap.get(1) {
                let qn = format!("{}::{}", source_path, name.as_str());
                elements.push(CodeElement {
                    qualified_name: qn,
                    element_type: "module".to_string(),
                    name: name.as_str().to_string(),
                    file_path: source_path.clone(),
                    line_start: 1,
                    line_end: 1,
                    language: "elixir".to_string(),
                    ..Default::default()
                });
            }
        }
        for cap in def_re.captures_iter(content) {
            if let Some(name) = cap.get(2) {
                let qn = format!("{}::{}", source_path, name.as_str());
                elements.push(CodeElement {
                    qualified_name: qn,
                    element_type: "function".to_string(),
                    name: name.as_str().to_string(),
                    file_path: source_path.clone(),
                    line_start: 1,
                    line_end: 1,
                    language: "elixir".to_string(),
                    ..Default::default()
                });
            }
        }
    }

    /// Regex-based import scan for grammars whose import forms are generic calls
    /// (ruby `require 'x'`, elixir `import Module`, R `library(x)`).
    fn extract_script_imports(&self, relationships: &mut Vec<Relationship>) {
        let content = std::str::from_utf8(self.source).unwrap_or("");
        let source_path = self.file_path.to_string();
        let regexes: &[&str] = match self.language {
            "ruby" => &[
                r#"(?m)^\s*require\s+['"]([^'"]+)['"]"#,
                r#"(?m)^\s*require_relative\s+['"]([^'"]+)['"]"#,
            ],
            "elixir" => &[r#"(?m)^\s*(?:import|alias|require|use)\s+([A-Z][\w.]+)"#],
            "r" => &[r#"(?m)^\s*(?:library|require)\s*\(\s*['"]?([\w.]+)['"]?\s*\)"#],
            "perl" => &[
                r#"(?m)^\s*use\s+([A-Za-z][\w:]+)"#,
                r#"(?m)^\s*require\s+([A-Za-z][\w:]*(?:\s+if\s+.*)?)"#,
            ],
            "lua" => &[
                r#"(?m)^\s*local\s+\w+\s*=\s*require\s*\(\s*['"]([^'"]+)['"]\s*\)"#,
                r#"(?m)^\s*require\s*\(\s*['"]([^'"]+)['"]\s*\)"#,
            ],
            "nim" => &[r#"(?m)^\s*import\s+([\w/]+)"#],
            "crystal" => &[r#"(?m)^\s*require\s+['"]([^'"]+)['"]"#],
            _ => &[],
        };
        for pat in regexes {
            let re = match regex::Regex::new(pat) {
                Ok(r) => r,
                Err(_) => continue,
            };
            for cap in re.captures_iter(content) {
                if let Some(target) = cap.get(1) {
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: source_path.clone(),
                        target_qualified: target.as_str().to_string(),
                        rel_type: "imports".to_string(),
                        confidence: 1.0,
                        metadata: serde_json::json!({}),
                        ..Default::default()
                    });
                }
            }
        }
    }

    /// TOML/Dockerfile grammar can't link — short-circuit to regex-only path.
    pub fn extract_regex_only(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();
        let relationships = Vec::new();
        if self.language == "toml" {
            self.extract_toml_sections(&mut elements);
        }
        if self.language == "dockerfile" {
            self.extract_dockerfile_directives(&mut elements);
        }
        (elements, relationships)
    }

    fn extract_toml_sections(&self, elements: &mut Vec<CodeElement>) {
        let content = std::str::from_utf8(self.source).unwrap_or("");
        let source_path = self.file_path.to_string();
        let Ok(re) = regex::Regex::new(r"(?m)^\s*\[([^\]]+)\]") else {
            return;
        };
        for cap in re.captures_iter(content) {
            let raw = cap.get(1).map(|m| m.as_str().trim()).unwrap_or("");
            let name = raw.split('.').next().unwrap_or(raw).to_string();
            if name.is_empty() {
                continue;
            }
            elements.push(CodeElement {
                qualified_name: format!("{}::{}", source_path, name),
                element_type: "section".to_string(),
                name,
                file_path: source_path.clone(),
                line_start: 1,
                line_end: 1,
                language: "toml".to_string(),
                ..Default::default()
            });
        }
    }

    fn extract_dockerfile_directives(&self, elements: &mut Vec<CodeElement>) {
        let content = std::str::from_utf8(self.source).unwrap_or("");
        let source_path = self.file_path.to_string();
        let Ok(re) = regex::Regex::new(r"(?m)^\s*FROM\s+(\S+)") else {
            return;
        };
        for cap in re.captures_iter(content) {
            let name = cap.get(1).map(|m| m.as_str()).unwrap_or("").to_string();
            if name.is_empty() {
                continue;
            }
            elements.push(CodeElement {
                qualified_name: format!("{}::stage::{}", source_path, name),
                element_type: "stage".to_string(),
                name: format!("stage:{}", name),
                file_path: source_path.clone(),
                line_start: 1,
                line_end: 1,
                language: "dockerfile".to_string(),
                ..Default::default()
            });
        }
    }

    fn extract_android_bindings(&self, relationships: &mut Vec<Relationship>) {
        let content = std::str::from_utf8(self.source).unwrap_or("");
        let source_path = self.file_path.to_string();

        self.extract_kotlin_synthetic_imports(content, &source_path, relationships);
        self.extract_find_view_by_id(content, &source_path, relationships);
        self.extract_viewbinding_access(content, &source_path, relationships);
    }

    fn extract_kotlin_synthetic_imports(
        &self,
        content: &str,
        source_path: &str,
        relationships: &mut Vec<Relationship>,
    ) {
        for cap in KOTLIN_SYNTHETIC_IMPORT.captures_iter(content) {
            if let Some(layout_name) = cap.get(1) {
                let layout_file = format!("res/layout/{}.xml", layout_name.as_str());
                relationships.push(Relationship {
                    id: None,
                    source_qualified: source_path.to_string(),
                    target_qualified: layout_file,
                    rel_type: "synthetic_binding".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "layout_name": layout_name.as_str(),
                    }),
                    ..Default::default()
                });
            }
        }
    }

    fn extract_find_view_by_id(
        &self,
        content: &str,
        source_path: &str,
        relationships: &mut Vec<Relationship>,
    ) {
        let patterns = [
            r#"findViewById<\w+>\(R\.id\.(\w+)\)"#,
            r#"findViewById\(R\.id\.(\w+)\)"#,
            r#"\.findViewById<\w+>\(R\.id\.(\w+)\)"#,
            r#"\.findViewById\(R\.id\.(\w+)\)"#,
        ];

        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();

        for pattern in &patterns {
            let re = Regex::new(pattern).unwrap();
            for cap in re.captures_iter(content) {
                if let Some(id_match) = cap.get(1) {
                    let view_id = id_match.as_str();
                    let key = format!("{}:{}", source_path, view_id);
                    if seen.contains(&key) {
                        continue;
                    }
                    seen.insert(key);

                    let view_id_qualified = format!("res/layout/__unknown__/@+id/{}", view_id);

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: source_path.to_string(),
                        target_qualified: view_id_qualified,
                        rel_type: "binds_view".to_string(),
                        confidence: 0.9,
                        metadata: serde_json::json!({
                            "view_id": view_id,
                            "method": "findViewById",
                        }),
                        ..Default::default()
                    });
                }
            }
        }
    }

    fn extract_viewbinding_access(
        &self,
        content: &str,
        source_path: &str,
        relationships: &mut Vec<Relationship>,
    ) {
        let binding_class_names: std::collections::HashSet<String> = VIEWBINDING_VAR
            .captures_iter(content)
            .filter_map(|cap| cap.get(1).map(|m| m.as_str().to_string()))
            .collect();

        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();

        for binding_name in binding_class_names {
            let escaped = regex::escape(&binding_name);
            let prop_pattern = format!(r#"{}\.(\w+)"#, escaped);
            let re = Regex::new(&prop_pattern).unwrap();

            for cap in re.captures_iter(content) {
                if let Some(prop_match) = cap.get(1) {
                    let prop_name = prop_match.as_str();
                    if prop_name == "root" || prop_name == "getRoot" {
                        continue;
                    }

                    let view_id = Self::to_snake_case(prop_name);
                    let key = format!("{}:{}:{}", source_path, binding_name, view_id);
                    if seen.contains(&key) {
                        continue;
                    }
                    seen.insert(key);

                    let view_id_qualified = format!("res/layout/__unknown__/@+id/{}", view_id);

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: source_path.to_string(),
                        target_qualified: view_id_qualified,
                        rel_type: "viewbinding_property".to_string(),
                        confidence: 0.9,
                        metadata: serde_json::json!({
                            "binding_class": binding_name,
                            "property_name": prop_name,
                            "view_id": view_id,
                        }),
                        ..Default::default()
                    });
                }
            }
        }
    }

    fn to_snake_case(s: &str) -> String {
        let mut result = String::new();
        for (i, c) in s.chars().enumerate() {
            if c.is_uppercase() && i > 0 {
                result.push('_');
            }
            result.push(c.to_lowercase().next().unwrap_or(c));
        }
        result
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
            | "function_signature"
            | "method_declaration"
            | "method_definition"
            | "method_signature"
            | "constructor_declaration"
            | "constructor_signature"
            | "secondary_constructor"
            | "getter"
            | "setter"
            | "method"
            | "singleton_method"
            | "def"
            | "defp"
            | "defmacro"
            | "defmacrop"
            | "defguard"
            | "sub"
            | "test_declaration"
            | "function"
            | "value_binding"
            | "fun_decl"
            | "function_statement"
            | "function_declaration_left"
            | "func_declaration"
            | "proc_declaration"
            | "function_clause"
            | "func"
            | "value_definition" => {
                self.extract_function(node, parent, elements, relationships);
            }
            "class_declaration"
            | "type_declaration"
            | "class_def"
            | "struct_item"
            | "class_definition"
            | "enum_declaration"
            | "record_declaration"
            | "object_declaration"
            | "companion_object"
            | "mixin_declaration"
            | "extension_declaration"
            | "type_alias"
            | "struct_specifier"
            | "class_specifier"
            | "union_specifier"
            | "enum_specifier"
            | "class"
            | "module"
            | "package_statement"
            | "package"
            | "defmodule"
            | "defprotocol"
            | "defimpl"
            | "trait_declaration"
            | "contract_declaration"
            | "struct_declaration"
            | "library_declaration"
            | "namespace_declaration"
            | "file_scoped_namespace_declaration"
            | "class_decl"
            | "data_type"
            | "type_alias_declaration"
            | "module_declaration"
            | "module_binding"
            | "type_definition"
            | "class_statement" => {
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
            | "preproc_include"
            | "import_from_statement"
            | "use_declaration"
            | "library_import"
            | "require"
            | "require_relative"
            | "require_statement"
            | "library"
            | "namespace_use_declaration"
            | "namespace_use_clause"
            | "alias"
            | "use"
            | "use_no_subs_statement"
            | "import_directive"
            | "using_directive"
            | "import_clause"
            | "open"
            | "open_module"
            | "import_attribute"
            | "using_statement"
            | "from_instruction"
            | "import_module" => {
                for source in self.get_import_sources(node, node_type) {
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: self.file_path.to_string(),
                        target_qualified: source,
                        rel_type: "imports".to_string(),
                        confidence: 1.0,
                        metadata: serde_json::json!({}),
                        ..Default::default()
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
            if let Some(child) = node.child(i as u32) {
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
                        | "mixin_declaration"
                        | "extension_declaration"
                        | "type_alias"
                        | "getter"
                        | "setter"
                        | "getter_signature"
                        | "setter_signature"
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
            "constructor_declaration" | "secondary_constructor" | "constructor_signature"
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
                metadata: self.build_function_metadata(node, signature, sig_end),
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
                    ..Default::default()
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
                    ..Default::default()
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
                            ..Default::default()
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
                    ..Default::default()
                });
            }
        }
    }

    fn build_function_metadata(
        &self,
        node: Node,
        signature: String,
        sig_end: u32,
    ) -> serde_json::Value {
        let mut metadata = serde_json::json!({
            "signature": signature,
            "signature_line_end": sig_end + 1,
        });

        // Add Kotlin-specific metadata
        if self.language == "kotlin" {
            if let Some(obj) = metadata.as_object_mut() {
                obj.insert(
                    "is_suspend".to_string(),
                    serde_json::json!(self.has_modifier(node, "suspend")),
                );
                obj.insert(
                    "is_inline".to_string(),
                    serde_json::json!(self.has_modifier(node, "inline")),
                );
                obj.insert(
                    "is_operator".to_string(),
                    serde_json::json!(self.has_modifier(node, "operator")),
                );
                obj.insert(
                    "is_infix".to_string(),
                    serde_json::json!(self.has_modifier(node, "infix")),
                );
                obj.insert(
                    "is_extension".to_string(),
                    serde_json::json!(self.is_extension_function(node)),
                );

                if let Some(receiver) = self.get_receiver_type(node) {
                    obj.insert("receiver_type".to_string(), serde_json::json!(receiver));
                }

                let type_params = self.get_type_parameters(node);
                if !type_params.is_empty() {
                    obj.insert(
                        "type_parameters".to_string(),
                        serde_json::json!(type_params),
                    );
                }
            }
        }

        metadata
    }

    fn has_modifier(&self, node: Node, modifier: &str) -> bool {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "modifiers" {
                let mut mod_cursor = child.walk();
                for mod_child in child.children(&mut mod_cursor) {
                    if mod_child.kind() == modifier {
                        return true;
                    }
                    // Also check inside annotation nodes
                    if mod_child.kind() == "annotation" || mod_child.kind() == "annotation_entry" {
                        if let Some(name) = self.get_annotation_name(mod_child) {
                            if name == modifier {
                                return true;
                            }
                        }
                    }
                }
            }
        }
        false
    }

    fn is_extension_function(&self, node: Node) -> bool {
        // Check for receiver_type field or child
        node.child_by_field_name("receiver_type").is_some()
    }

    fn get_receiver_type(&self, node: Node) -> Option<String> {
        if let Some(receiver) = node.child_by_field_name("receiver_type") {
            self.extract_type_name(receiver)
        } else {
            None
        }
    }

    fn extract_type_name(&self, node: Node) -> Option<String> {
        // Try to get the full type name from a type node
        if let Some(bytes) = self.source.get(node.byte_range()) {
            if let Ok(s) = std::str::from_utf8(bytes) {
                return Some(s.trim().to_string());
            }
        }

        // Walk for type_identifier or user_type
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "type_identifier" | "user_type" | "identifier" => {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            return Some(s.to_string());
                        }
                    }
                }
                _ => {
                    if let Some(name) = self.extract_type_name(child) {
                        return Some(name);
                    }
                }
            }
        }
        None
    }

    fn get_type_parameters(&self, node: Node) -> Vec<String> {
        let mut params = Vec::new();

        if let Some(type_params) = node.child_by_field_name("type_parameters") {
            let mut cursor = type_params.walk();
            for child in type_params.children(&mut cursor) {
                if child.kind() == "type_parameter" {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            params.push(s.trim().to_string());
                        }
                    }
                }
            }
        }

        params
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

    fn extract_class(
        &self,
        node: Node,
        parent: Option<&str>,
        elements: &mut Vec<CodeElement>,
        relationships: &mut Vec<Relationship>,
    ) {
        if let Some(name) = self.get_node_name(node) {
            let element_type =
                if node.kind() == "enum_declaration" || node.kind() == "enum_specifier" {
                    "enum"
                } else if node.kind() == "record_declaration" {
                    "record"
                } else if node.kind() == "struct_specifier" || node.kind() == "struct_item" {
                    "struct"
                } else if node.kind() == "union_specifier" {
                    "union"
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
                    ..Default::default()
                });
            } else {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name.clone(),
                    rel_type: "contains".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                    ..Default::default()
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
                metadata: self.build_class_metadata(node),
                ..Default::default()
            });

            self.extract_class_heritage(node, &qualified_name, relationships);
        }
    }

    fn build_class_metadata(&self, node: Node) -> serde_json::Value {
        let mut metadata = serde_json::json!({});

        if self.language == "kotlin" {
            if let Some(obj) = metadata.as_object_mut() {
                // Determine class type
                let class_type = if self.has_modifier(node, "data") {
                    "data"
                } else if self.has_modifier(node, "sealed") {
                    "sealed"
                } else if self.has_modifier(node, "abstract") {
                    "abstract"
                } else if self.has_modifier(node, "open") {
                    "open"
                } else if node.kind() == "object_declaration" {
                    "object"
                } else if node.kind() == "companion_object" {
                    "companion"
                } else if node.kind() == "enum_declaration" {
                    "enum"
                } else {
                    "class"
                };

                obj.insert("class_type".to_string(), serde_json::json!(class_type));
                obj.insert(
                    "is_data".to_string(),
                    serde_json::json!(class_type == "data"),
                );
                obj.insert(
                    "is_sealed".to_string(),
                    serde_json::json!(class_type == "sealed"),
                );
                obj.insert(
                    "is_abstract".to_string(),
                    serde_json::json!(self.has_modifier(node, "abstract")),
                );
                obj.insert(
                    "is_open".to_string(),
                    serde_json::json!(self.has_modifier(node, "open")),
                );
                obj.insert(
                    "is_object".to_string(),
                    serde_json::json!(node.kind() == "object_declaration"),
                );
                obj.insert(
                    "is_companion".to_string(),
                    serde_json::json!(node.kind() == "companion_object"),
                );

                let type_params = self.get_type_parameters(node);
                if !type_params.is_empty() {
                    obj.insert(
                        "type_parameters".to_string(),
                        serde_json::json!(type_params),
                    );
                }
            }
        }

        metadata
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
                            ..Default::default()
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
                            ..Default::default()
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
                    ..Default::default()
                });
            } else {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: qualified_name.clone(),
                    rel_type: "contains".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                    ..Default::default()
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
        visited: &mut Vec<usize>,
    ) {
        // Avoid infinite recursion
        let node_ptr = node.id();
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
                    self.extract_decorator_impl(child, parent, elements, visited);
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
                        ..Default::default()
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

        // Generic name-field fallback: many grammars (bash `word`, C++ specifier,
        // PHP, etc.) expose the declaration name via a `name` field. Try it before
        // the language-specific branches below.
        if node.child_by_field_name("name").is_some() {
            if let Some(name_node) = node.child_by_field_name("name") {
                if let Some(bytes) = self.source.get(name_node.byte_range()) {
                    if let Ok(s) = std::str::from_utf8(bytes) {
                        let trimmed = s.trim();
                        if !trimmed.is_empty() && !trimmed.contains(' ') && !trimmed.contains('(') {
                            return Some(trimmed.to_string());
                        }
                    }
                }
            }
        }

        // Elm: function_declaration_left has a lower_case_identifier child.
        // Powershell: function_statement has a function_name child.
        // OCaml: value_definition wraps let_binding; let_binding name is a
        // `value_name` child whose own text is the binding name.
        if (node_type == "value_definition" || node_type == "let_binding")
            && self.language == "ocaml"
        {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if child.kind() == "value_name" || child.kind() == "value_pattern" {
                    let name = self.get_node_name(child);
                    if name.is_some() {
                        return name;
                    }
                    // value_name's own text is the identifier.
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            let trimmed = s.trim();
                            if !trimmed.is_empty() && trimmed.len() < 64 {
                                return Some(trimmed.to_string());
                            }
                        }
                    }
                }
                if child.kind() == "let_binding" {
                    let name = self.get_node_name(child);
                    if name.is_some() {
                        return name;
                    }
                }
            }
        }

        // Elm: function_declaration_left has a lower_case_identifier child.
        // Powershell: function_statement has a function_name child.
        // OCaml: let_binding's first identifier child is the binding name.
        if node_type == "function_declaration_left"
            || node_type == "function_statement"
            || node_type == "let_binding"
        {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if matches!(
                    child.kind(),
                    "lower_case_identifier"
                        | "function_name"
                        | "identifier"
                        | "lower_identifier"
                        | "value_identifier"
                ) {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            let trimmed = s.trim();
                            if !trimmed.is_empty() {
                                return Some(trimmed.to_string());
                            }
                        }
                    }
                }
            }
        }

        // Zig: test "name" { ... } — the test name is the string child.
        if node_type == "test_declaration" {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if child.kind() == "string" || child.kind() == "identifier" {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            let trimmed = s.trim().trim_matches('"');
                            if !trimmed.is_empty() {
                                return Some(format!("test_{}", trimmed));
                            }
                        }
                    }
                }
            }
        }

        // Perl: package statement name lives in a `package_name` child.
        if node_type == "package_statement" || node_type == "package" {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if child.kind() == "package_name" {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            let trimmed = s.trim().trim_matches(';');
                            if !trimmed.is_empty() {
                                return Some(trimmed.to_string());
                            }
                        }
                    }
                }
            }
        }

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
                | "constructor_signature"
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

        // Dart getter_signature / setter_signature: the inner child is an
        // identifier (the member name). The outer method_signature wraps a
        // type_identifier + getter_signature/setter_signature child.
        if node_type == "getter_signature" || node_type == "setter_signature" {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if matches!(
                    child.kind(),
                    "identifier" | "property_identifier" | "field_identifier"
                ) {
                    return std::str::from_utf8(self.source.get(child.byte_range())?)
                        .ok()
                        .map(String::from);
                }
            }
        }

        // Dart getter/setter: the trailing identifier is the member name.
        // Skip the return type, the get/set keyword, and any primitive-type
        // tokens so we land on the actual identifier.
        let mut identifier_candidate: Option<String> = None;
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            // Recurse into Dart's getter_signature / setter_signature wrapper.
            if child.kind() == "getter_signature" || child.kind() == "setter_signature" {
                let mut inner_cursor = child.walk();
                for inner in child.children(&mut inner_cursor) {
                    if matches!(
                        inner.kind(),
                        "identifier" | "property_identifier" | "field_identifier"
                    ) {
                        let text = std::str::from_utf8(self.source.get(inner.byte_range())?)
                            .ok()
                            .map(String::from);
                        if let Some(t) = text {
                            return Some(t);
                        }
                    }
                }
            }
            if matches!(
                child.kind(),
                "get" | "set" | "primitive_type" | "type_identifier" | "void"
            ) {
                continue;
            }
            if matches!(
                child.kind(),
                "identifier" | "property_identifier" | "field_identifier"
            ) {
                let text = std::str::from_utf8(self.source.get(child.byte_range())?)
                    .ok()
                    .map(String::from);
                if text.is_some() {
                    identifier_candidate = text;
                }
            }
        }
        if let Some(name) = identifier_candidate {
            return Some(name);
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

        // C/C++: function_definition's name lives under the declarator
        // (function_definition → declarator → function_declarator → identifier).
        // Descend into the declarator field for the first identifier.
        if node_type == "function_definition" || node_type == "function_declarator" {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if child.kind() == "function_declarator"
                    || child.kind() == "pointer_declarator"
                    || child.kind() == "parenthesized_declarator"
                    || child.kind() == "declarator"
                {
                    let name = self.get_node_name(child);
                    if name.is_some() {
                        return name;
                    }
                }
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

        // C/C++: #include <stdio.h> / #include "my.h" — path field holds the target.
        if node_type == "preproc_include" {
            if let Some(path_node) = node.child_by_field_name("path") {
                if let Some(bytes) = self.source.get(path_node.byte_range()) {
                    if let Ok(s) = std::str::from_utf8(bytes) {
                        sources.push(
                            s.trim()
                                .trim_matches('"')
                                .trim_matches('<')
                                .trim_matches('>')
                                .to_string(),
                        );
                    }
                }
            }
            return sources;
        }

        // Scala: import scala.collection.mutable — identifiers joined by dots.
        if node_type == "import_declaration" && self.language == "scala" {
            let mut parts = Vec::new();
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if matches!(
                    child.kind(),
                    "identifier" | "operator_identifier" | "underscore"
                ) {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            parts.push(s.to_string());
                        }
                    }
                }
            }
            if !parts.is_empty() {
                sources.push(parts.join("."));
            }
            return sources;
        }

        // C#: using System; — scoped_identifier / identifier children.
        if node_type == "using_directive" {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if matches!(child.kind(), "identifier" | "scoped_identifier") {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            let trimmed = s.trim().trim_matches(';');
                            if !trimmed.is_empty() {
                                sources.push(trimmed.to_string());
                            }
                        }
                    }
                }
            }
            return sources;
        }

        // Haskell: import Data.List (sort) — module field holds the target.
        // Elm: import Html — moduleName field (camelCase).
        if (node_type == "import" && self.language == "haskell")
            || (node_type == "import_clause" && self.language == "elm")
        {
            let module_field = if self.language == "haskell" {
                "module"
            } else {
                "moduleName"
            };
            if let Some(module_node) = node.child_by_field_name(module_field) {
                if let Some(bytes) = self.source.get(module_node.byte_range()) {
                    if let Ok(s) = std::str::from_utf8(bytes) {
                        let trimmed = s.trim();
                        if !trimmed.is_empty() {
                            sources.push(trimmed.to_string());
                        }
                    }
                }
            }
            return sources;
        }

        // Haskell: import Data.List (sort) — module_name / identifier children.
        // Ocaml/F#: open List / open System — identifier child.
        // Elm: import Html exposing (text) — module_name child.
        // Nim: import std/strutils — identifier/string.
        // Erlang: -import(mod, [f/1]). — attribute.
        // Guard against java/kotlin/dart/go which have their own specific branches
        // below and would be shadowed by the generic identifier scan.
        if matches!(
            node_type,
            "import" | "open" | "open_module" | "import_clause" | "import_attribute"
        ) && !matches!(
            self.language,
            "java" | "kotlin" | "dart" | "go" | "typescript" | "javascript" | "rust"
        ) {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if matches!(
                    child.kind(),
                    "module_name"
                        | "identifier"
                        | "string"
                        | "interpreted_string_literal"
                        | "qualified_name"
                        | "atom"
                        | "variable"
                ) {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            let trimmed = s.trim().trim_matches('"');
                            if !trimmed.is_empty() {
                                sources.push(trimmed.to_string());
                            }
                        }
                    }
                } else if matches!(child.kind(), "module_path" | "module_name") {
                    // Descend one level into OCaml module_path wrappers.
                    let mut inner_cursor = child.walk();
                    for inner in child.children(&mut inner_cursor) {
                        if inner.kind() == "module_name" {
                            if let Some(bytes) = self.source.get(inner.byte_range()) {
                                if let Ok(s) = std::str::from_utf8(bytes) {
                                    let trimmed = s.trim().trim_matches('"');
                                    if !trimmed.is_empty() {
                                        sources.push(trimmed.to_string());
                                    }
                                }
                            }
                        }
                    }
                }
            }
            return sources;
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

        // Dart: library imports (e.g., import 'package:flutter/material.dart';)
        if node_type == "library_import" && self.language == "dart" {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                match child.kind() {
                    "interpreted_string_literal" | "string" => {
                        if let Some(bytes) = self.source.get(child.byte_range()) {
                            if let Ok(s) = std::str::from_utf8(bytes) {
                                let trimmed = s.trim_matches('"').to_string();
                                if !trimmed.is_empty() {
                                    sources.push(trimmed);
                                }
                            }
                        }
                    }
                    _ => {}
                }
            }
            return sources;
        }

        // Ruby: require 'x' / require_relative 'x' — first string/identifier arg.
        // Elixir: require/import/alias Module — the alias/module name.
        // R: library(x) / require(x) — the identifier argument.
        // Perl: use strict / use Module::Name — the module child.
        if matches!(
            node_type,
            "require" | "require_relative" | "require_statement" | "library" | "use"
        ) {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if matches!(
                    child.kind(),
                    "string"
                        | "interpreted_string_literal"
                        | "simple_symbol"
                        | "identifier"
                        | "constant"
                        | "alias"
                ) {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            let trimmed = s.trim().trim_matches(['"', '\'', ':']);
                            if !trimmed.is_empty() {
                                sources.push(trimmed.to_string());
                                return sources;
                            }
                        }
                    }
                }
            }
            return sources;
        }

        // PHP: use App\Support\Helper — namespace_use_declaration holds a
        // qualified_name / namespace_name child.
        if node_type == "namespace_use_declaration"
            || node_type == "namespace_use_clause"
            || node_type == "alias"
        {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if matches!(
                    child.kind(),
                    "namespace_name" | "qualified_name" | "name" | "scoped_identifier" | "alias"
                ) {
                    if let Some(bytes) = self.source.get(child.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            let trimmed = s.trim();
                            if !trimmed.is_empty() && !trimmed.starts_with('\\') {
                                sources.push(trimmed.to_string());
                            }
                        }
                    }
                }
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

    fn parse_dart(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_dart::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_c(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_c::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_cpp(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_cpp::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_bash(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_bash::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_ruby(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_ruby::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_php(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_php::LANGUAGE_PHP.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_perl(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_perl::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_r(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_r::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_elixir(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_elixir::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_scala(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_scala::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_zig(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_zig::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_solidity(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_solidity::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_lua(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_lua::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_json(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_json::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_yaml(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_yaml::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_css(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_css::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_html(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_html::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_graphql(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_graphql::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_protobuf(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_proto::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_csharp(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_c_sharp::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_haskell(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_haskell::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_elm(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_elm::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_ocaml(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_ocaml::LANGUAGE_OCAML.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_fsharp(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_fsharp::LANGUAGE_FSHARP.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_erlang(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_erlang::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_nim(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_nim::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_powershell(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_powershell::LANGUAGE.into();
        parser.set_language(&lang).ok()?;
        parser.parse(source, None)
    }

    fn parse_crystal(source: &[u8]) -> Option<tree_sitter::Tree> {
        let mut parser = Parser::new();
        let lang: tree_sitter::Language = tree_sitter_crystal::LANGUAGE.into();
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
    fn test_extract_c_function_and_struct() {
        let source = b"#include <stdio.h>\nstruct Point { int x; int y; };\nint add(int a, int b) { return a + b; }\nint main(void) { struct Point p; return add(1, 2); }";
        if let Some(tree) = parse_c(source) {
            let extractor = EntityExtractor::new(source, "main.c", "c");
            let (elements, relationships) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(
                !funcs.is_empty(),
                "expected C functions, got {:?}",
                elements
            );
            let structs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "struct")
                .collect();
            assert!(!structs.is_empty(), "expected C struct, got {:?}", elements);
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected C imports, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_cpp_class_and_method() {
        let source = b"#include <vector>\n#include \"utils.h\"\nusing namespace std;\nclass Foo {\npublic:\n    int bar(int x) { return x + 1; }\n};\nint main() { Foo f; return 0; }";
        if let Some(tree) = parse_cpp(source) {
            let extractor = EntityExtractor::new(source, "main.cpp", "cpp");
            let (elements, relationships) = extractor.extract(&tree);
            let classes: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert!(
                !classes.is_empty(),
                "expected C++ class, got {:?}",
                elements
            );
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function" && e.name == "bar")
                .collect();
            assert!(
                !funcs.is_empty(),
                "expected C++ method bar, got {:?}",
                elements
            );
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected C++ imports, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_bash_functions() {
        let source = b"#!/bin/bash\nGREETING=\"hello\"\ngreet() {\n  echo \"$GREETING\"\n}\nfunction farewell() {\n  echo \"bye\"\n}\ngreet\n";
        if let Some(tree) = parse_bash(source) {
            let extractor = EntityExtractor::new(source, "script.sh", "bash");
            let (elements, _) = extractor.extract(&tree);
            let funcs: Vec<&CodeElement> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert_eq!(
                funcs.len(),
                2,
                "expected 2 bash functions, got {:?}",
                elements
            );
        }
    }

    #[test]
    fn test_extract_ruby_class_and_method() {
        let source = b"require 'json'\nclass User\n  def initialize(name)\n    @name = name\n  end\n  def greet\n    \"hi #{@name}\"\n  end\nend\nmodule Utils\n  def self.helper\n  end\nend";
        if let Some(tree) = parse_ruby(source) {
            let extractor = EntityExtractor::new(source, "user.rb", "ruby");
            let (elements, relationships) = extractor.extract(&tree);
            let classes: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert!(
                !classes.is_empty(),
                "expected ruby class, got {:?}",
                elements
            );
            let methods: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function" || e.element_type == "method")
                .collect();
            assert!(
                methods.len() >= 2,
                "expected ruby methods, got {:?}",
                elements
            );
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected ruby require, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_php_class_and_function() {
        let source = b"<?php\nnamespace App\\Models;\nuse App\\Support\\Helper;\nclass User {\n    private $name;\n    public function greet() { return 'hi'; }\n}\nfunction helper() { return 1; }";
        if let Some(tree) = parse_php(source) {
            let extractor = EntityExtractor::new(source, "User.php", "php");
            let (elements, relationships) = extractor.extract(&tree);
            let classes: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert!(
                !classes.is_empty(),
                "expected php class, got {:?}",
                elements
            );
            let funcs: Vec<_> = elements.iter().filter(|e| e.name == "greet").collect();
            assert!(
                !funcs.is_empty(),
                "expected php method greet, got {:?}",
                elements
            );
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected php imports, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_perl_function_and_package() {
        let source = b"package My::Module;\nuse strict;\nuse warnings;\nsub greet {\n  my $name = shift;\n  return \"hi $name\";\n}\nsub helper { return 42; }";
        if let Some(tree) = parse_perl(source) {
            let extractor = EntityExtractor::new(source, "My/Module.pm", "perl");
            let (elements, relationships) = extractor.extract(&tree);
            let packages: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert!(
                !packages.is_empty(),
                "expected perl package, got {:?}",
                elements
            );
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(funcs.len() >= 2, "expected perl subs, got {:?}", elements);
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected perl use, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_r_functions() {
        let source = b"library(ggplot2)\nadd <- function(a, b) {\n  a + b\n}\nsquare <- function(x) {\n  x * x\n}\n";
        if let Some(tree) = parse_r(source) {
            let extractor = EntityExtractor::new(source, "math.R", "r");
            let (elements, relationships) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(funcs.len() >= 2, "expected R functions, got {:?}", elements);
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected R library, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_elixir_module_and_function() {
        let source = b"defmodule Greeter do\n  def hello(name) do\n    \"hi #{name}\"\n  end\n  defp secret do\n    42\n  end\nend";
        if let Some(tree) = parse_elixir(source) {
            let extractor = EntityExtractor::new(source, "greeter.ex", "elixir");
            let (elements, _) = extractor.extract(&tree);
            let modules: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class" || e.element_type == "module")
                .collect();
            assert!(
                !modules.is_empty(),
                "expected elixir module, got {:?}",
                elements
            );
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function" && e.name == "hello")
                .collect();
            assert!(
                !funcs.is_empty(),
                "expected elixir def hello, got {:?}",
                elements
            );
        }
    }

    #[test]
    fn test_extract_scala_class_and_function() {
        let source = b"package com.example\nimport scala.collection.mutable\nclass User(name: String) {\n  def greet: String = s\"hi $name\"\n}\ntrait Greetable {\n  def hello: String\n}\ndef helper(x: Int): Int = x + 1";
        if let Some(tree) = parse_scala(source) {
            let extractor = EntityExtractor::new(source, "User.scala", "scala");
            let (elements, relationships) = extractor.extract(&tree);
            let classes: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert!(
                !classes.is_empty(),
                "expected scala class, got {:?}",
                elements
            );
            let funcs: Vec<_> = elements.iter().filter(|e| e.name == "greet").collect();
            assert!(
                !funcs.is_empty(),
                "expected scala def greet, got {:?}",
                elements
            );
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected scala imports, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_zig_functions() {
        let source = b"const std = @import(\"std\");\nfn add(a: i32, b: i32) i32 {\n    return a + b;\n}\nconst Point = struct { x: i32, y: i32 };\ntest \"basic\" {\n    try std.testing.expect(add(1, 2) == 3);\n}";
        if let Some(tree) = parse_zig(source) {
            let extractor = EntityExtractor::new(source, "math.zig", "zig");
            let (elements, _) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(
                funcs.len() >= 2,
                "expected zig fns (add + test), got {:?}",
                elements
            );
        }
    }

    #[test]
    fn test_extract_solidity_contract_and_function() {
        let source = b"pragma solidity ^0.8.0;\nimport \"./Helper.sol\";\ncontract Counter {\n    uint256 private count;\n    function increment() public {\n        count += 1;\n    }\n}";
        if let Some(tree) = parse_solidity(source) {
            let extractor = EntityExtractor::new(source, "Counter.sol", "solidity");
            let (elements, relationships) = extractor.extract(&tree);
            let contracts: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class" && e.name == "Counter")
                .collect();
            assert!(
                !contracts.is_empty(),
                "expected solidity contract, got {:?}",
                elements
            );
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function" && e.name == "increment")
                .collect();
            assert!(
                !funcs.is_empty(),
                "expected solidity fn, got {:?}",
                elements
            );
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected solidity imports, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_lua_functions() {
        let source = b"local m = require(\"math\")\nfunction add(a, b)\n  return a + b\nend\nlocal function square(x)\n  return x * x\nend";
        if let Some(tree) = parse_lua(source) {
            let extractor = EntityExtractor::new(source, "math.lua", "lua");
            let (elements, relationships) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(funcs.len() >= 2, "expected lua fns, got {:?}", elements);
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected lua require, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_json_minimal_document() {
        let source = b"{\"name\": \"test\", \"count\": 3}";
        if let Some(tree) = parse_json(source) {
            let extractor = EntityExtractor::new(source, "config.json", "json");
            let (elements, _) = extractor.extract(&tree);
            assert!(
                !elements.is_empty(),
                "expected json document element, got {:?}",
                elements
            );
            assert!(elements.iter().any(|e| e.element_type == "document"));
        }
    }

    #[test]
    fn test_extract_yaml_minimal_document() {
        let source = b"name: test\nversion: 1.0\n";
        if let Some(tree) = parse_yaml(source) {
            let extractor = EntityExtractor::new(source, "config.yaml", "yaml");
            let (elements, _) = extractor.extract(&tree);
            assert!(
                !elements.is_empty(),
                "expected yaml document element, got {:?}",
                elements
            );
        }
    }

    #[test]
    fn test_extract_css_minimal_document() {
        let source = b".btn { color: red; }\n#id { padding: 4px; }\n";
        if let Some(tree) = parse_css(source) {
            let extractor = EntityExtractor::new(source, "styles.css", "css");
            let (elements, _) = extractor.extract(&tree);
            assert!(
                !elements.is_empty(),
                "expected css document element, got {:?}",
                elements
            );
            assert!(elements.iter().any(|e| e.element_type == "document"));
        }
    }

    #[test]
    fn test_extract_html_minimal_document() {
        let source = b"<!doctype html><html><head><title>t</title></head><body></body></html>";
        if let Some(tree) = parse_html(source) {
            let extractor = EntityExtractor::new(source, "index.html", "html");
            let (elements, _) = extractor.extract(&tree);
            assert!(
                !elements.is_empty(),
                "expected html document element, got {:?}",
                elements
            );
            assert!(elements.iter().any(|e| e.element_type == "document"));
        }
    }

    #[test]
    fn test_extract_graphql_minimal_document() {
        let source = b"type User { id: ID! name: String }\ntype Query { user: User }\n";
        if let Some(tree) = parse_graphql(source) {
            let extractor = EntityExtractor::new(source, "schema.graphql", "graphql");
            let (elements, _) = extractor.extract(&tree);
            assert!(
                !elements.is_empty(),
                "expected graphql document element, got {:?}",
                elements
            );
            assert!(elements.iter().any(|e| e.element_type == "document"));
        }
    }

    #[test]
    fn test_extract_protobuf_minimal_document() {
        let source = b"message User { required string name = 1; optional int32 age = 2; }\n";
        if let Some(tree) = parse_protobuf(source) {
            let extractor = EntityExtractor::new(source, "schema.proto", "protobuf");
            let (elements, _) = extractor.extract(&tree);
            assert!(
                !elements.is_empty(),
                "expected protobuf document element, got {:?}",
                elements
            );
            assert!(elements.iter().any(|e| e.element_type == "document"));
        }
    }

    #[test]
    fn test_extract_toml_sections() {
        let source = b"[package]\nname = \"foo\"\n[dependencies]\nserde = \"1\"\n";
        let extractor = EntityExtractor::new(source, "Cargo.toml", "toml");
        let (elements, _) = extractor.extract_regex_only();
        let sections: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "section")
            .collect();
        assert!(
            sections.len() >= 2,
            "expected at least 2 TOML sections, got {:?}",
            elements
        );
        assert!(sections.iter().any(|e| e.name == "package"));
        assert!(sections.iter().any(|e| e.name == "dependencies"));
    }

    #[test]
    fn test_extract_dockerfile_stages() {
        let source = b"FROM rust:1.70\nWORKDIR /app\nFROM alpine:3.18\n";
        let extractor = EntityExtractor::new(source, "Dockerfile", "dockerfile");
        let (elements, _) = extractor.extract_regex_only();
        let stages: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "stage")
            .collect();
        assert!(stages.len() >= 2);
        assert!(stages.iter().any(|e| e.name.contains("rust:1.70")));
        assert!(stages.iter().any(|e| e.name.contains("alpine:3.18")));
    }

    #[test]
    fn test_extract_csharp_class_and_method() {
        let source = b"using System;\nnamespace Demo {\n  public class User {\n    public string Greet(string name) {\n      return \"hi \" + name;\n    }\n  }\n}";
        if let Some(tree) = parse_csharp(source) {
            let extractor = EntityExtractor::new(source, "User.cs", "csharp");
            let (elements, relationships) = extractor.extract(&tree);
            let classes: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class" && e.name == "User")
                .collect();
            assert!(
                !classes.is_empty(),
                "expected csharp class, got {:?}",
                elements
            );
            let methods: Vec<_> = elements.iter().filter(|e| e.name == "Greet").collect();
            assert!(
                !methods.is_empty(),
                "expected csharp method Greet, got {:?}",
                elements
            );
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected csharp using, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_haskell_function_and_import() {
        let source = b"module Main where\nimport Data.List (sort)\ndouble :: Int -> Int\ndouble x = x * 2\nmain :: IO ()\nmain = putStrLn \"hi\"";
        if let Some(tree) = parse_haskell(source) {
            let extractor = EntityExtractor::new(source, "Main.hs", "haskell");
            let (elements, relationships) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(
                !funcs.is_empty(),
                "expected haskell funcs, got {:?}",
                elements
            );
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected haskell import, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_elm_function_and_type() {
        let source = b"module Main exposing (main)\nimport Html exposing (text)\ntype Msg = Increment | Decrement\ndouble : Int -> Int\ndouble x = x * 2";
        if let Some(tree) = parse_elm(source) {
            let extractor = EntityExtractor::new(source, "Main.elm", "elm");
            let (elements, relationships) = extractor.extract(&tree);
            let funcs: Vec<_> = elements.iter().filter(|e| e.name == "double").collect();
            assert!(!funcs.is_empty(), "expected elm double, got {:?}", elements);
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected elm import, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_ocaml_module_and_function() {
        let source =
            b"open List\nlet double x = x * 2\nmodule Math = struct\n  let add a b = a + b\nend";
        if let Some(tree) = parse_ocaml(source) {
            let extractor = EntityExtractor::new(source, "math.ml", "ocaml");
            let (elements, relationships) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(
                !funcs.is_empty(),
                "expected ocaml funcs, got {:?}",
                elements
            );
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected ocaml open, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_fsharp_module_and_function() {
        let source = b"module Math\nlet double x = x * 2\nlet add a b = a + b";
        if let Some(tree) = parse_fsharp(source) {
            let extractor = EntityExtractor::new(source, "math.fs", "fsharp");
            let (elements, _) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(
                funcs.len() >= 2,
                "expected fsharp funcs, got {:?}",
                elements
            );
        }
    }

    #[test]
    fn test_extract_erlang_function_and_module() {
        let source = b"-module(math).\n-export([double/1]).\ndouble(X) -> X * 2.";
        if let Some(tree) = parse_erlang(source) {
            let extractor = EntityExtractor::new(source, "math.erl", "erlang");
            let (elements, _) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(
                !funcs.is_empty(),
                "expected erlang funcs, got {:?}",
                elements
            );
        }
    }

    #[test]
    fn test_extract_nim_function_and_type() {
        let source = b"import std/strutils\nproc double(x: int): int =\n  x * 2\nfunc add(a, b: int): int = a + b";
        if let Some(tree) = parse_nim(source) {
            let extractor = EntityExtractor::new(source, "math.nim", "nim");
            let (elements, relationships) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(!funcs.is_empty(), "expected nim funcs, got {:?}", elements);
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected nim import, got {:?}",
                relationships
            );
        }
    }

    #[test]
    fn test_extract_powershell_function() {
        let source = b"function Get-User {\n  param($id)\n  return $id\n}\nfunction Test-Helper { Write-Host 'hi' }";
        if let Some(tree) = parse_powershell(source) {
            let extractor = EntityExtractor::new(source, "user.ps1", "powershell");
            let (elements, _) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(
                funcs.len() >= 2,
                "expected powershell funcs, got {:?}",
                elements
            );
        }
    }

    #[test]
    fn test_extract_crystal_class_and_method() {
        let source = b"require \"json\"\nclass User\n  def initialize(name)\n    @name = name\n  end\n  def greet\n    \"hi #{@name}\"\n  end\nend";
        if let Some(tree) = parse_crystal(source) {
            let extractor = EntityExtractor::new(source, "user.cr", "crystal");
            let (elements, relationships) = extractor.extract(&tree);
            let classes: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert!(
                !classes.is_empty(),
                "expected crystal class, got {:?}",
                elements
            );
            let imports: Vec<_> = relationships
                .iter()
                .filter(|r| r.rel_type == "imports")
                .collect();
            assert!(
                !imports.is_empty(),
                "expected crystal require, got {:?}",
                relationships
            );
        }
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

    // ── Dart / Flutter tests ──────────────────────────────────────────────────

    #[test]
    fn test_is_test_file_dart() {
        assert!(is_test_file("lib/foo_test.dart"));
        assert!(is_test_file("test/widget_test.dart"));
        assert!(is_test_file("test/unit/my_test.dart"));
        assert!(!is_test_file("lib/main.dart"));
        assert!(!is_test_file("lib/home_page.dart"));
    }

    #[test]
    fn test_get_tested_file_path_dart() {
        assert_eq!(
            get_tested_file_path("lib/foo_test.dart"),
            Some("lib/foo.dart".to_string())
        );
        assert_eq!(
            get_tested_file_path("test/widget_test.dart"),
            Some("test/widget.dart".to_string())
        );
        assert_eq!(get_tested_file_path("lib/main.dart"), None);
    }

    #[test]
    fn test_is_noise_call_dart_builtins() {
        assert!(is_noise_call("setState"));
        assert!(is_noise_call("initState"));
        assert!(is_noise_call("dispose"));
        assert!(is_noise_call("build"));
        assert!(is_noise_call("context"));
        assert!(is_noise_call("mounted"));
        assert!(is_noise_call("widget"));
        assert!(is_noise_call("debugPrint"));
        assert!(is_noise_call("late"));
        assert!(is_noise_call("required"));
        assert!(is_noise_call("async"));
        assert!(is_noise_call("await"));
    }

    #[test]
    fn test_is_noise_call_dart_test_functions() {
        assert!(is_noise_call("group"));
        assert!(is_noise_call("testWidgets"));
        assert!(is_noise_call("test"));
        assert!(is_noise_call("setUp"));
        assert!(is_noise_call("tearDown"));
        assert!(is_noise_call("setUpAll"));
        assert!(is_noise_call("tearDownAll"));
    }

    #[test]
    fn test_extract_dart_class() {
        let source = b"class MyWidget extends StatelessWidget {}";
        if let Some(tree) = parse_dart(source) {
            let extractor = EntityExtractor::new(source, "my_widget.dart", "dart");
            let (elements, _) = extractor.extract(&tree);
            let classes: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert!(!classes.is_empty(), "Should extract Dart class");
            assert_eq!(classes[0].name, "MyWidget");
        }
    }

    #[test]
    fn test_extract_dart_mixin() {
        let source = b"mixin Toggleable {}";
        if let Some(tree) = parse_dart(source) {
            let extractor = EntityExtractor::new(source, "toggleable.dart", "dart");
            let (elements, _) = extractor.extract(&tree);
            let mixins: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class" && e.name == "Toggleable")
                .collect();
            assert!(!mixins.is_empty(), "Should extract Dart mixin");
        }
    }

    #[test]
    fn test_extract_dart_extension() {
        let source = b"extension StringExtensions on String {}";
        if let Some(tree) = parse_dart(source) {
            let extractor = EntityExtractor::new(source, "string_ext.dart", "dart");
            let (elements, _) = extractor.extract(&tree);
            let extensions: Vec<_> = elements
                .iter()
                .filter(|e| e.name == "StringExtensions")
                .collect();
            assert!(!extensions.is_empty(), "Should extract Dart extension");
        }
    }

    #[test]
    fn test_extract_dart_function() {
        let source = b"void greet(String name) => print('Hello $name');";
        if let Some(tree) = parse_dart(source) {
            let extractor = EntityExtractor::new(source, "greet.dart", "dart");
            let (elements, _) = extractor.extract(&tree);
            let funcs: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "function")
                .collect();
            assert!(!funcs.is_empty(), "Should extract Dart function");
            assert_eq!(funcs[0].name, "greet");
        }
    }

    #[test]
    fn test_extract_dart_method() {
        let source = b"class Counter { void increment() {} }";
        if let Some(tree) = parse_dart(source) {
            let extractor = EntityExtractor::new(source, "counter.dart", "dart");
            let (elements, _) = extractor.extract(&tree);
            let methods: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "method")
                .collect();
            assert!(!methods.is_empty(), "Should extract Dart method");
            assert_eq!(methods[0].name, "increment");
        }
    }

    #[test]
    fn test_extract_dart_import() {
        // Import extraction depends on tree-sitter-dart node types
        // This test verifies the parser can process Dart files
        let source = br#"import 'package:flutter/material.dart';"#;
        if let Some(tree) = parse_dart(source) {
            let extractor = EntityExtractor::new(source, "main.dart", "dart");
            let _ = extractor.extract(&tree);
            // Parser should not panic - import handling verified manually
        }
    }

    #[test]
    fn test_extract_dart_stateful_widget() {
        let source = br#"
class MyHomePage extends StatefulWidget {
  @override
  _MyHomePageState createState() => _MyHomePageState();
}
class _MyHomePageState extends State<MyHomePage> {
  @override
  Widget build(BuildContext context) => Text('hello');
}
"#;
        if let Some(tree) = parse_dart(source) {
            let extractor = EntityExtractor::new(source, "my_home_page.dart", "dart");
            let (elements, _) = extractor.extract(&tree);
            let classes: Vec<_> = elements
                .iter()
                .filter(|e| e.element_type == "class")
                .collect();
            assert!(classes.len() >= 2, "Should extract both widget classes");
        }
    }

    #[test]
    fn test_extract_dart_enum() {
        let source = br#"
enum Color { red, green, blue }
"#;
        if let Some(tree) = parse_dart(source) {
            let extractor = EntityExtractor::new(source, "color.dart", "dart");
            let (elements, _) = extractor.extract(&tree);
            assert!(
                elements
                    .iter()
                    .any(|e| e.element_type == "enum" && e.name == "Color"),
                "Should extract enum Color: {:?}",
                elements
                    .iter()
                    .map(|e| (&e.element_type, &e.name))
                    .collect::<Vec<_>>()
            );
        }
    }

    #[test]
    fn test_extract_dart_getter_setter() {
        let source = br#"
class Box {
  int get value => 42;
  set value(int v) {}
}
"#;
        if let Some(tree) = parse_dart(source) {
            let extractor = EntityExtractor::new(source, "box.dart", "dart");
            let (elements, _) = extractor.extract(&tree);
            assert!(
                elements
                    .iter()
                    .any(|e| e.element_type == "class" && e.name == "Box"),
                "Should extract Box class"
            );
            assert!(
                elements
                    .iter()
                    .any(|e| e.name == "value" && e.element_type != "class"),
                "Should extract value getter/setter"
            );
        }
    }
}
