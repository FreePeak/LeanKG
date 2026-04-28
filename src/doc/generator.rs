#![allow(dead_code)]
use crate::db::models::{BusinessLogic, CodeElement, Relationship};
use crate::graph::GraphEngine;
use std::collections::HashMap;
use std::path::PathBuf;

#[allow(dead_code)]
#[derive(Debug, thiserror::Error)]
pub enum DocError {
    #[error("Element not found: {0}")]
    NotFound(String),
    #[error("Database error: {0}")]
    Database(String),
    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),
    #[error("Template error: {0}")]
    Template(String),
}

pub struct DocGenerator {
    graph: GraphEngine,
    #[allow(dead_code)]
    output_path: PathBuf,
    templates_path: PathBuf,
}

#[allow(dead_code)]
impl DocGenerator {
    pub fn new(graph: GraphEngine, output_path: PathBuf) -> Self {
        Self {
            graph,
            output_path: output_path.clone(),
            templates_path: output_path.join("templates"),
        }
    }

    pub fn with_templates_path(mut self, templates_path: PathBuf) -> Self {
        self.templates_path = templates_path;
        self
    }

    pub fn generate_for_element(&self, qualified_name: &str) -> Result<String, DocError> {
        let element = self
            .graph
            .find_element(qualified_name)
            .map_err(|e| DocError::Database(e.to_string()))?;

        let mut output = String::new();
        output.push_str(&format!("# {}\n\n", qualified_name));
        output.push_str(&format!(
            "**Type:** {}\n",
            element
                .as_ref()
                .map(|e| e.element_type.as_str())
                .unwrap_or("unknown")
        ));
        output.push_str(&format!(
            "**File:** {}\n",
            element
                .as_ref()
                .map(|e| e.file_path.as_str())
                .unwrap_or("unknown")
        ));
        output.push_str(&format!(
            "**Lines:** {}-{}\n\n",
            element.as_ref().map(|e| e.line_start).unwrap_or(0),
            element.as_ref().map(|e| e.line_end).unwrap_or(0)
        ));

        let relationships = self
            .graph
            .get_relationships(qualified_name)
            .map_err(|e| DocError::Database(e.to_string()))?;
        if !relationships.is_empty() {
            output.push_str("## Relationships\n\n");
            for rel in relationships {
                output.push_str(&format!("- {}: {}\n", rel.rel_type, rel.target_qualified));
            }
        }

        Ok(output)
    }

    pub fn generate_for_element_with_annotation(
        &self,
        qualified_name: &str,
        annotation: &BusinessLogic,
    ) -> Result<String, DocError> {
        let mut output = self.generate_for_element(qualified_name)?;

        output.push_str("\n## Business Logic\n\n");
        output.push_str(&format!("{}\n", annotation.description));

        if let Some(ref story) = annotation.user_story_id {
            output.push_str(&format!("\n**User Story:** {}\n", story));
        }
        if let Some(ref feature) = annotation.feature_id {
            output.push_str(&format!("\n**Feature:** {}\n", feature));
        }

        Ok(output)
    }

    pub fn generate_for_element_with_template(
        &self,
        qualified_name: &str,
        template_name: &str,
    ) -> Result<String, DocError> {
        let element = self
            .graph
            .find_element(qualified_name)
            .map_err(|e| DocError::Database(e.to_string()))?;
        let relationships = self
            .graph
            .get_relationships(qualified_name)
            .map_err(|e| DocError::Database(e.to_string()))?;

        let rel_strings: Vec<String> = relationships
            .iter()
            .map(|r| format!("{}: {}", r.rel_type, r.target_qualified))
            .collect();

        let template_engine = crate::doc::TemplateEngine::new(self.templates_path.clone());

        if let Some(elem) = element.as_ref() {
            template_engine
                .render_element_template(
                    template_name,
                    &elem.qualified_name,
                    &elem.element_type,
                    &rel_strings,
                )
                .map_err(|e| DocError::Template(e.to_string()))
        } else {
            Err(DocError::NotFound(qualified_name.to_string()))
        }
    }

    pub fn regenerate_for_file(&self, file_path: &str) -> Result<Vec<String>, DocError> {
        let elements = self
            .graph
            .all_elements()
            .map_err(|e| DocError::Database(e.to_string()))?;
        let file_elements: Vec<&CodeElement> = elements
            .iter()
            .filter(|e| e.file_path == file_path)
            .collect();

        let mut regenerated = Vec::new();
        for elem in file_elements {
            let _ = self.generate_for_element(&elem.qualified_name)?;
            regenerated.push(elem.qualified_name.clone());
        }

        Ok(regenerated)
    }

    pub fn generate_agents_md(&self) -> Result<String, DocError> {
        let elements = self
            .graph
            .all_elements()
            .map_err(|e| DocError::Database(e.to_string()))?;
        let relationships = self
            .graph
            .all_relationships()
            .map_err(|e| DocError::Database(e.to_string()))?;

        let mut content = String::from("# Agent Guidelines for LeanKG\n\n");
        content.push_str("## Project Overview\n\n");
        content.push_str("LeanKG is a Rust-based knowledge graph system that indexes codebases using tree-sitter parsers, stores data in CozoDB, and exposes functionality via CLI and MCP protocol.\n\n");
        content.push_str("**Tech Stack**: Rust 1.70+, CozoDB (embedded relational-graph), tree-sitter, Axum, Clap, Tokio\n\n");
        content.push_str("---\n\n## Build Commands\n\n### Standard Build\n```bash\ncargo build                    # Debug build\ncargo build --release          # Release build\n```\n\n### Testing\n```bash\ncargo test                     # Run all tests\ncargo test <test_name>         # Run specific test (partial name matches)\ncargo test --package <pkg>     # Test specific package\ncargo test -- --nocapture      # Show println output during tests\n```\n\n### Code Quality\n```bash\ncargo fmt                      # Format code\ncargo fmt -- --check           # Check formatting without changes\ncargo clippy                   # Run linter\ncargo clippy -- -D warnings    # Treat warnings as errors\ncargo check                    # Type check without building\ncargo doc                      # Build documentation\n```\n\n### Codebase Indexing & Server\n```bash\ncargo run -- init              # Initialize LeanKG project\ncargo run -- index ./src       # Index codebase\ncargo run -- serve             # Start MCP server\ncargo run -- impact <file> --depth 3   # Calculate impact radius\ncargo run -- status            # Show index status\n```\n\n---\n\n## Code Structure Overview\n\n");
        content.push_str(&format!(
            "This codebase contains {} elements and {} relationships.\n\n",
            elements.len(),
            relationships.len()
        ));

        let mut files: Vec<&CodeElement> = elements
            .iter()
            .filter(|e| e.element_type == "file")
            .collect();
        files.sort_by_key(|e| &e.file_path);

        let mut modules: Vec<&CodeElement> = elements
            .iter()
            .filter(|e| e.element_type == "module")
            .collect();
        modules.sort_by_key(|e| &e.qualified_name);

        let mut funcs: Vec<&CodeElement> = elements
            .iter()
            .filter(|e| e.element_type == "function")
            .collect();
        funcs.sort_by_key(|e| &e.qualified_name);

        let mut classes: Vec<&CodeElement> = elements
            .iter()
            .filter(|e| e.element_type == "class")
            .collect();
        classes.sort_by_key(|e| &e.qualified_name);

        content.push_str("### Key Modules\n\n");
        if modules.is_empty() {
            content.push_str("```\nsrc/\n├── cli/          # Clap CLI commands\n├── config/       # Project configuration\n├── db/           # CozoDB layer (models, schema)\n├── doc/          # Documentation generator\n├── graph/        # Graph engine, query, traversal\n├── indexer/      # tree-sitter parsers, entity extraction\n├── mcp/          # MCP protocol implementation\n├── watcher/      # File system watcher\n├── web/          # Axum web server\n└── main.rs       # CLI entry point\n```\n\n");
        } else {
            for m in modules.iter().take(20) {
                content.push_str(&format!("- `{}` ({})\n", m.qualified_name, m.file_path));
            }
            content.push('\n');
        }

        content.push_str("### Files\n\n");
        for elem in files.iter().take(30) {
            content.push_str(&format!("- `{}`\n", elem.file_path));
        }
        if files.len() > 30 {
            content.push_str(&format!("- ... and {} more files\n", files.len() - 30));
        }
        content.push('\n');

        content.push_str("### Functions\n\n");
        for elem in funcs.iter().take(50) {
            content.push_str(&format!(
                "- `{}` ({}:{})\n",
                elem.qualified_name, elem.file_path, elem.line_start
            ));
        }
        if funcs.len() > 50 {
            content.push_str(&format!("- ... and {} more functions\n", funcs.len() - 50));
        }
        content.push('\n');

        content.push_str("### Classes/Structs\n\n");
        for elem in classes.iter().take(30) {
            content.push_str(&format!(
                "- `{}` ({}:{})\n",
                elem.qualified_name, elem.file_path, elem.line_start
            ));
        }
        if classes.len() > 30 {
            content.push_str(&format!("- ... and {} more classes\n", classes.len() - 30));
        }
        content.push('\n');

        content.push_str("---\n\n## Relationship Types\n\n");
        let mut rel_types: HashMap<&str, usize> = HashMap::new();
        for rel in &relationships {
            *rel_types.entry(rel.rel_type.as_str()).or_insert(0) += 1;
        }
        let mut sorted_rel_types: Vec<_> = rel_types.iter().collect();
        sorted_rel_types.sort_by_key(|(_, count)| *count);
        sorted_rel_types.reverse();
        for (rel_type, count) in sorted_rel_types.iter().take(10) {
            content.push_str(&format!("- `{}`: {} occurrences\n", rel_type, count));
        }
        content.push('\n');

        content.push_str("---\n\n## Testing Guidelines\n\n");
        content.push_str(
            "1. Unit tests are placed in `#[cfg(test)]` modules within each source file\n",
        );
        content.push_str("2. Integration tests are located in the `tests/` directory\n");
        content.push_str("3. Use `tempfile::TempDir` for tests requiring filesystem access\n");
        content.push_str("4. Use `tokio::test` for async tests\n");
        content.push_str("5. Follow Arrange-Act-Assert pattern in all tests\n\n");

        Ok(content)
    }

    pub fn generate_claude_md(&self) -> Result<String, DocError> {
        let elements = self
            .graph
            .all_elements()
            .map_err(|e| DocError::Database(e.to_string()))?;
        let relationships = self
            .graph
            .all_relationships()
            .map_err(|e| DocError::Database(e.to_string()))?;
        let annotations = self
            .graph
            .all_annotations()
            .map_err(|e| DocError::Database(e.to_string()))?;

        let mut content = String::from("# CLAUDE.md\n\n");
        content.push_str("## Project Overview\n\n");
        content.push_str("LeanKG is a Rust-based knowledge graph system that indexes codebases using tree-sitter parsers, stores data in CozoDB, and exposes functionality via CLI and MCP protocol.\n\n");
        content.push_str("---\n\n## Architecture Decisions\n\n");
        content.push_str("### Knowledge Graph Storage\n");
        content.push_str("- **CozoDB with SQLite**: Provides embedded relational-graph database with Datalog queries\n");
        content.push_str("- **Schema-full design**: Relations defined with explicit schemas for code_elements, relationships, and business_logic\n");
        content.push_str("- **Qualified naming**: Elements identified by `qualified_name` combining file path and element name\n\n");

        content.push_str("### Code Indexing\n");
        content.push_str("- **tree-sitter integration**: Multi-language parser support via tree-sitter-go, tree-sitter-typescript, tree-sitter-python\n");
        content.push_str("- **ParserManager**: Centralized parser lifecycle management\n");
        content.push_str("- **Entity extraction**: CodeElement captures type, name, location, parent hierarchy, and metadata\n\n");

        content.push_str("### Documentation Generation\n");
        content.push_str(
            "- **Template-based**: Uses mustache-style templates with variable substitution\n",
        );
        content.push_str("- **Auto-sync**: Documentation regenerates when source code changes\n");
        content.push_str("- **Business logic linking**: Annotations connect code elements to user stories and features\n\n");

        content.push_str("---\n\n## Context Statistics\n\n");
        content.push_str(&format!("- **Total elements**: {}\n", elements.len()));
        content.push_str(&format!(
            "- **Total relationships**: {}\n",
            relationships.len()
        ));
        content.push_str(&format!(
            "- **Business logic annotations**: {}\n",
            annotations.len()
        ));

        let files = elements.iter().filter(|e| e.element_type == "file").count();
        let functions = elements
            .iter()
            .filter(|e| e.element_type == "function")
            .count();
        let classes = elements
            .iter()
            .filter(|e| e.element_type == "class")
            .count();
        let modules = elements
            .iter()
            .filter(|e| e.element_type == "module")
            .count();

        content.push_str(&format!("- **Files**: {}\n", files));
        content.push_str(&format!("- **Functions**: {}\n", functions));
        content.push_str(&format!("- **Classes/Structs**: {}\n", classes));
        content.push_str(&format!("- **Modules**: {}\n\n", modules));

        content.push_str("---\n\n## Key Files\n\n");
        let mut files_list: Vec<&CodeElement> = elements
            .iter()
            .filter(|e| e.element_type == "file")
            .collect();
        files_list.sort_by_key(|e| &e.file_path);
        for elem in files_list.iter().take(15) {
            content.push_str(&format!("- `{}`\n", elem.file_path));
        }
        if files_list.len() > 15 {
            content.push_str(&format!(
                "- ... and {} more files\n\n",
                files_list.len() - 15
            ));
        } else {
            content.push('\n');
        }

        content.push_str("---\n\n## Context Guidelines for AI\n\n");
        content.push_str("1. **Code change workflow**: When code changes, use `DocGenerator` to regenerate affected documentation\n");
        content.push_str("2. **Element lookup**: Use `GraphEngine::find_element()` with qualified_name to locate code elements\n");
        content.push_str("3. **Impact analysis**: Use `ImpactAnalyzer::calculate_impact_radius()` to find affected code on changes\n");
        content.push_str("4. **Annotation tracking**: Use `DocSync::track_generated()` to maintain `generated_from` relationships\n");
        content.push_str("5. **Template customization**: Place custom templates in `.leankg/templates/` directory\n\n");

        if !annotations.is_empty() {
            content.push_str("---\n\n## Business Logic Annotations\n\n");
            content.push_str("The following code elements have business logic descriptions:\n\n");
            for ann in annotations.iter().take(20) {
                content.push_str(&format!(
                    "- `{}`: {}\n",
                    ann.element_qualified, ann.description
                ));
                if let Some(ref story) = ann.user_story_id {
                    content.push_str(&format!("  - User Story: {}\n", story));
                }
                if let Some(ref feature) = ann.feature_id {
                    content.push_str(&format!("  - Feature: {}\n", feature));
                }
            }
            if annotations.len() > 20 {
                content.push_str(&format!(
                    "- ... and {} more annotations\n",
                    annotations.len() - 20
                ));
            }
        }

        Ok(content)
    }

    pub fn sync_docs_for_file(&self, file_path: &str) -> Result<DocSyncResult, DocError> {
        let elements = self
            .graph
            .get_elements_by_file(file_path)
            .map_err(|e| DocError::Database(e.to_string()))?;

        let mut regenerated = Vec::new();
        let mut relationships_updated = 0;

        for elem in &elements {
            let _ = self.generate_for_element(&elem.qualified_name)?;
            regenerated.push(elem.qualified_name.clone());

            let rels = self
                .graph
                .get_relationships(&elem.qualified_name)
                .map_err(|e| DocError::Database(e.to_string()))?;
            relationships_updated += rels.len();
        }

        Ok(DocSyncResult {
            file_path: file_path.to_string(),
            elements_regenerated: regenerated.len(),
            relationships_updated,
            regenerated_elements: regenerated,
        })
    }

    pub fn get_doc_tracking_info(
        &self,
        element_qualified: &str,
    ) -> Result<Option<DocTrackingInfo>, DocError> {
        let element = self
            .graph
            .find_element(element_qualified)
            .map_err(|e| DocError::Database(e.to_string()))?;

        let relationships = self
            .graph
            .get_relationships(element_qualified)
            .map_err(|e| DocError::Database(e.to_string()))?;

        let annotation = self
            .graph
            .get_annotation(element_qualified)
            .map_err(|e| DocError::Database(e.to_string()))?;

        if let Some(elem) = element {
            Ok(Some(DocTrackingInfo {
                element: elem,
                relationships,
                annotation,
                generated_from: vec![],
            }))
        } else {
            Ok(None)
        }
    }
}

#[allow(dead_code)]
#[derive(Debug, Clone)]
pub struct DocSyncResult {
    pub file_path: String,
    pub elements_regenerated: usize,
    pub relationships_updated: usize,
    pub regenerated_elements: Vec<String>,
}

#[allow(dead_code)]
#[derive(Debug, Clone)]
pub struct DocTrackingInfo {
    pub element: CodeElement,
    pub relationships: Vec<Relationship>,
    pub annotation: Option<BusinessLogic>,
    pub generated_from: Vec<String>,
}
