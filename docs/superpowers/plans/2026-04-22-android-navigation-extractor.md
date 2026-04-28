# Android Navigation Extractor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add navigation graph extraction for Jetpack Nav (XML + DSL), FragmentManager backstack, and Leanback presenter chains, plus four MCP tools to query navigation structure.

**Architecture:** Four extractors (`android_nav_jetpack.rs`, `android_nav_fragments.rs`, `android_nav_leanback.rs`, `android_nav_model.rs`) share common types from `android_nav_model.rs`. New rel types added to `RelationshipType` enum. Integration wired in `mod.rs`. Four MCP tools added to `tools.rs` + `handler.rs` using existing graph query methods.

**Tech Stack:** Rust, tree-sitter-kotlin-ng, roxmltree (XML parsing), regex, CozoDB via `GraphEngine`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `src/indexer/android_nav_model.rs` | Shared types: `NavDestination`, `NavArg`, `DestType` |
| Create | `src/indexer/android_nav_jetpack.rs` | XML nav graphs + Kotlin DSL extraction |
| Create | `src/indexer/android_nav_fragments.rs` | FragmentManager `replace`/`add`, `startActivity` |
| Create | `src/indexer/android_nav_leanback.rs` | Leanback presenter chain detection |
| Modify | `src/indexer/mod.rs` | Register 4 new modules, wire into `extract_elements_for_file` + `try_extract_android` |
| Modify | `src/db/models.rs` | Add `NavigatesTo`, `NavAction`, `ProvidesArg`, `RequiresArg`, `DeepLink`, `Presents` to `RelationshipType` |
| Modify | `src/mcp/tools.rs` | Add `get_nav_graph`, `find_route`, `get_screen_args`, `get_nav_callers` tool definitions |
| Modify | `src/mcp/handler.rs` | Add 4 handler methods + route them in `execute_tool` |
| Modify | `src/compress/response.rs` | Add `compress_nav_graph` method |
| Modify | `tests/android_integration_tests.rs` | Add integration tests for nav extractors |

---

## Task 1: Shared nav types in `android_nav_model.rs`

**Files:**
- Create: `src/indexer/android_nav_model.rs`

- [ ] **Step 1: Create the file**

```rust
#[derive(Debug, Clone, PartialEq)]
pub enum DestType {
    Fragment,
    Composable,
    Activity,
    Dialog,
}

impl DestType {
    pub fn as_str(&self) -> &'static str {
        match self {
            DestType::Fragment => "fragment",
            DestType::Composable => "composable",
            DestType::Activity => "activity",
            DestType::Dialog => "dialog",
        }
    }
}

#[derive(Debug, Clone)]
pub struct NavDestination {
    pub id: String,
    pub class_name: Option<String>,
    pub dest_type: DestType,
    pub start_destination: bool,
}

#[derive(Debug, Clone)]
pub struct NavArg {
    pub name: String,
    pub arg_type: String,
    pub nullable: bool,
    pub default_value: Option<String>,
}

#[derive(Debug, Clone)]
pub struct NavAction {
    pub id: String,
    pub source_dest: String,
    pub target_dest: String,
    pub pop_up_to: Option<String>,
}

#[derive(Debug, Clone)]
pub struct NavDeepLink {
    pub uri: String,
    pub destination: String,
}
```

- [ ] **Step 2: Verify compile**

```bash
cargo build 2>&1 | head -20
```

Expected: may have warnings about unused code, no errors. (Module not yet registered in `mod.rs`; that happens in Task 6.)

---

## Task 2: Add new relationship types to `RelationshipType`

**Files:**
- Modify: `src/db/models.rs`

- [ ] **Step 1: Write failing test**

Add to `src/db/models.rs` `mod tests`:

```rust
#[test]
fn test_nav_relationship_types() {
    assert_eq!(RelationshipType::NavigatesTo.as_str(), "navigates_to");
    assert_eq!(RelationshipType::NavAction.as_str(), "nav_action");
    assert_eq!(RelationshipType::ProvidesArg.as_str(), "provides_arg");
    assert_eq!(RelationshipType::RequiresArg.as_str(), "requires_arg");
    assert_eq!(RelationshipType::DeepLink.as_str(), "deep_link");
    assert_eq!(RelationshipType::Presents.as_str(), "presents");
    assert_eq!(RelationshipType::from_str("navigates_to"), Some(RelationshipType::NavigatesTo));
    assert_eq!(RelationshipType::from_str("presents"), Some(RelationshipType::Presents));
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cargo test test_nav_relationship_types 2>&1 | tail -5
```

Expected: FAIL — variants don't exist yet.

- [ ] **Step 3: Add variants to the enum**

Add to the `RelationshipType` enum (after `UsesLibrary`):

```rust
    NavigatesTo,
    NavAction,
    ProvidesArg,
    RequiresArg,
    DeepLink,
    Presents,
```

Add to `as_str()` match (after `UsesLibrary => "uses_library"`):

```rust
            RelationshipType::NavigatesTo => "navigates_to",
            RelationshipType::NavAction => "nav_action",
            RelationshipType::ProvidesArg => "provides_arg",
            RelationshipType::RequiresArg => "requires_arg",
            RelationshipType::DeepLink => "deep_link",
            RelationshipType::Presents => "presents",
```

Add to `from_str()` match (after `"uses_library" => Some(RelationshipType::UsesLibrary)`):

```rust
            "navigates_to" => Some(RelationshipType::NavigatesTo),
            "nav_action" => Some(RelationshipType::NavAction),
            "provides_arg" => Some(RelationshipType::ProvidesArg),
            "requires_arg" => Some(RelationshipType::RequiresArg),
            "deep_link" => Some(RelationshipType::DeepLink),
            "presents" => Some(RelationshipType::Presents),
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cargo test test_nav_relationship_types 2>&1 | tail -5
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/db/models.rs
git commit -m "feat: add navigation relationship types to RelationshipType enum"
```

---

## Task 3: Jetpack Navigation extractor — XML nav graphs

**Files:**
- Create: `src/indexer/android_nav_jetpack.rs`
- Modify: `src/indexer/mod.rs` (partially — XML routing only)

- [ ] **Step 1: Write the failing test (at bottom of new file)**

Create `src/indexer/android_nav_jetpack.rs` with:

```rust
use crate::db::models::{CodeElement, Relationship};

pub struct JetpackNavExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> JetpackNavExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract_xml(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        todo!()
    }

    pub fn extract_kotlin_dsl(&self, tree: &tree_sitter::Tree) -> (Vec<CodeElement>, Vec<Relationship>) {
        todo!()
    }
}

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

        let destinations: Vec<_> = elements.iter().filter(|e| e.element_type == "nav_destination").collect();
        assert_eq!(destinations.len(), 2, "Should find 2 destinations");
        assert!(destinations.iter().any(|e| e.name == "homeFragment"));
        assert!(destinations.iter().any(|e| e.name == "detailFragment"));

        let actions: Vec<_> = relationships.iter().filter(|r| r.rel_type == "nav_action").collect();
        assert_eq!(actions.len(), 1, "Should find 1 action");

        let args: Vec<_> = elements.iter().filter(|e| e.element_type == "nav_argument").collect();
        assert_eq!(args.len(), 1, "Should find 1 argument (userId)");

        let deep_links: Vec<_> = relationships.iter().filter(|r| r.rel_type == "deep_link").collect();
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

        let start = elements.iter().find(|e| e.element_type == "nav_destination" && e.name == "loginFragment");
        assert!(start.is_some());
        assert_eq!(
            start.unwrap().metadata.get("start_destination").and_then(|v| v.as_bool()),
            Some(true),
            "loginFragment should be marked as start destination"
        );
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cargo test -p leankg android_nav_jetpack 2>&1 | tail -10
```

Expected: FAIL — `todo!()` panics.

- [ ] **Step 3: Implement `extract_xml`**

Add roxmltree dependency first — check `Cargo.toml`:

```bash
grep roxmltree Cargo.toml
```

If missing, add to `Cargo.toml` dependencies:
```toml
roxmltree = "0.20"
```

Then implement `extract_xml` in `android_nav_jetpack.rs`:

```rust
pub fn extract_xml(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
    let content = match std::str::from_utf8(self.source) {
        Ok(s) => s,
        Err(_) => return (Vec::new(), Vec::new()),
    };

    let doc = match roxmltree::Document::parse(content) {
        Ok(d) => d,
        Err(_) => return (Vec::new(), Vec::new()),
    };

    let mut elements = Vec::new();
    let mut relationships = Vec::new();

    // Find <navigation> root
    let root = doc.root_element();
    if root.tag_name().name() != "navigation" {
        return (Vec::new(), Vec::new());
    }

    let graph_id = root.attribute("android:id")
        .map(|id| id.trim_start_matches("@+id/").trim_start_matches("@id/").to_string())
        .unwrap_or_else(|| "nav_graph".to_string());

    let start_dest = root.attribute("app:startDestination")
        .map(|id| id.trim_start_matches("@+id/").trim_start_matches("@id/").to_string());

    let graph_qn = format!("{}::nav_graph::{}", self.file_path, graph_id);

    elements.push(CodeElement {
        qualified_name: graph_qn.clone(),
        element_type: "nav_graph".to_string(),
        name: graph_id.clone(),
        file_path: self.file_path.to_string(),
        language: "xml".to_string(),
        metadata: serde_json::json!({
            "graph_id": graph_id,
            "start_destination": start_dest.clone().unwrap_or_default(),
        }),
        ..Default::default()
    });

    for node in root.children() {
        if !node.is_element() { continue; }

        let tag = node.tag_name().name();
        let dest_type = match tag {
            "fragment" => Some("fragment"),
            "activity" => Some("activity"),
            "dialog" => Some("dialog"),
            _ => None,
        };

        if let Some(dtype) = dest_type {
            let dest_id = node.attribute("android:id")
                .map(|id| id.trim_start_matches("@+id/").trim_start_matches("@id/").to_string())
                .unwrap_or_default();
            let class_name = node.attribute("android:name").map(|s| s.to_string());

            let dest_qn = format!("{}::nav_graph::{}::{}", self.file_path, graph_id, dest_id);
            let is_start = start_dest.as_deref() == Some(&dest_id);

            elements.push(CodeElement {
                qualified_name: dest_qn.clone(),
                element_type: "nav_destination".to_string(),
                name: dest_id.clone(),
                file_path: self.file_path.to_string(),
                language: "xml".to_string(),
                parent_qualified: Some(graph_qn.clone()),
                metadata: serde_json::json!({
                    "destination_id": dest_id,
                    "class_name": class_name,
                    "dest_type": dtype,
                    "start_destination": is_start,
                }),
                ..Default::default()
            });

            // Process children: <action>, <argument>, <deepLink>
            for child in node.children() {
                if !child.is_element() { continue; }
                match child.tag_name().name() {
                    "action" => {
                        let action_id = child.attribute("android:id")
                            .map(|id| id.trim_start_matches("@+id/").trim_start_matches("@id/").to_string())
                            .unwrap_or_default();
                        let target = child.attribute("app:destination")
                            .map(|id| id.trim_start_matches("@+id/").trim_start_matches("@id/").to_string())
                            .unwrap_or_default();
                        let pop_up_to = child.attribute("app:popUpTo")
                            .map(|id| id.trim_start_matches("@+id/").trim_start_matches("@id/").to_string());
                        let target_qn = format!("{}::nav_graph::{}::{}", self.file_path, graph_id, target);

                        relationships.push(Relationship {
                            id: None,
                            source_qualified: dest_qn.clone(),
                            target_qualified: target_qn,
                            rel_type: "nav_action".to_string(),
                            confidence: 0.95,
                            metadata: serde_json::json!({
                                "action_id": action_id,
                                "pop_up_to": pop_up_to,
                            }),
                        });
                    }
                    "argument" => {
                        let arg_name = child.attribute("android:name").unwrap_or("").to_string();
                        let arg_type = child.attribute("app:argType").unwrap_or("string").to_string();
                        let nullable = child.attribute("app:nullable").map(|v| v == "true").unwrap_or(false);
                        let default_val = child.attribute("android:defaultValue").map(|s| s.to_string());

                        let arg_qn = format!("{}::arg::{}", dest_qn, arg_name);
                        elements.push(CodeElement {
                            qualified_name: arg_qn.clone(),
                            element_type: "nav_argument".to_string(),
                            name: arg_name.clone(),
                            file_path: self.file_path.to_string(),
                            language: "xml".to_string(),
                            parent_qualified: Some(dest_qn.clone()),
                            metadata: serde_json::json!({
                                "arg_type": arg_type,
                                "nullable": nullable,
                                "default_value": default_val,
                            }),
                            ..Default::default()
                        });

                        relationships.push(Relationship {
                            id: None,
                            source_qualified: dest_qn.clone(),
                            target_qualified: arg_qn,
                            rel_type: "requires_arg".to_string(),
                            confidence: 1.0,
                            metadata: serde_json::json!({"arg_name": arg_name}),
                        });
                    }
                    "deepLink" => {
                        let uri = child.attribute("app:uri").unwrap_or("").to_string();
                        let dl_qn = format!("{}::deeplink::{}", dest_qn, uri);

                        elements.push(CodeElement {
                            qualified_name: dl_qn.clone(),
                            element_type: "nav_deep_link".to_string(),
                            name: uri.clone(),
                            file_path: self.file_path.to_string(),
                            language: "xml".to_string(),
                            parent_qualified: Some(dest_qn.clone()),
                            metadata: serde_json::json!({"uri": uri}),
                            ..Default::default()
                        });

                        relationships.push(Relationship {
                            id: None,
                            source_qualified: dl_qn,
                            target_qualified: dest_qn.clone(),
                            rel_type: "deep_link".to_string(),
                            confidence: 1.0,
                            metadata: serde_json::json!({}),
                        });
                    }
                    _ => {}
                }
            }
        }
    }

    (elements, relationships)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cargo test -p leankg android_nav_jetpack 2>&1 | tail -10
```

Expected: PASS for both XML tests. (`extract_kotlin_dsl` still panics but those tests don't call it yet.)

- [ ] **Step 5: Commit**

```bash
git add src/indexer/android_nav_jetpack.rs Cargo.toml Cargo.lock
git commit -m "feat: add JetpackNavExtractor XML parsing for nav graphs"
```

---

## Task 4: Jetpack Navigation extractor — Kotlin DSL

**Files:**
- Modify: `src/indexer/android_nav_jetpack.rs`

- [ ] **Step 1: Write the failing test**

Add to the `tests` module in `android_nav_jetpack.rs`:

```rust
#[test]
fn test_kotlin_dsl_nav_graph() {
    let mut parser = tree_sitter::Parser::new();
    let lang: tree_sitter::Language = tree_sitter_kotlin_ng::LANGUAGE.into();
    parser.set_language(&lang).unwrap();

    let source = r#"
fun NavGraphBuilder.onboardingGraph() {
    navigation(startDestination = "welcome", route = "onboarding") {
        composable("welcome") {}
        composable("profile/{userId}") {
            navArgument("userId") { type = NavType.StringType }
        }
    }
}
"#;
    let tree = parser.parse(source.as_bytes(), None).unwrap();
    let extractor = JetpackNavExtractor::new(source.as_bytes(), "nav/OnboardingGraph.kt");
    let (elements, _) = extractor.extract_kotlin_dsl(&tree);

    let destinations: Vec<_> = elements.iter().filter(|e| e.element_type == "nav_destination").collect();
    assert!(!destinations.is_empty(), "Should find composable destinations");
    assert!(destinations.iter().any(|e| e.name == "welcome"), "Should find 'welcome' composable");
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cargo test test_kotlin_dsl_nav_graph 2>&1 | tail -5
```

Expected: FAIL — `todo!()`.

- [ ] **Step 3: Implement `extract_kotlin_dsl`**

Replace the `todo!()` in `extract_kotlin_dsl` with:

```rust
pub fn extract_kotlin_dsl(&self, tree: &tree_sitter::Tree) -> (Vec<CodeElement>, Vec<Relationship>) {
    let mut elements = Vec::new();
    let mut relationships = Vec::new();
    self.visit_dsl_node(tree.root_node(), None, &mut elements, &mut relationships);
    (elements, relationships)
}

fn visit_dsl_node(
    &self,
    node: tree_sitter::Node,
    current_graph: Option<&str>,
    elements: &mut Vec<CodeElement>,
    relationships: &mut Vec<Relationship>,
) {
    let node_type = node.kind();

    if node_type == "call_expression" {
        if let Some(name) = self.get_call_name(node) {
            match name.as_str() {
                "composable" => {
                    if let Some(route) = self.get_first_string_arg(node) {
                        let dest_qn = format!("{}::composable::{}", self.file_path, route);
                        elements.push(CodeElement {
                            qualified_name: dest_qn.clone(),
                            element_type: "nav_destination".to_string(),
                            name: route.clone(),
                            file_path: self.file_path.to_string(),
                            line_start: node.start_position().row as u32 + 1,
                            line_end: node.end_position().row as u32 + 1,
                            language: "kotlin".to_string(),
                            parent_qualified: current_graph.map(|g| g.to_string()),
                            metadata: serde_json::json!({
                                "dest_type": "composable",
                                "route": route,
                            }),
                            ..Default::default()
                        });
                        // Recurse into lambda for navArgument calls
                        let mut cursor = node.walk();
                        for child in node.children(&mut cursor) {
                            self.visit_dsl_node(child, Some(&dest_qn.clone()), elements, relationships);
                        }
                        return;
                    }
                }
                "navigation" => {
                    let route = self.get_named_string_arg(node, "route")
                        .or_else(|| self.get_first_string_arg(node));
                    let start_dest = self.get_named_string_arg(node, "startDestination");
                    if let Some(r) = route {
                        let graph_qn = format!("{}::nav_graph::{}", self.file_path, r);
                        elements.push(CodeElement {
                            qualified_name: graph_qn.clone(),
                            element_type: "nav_graph".to_string(),
                            name: r.clone(),
                            file_path: self.file_path.to_string(),
                            line_start: node.start_position().row as u32 + 1,
                            language: "kotlin".to_string(),
                            metadata: serde_json::json!({
                                "route": r,
                                "start_destination": start_dest.unwrap_or_default(),
                            }),
                            ..Default::default()
                        });
                        let mut cursor = node.walk();
                        for child in node.children(&mut cursor) {
                            self.visit_dsl_node(child, Some(&graph_qn.clone()), elements, relationships);
                        }
                        return;
                    }
                }
                "navArgument" => {
                    if let Some(arg_name) = self.get_first_string_arg(node) {
                        if let Some(graph_qn) = current_graph {
                            let arg_qn = format!("{}::arg::{}", graph_qn, arg_name);
                            elements.push(CodeElement {
                                qualified_name: arg_qn.clone(),
                                element_type: "nav_argument".to_string(),
                                name: arg_name.clone(),
                                file_path: self.file_path.to_string(),
                                line_start: node.start_position().row as u32 + 1,
                                language: "kotlin".to_string(),
                                parent_qualified: Some(graph_qn.to_string()),
                                metadata: serde_json::json!({"arg_name": arg_name}),
                                ..Default::default()
                            });
                            relationships.push(Relationship {
                                id: None,
                                source_qualified: graph_qn.to_string(),
                                target_qualified: arg_qn,
                                rel_type: "requires_arg".to_string(),
                                confidence: 0.90,
                                metadata: serde_json::json!({}),
                            });
                        }
                    }
                }
                _ => {}
            }
        }
    }

    let mut cursor = node.walk();
    for child in node.children(&mut cursor) {
        self.visit_dsl_node(child, current_graph, elements, relationships);
    }
}

fn get_call_name(&self, node: tree_sitter::Node) -> Option<String> {
    let mut cursor = node.walk();
    for child in node.children(&mut cursor) {
        if child.kind() == "simple_identifier" || child.kind() == "identifier" {
            if let Some(bytes) = self.source.get(child.byte_range()) {
                if let Ok(s) = std::str::from_utf8(bytes) {
                    return Some(s.to_string());
                }
            }
        }
        if child.kind() == "navigation_expression" {
            // pkg.composable("route") — get rightmost identifier
            let mut nc = child.walk();
            let mut last = None;
            for n in child.children(&mut nc) {
                if n.kind() == "simple_identifier" || n.kind() == "identifier" {
                    if let Some(bytes) = self.source.get(n.byte_range()) {
                        if let Ok(s) = std::str::from_utf8(bytes) {
                            last = Some(s.to_string());
                        }
                    }
                }
            }
            return last;
        }
    }
    None
}

fn get_first_string_arg(&self, node: tree_sitter::Node) -> Option<String> {
    let mut cursor = node.walk();
    for child in node.children(&mut cursor) {
        if child.kind() == "value_arguments" || child.kind() == "call_suffix" {
            let mut ac = child.walk();
            for arg in child.children(&mut ac) {
                if arg.kind() == "value_argument" {
                    let mut vc = arg.walk();
                    for val in arg.children(&mut vc) {
                        if val.kind() == "string_literal" {
                            // Extract content between quotes
                            if let Some(bytes) = self.source.get(val.byte_range()) {
                                if let Ok(s) = std::str::from_utf8(bytes) {
                                    return Some(s.trim_matches('"').to_string());
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    None
}

fn get_named_string_arg(&self, node: tree_sitter::Node, param_name: &str) -> Option<String> {
    let mut cursor = node.walk();
    for child in node.children(&mut cursor) {
        if child.kind() == "value_arguments" || child.kind() == "call_suffix" {
            let mut ac = child.walk();
            for arg in child.children(&mut ac) {
                if arg.kind() == "value_argument" {
                    let text = self.source.get(arg.byte_range())
                        .and_then(|b| std::str::from_utf8(b).ok())
                        .unwrap_or("");
                    if text.starts_with(param_name) && text.contains('=') {
                        // Extract string after =
                        if let Some(val_part) = text.split('=').nth(1) {
                            let trimmed = val_part.trim().trim_matches('"').to_string();
                            return Some(trimmed);
                        }
                    }
                }
            }
        }
    }
    None
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cargo test test_kotlin_dsl_nav_graph 2>&1 | tail -5
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/indexer/android_nav_jetpack.rs
git commit -m "feat: add JetpackNavExtractor Kotlin DSL nav graph parsing"
```

---

## Task 5: FragmentManager nav extractor

**Files:**
- Create: `src/indexer/android_nav_fragments.rs`

- [ ] **Step 1: Write the failing tests**

Create `src/indexer/android_nav_fragments.rs`:

```rust
use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static FRAGMENT_REPLACE_RE: OnceLock<Regex> = OnceLock::new();
static FRAGMENT_ADD_RE: OnceLock<Regex> = OnceLock::new();
static BACKSTACK_RE: OnceLock<Regex> = OnceLock::new();
static START_ACTIVITY_RE: OnceLock<Regex> = OnceLock::new();
static NAV_CONTROLLER_RE: OnceLock<Regex> = OnceLock::new();

pub struct FragmentNavExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> FragmentNavExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        todo!()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_fragment_replace_extraction() {
        let source = r#"
fun navigate() {
    supportFragmentManager.beginTransaction()
        .replace(R.id.container, DetailFragment())
        .addToBackStack("detail")
        .commit()
}
"#;
        let extractor = FragmentNavExtractor::new(source.as_bytes(), "ui/MainActivity.kt");
        let (_, relationships) = extractor.extract();

        let nav_rels: Vec<_> = relationships.iter().filter(|r| r.rel_type == "navigates_to").collect();
        assert!(!nav_rels.is_empty(), "Should find navigates_to relationship");
        assert!(nav_rels[0].target_qualified.contains("DetailFragment"), "Target should be DetailFragment");
        assert_eq!(
            nav_rels[0].metadata.get("backstack_tag").and_then(|v| v.as_str()),
            Some("detail"),
            "Should capture backstack tag"
        );
    }

    #[test]
    fn test_start_activity_extraction() {
        let source = r#"
fun openProfile() {
    startActivity(Intent(this, ProfileActivity::class.java))
}
"#;
        let extractor = FragmentNavExtractor::new(source.as_bytes(), "ui/HomeFragment.kt");
        let (_, relationships) = extractor.extract();

        let nav_rels: Vec<_> = relationships.iter().filter(|r| r.rel_type == "navigates_to").collect();
        assert!(!nav_rels.is_empty(), "Should find navigates_to from startActivity");
        assert!(nav_rels[0].target_qualified.contains("ProfileActivity"));
    }

    #[test]
    fn test_nav_controller_navigate() {
        let source = r#"
fun goToDetail() {
    findNavController().navigate(R.id.action_home_to_detail)
}
"#;
        let extractor = FragmentNavExtractor::new(source.as_bytes(), "ui/HomeFragment.kt");
        let (_, relationships) = extractor.extract();

        let nav_rels: Vec<_> = relationships.iter().filter(|r| r.rel_type == "navigates_to").collect();
        assert!(!nav_rels.is_empty(), "Should find navigates_to from NavController");
        assert!(nav_rels[0].target_qualified.contains("action_home_to_detail"));
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cargo test android_nav_fragments 2>&1 | tail -10
```

Expected: FAIL — `todo!()`.

- [ ] **Step 3: Implement `extract`**

Replace `todo!()` with:

```rust
pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
    let content = match std::str::from_utf8(self.source) {
        Ok(s) => s,
        Err(_) => return (Vec::new(), Vec::new()),
    };

    let mut relationships = Vec::new();

    // FragmentManager.replace() or .add()
    let replace_re = FRAGMENT_REPLACE_RE.get_or_init(|| {
        Regex::new(r"\.(?:replace|add)\s*\(\s*[^,]+,\s*(\w+Fragment)\s*\(").unwrap()
    });

    let backstack_re = BACKSTACK_RE.get_or_init(|| {
        Regex::new(r#"\.addToBackStack\s*\(\s*"([^"]+)"\s*\)"#).unwrap()
    });

    for cap in replace_re.captures_iter(content) {
        if let Some(frag_match) = cap.get(1) {
            let fragment_name = frag_match.as_str();
            let target_qn = format!("class:{}", fragment_name);

            // Look for backstack tag in nearby context (within 200 chars)
            let pos = cap.get(0).map(|m| m.start()).unwrap_or(0);
            let window = &content[pos..std::cmp::min(pos + 300, content.len())];
            let backstack_tag = backstack_re.captures(window)
                .and_then(|bc| bc.get(1))
                .map(|m| m.as_str().to_string());

            relationships.push(Relationship {
                id: None,
                source_qualified: self.file_path.to_string(),
                target_qualified: target_qn,
                rel_type: "navigates_to".to_string(),
                confidence: 0.85,
                metadata: serde_json::json!({
                    "nav_type": "fragment_manager",
                    "fragment_name": fragment_name,
                    "backstack_tag": backstack_tag,
                }),
            });
        }
    }

    // startActivity(Intent(this, TargetActivity::class.java))
    let start_activity_re = START_ACTIVITY_RE.get_or_init(|| {
        Regex::new(r"startActivity\s*\(\s*Intent\s*\([^,]+,\s*(\w+Activity)::class\.java\s*\)").unwrap()
    });

    for cap in start_activity_re.captures_iter(content) {
        if let Some(activity_match) = cap.get(1) {
            let activity_name = activity_match.as_str();
            relationships.push(Relationship {
                id: None,
                source_qualified: self.file_path.to_string(),
                target_qualified: format!("class:{}", activity_name),
                rel_type: "navigates_to".to_string(),
                confidence: 0.90,
                metadata: serde_json::json!({
                    "nav_type": "start_activity",
                    "activity_name": activity_name,
                }),
            });
        }
    }

    // findNavController().navigate(R.id.action_... or "route")
    let nav_controller_re = NAV_CONTROLLER_RE.get_or_init(|| {
        Regex::new(r#"(?:findNavController|navController)\s*\(\s*\)\s*\.navigate\s*\(\s*(?:R\.id\.([\w_]+)|"([^"]+)")"#).unwrap()
    });

    for cap in nav_controller_re.captures_iter(content) {
        let action_or_route = cap.get(1)
            .or_else(|| cap.get(2))
            .map(|m| m.as_str().to_string())
            .unwrap_or_default();

        if !action_or_route.is_empty() {
            relationships.push(Relationship {
                id: None,
                source_qualified: self.file_path.to_string(),
                target_qualified: format!("nav_action:{}", action_or_route),
                rel_type: "navigates_to".to_string(),
                confidence: 0.85,
                metadata: serde_json::json!({
                    "nav_type": "nav_controller",
                    "action_or_route": action_or_route,
                }),
            });
        }
    }

    (Vec::new(), relationships)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cargo test android_nav_fragments 2>&1 | tail -5
```

Expected: PASS for all 3 tests.

- [ ] **Step 5: Commit**

```bash
git add src/indexer/android_nav_fragments.rs
git commit -m "feat: add FragmentNavExtractor for FragmentManager and startActivity patterns"
```

---

## Task 6: Leanback nav extractor

**Files:**
- Create: `src/indexer/android_nav_leanback.rs`

- [ ] **Step 1: Write the failing tests**

Create `src/indexer/android_nav_leanback.rs`:

```rust
use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::sync::OnceLock;

static BROWSE_FRAGMENT_RE: OnceLock<Regex> = OnceLock::new();
static ITEM_CLICKED_RE: OnceLock<Regex> = OnceLock::new();
static DETAILS_ACTIVITY_RE: OnceLock<Regex> = OnceLock::new();

pub struct LeanbackNavExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> LeanbackNavExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        todo!()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_browse_fragment_detected() {
        let source = r#"
class MainFragment : BrowseSupportFragment() {
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        setOnItemViewClickedListener { _, item, _, _ ->
            startActivity(Intent(activity, DetailsActivity::class.java))
        }
    }
}
"#;
        let extractor = LeanbackNavExtractor::new(source.as_bytes(), "tv/MainFragment.kt");
        let (elements, relationships) = extractor.extract();

        let browse_elem = elements.iter().find(|e| e.element_type == "nav_destination" && e.name.contains("MainFragment"));
        assert!(browse_elem.is_some(), "Should detect BrowseSupportFragment as nav_destination");

        let nav_rels: Vec<_> = relationships.iter().filter(|r| r.rel_type == "presents").collect();
        assert!(!nav_rels.is_empty(), "Should find presents relationship");
        assert!(nav_rels[0].target_qualified.contains("DetailsActivity"));
    }

    #[test]
    fn test_non_leanback_not_detected() {
        let source = r#"
class RegularFragment : Fragment() {
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {}
}
"#;
        let extractor = LeanbackNavExtractor::new(source.as_bytes(), "ui/RegularFragment.kt");
        let (elements, relationships) = extractor.extract();

        assert!(elements.is_empty(), "Non-leanback fragment should produce no elements");
        assert!(relationships.is_empty());
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cargo test android_nav_leanback 2>&1 | tail -10
```

Expected: FAIL — `todo!()`.

- [ ] **Step 3: Implement `extract`**

```rust
pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
    let content = match std::str::from_utf8(self.source) {
        Ok(s) => s,
        Err(_) => return (Vec::new(), Vec::new()),
    };

    // Check if this file has Leanback browse/details/playback fragments
    let browse_re = BROWSE_FRAGMENT_RE.get_or_init(|| {
        Regex::new(r"class\s+(\w+)\s*:\s*(?:BrowseSupportFragment|BrowseFragment|VerticalGridSupportFragment)\s*\(").unwrap()
    });

    let mut elements = Vec::new();
    let mut relationships = Vec::new();

    for cap in browse_re.captures_iter(content) {
        if let Some(class_match) = cap.get(1) {
            let class_name = class_match.as_str();
            let elem_qn = format!("{}::{}", self.file_path, class_name);

            elements.push(CodeElement {
                qualified_name: elem_qn.clone(),
                element_type: "nav_destination".to_string(),
                name: class_name.to_string(),
                file_path: self.file_path.to_string(),
                language: "kotlin".to_string(),
                metadata: serde_json::json!({
                    "dest_type": "leanback_browse",
                    "class_name": class_name,
                }),
                ..Default::default()
            });

            // Find setOnItemViewClickedListener with startActivity
            let details_re = DETAILS_ACTIVITY_RE.get_or_init(|| {
                Regex::new(r"startActivity\s*\(\s*Intent\s*\([^,]+,\s*(\w+Activity)::class\.java\s*\)").unwrap()
            });

            for dcap in details_re.captures_iter(content) {
                if let Some(activity_match) = dcap.get(1) {
                    let activity_name = activity_match.as_str();
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: elem_qn.clone(),
                        target_qualified: format!("class:{}", activity_name),
                        rel_type: "presents".to_string(),
                        confidence: 0.80,
                        metadata: serde_json::json!({
                            "nav_type": "leanback_browse_to_details",
                            "activity_name": activity_name,
                        }),
                    });
                }
            }

            // Also detect setOnItemViewClickedListener block for DetailsFragment instantiation
            let item_clicked_re = ITEM_CLICKED_RE.get_or_init(|| {
                Regex::new(r"setOnItemViewClickedListener\b").unwrap()
            });

            if item_clicked_re.is_match(content) {
                // Any fragment mentioned in the listener block
                let frag_re = Regex::new(r"(\w+Fragment)\s*\(").unwrap();
                let listener_pos = item_clicked_re.find(content).map(|m| m.start()).unwrap_or(0);
                let window = &content[listener_pos..std::cmp::min(listener_pos + 500, content.len())];
                for fcap in frag_re.captures_iter(window) {
                    if let Some(fm) = fcap.get(1) {
                        let frag_name = fm.as_str();
                        if frag_name != class_name {
                            relationships.push(Relationship {
                                id: None,
                                source_qualified: elem_qn.clone(),
                                target_qualified: format!("class:{}", frag_name),
                                rel_type: "presents".to_string(),
                                confidence: 0.75,
                                metadata: serde_json::json!({
                                    "nav_type": "leanback_item_click",
                                    "fragment_name": frag_name,
                                }),
                            });
                        }
                    }
                }
            }
        }
    }

    (elements, relationships)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cargo test android_nav_leanback 2>&1 | tail -5
```

Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add src/indexer/android_nav_leanback.rs
git commit -m "feat: add LeanbackNavExtractor for TV presenter chain navigation"
```

---

## Task 7: Wire extractors into `mod.rs`

**Files:**
- Modify: `src/indexer/mod.rs`

- [ ] **Step 1: Add module declarations and pub use**

In `src/indexer/mod.rs`, add after the existing android module declarations (e.g., after `pub mod android_room;`):

```rust
pub mod android_nav_fragments;
pub mod android_nav_jetpack;
pub mod android_nav_leanback;
pub mod android_nav_model;
```

Add after the existing `pub use android_room::AndroidRoomExtractor;`:

```rust
pub use android_nav_fragments::FragmentNavExtractor;
pub use android_nav_jetpack::JetpackNavExtractor;
pub use android_nav_leanback::LeanbackNavExtractor;
```

- [ ] **Step 2: Wire XML nav graphs into `try_extract_android`**

In the `try_extract_android` function, add before the `None` return at the end:

```rust
    // Navigation XML graphs: res/navigation/*.xml
    if file_path.contains("/res/navigation/") && file_path.ends_with(".xml") {
        let extractor = crate::indexer::JetpackNavExtractor::new(source, file_path);
        return Some(extractor.extract_xml());
    }
```

This goes before the final `None`. The existing `if file_path.contains("/res/") && file_path.ends_with(".xml")` line must remain but needs to come AFTER the navigation check (navigation is a subdirectory of res). Move the navigation check to be the first `.xml` check.

Current order in `try_extract_android`:
1. `AndroidManifest.xml`
2. `/res/values/` → AndroidResourcesExtractor
3. `/res/` → XmlLayoutExtractor

New order:
1. `AndroidManifest.xml`
2. `/res/values/` → AndroidResourcesExtractor
3. `/res/navigation/` → JetpackNavExtractor XML  ← insert here
4. `/res/` → XmlLayoutExtractor

- [ ] **Step 3: Wire Kotlin nav extractors into `extract_elements_for_file`**

In the `if language == "kotlin"` block in `extract_elements_for_file`, add after the `resource_link_rels` section:

```rust
        let mut nav_elements = Vec::new();
        let mut nav_relationships = Vec::new();

        // JetpackNavExtractor: Kotlin DSL (only for files with nav imports)
        if content.windows(b"NavGraphBuilder".len()).any(|w| w == b"NavGraphBuilder")
            || content.windows(b"composable(".len()).any(|w| w == b"composable(") {
            let nav_dsl_extractor = crate::indexer::JetpackNavExtractor::new(source, file_path);
            let (ne, nr) = nav_dsl_extractor.extract_kotlin_dsl(&tree);
            nav_elements.extend(ne);
            nav_relationships.extend(nr);
        }

        // FragmentNavExtractor
        let frag_nav_extractor = crate::indexer::FragmentNavExtractor::new(source, file_path);
        let (_, fnr) = frag_nav_extractor.extract();
        nav_relationships.extend(fnr);

        // LeanbackNavExtractor
        let leanback_extractor = crate::indexer::LeanbackNavExtractor::new(source, file_path);
        let (lne, lnr) = leanback_extractor.extract();
        nav_elements.extend(lne);
        nav_relationships.extend(lnr);
```

Then add to the final element/relationship merging (before `Ok(ParsedFile { ... })`):

```rust
    elements.extend(nav_elements);
    relationships.extend(nav_relationships);
```

- [ ] **Step 4: Verify build passes**

```bash
cargo build 2>&1 | grep -E "^error" | head -10
```

Expected: no errors.

- [ ] **Step 5: Run existing tests to verify nothing broken**

```bash
cargo test --lib 2>&1 | tail -15
```

Expected: all previously passing tests still pass.

- [ ] **Step 6: Commit**

```bash
git add src/indexer/mod.rs
git commit -m "feat: wire navigation extractors into indexer pipeline"
```

---

## Task 8: Integration tests

**Files:**
- Modify: `tests/android_integration_tests.rs`

- [ ] **Step 1: Write the failing tests**

Append to `tests/android_integration_tests.rs`:

```rust
#[cfg(test)]
mod nav_tests {
    // Note: integration tests that require a live GraphEngine are skipped here.
    // These tests verify extractor output directly.

    use leankg::indexer::{FragmentNavExtractor, JetpackNavExtractor, LeanbackNavExtractor};

    #[test]
    fn test_xml_nav_full_integration() {
        let xml = r#"<?xml version="1.0" encoding="utf-8"?>
<navigation xmlns:android="http://schemas.android.com/apk/res/android"
    xmlns:app="http://schemas.android.com/apk/res-auto"
    android:id="@+id/nav_main"
    app:startDestination="@id/homeFragment">
    <fragment android:id="@+id/homeFragment" android:name="com.example.HomeFragment">
        <action android:id="@+id/to_checkout" app:destination="@id/checkoutFragment" />
        <argument android:name="userId" app:argType="string" />
    </fragment>
    <fragment android:id="@+id/checkoutFragment" android:name="com.example.CheckoutFragment">
        <deepLink app:uri="example://checkout/{orderId}" />
    </fragment>
</navigation>"#;

        let extractor = JetpackNavExtractor::new(xml.as_bytes(), "app/res/navigation/nav_main.xml");
        let (elements, relationships) = extractor.extract_xml();

        // Verify all element types
        assert!(elements.iter().any(|e| e.element_type == "nav_graph"), "Missing nav_graph");
        assert_eq!(elements.iter().filter(|e| e.element_type == "nav_destination").count(), 2);
        assert_eq!(elements.iter().filter(|e| e.element_type == "nav_argument").count(), 1);
        assert_eq!(elements.iter().filter(|e| e.element_type == "nav_deep_link").count(), 1);

        // Verify all relationship types
        assert!(relationships.iter().any(|r| r.rel_type == "nav_action"));
        assert!(relationships.iter().any(|r| r.rel_type == "requires_arg"));
        assert!(relationships.iter().any(|r| r.rel_type == "deep_link"));
    }

    #[test]
    fn test_fragment_nav_multiple_patterns() {
        let source = r#"
class HomeFragment : Fragment() {
    fun goToDetail() {
        supportFragmentManager.beginTransaction()
            .replace(R.id.container, DetailFragment())
            .addToBackStack("detail")
            .commit()
    }
    fun openSettings() {
        startActivity(Intent(requireActivity(), SettingsActivity::class.java))
    }
    fun navToProfile() {
        findNavController().navigate("profile/{userId}")
    }
}
"#;
        let extractor = FragmentNavExtractor::new(source.as_bytes(), "ui/HomeFragment.kt");
        let (_, relationships) = extractor.extract();

        let nav_rels: Vec<_> = relationships.iter().filter(|r| r.rel_type == "navigates_to").collect();
        assert!(nav_rels.len() >= 2, "Should find multiple navigates_to relationships (got {})", nav_rels.len());
        assert!(nav_rels.iter().any(|r| r.target_qualified.contains("DetailFragment")));
        assert!(nav_rels.iter().any(|r| r.target_qualified.contains("SettingsActivity")));
    }

    #[test]
    fn test_leanback_full_chain() {
        let source = r#"
class BrowseFragment : BrowseSupportFragment() {
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        setOnItemViewClickedListener { _, item, _, _ ->
            val intent = Intent(activity, VideoDetailsActivity::class.java)
            startActivity(intent)
        }
    }
}
"#;
        let extractor = LeanbackNavExtractor::new(source.as_bytes(), "tv/BrowseFragment.kt");
        let (elements, relationships) = extractor.extract();

        assert!(!elements.is_empty(), "Should detect leanback fragment");
        assert!(elements.iter().any(|e| e.name == "BrowseFragment"));
        assert!(relationships.iter().any(|r| r.rel_type == "presents"));
        assert!(relationships.iter().any(|r| r.target_qualified.contains("VideoDetailsActivity")));
    }
}
```

- [ ] **Step 2: Run tests to verify they pass**

```bash
cargo test nav_tests 2>&1 | tail -10
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add tests/android_integration_tests.rs
git commit -m "test: add navigation extractor integration tests"
```

---

## Task 9: MCP tools — definitions

**Files:**
- Modify: `src/mcp/tools.rs`

- [ ] **Step 1: Add tool definitions**

Find the last `ToolDefinition` entry in `src/mcp/tools.rs` (currently `search_annotations`). Add these four after it, before the closing `]` of `list_tools`:

```rust
            ToolDefinition {
                name: "get_nav_graph".to_string(),
                description: "Get the navigation graph structure for a screen or nav file. Returns destinations, actions, arguments, and deep links.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "Nav XML file path or Kotlin DSL file path"},
                        "graph_id": {"type": "string", "description": "Nav graph ID (alternative to file)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "find_route".to_string(),
                description: "Find which destination a route string or action ID resolves to.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "route": {"type": "string", "description": "Route string (e.g. 'profile/{userId}') or action ID (e.g. 'action_home_to_detail')"}
                    },
                    "required": ["route"]
                }),
            },
            ToolDefinition {
                name: "get_screen_args".to_string(),
                description: "List all arguments a screen/destination requires, with types and default values.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "destination": {"type": "string", "description": "Destination name, route, or file path"},
                        "limit": {"type": "integer", "default": 20, "description": "Maximum results"}
                    },
                    "required": ["destination"]
                }),
            },
            ToolDefinition {
                name: "get_nav_callers".to_string(),
                description: "Find all call sites that navigate to a given destination. Use for impact radius when changing screen args.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "destination": {"type": "string", "description": "Destination name, route, fragment class, or activity class"}
                    },
                    "required": ["destination"]
                }),
            },
```

- [ ] **Step 2: Verify compile**

```bash
cargo build 2>&1 | grep "^error" | head -5
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add src/mcp/tools.rs
git commit -m "feat: add nav MCP tool definitions (get_nav_graph, find_route, get_screen_args, get_nav_callers)"
```

---

## Task 10: MCP handler — implement 4 nav tools

**Files:**
- Modify: `src/mcp/handler.rs`
- Modify: `src/compress/response.rs`

- [ ] **Step 1: Add `compress_nav_graph` to `response.rs`**

In `src/compress/response.rs`, add after `compress_search_annotations`:

```rust
pub fn compress_nav_graph(&self, response: &Value) -> Value {
    if !self.compress_enabled {
        return response.clone();
    }

    let destinations = response
        .get("destinations")
        .and_then(|v| v.as_array())
        .map(|arr| arr.to_vec())
        .unwrap_or_default();

    let total = destinations.len();
    let top: Vec<Value> = destinations.into_iter().take(self.max_elements).collect();

    serde_json::json!({
        "count": total,
        "destinations": top,
        "relationships": response.get("relationships").cloned().unwrap_or(serde_json::json!([])),
        "_compression_note": "Use get_nav_graph with full file path for complete results"
    })
}
```

- [ ] **Step 2: Add `get_nav_graph` handler method**

In `src/mcp/handler.rs`, add after the `search_annotations` method:

```rust
fn get_nav_graph(&self, args: &Value) -> Result<Value, String> {
    let file = args["file"].as_str();
    let graph_id = args["graph_id"].as_str();

    // Get all nav elements: nav_destination, nav_graph, nav_argument, nav_deep_link
    let all_elements = self
        .graph_engine
        .get_all_elements()
        .map_err(|e| e.to_string())?;

    let nav_types = ["nav_graph", "nav_destination", "nav_argument", "nav_deep_link"];

    let destinations: Vec<_> = all_elements
        .iter()
        .filter(|e| {
            nav_types.contains(&e.element_type.as_str())
                && (file.is_none_or(|f| e.file_path.contains(f)))
                && (graph_id.is_none_or(|g| e.qualified_name.contains(g)))
        })
        .map(|e| {
            json!({
                "qualified_name": e.qualified_name,
                "name": e.name,
                "type": e.element_type,
                "file": e.file_path,
                "line": e.line_start,
                "metadata": e.metadata,
            })
        })
        .collect();

    let all_rels = self
        .graph_engine
        .all_relationships()
        .map_err(|e| e.to_string())?;

    let nav_rel_types = ["nav_action", "requires_arg", "deep_link", "navigates_to", "presents"];
    let relationships: Vec<_> = all_rels
        .iter()
        .filter(|r| {
            nav_rel_types.contains(&r.rel_type.as_str())
                && (file.is_none_or(|f| r.source_qualified.contains(f)))
        })
        .map(|r| {
            json!({
                "source": r.source_qualified,
                "target": r.target_qualified,
                "type": r.rel_type,
                "metadata": r.metadata,
            })
        })
        .collect();

    Ok(json!({
        "destinations": destinations,
        "relationships": relationships,
    }))
}

fn find_route(&self, args: &Value) -> Result<Value, String> {
    let route = args["route"].as_str().ok_or("Missing 'route' parameter")?;

    let all_elements = self
        .graph_engine
        .get_all_elements()
        .map_err(|e| e.to_string())?;

    let matches: Vec<_> = all_elements
        .iter()
        .filter(|e| {
            (e.element_type == "nav_destination" || e.element_type == "nav_action")
                && (e.name.contains(route)
                    || e.qualified_name.contains(route)
                    || e.metadata
                        .get("route")
                        .and_then(|v| v.as_str())
                        .map(|r| r.contains(route))
                        .unwrap_or(false)
                    || e.metadata
                        .get("action_id")
                        .and_then(|v| v.as_str())
                        .map(|a| a.contains(route))
                        .unwrap_or(false))
        })
        .map(|e| {
            json!({
                "qualified_name": e.qualified_name,
                "name": e.name,
                "type": e.element_type,
                "file": e.file_path,
                "line": e.line_start,
                "metadata": e.metadata,
            })
        })
        .collect();

    Ok(json!({ "destinations": matches, "query": route }))
}

fn get_screen_args(&self, args: &Value) -> Result<Value, String> {
    let destination = args["destination"]
        .as_str()
        .ok_or("Missing 'destination' parameter")?;
    let limit = args["limit"].as_u64().unwrap_or(20) as usize;

    let all_elements = self
        .graph_engine
        .get_all_elements()
        .map_err(|e| e.to_string())?;

    let screen_args: Vec<_> = all_elements
        .iter()
        .filter(|e| {
            e.element_type == "nav_argument"
                && (e.parent_qualified
                    .as_deref()
                    .map(|p| p.contains(destination))
                    .unwrap_or(false)
                    || e.file_path.contains(destination))
        })
        .take(limit)
        .map(|e| {
            json!({
                "name": e.name,
                "qualified_name": e.qualified_name,
                "file": e.file_path,
                "arg_type": e.metadata.get("arg_type").cloned().unwrap_or(json!("unknown")),
                "nullable": e.metadata.get("nullable").cloned().unwrap_or(json!(false)),
                "default_value": e.metadata.get("default_value").cloned(),
            })
        })
        .collect();

    Ok(json!({
        "destination": destination,
        "args": screen_args,
        "count": screen_args.len(),
    }))
}

fn get_nav_callers(&self, args: &Value) -> Result<Value, String> {
    let destination = args["destination"]
        .as_str()
        .ok_or("Missing 'destination' parameter")?;

    let all_rels = self
        .graph_engine
        .all_relationships()
        .map_err(|e| e.to_string())?;

    let nav_rel_types = ["navigates_to", "nav_action", "presents"];
    let callers: Vec<_> = all_rels
        .iter()
        .filter(|r| {
            nav_rel_types.contains(&r.rel_type.as_str())
                && r.target_qualified.contains(destination)
        })
        .map(|r| {
            json!({
                "source": r.source_qualified,
                "target": r.target_qualified,
                "type": r.rel_type,
                "metadata": r.metadata,
            })
        })
        .collect();

    Ok(json!({
        "destination": destination,
        "callers": callers,
        "count": callers.len(),
    }))
}
```

- [ ] **Step 3: Route the 4 tools in `execute_tool`**

In the `match tool_name` block of `execute_tool` (after `"search_annotations" => self.search_annotations(arguments)`), add:

```rust
            "get_nav_graph" => self.get_nav_graph(arguments),
            "find_route" => self.find_route(arguments),
            "get_screen_args" => self.get_screen_args(arguments),
            "get_nav_callers" => self.get_nav_callers(arguments),
```

- [ ] **Step 4: Route `get_nav_graph` through compressor**

In the `match tool_name` block of `compress_response` (after `"search_annotations" => compressor.compress_search_annotations(&response)`), add:

```rust
            "get_nav_graph" => compressor.compress_nav_graph(&response),
```

- [ ] **Step 5: Check that `get_all_elements` exists on `GraphEngine`**

```bash
grep "pub fn get_all_elements" src/graph/query.rs
```

If missing, add to `src/graph/query.rs` (near other element query methods):

```rust
pub fn get_all_elements(&self) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
    let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]"#;

    let result = self.db.run_query(query)?;

    Ok(result
        .rows
        .iter()
        .filter_map(|row| {
            if row.len() < 11 { return None; }
            let metadata_str = row[10].as_str().unwrap_or("{}");
            Some(CodeElement {
                qualified_name: row[0].as_str().unwrap_or("").to_string(),
                element_type: row[1].as_str().unwrap_or("").to_string(),
                name: row[2].as_str().unwrap_or("").to_string(),
                file_path: row[3].as_str().unwrap_or("").to_string(),
                line_start: row[4].as_i64().unwrap_or(0) as u32,
                line_end: row[5].as_i64().unwrap_or(0) as u32,
                language: row[6].as_str().unwrap_or("").to_string(),
                parent_qualified: row[7].as_str().filter(|s| !s.is_empty()).map(|s| s.to_string()),
                cluster_id: row[8].as_str().filter(|s| !s.is_empty()).map(|s| s.to_string()),
                cluster_label: row[9].as_str().filter(|s| !s.is_empty()).map(|s| s.to_string()),
                metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
            })
        })
        .collect())
}
```

- [ ] **Step 6: Build to verify**

```bash
cargo build 2>&1 | grep "^error" | head -10
```

Expected: no errors.

- [ ] **Step 7: Run all lib tests**

```bash
cargo test --lib 2>&1 | tail -15
```

Expected: all passing.

- [ ] **Step 8: Commit**

```bash
git add src/mcp/handler.rs src/compress/response.rs src/graph/query.rs
git commit -m "feat: add get_nav_graph, find_route, get_screen_args, get_nav_callers MCP tools"
```

---

## Task 11: Final verification and push

- [ ] **Step 1: Run full test suite**

```bash
cargo test 2>&1 | tail -20
```

Expected: all unit tests pass; integration test failures are pre-existing DB lock issues (not new).

- [ ] **Step 2: Run cargo fmt and cargo clippy**

```bash
cargo fmt && cargo clippy -- -D warnings 2>&1 | grep "^error" | head -20
```

Fix any clippy errors before continuing.

- [ ] **Step 3: Push**

```bash
git pull --rebase && git push
```

- [ ] **Step 4: Bump version in `Cargo.toml`**

Increment the patch version (e.g., `0.17.0` → `0.17.1`).

```bash
grep '^version' Cargo.toml
```

Edit `Cargo.toml` to bump, then:

```bash
git add Cargo.toml Cargo.lock
git commit -m "chore: bump version for navigation extractor release"
git push
```
