use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static COMPOSABLE_RE: OnceLock<Regex> = OnceLock::new();
static NAV_RE: OnceLock<Regex> = OnceLock::new();
#[allow(dead_code)]
static ARG_RE: OnceLock<Regex> = OnceLock::new();
#[allow(dead_code)]
static NAVIGATE_RE: OnceLock<Regex> = OnceLock::new();
#[allow(clippy::regex_creation_in_loops)]
static ARG_SCOPE_RE: OnceLock<Regex> = OnceLock::new();

pub struct JetpackNavExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> JetpackNavExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    /// Parse Android XML navigation graph files (`res/navigation/*.xml`).
    pub fn extract_xml(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = match std::str::from_utf8(self.source) {
            Ok(s) => s,
            Err(_) => return (Vec::new(), Vec::new()),
        };

        // Ensure the android namespace is declared — real-world nav XMLs always
        // have it, but some test fixtures omit it.  Inject it when missing so
        // roxmltree (which is strict about undeclared prefixes) can parse the doc.
        let injected;
        let content: &str = if content.contains("android:") && !content.contains("xmlns:android") {
            injected = content.replacen(
                "<navigation",
                "<navigation xmlns:android=\"http://schemas.android.com/apk/res/android\"",
                1,
            );
            &injected
        } else {
            content
        };

        let doc = match roxmltree::Document::parse(content) {
            Ok(d) => d,
            Err(_) => return (Vec::new(), Vec::new()),
        };

        let mut elements: Vec<CodeElement> = Vec::new();
        let mut relationships: Vec<Relationship> = Vec::new();

        let root = doc.root_element();

        // Only process <navigation> root elements
        if root.tag_name().name() != "navigation" {
            return (Vec::new(), Vec::new());
        }

        let graph_id = android_id(&root).unwrap_or_else(|| "unknown".to_string());
        let graph_qn = format!("{}::nav_graph::{}", self.file_path, graph_id);

        let start_dest_raw = root
            .attributes()
            .find(|a| {
                a.name() == "startDestination"
                    && a.namespace() == Some("http://schemas.android.com/apk/res-auto")
            })
            .map(|a| a.value().to_string());
        let start_dest_id = start_dest_raw.as_deref().map(strip_id_prefix);

        // nav_graph element
        elements.push(CodeElement {
            qualified_name: graph_qn.clone(),
            element_type: "nav_graph".to_string(),
            name: graph_id.clone(),
            file_path: self.file_path.to_string(),
            line_start: root.range().start as u32,
            line_end: root.range().end as u32,
            language: "xml".to_string(),
            metadata: serde_json::json!({
                "graph_id": graph_id,
                "start_destination": start_dest_id,
            }),
            ..Default::default()
        });

        // Destination tags
        const DEST_TAGS: &[&str] = &["fragment", "activity", "dialog"];

        for child in root.children().filter(|n| n.is_element()) {
            let tag = child.tag_name().name();
            if !DEST_TAGS.contains(&tag) {
                continue;
            }

            let dest_id = match android_id(&child) {
                Some(id) => id,
                None => continue,
            };
            let dest_qn = format!("{}::{}", graph_qn, dest_id);
            let is_start = start_dest_id.map(|s| s == dest_id).unwrap_or(false);

            let class_name = android_attr(&child, "name");

            elements.push(CodeElement {
                qualified_name: dest_qn.clone(),
                element_type: "nav_destination".to_string(),
                name: dest_id.clone(),
                file_path: self.file_path.to_string(),
                line_start: child.range().start as u32,
                line_end: child.range().end as u32,
                language: "xml".to_string(),
                parent_qualified: Some(graph_qn.clone()),
                metadata: serde_json::json!({
                    "destination_id": dest_id,
                    "dest_type": tag,
                    "class_name": class_name,
                    "start_destination": is_start,
                }),
                ..Default::default()
            });

            // Children: action, argument, deepLink
            for sub in child.children().filter(|n| n.is_element()) {
                match sub.tag_name().name() {
                    "action" => {
                        let action_id = android_id(&sub);
                        let target_raw = app_attr(&sub, "destination");
                        let target_id = target_raw.as_deref().map(strip_id_prefix);
                        let pop_up_to = app_attr(&sub, "popUpTo");

                        if let Some(target) = target_id {
                            let target_qn = format!("{}::{}", graph_qn, target);
                            relationships.push(Relationship {
                                id: None,
                                source_qualified: dest_qn.clone(),
                                target_qualified: target_qn,
                                rel_type: "nav_action".to_string(),
                                confidence: 1.0,
                                metadata: serde_json::json!({
                                    "action_id": action_id,
                                    "pop_up_to": pop_up_to,
                                }),
                            });
                        }
                    }
                    "argument" => {
                        let arg_name = match android_attr(&sub, "name") {
                            Some(n) => n,
                            None => continue,
                        };
                        let arg_qn = format!("{}::arg::{}", dest_qn, arg_name);
                        let arg_type =
                            app_attr(&sub, "argType").unwrap_or_else(|| "string".to_string());
                        let nullable = app_attr(&sub, "nullable")
                            .map(|v| v == "true")
                            .unwrap_or(false);

                        elements.push(CodeElement {
                            qualified_name: arg_qn.clone(),
                            element_type: "nav_argument".to_string(),
                            name: arg_name.clone(),
                            file_path: self.file_path.to_string(),
                            line_start: sub.range().start as u32,
                            line_end: sub.range().end as u32,
                            language: "xml".to_string(),
                            parent_qualified: Some(dest_qn.clone()),
                            metadata: serde_json::json!({
                                "arg_type": arg_type,
                                "nullable": nullable,
                            }),
                            ..Default::default()
                        });

                        relationships.push(Relationship {
                            id: None,
                            source_qualified: dest_qn.clone(),
                            target_qualified: arg_qn,
                            rel_type: "requires_arg".to_string(),
                            confidence: 1.0,
                            metadata: serde_json::json!({
                                "arg_name": arg_name,
                            }),
                        });
                    }
                    "deepLink" => {
                        let uri = match app_attr(&sub, "uri") {
                            Some(u) => u,
                            None => continue,
                        };
                        let dl_qn = format!("{}::deeplink::{}", dest_qn, uri);

                        elements.push(CodeElement {
                            qualified_name: dl_qn.clone(),
                            element_type: "nav_deep_link".to_string(),
                            name: uri.clone(),
                            file_path: self.file_path.to_string(),
                            line_start: sub.range().start as u32,
                            line_end: sub.range().end as u32,
                            language: "xml".to_string(),
                            parent_qualified: Some(dest_qn.clone()),
                            metadata: serde_json::json!({ "uri": uri }),
                            ..Default::default()
                        });

                        relationships.push(Relationship {
                            id: None,
                            source_qualified: dl_qn,
                            target_qualified: dest_qn.clone(),
                            rel_type: "deep_link".to_string(),
                            confidence: 1.0,
                            metadata: serde_json::Value::Object(serde_json::Map::new()),
                        });
                    }
                    _ => {}
                }
            }
        }

        (elements, relationships)
    }

    /// Parse Compose Navigation DSL in Kotlin source files.
    pub fn extract_kotlin_dsl(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = match std::str::from_utf8(self.source) {
            Ok(s) => s,
            Err(_) => return (Vec::new(), Vec::new()),
        };

        let mut elements: Vec<CodeElement> = Vec::new();
        let mut relationships: Vec<Relationship> = Vec::new();

        let graph_id = "compose_nav".to_string();
        let graph_qn = format!("{}::nav_graph::{}", self.file_path, graph_id);

        // Create a root nav_graph element for the compose DSL
        elements.push(CodeElement {
            qualified_name: graph_qn.clone(),
            element_type: "nav_graph".to_string(),
            name: graph_id.clone(),
            file_path: self.file_path.to_string(),
            line_start: 1,
            line_end: content
                .lines()
                .last()
                .map(|l| l.len() as u32 + 1)
                .unwrap_or(1),
            language: "kotlin".to_string(),
            metadata: serde_json::json!({
                "graph_id": graph_id,
                "dsl_type": "compose",
            }),
            ..Default::default()
        });

        // First pass: identify all composable/navigation blocks with their line ranges and arguments
        #[derive(Clone)]
        struct BlockInfo {
            dest_qn: String,
            #[allow(dead_code)]
            dest_type: &'static str,
            start_line: u32,
            end_line: u32,
            #[allow(dead_code)]
            args: Vec<(String, u32)>,
        }

        let mut blocks: Vec<BlockInfo> = Vec::new();

        let composable_re = COMPOSABLE_RE.get_or_init(|| {
            Regex::new(r#"composable\s*\(\s*route\s*=\s*"([^"]+)"#).unwrap()
        });
        for cap in composable_re.captures_iter(content) {
            if let Some(route_match) = cap.get(1) {
                let route = route_match.as_str();
                let dest_id = route.to_string();
                let dest_qn = format!("{}::{}", graph_qn, dest_id);

                // Calculate line number from byte offset
                let start_byte = cap.get(0).unwrap().start();
                let start_line = content[..start_byte].lines().count() as u32;

                // Find the closing brace of this composable block
                let after_match = &content[start_byte..];
                let mut depth = 1i32;
                let mut end_pos = start_byte;
                for (i, ch) in after_match.char_indices() {
                    match ch {
                        '{' => depth += 1,
                        '}' => {
                            depth -= 1;
                            if depth == 0 {
                                end_pos = start_byte + i + 1;
                                break;
                            }
                        }
                        _ => {}
                    }
                }
                let end_line = content[..end_pos].lines().count() as u32;

                // Extract arguments within this block
                let block_content = &content[start_byte..end_pos];
                let arg_re = regex::Regex::new(r#"argument\s*\(\s*name\s*=\s*"([^"]+)"#).unwrap();
                let mut args: Vec<(String, u32)> = Vec::new();
                for arg_cap in arg_re.captures_iter(block_content) {
                    if let Some(arg_match) = arg_cap.get(1) {
                        // Calculate line relative to block start
                        let arg_byte_offset = arg_cap.get(0).unwrap().start();
                        let rel_line = block_content[..arg_byte_offset].lines().count() as u32;
                        args.push((arg_match.as_str().to_string(), start_line + rel_line));
                    }
                }

                blocks.push(BlockInfo {
                    dest_qn: dest_qn.clone(),
                    dest_type: "composable",
                    start_line,
                    end_line,
                    args,
                });

                elements.push(CodeElement {
                    qualified_name: dest_qn.clone(),
                    element_type: "nav_destination".to_string(),
                    name: dest_id.clone(),
                    file_path: self.file_path.to_string(),
                    line_start: start_line,
                    line_end: end_line,
                    language: "kotlin".to_string(),
                    parent_qualified: Some(graph_qn.clone()),
                    metadata: serde_json::json!({
                        "destination_id": dest_id,
                        "dest_type": "composable",
                        "route": route,
                    }),
                    ..Default::default()
                });

            }
        }

        let nav_re = NAV_RE.get_or_init(|| {
            Regex::new(
                r#"navigation\s*\(\s*route\s*=\s*"([^"]+)"\s*,\s*startDestination\s*=\s*"([^"]+)""#,
            )
            .unwrap()
        });

        for cap in nav_re.captures_iter(content) {
            if let (Some(route_match), Some(start_match)) = (cap.get(1), cap.get(2)) {
                let route = route_match.as_str();
                let start_dest = start_match.as_str();
                let dest_id = route.to_string();
                let dest_qn = format!("{}::{}", graph_qn, dest_id);

                let start_byte = cap.get(0).unwrap().start();
                let start_line = content[..start_byte].lines().count() as u32;

                // Find the closing brace of this navigation block
                let after_match = &content[start_byte..];
                let mut depth = 1i32;
                let mut end_pos = start_byte;
                for (i, ch) in after_match.char_indices() {
                    match ch {
                        '{' => depth += 1,
                        '}' => {
                            depth -= 1;
                            if depth == 0 {
                                end_pos = start_byte + i + 1;
                                break;
                            }
                        }
                        _ => {}
                    }
                }
                let end_line = content[..end_pos].lines().count() as u32;

                // Extract arguments within this block
                let block_content = &content[start_byte..end_pos];
                let arg_re = regex::Regex::new(r#"argument\s*\(\s*name\s*=\s*"([^"]+)"#).unwrap();
                let mut args: Vec<(String, u32)> = Vec::new();
                for arg_cap in arg_re.captures_iter(block_content) {
                    if let Some(arg_match) = arg_cap.get(1) {
                        let arg_byte_offset = arg_cap.get(0).unwrap().start();
                        let rel_line = block_content[..arg_byte_offset].lines().count() as u32;
                        args.push((arg_match.as_str().to_string(), start_line + rel_line));
                    }
                }

                blocks.push(BlockInfo {
                    dest_qn: dest_qn.clone(),
                    dest_type: "navigation",
                    start_line,
                    end_line,
                    args,
                });

                elements.push(CodeElement {
                    qualified_name: dest_qn.clone(),
                    element_type: "nav_destination".to_string(),
                    name: dest_id.clone(),
                    file_path: self.file_path.to_string(),
                    line_start: start_line,
                    line_end: end_line,
                    language: "kotlin".to_string(),
                    parent_qualified: Some(graph_qn.clone()),
                    metadata: serde_json::json!({
                        "destination_id": dest_id,
                        "dest_type": "navigation",
                        "route": route,
                        "start_destination": start_dest,
                    }),
                    ..Default::default()
                });

            }
        }

        // Second pass: assign arguments to their correct blocks by line number proximity
        let arg_re_global = regex::Regex::new(r#"argument\s*\(\s*name\s*=\s*"([^"]+)"#).unwrap();
        for cap in arg_re_global.captures_iter(content) {
            if let Some(arg_match) = cap.get(1) {
                let arg_name = arg_match.as_str().to_string();
                let arg_line = content[..cap.get(0).unwrap().start()].lines().count() as u32;

                // Find the closest block that ends after this argument line
                let best_block = blocks.iter().rev().find(|b| b.end_line > arg_line);
                if let Some(block) = best_block {
                    let arg_qn = format!("{}::arg::{}", block.dest_qn, arg_name);
                    let arg_type = "string".to_string();
                    let nullable = false;

                    elements.push(CodeElement {
                        qualified_name: arg_qn.clone(),
                        element_type: "nav_argument".to_string(),
                        name: arg_name.clone(),
                        file_path: self.file_path.to_string(),
                        line_start: arg_line,
                        line_end: arg_line,
                        language: "kotlin".to_string(),
                        parent_qualified: Some(block.dest_qn.clone()),
                        metadata: serde_json::json!({
                            "arg_type": arg_type,
                            "nullable": nullable,
                        }),
                        ..Default::default()
                    });

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: block.dest_qn.clone(),
                        target_qualified: arg_qn,
                        rel_type: "requires_arg".to_string(),
                        confidence: 0.85,
                        metadata: serde_json::json!({
                            "arg_name": arg_name,
                        }),
                    });
                }
            }
        }

        // Extract navigate() calls to infer navigation actions with line numbers
        let navigate_re = regex::Regex::new(r#"navigate\s*\(\s*"([^"]+)""#).unwrap();

        let mut seen_nav = std::collections::HashSet::new();
        for cap in navigate_re.captures_iter(content) {
            if let Some(dest_match) = cap.get(1) {
                let target_route = dest_match.as_str();
                let key = target_route.to_string();
                if !seen_nav.contains(&key) && !blocks.is_empty() {
                    seen_nav.insert(key);

                    // Find the block closest before this navigate call
                    let nav_line = content[..cap.get(0).unwrap().start()].lines().count() as u32;
                    let source_block = blocks.iter().rev().find(|b| b.end_line < nav_line);

                    if let Some(source) = source_block {
                        let target_qn = format!("{}::{}", graph_qn, target_route);
                        relationships.push(Relationship {
                            id: None,
                            source_qualified: source.dest_qn.clone(),
                            target_qualified: target_qn,
                            rel_type: "nav_action".to_string(),
                            confidence: 0.75,
                            metadata: serde_json::json!({
                                "action_id": None::<String>,
                                "source_line": source.start_line,
                                "target_route": target_route,
                            }),
                        });
                    } else if !elements.is_empty() {
                        // Fallback: use first destination as source
                        let source_dest = &elements[0];
                        let target_qn = format!("{}::{}", graph_qn, target_route);
                        relationships.push(Relationship {
                            id: None,
                            source_qualified: source_dest.qualified_name.clone(),
                            target_qualified: target_qn,
                            rel_type: "nav_action".to_string(),
                            confidence: 0.75,
                            metadata: serde_json::json!({
                                "action_id": None::<String>,
                                "target_route": target_route,
                            }),
                        });
                    }
                }
            }
        }

        (elements, relationships)
    }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const NS_ANDROID: &str = "http://schemas.android.com/apk/res/android";
const NS_APP: &str = "http://schemas.android.com/apk/res-auto";

/// Get the value of `android:<attr_name>` on a node.
fn android_attr(node: &roxmltree::Node, attr_name: &str) -> Option<String> {
    node.attributes()
        .find(|a| a.name() == attr_name && a.namespace() == Some(NS_ANDROID))
        .map(|a| a.value().to_string())
}

/// Get the value of `app:<attr_name>` on a node.
fn app_attr(node: &roxmltree::Node, attr_name: &str) -> Option<String> {
    node.attributes()
        .find(|a| a.name() == attr_name && a.namespace() == Some(NS_APP))
        .map(|a| a.value().to_string())
}

/// Get the stripped `android:id` value (strips `@+id/` / `@id/` prefix).
fn android_id(node: &roxmltree::Node) -> Option<String> {
    android_attr(node, "id").map(|v| strip_id_prefix(&v).to_string())
}

/// Strip `@+id/` or `@id/` prefix from an Android resource reference.
fn strip_id_prefix(s: &str) -> &str {
    if let Some(rest) = s.strip_prefix("@+id/") {
        rest
    } else if let Some(rest) = s.strip_prefix("@id/") {
        rest
    } else {
        s
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_xml_nav_graph_destinations() {
        let xml = r#"<?xml version="1.0" encoding="utf-8"?>
<navigation xmlns:android="http://schemas.android.com/apk/res/android"
    xmlns:app="http://schemas.android.com/apk/res-auto"
    android:id="@+id/nav_graph"
    app:startDestination="@id/homeFragment">

    <fragment
        android:id="@+id/homeFragment"
        android:name="com.example.HomeFragment">
        <action
            android:id="@+id/action_home_to_detail"
            app:destination="@id/detailFragment" />
        <argument
            android:name="userId"
            app:argType="string"
            app:nullable="true" />
    </fragment>

    <fragment
        android:id="@+id/detailFragment"
        android:name="com.example.DetailFragment">
        <deepLink app:uri="example://detail/{id}" />
    </fragment>
</navigation>"#;

        let extractor = JetpackNavExtractor::new(xml.as_bytes(), "res/navigation/nav_graph.xml");
        let (elements, relationships) = extractor.extract_xml();

        let destinations: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "nav_destination")
            .collect();
        assert_eq!(destinations.len(), 2, "Should find 2 destinations");
        assert!(destinations.iter().any(|e| e.name == "homeFragment"));
        assert!(destinations.iter().any(|e| e.name == "detailFragment"));

        let actions: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "nav_action")
            .collect();
        assert_eq!(actions.len(), 1, "Should find 1 action");

        let args: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "nav_argument")
            .collect();
        assert_eq!(args.len(), 1, "Should find 1 argument (userId)");

        let deep_links: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "deep_link")
            .collect();
        assert_eq!(deep_links.len(), 1, "Should find 1 deep link");
    }

    #[test]
    fn test_xml_nav_start_destination() {
        let xml = r#"<?xml version="1.0" encoding="utf-8"?>
<navigation xmlns:app="http://schemas.android.com/apk/res-auto"
    android:id="@+id/nav_main"
    app:startDestination="@id/loginFragment">
    <fragment android:id="@+id/loginFragment" android:name="com.example.LoginFragment" />
    <fragment android:id="@+id/dashboardFragment" android:name="com.example.DashboardFragment" />
</navigation>"#;

        let extractor = JetpackNavExtractor::new(xml.as_bytes(), "res/navigation/nav_main.xml");
        let (elements, _) = extractor.extract_xml();

        let nav_graph = elements.iter().find(|e| e.element_type == "nav_graph");
        assert!(nav_graph.is_some(), "Should have a nav_graph element");

        let start = elements
            .iter()
            .find(|e| e.element_type == "nav_destination" && e.name == "loginFragment");
        assert!(start.is_some());
        assert_eq!(
            start
                .unwrap()
                .metadata
                .get("start_destination")
                .and_then(|v| v.as_bool()),
            Some(true),
            "loginFragment should be marked as start destination"
        );
    }
}
