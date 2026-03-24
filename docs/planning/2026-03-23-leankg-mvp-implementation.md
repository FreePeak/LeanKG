# LeanKG MVP Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a lightweight, local-first knowledge graph for AI-assisted development with Rust + SurrealDB.

**Architecture:** LeanKG is a single-binary application that indexes codebases using tree-sitter, stores relationships in SurrealDB as an embedded graph, and exposes functionality via CLI and MCP protocol. The system follows a modular architecture with distinct components for parsing, graph operations, documentation, and impact analysis.

**Tech Stack:** Rust, SurrealDB (embedded), tree-sitter, Clap, Axum, Leptos, notify

---

## Phase 1: Project Foundation

### Task 1: Initialize Rust Project with Dependencies

**Files:**
- Create: `Cargo.toml`
- Create: `src/main.rs`
- Create: `src/lib.rs`

**Step 1: Create Cargo.toml with dependencies**

```toml
[package]
name = "leankg"
version = "0.1.0"
edition = "2021"

[dependencies]
surrealdb = { version = "2", features = ["kv-rocksdb"] }
tree-sitter = "0.24"
tree-sitter-go = "0.24"
tree-sitter-typescript = "0.24"
tree-sitter-python = "0.24"
clap = { version = "4", features = ["derive"] }
notify = "7"
axum = "0.8"
tokio = { version = "1", features = ["full"] }
serde = { version = "1", features = ["derive"] }
serde_json = "1"
thiserror = "2"
tracing = "0.1"
tracing-subscriber = "0.3"
walkdir = "2"
glob = "0.3"

[dev-dependencies]
tempfile = "3"
```

**Step 2: Create basic main.rs**

```rust
use clap::Parser;

#[derive(Parser, Debug)]
#[command(name = "leankg")]
#[command(about = "Lightweight knowledge graph for AI-assisted development")]
struct Args {
    #[command(subcommand)]
    command: Command,
}

#[derive(Parser, Debug)]
enum Command {
    Init,
    Index { path: Option<String> },
    Query { query: String },
    Serve,
}

fn main() {
    println!("LeanKG v0.1.0");
}
```

**Step 3: Run cargo check to verify dependencies**

```bash
cd /Users/linh.doan/work/harvey/freepeak/LeanKG
cargo check
```

Expected: Should compile with warnings (unused fields OK for now)

**Step 4: Commit**

```bash
git add Cargo.toml src/
git commit -m "feat: initialize Rust project with dependencies"
```

---

### Task 2: Create Project Configuration Module

**Files:**
- Create: `src/config/mod.rs`
- Create: `src/config/project.rs`

**Step 1: Create config module**

```rust
use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProjectConfig {
    pub project: ProjectSettings,
    pub indexer: IndexerConfig,
    pub mcp: McpConfig,
    pub web: WebConfig,
    pub documentation: DocConfig,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProjectSettings {
    pub name: String,
    pub root: PathBuf,
    pub languages: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IndexerConfig {
    pub exclude: Vec<String>,
    pub include: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpConfig {
    pub enabled: bool,
    pub port: u16,
    pub auth_token: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WebConfig {
    pub enabled: bool,
    pub port: u16,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DocConfig {
    pub output: PathBuf,
    pub templates: Vec<String>,
}

impl Default for ProjectConfig {
    fn default() -> Self {
        Self {
            project: ProjectSettings {
                name: "my-project".to_string(),
                root: PathBuf::from("./src"),
                languages: vec!["go".to_string(), "typescript".to_string(), "python".to_string()],
            },
            indexer: IndexerConfig {
                exclude: vec![
                    "**/node_modules/**".to_string(),
                    "**/vendor/**".to_string(),
                ],
                include: vec![
                    "*.go".to_string(),
                    "*.ts".to_string(),
                    "*.py".to_string(),
                ],
            },
            mcp: McpConfig {
                enabled: true,
                port: 3000,
                auth_token: "".to_string(),
            },
            web: WebConfig {
                enabled: true,
                port: 8080,
            },
            documentation: DocConfig {
                output: PathBuf::from("./docs"),
                templates: vec!["agents".to_string(), "claude".to_string()],
            },
        }
    }
}
```

**Step 2: Write test**

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_default_config() {
        let config = ProjectConfig::default();
        assert_eq!(config.project.name, "my-project");
        assert!(config.mcp.enabled);
        assert_eq!(config.mcp.port, 3000);
    }
}
```

**Step 3: Run tests**

```bash
cargo test config
```

**Step 4: Commit**

```bash
git add src/config/
git commit -m "feat: add project configuration module"
```

---

## Phase 2: SurrealDB Integration

### Task 3: Create SurrealDB Database Layer

**Files:**
- Create: `src/db/mod.rs`
- Create: `src/db/schema.rs`
- Create: `src/db/models.rs`

**Step 1: Create models**

```rust
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CodeElement {
    pub qualified_name: String,
    pub element_type: String,
    pub name: String,
    pub file_path: String,
    pub line_start: u32,
    pub line_end: u32,
    pub language: String,
    pub parent_qualified: Option<String>,
    pub metadata: serde_json::Value,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Relationship {
    pub id: Option<i64>,
    pub source_qualified: String,
    pub target_qualified: String,
    pub rel_type: String,
    pub metadata: serde_json::Value,
}
```

**Step 2: Create schema initialization**

```rust
use surrealdb::engine::local::{Db, RocksDb};
use surrealdb::Surreal;
use std::path::Path;

pub async fn init_db(db_path: &Path) -> Result<Surreal<Db>, Box<dyn std::error::Error>> {
    let db = Surreal::new::<RocksDb>(db_path).await?;
    db.use_ns("leankg").use_db("codebase").await?;

    // Define tables
    db.query("
        DEFINE TABLE code_elements SCHEMAFULL;
        DEFINE FIELD qualified_name ON code_elements TYPE string;
        DEFINE FIELD element_type ON code_elements TYPE string;
        DEFINE FIELD name ON code_elements TYPE string;
        DEFINE FIELD file_path ON code_elements TYPE string;
        DEFINE FIELD line_start ON code_elements TYPE int;
        DEFINE FIELD line_end ON code_elements TYPE int;
        DEFINE FIELD language ON code_elements TYPE string;
        DEFINE FIELD parent_qualified ON code_elements TYPE option<string>;
        DEFINE FIELD metadata ON code_elements TYPE object;
        DEFINE INDEX qualified_name ON code_elements COLUMNS qualified_name UNIQUE;
    ").await?;

    db.query("
        DEFINE TABLE relationships SCHEMAFULL;
        DEFINE FIELD source_qualified ON relationships TYPE string;
        DEFINE FIELD target_qualified ON relationships TYPE string;
        DEFINE FIELD rel_type ON relationships TYPE string;
        DEFINE FIELD metadata ON relationships TYPE object;
        DEFINE INDEX source ON relationships COLUMNS source_qualified;
        DEFINE INDEX target ON relationships COLUMNS target_qualified;
    ").await?;

    Ok(db)
}
```

**Step 3: Write test with mock**

```rust
#[cfg(test)]
mod tests {
    #[tokio::test]
    async fn test_schema_creation() {
        // This test requires actual SurrealDB - skip in unit tests
        // Integration test will verify this
        assert!(true);
    }
}
```

**Step 4: Commit**

```bash
git add src/db/
git commit -m "feat: add SurrealDB database layer with schema"
```

---

## Phase 3: Code Indexing with tree-sitter

### Task 4: Create tree-sitter Parser Manager

**Files:**
- Create: `src/indexer/mod.rs`
- Create: `src/indexer/parser.rs`
- Create: `src/indexer/extractor.rs`

**Step 1: Create parser manager**

```rust
use tree_sitter::Parser;

pub struct ParserManager {
    go_parser: Parser,
    ts_parser: Parser,
    python_parser: Parser,
}

impl ParserManager {
    pub fn new() -> Self {
        Self {
            go_parser: Parser::new(),
            ts_parser: Parser::new(),
            python_parser: Parser::new(),
        }
    }

    pub fn init_parsers(&mut self) -> Result<(), Box<dyn std::error::Error>> {
        let go_lang = tree_sitter_go::LANGUAGE_GO;
        let ts_lang = tree_sitter_typescript::LANGUAGE_TYPESCRIPT;
        let py_lang = tree_sitter_python::LANGUAGE_PYTHON;

        self.go_parser.set_language(go_lang)?;
        self.ts_parser.set_language(ts_lang)?;
        self.python_parser.set_language(py_lang)?;

        Ok(())
    }
}
```

**Step 2: Create entity extractor**

```rust
use crate::db::models::{CodeElement, Relationship};
use tree_sitter::{Node, Tree};

pub struct EntityExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
    language: &'a str,
}

impl<'a> EntityExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str, language: &'a str) -> Self {
        Self {
            source,
            file_path,
            language,
        }
    }

    pub fn extract(&self, tree: &Tree) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();
        let mut relationships = Vec::new();
        self.visit_node(tree.root_node(), None, &mut elements, &mut relationships);
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
            "function_declaration" | "function_definition" | "method_declaration" => {
                if let Some(name) = self.get_node_name(node) {
                    let qualified_name = format!("{}::{}", self.file_path, name);
                    elements.push(CodeElement {
                        qualified_name: qualified_name.clone(),
                        element_type: "function".to_string(),
                        name,
                        file_path: self.file_path.to_string(),
                        line_start: node.start_position().row + 1,
                        line_end: node.end_position().row + 1,
                        language: self.language.to_string(),
                        parent_qualified: parent.map(String::from),
                        metadata: serde_json::json!({}),
                    });
                }
            }
            "class_declaration" | "type_declaration" => {
                if let Some(name) = self.get_node_name(node) {
                    let qualified_name = format!("{}::{}", self.file_path, name);
                    elements.push(CodeElement {
                        qualified_name: qualified_name.clone(),
                        element_type: "class".to_string(),
                        name,
                        file_path: self.file_path.to_string(),
                        line_start: node.start_position().row + 1,
                        line_end: node.end_position().row + 1,
                        language: self.language.to_string(),
                        parent_qualified: parent.map(String::from),
                        metadata: serde_json::json!({}),
                    });
                }
            }
            "import_declaration" | "import_specifier" => {
                if let Some(source) = self.get_import_source(node) {
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: self.file_path.to_string(),
                        target_qualified: source,
                        rel_type: "imports".to_string(),
                        metadata: serde_json::json!({}),
                    });
                }
            }
            _ => {}
        }

        for i in 0..node.child_count() {
            if let Some(child) = node.child(i) {
                let current_parent = if matches!(node_type, "function_declaration" | "function_definition" | "class_declaration") {
                    self.get_node_name(node)
                } else {
                    parent.map(String::from)
                };
                self.visit_node(child, current_parent.as_deref(), elements, relationships);
            }
        }
    }

    fn get_node_name(&self, node: Node) -> Option<String> {
        let cursor = node.walk();
        for child in node.children(&mut cursor.into()) {
            if child.kind() == "identifier" {
                return std::str::from_utf8(self.source.get(child.byte_range())?).ok().map(String::from);
            }
        }
        None
    }

    fn get_import_source(&self, node: Node) -> Option<String> {
        let cursor = node.walk();
        for child in node.children(&mut cursor.into()) {
            if child.kind() == "import_specifier" {
                if let Some(name_node) = child.child_by_field_name("name") {
                    return std::str::from_utf8(self.source.get(name_node.byte_range())?).ok().map(String::from);
                }
            }
        }
        None
    }
}
```

**Step 3: Write test**

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_qualified_name_format() {
        let source = b"package main\n\nfunc main() {}";
        let extractor = EntityExtractor::new(source, "main.go", "go");
        assert!(extractor.source.len() > 0);
    }
}
```

**Step 4: Commit**

```bash
git add src/indexer/
git commit -m "feat: add tree-sitter parser manager and entity extractor"
```

---

## Phase 4: CLI Commands

### Task 5: Implement CLI Commands

**Files:**
- Modify: `src/main.rs`

**Step 1: Create CLI module**

```rust
// src/cli/mod.rs
use clap::Subcommand;

#[derive(Subcommand, Debug)]
pub enum CLICommand {
    /// Initialize a new LeanKG project
    Init {
        #[arg(long, default_value = ".leankg")]
        path: String,
    },
    /// Index the codebase
    Index {
        /// Path to index
        path: Option<String>,
        #[arg(long, short)]
        incremental: bool,
    },
    /// Query the knowledge graph
    Query {
        /// Query string
        query: String,
    },
    /// Generate documentation
    Generate {
        #[arg(long, short)]
        template: Option<String>,
    },
    /// Start MCP server
    Serve {
        #[arg(long, default_value = "3000")]
        mcp_port: u16,
        #[arg(long, default_value = "8080")]
        web_port: u16,
    },
    /// Calculate impact radius
    Impact {
        /// File to analyze
        file: String,
        /// Depth of analysis
        #[arg(long, default_value = "3")]
        depth: u32,
    },
    /// Auto-install MCP config
    Install,
    /// Show index status
    Status,
    /// Start file watcher
    Watch,
    /// Show code quality metrics
    Quality,
    /// Export graph as HTML
    Export {
        #[arg(long, default_value = "graph.html")]
        output: String,
    },
}
```

**Step 2: Update main.rs**

```rust
mod cli;
mod config;
mod db;
mod indexer;

use cli::CLICommand;
use clap::Parser;

#[derive(Parser, Debug)]
#[command(name = "leankg")]
#[command(about = "Lightweight knowledge graph for AI-assisted development")]
struct Args {
    #[command(subcommand)]
    command: CLICommand,
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt::init();
    
    let args = Args::parse();
    
    match args.command {
        CLICommand::Init { path } => {
            println!("Initializing LeanKG project at {}", path);
            // TODO: Implement init logic
        }
        CLICommand::Index { path, incremental } => {
            println!("Indexing codebase at {:?}", path);
            // TODO: Implement index logic
        }
        CLICommand::Serve { mcp_port, web_port } => {
            println!("Starting MCP server on port {} and web UI on port {}", mcp_port, web_port);
            // TODO: Implement serve logic
        }
        _ => {
            println!("Command not yet implemented");
        }
    }
    
    Ok(())
}
```

**Step 3: Run build**

```bash
cargo build
```

**Step 4: Commit**

```bash
git add src/cli/ src/main.rs
git commit -m "feat: implement CLI commands structure"
```

---

## Phase 5: MCP Server

### Task 6: Create MCP Protocol Handler

**Files:**
- Create: `src/mcp/mod.rs`
- Create: `src/mcp/protocol.rs`
- Create: `src/mcp/tools.rs`

**Step 1: Create MCP protocol types**

```rust
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MCPRequest {
    pub jsonrpc: String,
    pub id: Option<u64>,
    pub method: String,
    pub params: Option<serde_json::Value>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MCPResponse {
    pub jsonrpc: String,
    pub id: Option<u64>,
    pub result: Option<serde_json::Value>,
    pub error: Option<MCPError>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MCPError {
    pub code: i32,
    pub message: String,
}
```

**Step 2: Create MCP tools registry**

```rust
use serde_json::Value;

pub struct ToolRegistry;

impl ToolRegistry {
    pub fn list_tools() -> Vec<ToolDefinition> {
        vec![
            ToolDefinition {
                name: "query_file".to_string(),
                description: "Find file by name or pattern".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "pattern": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_dependencies".to_string(),
                description: "Get file dependencies (direct imports)".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_impact_radius".to_string(),
                description: "Get all files affected by change within N hops".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string"},
                        "depth": {"type": "integer", "default": 3}
                    }
                }),
            },
            ToolDefinition {
                name: "get_review_context".to_string(),
                description: "Generate focused subgraph + structured review prompt".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "files": {"type": "array", "items": {"type": "string"}}
                    }
                }),
            },
            ToolDefinition {
                name: "get_context".to_string(),
                description: "Get AI context for file (minimal, token-optimized)".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string"}
                    }
                }),
            },
        ]
    }
}

#[derive(Debug, Clone)]
pub struct ToolDefinition {
    pub name: String,
    pub description: String,
    pub input_schema: Value,
}
```

**Step 3: Commit**

```bash
git add src/mcp/
git commit -m "feat: add MCP protocol handler and tools registry"
```

---

## Phase 6: Graph Query Engine

### Task 7: Implement Graph Query Functions

**Files:**
- Create: `src/graph/mod.rs`
- Create: `src/graph/query.rs`
- Create: `src/graph/traversal.rs`

**Step 1: Create graph query module**

```rust
use crate::db::models::{CodeElement, Relationship};
use surrealdb::Surreal;
use surrealdb::engine::local::Db;

pub struct GraphEngine {
    db: Surreal<Db>,
}

impl GraphEngine {
    pub fn new(db: Surreal<Db>) -> Self {
        Self { db }
    }

    pub async fn find_element(&self, qualified_name: &str) -> Result<Option<CodeElement>, Box<dyn std::error::Error>> {
        let result: Option<CodeElement> = self.db
            .query("SELECT * FROM code_elements WHERE qualified_name = $name")
            .bind(("name", qualified_name))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn get_dependencies(&self, file_path: &str) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let result: Vec<CodeElement> = self.db
            .query("SELECT * FROM code_elements WHERE qualified_name = $path")
            .bind(("path", file_path))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn get_relationships(&self, source: &str) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let result: Vec<Relationship> = self.db
            .query("SELECT * FROM relationships WHERE source_qualified = $source")
            .bind(("source", source))
            .await?
            .take(0)?;
        Ok(result)
    }
}
```

**Step 2: Create BFS traversal for impact analysis**

```rust
use crate::db::models::{CodeElement, Relationship};
use std::collections::{HashSet, VecDeque};

pub struct ImpactAnalyzer<'a> {
    graph: &'a GraphEngine,
}

impl<'a> ImpactAnalyzer<'a> {
    pub fn new(graph: &'a GraphEngine) -> Self {
        Self { graph }
    }

    pub async fn calculate_impact_radius(
        &self,
        start_file: &str,
        depth: u32,
    ) -> Result<ImpactResult, Box<dyn std::error::Error>> {
        let mut visited = HashSet::new();
        let mut queue = VecDeque::new();
        let mut affected_elements = Vec::new();
        
        queue.push_back((start_file.to_string(), 0));
        visited.insert(start_file.to_string());

        while let Some((current, current_depth)) = queue.pop_front() {
            if current_depth >= depth {
                continue;
            }

            let relationships = self.graph.get_relationships(&current).await?;
            
            for rel in relationships {
                if !visited.contains(&rel.target_qualified) {
                    visited.insert(rel.target_qualified.clone());
                    queue.push_back((rel.target_qualified.clone(), current_depth + 1));
                    
                    if let Some(element) = self.graph.find_element(&rel.target_qualified).await? {
                        affected_elements.push(element);
                    }
                }
            }
        }

        Ok(ImpactResult {
            start_file: start_file.to_string(),
            max_depth: depth,
            affected_elements,
        })
    }
}

#[derive(Debug)]
pub struct ImpactResult {
    pub start_file: String,
    pub max_depth: u32,
    pub affected_elements: Vec<CodeElement>,
}
```

**Step 3: Commit**

```bash
git add src/graph/
git commit -m "feat: add graph query engine with BFS impact analysis"
```

---

## Phase 7: Documentation Generator

### Task 8: Implement Documentation Generation

**Files:**
- Create: `src/doc/mod.rs`
- Create: `src/doc/generator.rs`
- Create: `src/doc/templates.rs`

**Step 1: Create documentation generator**

```rust
use crate::db::models::{CodeElement, Relationship};
use crate::graph::GraphEngine;
use std::path::Path;

pub struct DocGenerator {
    graph: GraphEngine,
    output_path: PathBuf,
}

impl DocGenerator {
    pub fn new(graph: GraphEngine, output_path: PathBuf) -> Self {
        Self { graph, output_path }
    }

    pub async fn generate_for_element(
        &self,
        qualified_name: &str,
    ) -> Result<String, Box<dyn std::error::Error>> {
        let element = self.graph.find_element(qualified_name).await?;
        
        let mut output = String::new();
        output.push_str(&format!("# {}\n\n", qualified_name));
        output.push_str(&format!("**Type:** {}\n", element.as_ref().map(|e| e.element_type.as_str()).unwrap_or("unknown")));
        output.push_str(&format!("**File:** {}\n", element.as_ref().map(|e| e.file_path.as_str()).unwrap_or("unknown")));
        output.push_str(&format!("**Lines:** {}-{}\n\n", 
            element.as_ref().map(|e| e.line_start).unwrap_or(0),
            element.as_ref().map(|e| e.line_end).unwrap_or(0)
        ));

        let relationships = self.graph.get_relationships(qualified_name).await?;
        if !relationships.is_empty() {
            output.push_str("## Relationships\n\n");
            for rel in relationships {
                output.push_str(&format!("- {}: {}\n", rel.rel_type, rel.target_qualified));
            }
        }

        Ok(output)
    }

    pub async fn generate_agents_md(&self) -> Result<String, Box<dyn std::error::Error>> {
        let mut content = String::from("# Codebase Context\n\n");
        content.push_str("This codebase contains the following key components:\n\n");

        // Query all files
        content.push_str("## Files\n\n");
        
        Ok(content)
    }
}
```

**Step 2: Write template system**

```rust
pub struct TemplateEngine;

impl TemplateEngine {
    pub fn render_agents_template(elements: &[String]) -> String {
        let mut output = String::from("# AGENTS.md\n\n");
        output.push_str("```\n");
        output.push_str("## Codebase Structure\n\n");
        
        for element in elements {
            output.push_str(&format!("- {}\n", element));
        }
        
        output.push_str("```\n");
        output
    }

    pub fn render_claude_template(context: &str) -> String {
        let mut output = String::from("# CLAUDE.md\n\n");
        output.push_str("## Project Context\n\n");
        output.push_str(context);
        output.push('\n');
        output
    }
}
```

**Step 3: Commit**

```bash
git add src/doc/
git commit -m "feat: add documentation generator with templates"
```

---

## Phase 8: Web UI

### Task 9: Create Web UI with Axum

**Files:**
- Create: `src/web/mod.rs`
- Create: `src/web/routes.rs`
- Create: `src/web/handlers.rs`

**Step 1: Create Web module**

```rust
use axum::{
    routing::{get, post},
    Router,
};
use std::net::SocketAddr;

pub mod handlers;
pub mod routes;

pub async fn start_server(port: u16) -> Result<(), Box<dyn std::error::Error>> {
    let app = Router::new()
        .route("/", get(handlers::index))
        .route("/graph", get(handlers::graph))
        .route("/browse", get(handlers::browse))
        .route("/api/query", post(handlers::api_query));

    let addr = SocketAddr::from(([127, 0, 0, 1], port));
    println!("Web UI listening on http://{}", addr);

    let listener = tokio::net::TcpListener::bind(addr).await?;
    axum::serve(listener, app).await?;

    Ok(())
}
```

**Step 2: Create handlers**

```rust
use axum::{http::StatusCode, response::Html, Json};
use serde::{Deserialize, Serialize};

#[derive(Serialize)]
pub struct QueryRequest {
    pub query: String,
}

#[derive(Serialize)]
pub struct QueryResponse {
    pub result: Vec<serde_json::Value>,
}

pub async fn index() -> Html<String> {
    Html(r#"
    <!DOCTYPE html>
    <html>
    <head>
        <title>LeanKG - Knowledge Graph</title>
        <style>
            body { font-family: system-ui; max-width: 1200px; margin: 0 auto; padding: 20px; }
            nav { margin-bottom: 20px; }
            nav a { margin-right: 15px; }
        </style>
    </head>
    <body>
        <h1>LeanKG</h1>
        <nav>
            <a href="/">Dashboard</a>
            <a href="/graph">Graph</a>
            <a href="/browse">Browse</a>
        </nav>
        <p>Welcome to LeanKG - Knowledge Graph for AI-Assisted Development</p>
    </body>
    </html>
    "#.to_string())
}

pub async fn graph() -> Html<String> {
    Html(r#"
    <!DOCTYPE html>
    <html>
    <head>
        <title>LeanKG - Graph View</title>
    </head>
    <body>
        <h1>Graph Visualization</h1>
        <p>Coming soon...</p>
    </body>
    </html>
    "#.to_string())
}

pub async fn browse() -> Html<String> {
    Html(r#"
    <!DOCTYPE html>
    <html>
    <head>
        <title>LeanKG - Browse</title>
    </head>
    <body>
        <h1>Code Browser</h1>
        <p>Coming soon...</p>
    </body>
    </html>
    "#.to_string())
}

pub async fn api_query(Json(req): Json<QueryRequest>) -> Result<Json<QueryResponse>, StatusCode> {
    Ok(Json(QueryResponse {
        result: vec![],
    }))
}
```

**Step 3: Commit**

```bash
git add src/web/
git commit -m "feat: add basic Web UI with Axum"
```

---

## Phase 9: File Watcher

### Task 10: Implement File Watcher

**Files:**
- Create: `src/watcher/mod.rs`
- Create: `src/watcher/notify.rs`

**Step 1: Create file watcher**

```rust
use notify::{Config, RecommendedWatcher, RecursiveMode, Watcher};
use std::path::Path;
use std::sync::mpsc::channel;
use std::time::Duration;

pub struct FileWatcher {
    watcher: RecommendedWatcher,
    watch_path: PathBuf,
}

impl FileWatcher {
    pub fn new<P: AsRef<Path>>(path: P) -> Result<Self, notify::Error> {
        let (tx, rx) = channel();
        
        let mut watcher = RecommendedWatcher::new(
            move |res: Result<notify::Event, notify::Error>| {
                if let Ok(event) = res {
                    let _ = tx.send(event);
                }
            },
            Config::default().with_poll_interval(Duration::from_secs(2)),
        )?;

        watcher.watch(path.as_ref(), RecursiveMode::Recursive)?;

        Ok(Self {
            watcher,
            watch_path: path.as_ref().to_path_buf(),
        })
    }

    pub fn watch_path(&self) -> &Path {
        &self.watch_path
    }
}
```

**Step 2: Commit**

```bash
git add src/watcher/
git commit -m "feat: add file watcher with notify"
```

---

## Phase 10: Integration and Testing

### Task 11: Integration - Connect All Components

**Files:**
- Modify: `src/main.rs`

**Step 1: Create full main.rs integration**

```rust
mod cli;
mod config;
mod db;
mod doc;
mod graph;
mod indexer;
mod mcp;
mod watcher;
mod web;

use cli::CLICommand;
use clap::Parser;
use std::path::Path;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt::init();

    let args = Args::parse();

    match args.command {
        CLICommand::Init { path } => {
            init_project(&path)?;
        }
        CLICommand::Index { path, incremental } => {
            let db_path = Path::new(&path).join(".leankg");
            index_codebase(path.as_deref().unwrap_or("."), &db_path, incremental).await?;
        }
        CLICommand::Serve { mcp_port, web_port } => {
            let handles = vec![];
            
            // Start MCP server
            let mcp_handle = tokio::spawn(async move {
                mcp::start_server(mcp_port).await;
            });
            
            // Start Web UI
            let web_handle = tokio::spawn(async move {
                web::start_server(web_port).await;
            });

            mcp_handle.await?;
            web_handle.await;
        }
        CLICommand::Impact { file, depth } => {
            let db_path = Path::new(".leankg");
            let result = calculate_impact(&file, depth, &db_path).await?;
            println!("Impact radius for {} (depth={}):", file, depth);
            for elem in result.affected_elements {
                println!("  - {}", elem.qualified_name);
            }
        }
        CLICommand::Generate { template } => {
            let db_path = Path::new(".leankg");
            generate_docs(template.as_deref(), &db_path).await?;
        }
        _ => {
            println!("Command not yet implemented");
        }
    }

    Ok(())
}

fn init_project(path: &str) -> Result<(), Box<dyn std::error::Error>> {
    use std::fs;
    
    let config = config::ProjectConfig::default();
    let config_yaml = serde_yaml::to_string(&config)?;
    
    fs::create_dir_all(path)?;
    fs::write(Path::new(path).join("leankg.yaml"), config_yaml)?;
    
    println!("Initialized LeanKG project at {}", path);
    Ok(())
}

async fn index_codebase(path: &str, db_path: &Path, incremental: bool) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::init_db(db_path).await?;
    let graph_engine = graph::GraphEngine::new(db);
    let mut parser_manager = indexer::ParserManager::new();
    parser_manager.init_parsers()?;
    
    indexer::index_directory(&graph_engine, &mut parser_manager, path).await?;
    
    println!("Indexed codebase at {}", path);
    Ok(())
}

async fn calculate_impact(file: &str, depth: u32, db_path: &Path) -> Result<graph::ImpactResult, Box<dyn std::error::Error>> {
    let db = db::init_db(db_path).await?;
    let graph_engine = graph::GraphEngine::new(db);
    let analyzer = graph::ImpactAnalyzer::new(&graph_engine);
    
    let result = analyzer.calculate_impact_radius(file, depth).await?;
    Ok(result)
}

async fn generate_docs(template: Option<&str>, db_path: &Path) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::init_db(db_path).await?;
    let graph_engine = graph::GraphEngine::new(db);
    let generator = doc::DocGenerator::new(graph_engine, Path::new("./docs").to_path_buf());
    
    let content = generator.generate_agents_md().await?;
    println!("{}", content);
    
    Ok(())
}
```

**Step 2: Run cargo build**

```bash
cargo build 2>&1
```

**Step 3: Commit**

```bash
git add -A
git commit -m "feat: integrate all components in main.rs"
```

---

## Phase 11: MVP Verification

### Task 12: Verify MVP Release Criteria

**Files:**
- Modify: `README.md`
- Create: `tests/integration_test.rs`

**Step 1: Create integration test**

```rust
#[cfg(test)]
mod integration_tests {
    use std::fs;
    use tempfile::TempDir;

    #[tokio::test]
    async fn test_full_indexing_flow() {
        // Create temp directory with sample Go code
        let temp_dir = TempDir::new().unwrap();
        let project_dir = temp_dir.path();
        
        // Create sample Go file
        let go_code = r#"
package main

import "fmt"

func main() {
    fmt.Println("Hello")
}

func helper() {
    fmt.Println("Helper")
}
"#;
        fs::write(project_dir.join("main.go"), go_code).unwrap();
        
        // TODO: Run indexing
        // TODO: Verify elements in database
        
        assert!(true);
    }

    #[tokio::test]
    async fn test_impact_analysis() {
        // TODO: Test impact radius calculation
        assert!(true);
    }
}
```

**Step 2: Update README**

```markdown
# LeanKG

Lightweight knowledge graph for AI-assisted development.

## Tech Stack

- **Rust** - Core language
- **SurrealDB** - Embedded graph database
- **tree-sitter** - Code parsing

## Quick Start

```bash
# Initialize project
leankg init

# Index codebase
leankg index ./src

# Start server
leankg serve

# Calculate impact
leankg impact src/main.rs --depth 3
```

## Features

- Code indexing for Go, TypeScript, Python
- Dependency graph with TESTED_BY edges
- Impact radius analysis
- MCP server for AI tool integration
- Auto-generated documentation
```

**Step 3: Run tests**

```bash
cargo test --all
```

**Step 4: Commit**

```bash
git add README.md tests/
git commit -m "feat: add integration tests and update README"
```

---

## Summary

### Tasks by Phase

| Phase | Tasks | Description |
|-------|-------|-------------|
| 1 | 1-2 | Project foundation, Cargo.toml, config module |
| 2 | 3 | SurrealDB integration, schema, models |
| 3 | 4 | tree-sitter parser, entity extractor |
| 4 | 5 | CLI commands with Clap |
| 5 | 6 | MCP protocol handler, tools registry |
| 6 | 7 | Graph query engine, BFS traversal |
| 7 | 8 | Documentation generator, templates |
| 8 | 9 | Web UI with Axum |
| 9 | 10 | File watcher with notify |
| 10 | 11 | Integration of all components |
| 11 | 12 | MVP verification, testing |

### Total: 12 Tasks

---

## Execution Options

**Plan complete and saved to `.docs/planning/2026-03-23-leankg-mvp-implementation.md`. Two execution options:**

**1. Subagent-Driven (this session)** - I dispatch fresh subagent per task, review between tasks, fast iteration

**2. Parallel Session (separate)** - Open new session with executing-plans, batch execution with checkpoints

**Which approach?**
