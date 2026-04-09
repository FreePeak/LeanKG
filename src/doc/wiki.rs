use crate::db::models::{BusinessLogic, CodeElement, Relationship};
use crate::graph::clustering::{Cluster, ClusterStats};
use crate::graph::{CommunityDetector, GraphEngine};
use std::collections::HashMap;
use std::fs;
use std::path::PathBuf;

#[derive(Debug, thiserror::Error)]
pub enum WikiError {
    #[error("Element not found: {0}")]
    NotFound(String),
    #[error("Database error: {0}")]
    Database(String),
    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),
    #[error("Cluster error: {0}")]
    Cluster(String),
}

#[derive(Debug, Clone)]
pub struct WikiStats {
    pub pages_generated: usize,
    pub elements_documented: usize,
    pub mermaid_diagrams: usize,
}

pub struct WikiGenerator<'a> {
    graph: &'a GraphEngine,
    output_path: PathBuf,
}

impl<'a> WikiGenerator<'a> {
    pub fn new(graph: &'a GraphEngine, output_path: PathBuf) -> Self {
        Self { graph, output_path }
    }

    pub fn generate(&self) -> Result<WikiStats, WikiError> {
        let mut pages_generated = 0;
        let mut elements_documented = 0;
        let mut mermaid_diagrams = 0;

        let wiki_dirs = [
            "architecture",
            "api",
            "clusters",
            "annotations/by-feature",
            "god-nodes",
        ];
        for dir in &wiki_dirs {
            fs::create_dir_all(self.output_path.join(dir))?;
        }

        let elements = self
            .graph
            .all_elements()
            .map_err(|e| WikiError::Database(e.to_string()))?;
        let relationships = self
            .graph
            .all_relationships()
            .map_err(|e| WikiError::Database(e.to_string()))?;
        elements_documented = elements.len();

        self.generate_overview(&elements, &relationships)?;
        pages_generated += 1;

        self.generate_architecture_pages(&elements, &relationships)?;
        pages_generated += 3;

        let (cluster_pages, cluster_mermaid) = self.generate_cluster_pages()?;
        pages_generated += cluster_pages;
        mermaid_diagrams += cluster_mermaid;

        let cli_pages = self.generate_cli_commands_page();
        pages_generated += cli_pages;

        let mcp_pages = self.generate_mcp_tools_page();
        pages_generated += mcp_pages;

        let god_pages = self.generate_god_nodes_page()?;
        pages_generated += god_pages;

        let (anno_pages, anno_mermaid) = self.generate_annotations_pages()?;
        pages_generated += anno_pages;
        mermaid_diagrams += anno_mermaid;

        let diagrams = self.generate_mermaid_diagrams()?;
        mermaid_diagrams += diagrams.len();

        Ok(WikiStats {
            pages_generated,
            elements_documented,
            mermaid_diagrams,
        })
    }

    fn generate_overview(
        &self,
        elements: &[CodeElement],
        relationships: &[Relationship],
    ) -> Result<(), WikiError> {
        let mut content = String::from("# LeanKG Knowledge Graph\n\n");
        content.push_str("> Auto-generated documentation from code analysis\n\n");

        content.push_str("## Project Overview\n\n");
        content.push_str("LeanKG is a Rust-based knowledge graph system that indexes codebases using tree-sitter parsers, stores data in CozoDB, and exposes functionality via CLI and MCP protocol.\n\n");

        content.push_str("---\n\n## Statistics\n\n");
        let files = elements.iter().filter(|e| e.element_type == "file").count();
        let functions = elements
            .iter()
            .filter(|e| e.element_type == "function")
            .count();
        let classes = elements
            .iter()
            .filter(|e| e.element_type == "class" || e.element_type == "struct")
            .count();
        let modules = elements
            .iter()
            .filter(|e| e.element_type == "module")
            .count();

        content.push_str(&format!("| Metric | Count |\n"));
        content.push_str(&format!("|--------|-------|\n"));
        content.push_str(&format!("| Total Elements | {} |\n", elements.len()));
        content.push_str(&format!("| Files | {} |\n", files));
        content.push_str(&format!("| Functions | {} |\n", functions));
        content.push_str(&format!("| Classes/Structs | {} |\n", classes));
        content.push_str(&format!("| Modules | {} |\n", modules));
        content.push_str(&format!("| Relationships | {} |\n", relationships.len()));
        content.push_str("\n---\n\n");

        let mut rel_types: HashMap<&str, usize> = HashMap::new();
        for rel in relationships {
            *rel_types.entry(rel.rel_type.as_str()).or_insert(0) += 1;
        }
        let mut sorted_rel_types: Vec<_> = rel_types.iter().collect();
        sorted_rel_types.sort_by_key(|(_, count)| *count);
        sorted_rel_types.reverse();

        content.push_str("## Relationship Types\n\n");
        content.push_str(&format!("| Type | Count |\n"));
        content.push_str("|------|-------|\n");
        for (rel_type, count) in sorted_rel_types.iter().take(10) {
            content.push_str(&format!("| `{}` | {} |\n", rel_type, count));
        }
        content.push_str("\n---\n\n");

        content.push_str("## Quick Navigation\n\n");
        content.push_str("- [Architecture Overview](architecture/overview.md)\n");
        content.push_str("- [Module Dependencies](architecture/modules.md)\n");
        content.push_str("- [Data Flow Diagrams](architecture/data-flow.md)\n");
        content.push_str("- [CLI Commands](api/cli-commands.md)\n");
        content.push_str("- [MCP Tools](api/mcp-tools.md)\n");
        content.push_str("- [Code Clusters](clusters/index.md)\n");
        content.push_str("- [High-Connectivity Elements](god-nodes.md)\n");
        content.push_str("- [Annotations](annotations/index.md)\n");

        let index_path = self.output_path.join("index.md");
        fs::write(index_path, &content)?;
        Ok(())
    }

    fn generate_architecture_pages(
        &self,
        elements: &[CodeElement],
        relationships: &[Relationship],
    ) -> Result<(), WikiError> {
        self.generate_architecture_overview(elements)?;
        self.generate_module_dependencies(elements, relationships)?;
        self.generate_data_flow_diagram(relationships)?;
        Ok(())
    }

    fn generate_architecture_overview(&self, elements: &[CodeElement]) -> Result<(), WikiError> {
        let mut content = String::from("# Architecture Overview\n\n");

        content.push_str("## System Architecture\n\n");
        content.push_str("```\n┌─────────────────────────────────────────────────────────────┐\n");
        content.push_str("│                      CLI Layer                       │\n");
        content.push_str("│  init │ index │ serve │ impact │ query │ export │ ... │\n");
        content.push_str("└──────────────────────┬──────────────────────────────────┘\n");
        content.push_str("                       │\n");
        content.push_str("┌──────────────────────▼──────────────────────────────────┐\n");
        content.push_str("│                   Graph Engine                        │\n");
        content.push_str("│  Query │ Traversal │ Impact Analysis │ Clustering   │\n");
        content.push_str("└──────────────────────┬──────────────────────────────────┘\n");
        content.push_str("                       │\n");
        content.push_str("┌──────────────────────▼──────────────────────────────────┐\n");
        content.push_str("│                     CozoDB                          │\n");
        content.push_str("│  code_elements │ relationships │ business_logic      │\n");
        content.push_str("└─────────────────────────────────────────────────────┘\n");
        content.push_str("```\n\n");

        content.push_str("## Module Structure\n\n");
        content.push_str("| Module | Description |\n");
        content.push_str("|--------|-------------|\n");
        content.push_str("| `cli/` | Clap CLI commands |\n");
        content.push_str("| `config/` | Project configuration |\n");
        content.push_str("| `db/` | CozoDB layer (models, schema) |\n");
        content.push_str("| `doc/` | Documentation generator |\n");
        content.push_str("| `graph/` | Graph engine, query, traversal |\n");
        content.push_str("| `indexer/` | tree-sitter parsers, entity extraction |\n");
        content.push_str("| `mcp/` | MCP protocol implementation |\n");
        content.push_str("| `watcher/` | File system watcher |\n");
        content.push_str("| `web/` | Axum web server |\n\n");

        let path = self.output_path.join("architecture/overview.md");
        fs::write(path, &content)?;
        Ok(())
    }

    fn generate_module_dependencies(
        &self,
        elements: &[CodeElement],
        relationships: &[Relationship],
    ) -> Result<(), WikiError> {
        let mut content = String::from("# Module Dependencies\n\n");
        content.push_str("## Dependency Graph\n\n");
        content.push_str("```mermaid\n");
        content.push_str("graph TB\n");

        let mut module_deps: HashMap<String, Vec<String>> = HashMap::new();
        for rel in relationships {
            if rel.rel_type == "imports" {
                let source_module = rel
                    .source_qualified
                    .split("::")
                    .next()
                    .unwrap_or("")
                    .to_string();
                let target_module = rel
                    .target_qualified
                    .split("::")
                    .next()
                    .unwrap_or("")
                    .to_string();
                if !source_module.is_empty()
                    && !target_module.is_empty()
                    && source_module != target_module
                {
                    module_deps
                        .entry(source_module.clone())
                        .or_default()
                        .push(target_module);
                }
            }
        }

        let mut processed: HashSet<String> = HashSet::new();
        for (module, deps) in &module_deps {
            for dep in deps {
                if processed.insert(format!("{}-->{}", module, dep)) {
                    content.push_str(&format!("    {} --> {}\n", module, dep));
                }
            }
        }
        content.push_str("```\n\n");

        content.push_str("## Direct Dependencies\n\n");
        let mut sorted_deps: Vec<_> = module_deps.iter().collect();
        sorted_deps.sort_by_key(|(k, _)| *k);
        for (module, deps) in sorted_deps {
            content.push_str(&format!("### `{}`\n\n", module));
            for dep in deps {
                content.push_str(&format!("- {}\n", dep));
            }
            content.push('\n');
        }

        let path = self.output_path.join("architecture/modules.md");
        fs::write(path, &content)?;
        Ok(())
    }

    fn generate_data_flow_diagram(&self, relationships: &[Relationship]) -> Result<(), WikiError> {
        let mut content = String::from("# Data Flow Diagrams\n\n");
        content.push_str("## Relationship Flow\n\n");
        content.push_str("```mermaid\n");
        content.push_str("flowchart LR\n");
        content.push_str("    subgraph Elements\n");
        content.push_str("        E1[Code Elements]\n");
        content.push_str("        E2[Functions]\n");
        content.push_str("        E3[Classes]\n");
        content.push_str("        E4[Modules]\n");
        content.push_str("    end\n");
        content.push_str("    subgraph Relationships\n");
        content.push_str("        R1[imports]\n");
        content.push_str("        R2[calls]\n");
        content.push_str("        R3[references]\n");
        content.push_str("        R4[tested_by]\n");
        content.push_str("    end\n");
        content.push_str("    E1 --> R1\n");
        content.push_str("    E2 --> R2\n");
        content.push_str("    E3 --> R3\n");
        content.push_str("    E4 --> R4\n");
        content.push_str("```\n\n");

        content.push_str("## Call Graph Example\n\n");
        content.push_str("```mermaid\n");
        content.push_str("graph TD\n");

        let call_rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "calls")
            .take(20)
            .collect();

        for rel in &call_rels {
            let source_short = rel.source_qualified.split("::").last().unwrap_or("src");
            let target_short = rel.target_qualified.split("::").last().unwrap_or("tgt");
            content.push_str(&format!(
                "    {} --> {}:{}\n",
                source_short, target_short, rel.rel_type
            ));
        }
        content.push_str("```\n\n");

        let path = self.output_path.join("architecture/data-flow.md");
        fs::write(path, &content)?;
        Ok(())
    }

    fn generate_cluster_pages(&self) -> Result<(usize, usize), WikiError> {
        let db = self.graph.db();
        let detector = CommunityDetector::new(db);
        let clusters = detector
            .detect_communities()
            .map_err(|e| WikiError::Cluster(e.to_string()))?;

        if clusters.is_empty() {
            let content =
                "# Clusters\n\nNo clusters detected. Run `leankg detect-clusters` first.\n";
            fs::write(self.output_path.join("clusters/index.md"), &content)?;
            return Ok((1, 0));
        }

        let stats = ClusterStats {
            total_clusters: clusters.len(),
            total_members: clusters.values().map(|c| c.members.len()).sum(),
            avg_cluster_size: if clusters.is_empty() {
                0.0
            } else {
                clusters.values().map(|c| c.members.len()).sum::<usize>() as f64
                    / clusters.len() as f64
            },
        };

        let mut content = String::from("# Code Clusters\n\n");
        content.push_str(&format!(
            "Detected {} functional clusters in the codebase.\n\n",
            clusters.len()
        ));
        content.push_str(&format!("| Metric | Value |\n"));
        content.push_str(&format!("|--------|-------|\n"));
        content.push_str(&format!("| Total Clusters | {} |\n", stats.total_clusters));
        content.push_str(&format!("| Total Members | {} |\n", stats.total_members));
        content.push_str(&format!(
            "| Avg Cluster Size | {:.1} |\n\n",
            stats.avg_cluster_size
        ));

        content.push_str("## Cluster List\n\n");
        content.push_str("| Cluster | Label | Members | Representative Files |\n");
        content.push_str("|---------|-------|--------|----------------------|\n");

        let mut sorted_clusters: Vec<_> = clusters.values().collect();
        sorted_clusters.sort_by(|a, b| b.members.len().cmp(&a.members.len()));

        for cluster in &sorted_clusters {
            let files_str = cluster.representative_files.join(", ");
            content.push_str(&format!(
                "| `{}` | {} | {} | {} |\n",
                cluster.id,
                cluster.label,
                cluster.members.len(),
                files_str
            ));
        }
        content.push('\n');

        let index_path = self.output_path.join("clusters/index.md");
        fs::write(index_path, &content)?;

        let mut pages = 1;
        let mut mermaid_count = 1;

        let cluster_content = format!(
            r#"# Cluster Relationship Graph

```mermaid
graph LR
{}
```

## Cluster Details

"#,
            sorted_clusters
                .iter()
                .map(|c| {
                    let members_str: String = c
                        .members
                        .iter()
                        .take(3)
                        .map(|s| s.as_str())
                        .collect::<Vec<_>>()
                        .join(", ");
                    format!(
                        "    subgraph {}\n        {} [{}]\n    end",
                        c.label, c.id, members_str
                    )
                })
                .collect::<Vec<_>>()
                .join("\n")
        );
        let graph_path = self.output_path.join("clusters/cluster-graph.md");
        fs::write(graph_path, &cluster_content)?;
        pages += 1;

        for cluster in sorted_clusters {
            let cluster_file_name = format!("cluster-{}.md", cluster.id.replace("cluster_", ""));
            let mut cluster_md = String::new();
            cluster_md.push_str(&format!("# Cluster: {}\n\n", cluster.label));
            cluster_md.push_str(&format!("**ID:** `{}`\n\n", cluster.id));
            cluster_md.push_str(&format!(
                "**Members:** {} elements\n\n",
                cluster.members.len()
            ));
            cluster_md.push_str("## Representative Files\n\n");
            for file in &cluster.representative_files {
                cluster_md.push_str(&format!("- `{}`\n", file));
            }
            cluster_md.push_str("\n## Members\n\n");
            for member in &cluster.members {
                cluster_md.push_str(&format!("- `{}`\n", member));
            }

            let path = self.output_path.join("clusters").join(&cluster_file_name);
            fs::write(path, &cluster_md)?;
            pages += 1;
        }

        Ok((pages, mermaid_count))
    }

    fn generate_cli_commands_page(&self) -> usize {
        let mut content = String::from("# CLI Commands\n\n");
        content.push_str("> Auto-generated from Clap definitions\n\n");
        content.push_str("## Command Reference\n\n");
        content.push_str("### init\n\nInitialize a new LeanKG project.\n\n```bash\nleankg init [--path <path>]\n```\n\n");
        content.push_str("### index\n\nIndex the codebase.\n\n```bash\nleankg index [--path <path>] [--incremental] [--lang <lang>] [--exclude <patterns>]\n```\n\n");
        content.push_str(
            "### serve\n\nStart web UI server.\n\n```bash\nleankg serve [--port <port>]\n```\n\n",
        );
        content.push_str("### impact\n\nCalculate impact radius for a file.\n\n```bash\nleankg impact <file> [--depth <depth>]\n```\n\n");
        content.push_str("### query\n\nQuery the knowledge graph.\n\n```bash\nleankg query <query> [--kind name|type|rel|pattern]\n```\n\n");
        content.push_str("### export\n\nExport knowledge graph.\n\n```bash\nleankg export [--output <file>] [--format json|dot|mermaid] [--file <file>] [--depth <depth>]\n```\n\n");
        content.push_str("### annotate\n\nAnnotate code element with business logic.\n\n```bash\nleankg annotate <element> --description <desc> [--user-story <id>] [--feature <id>]\n```\n\n");
        content.push_str("### trace\n\nShow feature-to-code traceability.\n\n```bash\nleankg trace [--feature <id>] [--user-story <id>] [--all]\n```\n\n");
        content.push_str("### detect-clusters\n\nRun community detection.\n\n```bash\nleankg detect-clusters [--path <path>] [--min-hub-edges <n>]\n```\n\n");
        content.push_str("### mcp-stdio\n\nStart MCP server with stdio transport.\n\n```bash\nleankg mcp-stdio [--watch]\n```\n\n");

        let path = self.output_path.join("api/cli-commands.md");
        fs::write(path, &content).ok();
        1
    }

    fn generate_mcp_tools_page(&self) -> usize {
        let mut content = String::from("# MCP Tools Reference\n\n");
        content.push_str("> Auto-generated MCP tool definitions\n\n");
        content.push_str("## Tool Reference\n\n");
        content.push_str("### search_code\n\nSearch code elements by name or type.\n\n**Parameters:**\n- `query` (string): Search query\n- `element_type` (string, optional): Filter by type\n- `limit` (number, optional): Max results\n\n");
        content.push_str("### find_function\n\nLocate function definition by name.\n\n**Parameters:**\n- `name` (string): Function name\n- `file` (string, optional): File scope\n\n");
        content.push_str("### get_impact_radius\n\nCalculate blast radius for a file change.\n\n**Parameters:**\n- `file` (string): File to analyze\n- `depth` (number): Hop depth\n- `min_confidence` (number, optional): Confidence threshold\n\n");
        content.push_str("### get_dependencies\n\nGet direct imports of a file.\n\n**Parameters:**\n- `file` (string): File to get dependencies for\n\n");
        content.push_str("### get_dependents\n\nGet files depending on target.\n\n\n**Parameters:**\n- `file` (string): File to get dependents for\n\n");
        content.push_str("### get_context\n\nGet AI-optimized context for a file.\n\n**Parameters:**\n- `file` (string): File to get context for\n- `max_tokens` (number, optional): Token budget\n- `signature_only` (boolean, optional): Return only signatures\n\n");
        content.push_str("### get_tested_by\n\nGet test coverage for a function/file.\n\n**Parameters:**\n- `file` (string): File to get test coverage for\n\n");
        content.push_str("### get_call_graph\n\nGet bounded function call chain.\n\n**Parameters:**\n- `function` (string): Function to get call graph for\n- `depth` (number, optional): Max depth\n- `max_results` (number, optional): Max results\n\n");

        let path = self.output_path.join("api/mcp-tools.md");
        fs::write(path, &content).ok();
        1
    }

    fn generate_god_nodes_page(&self) -> Result<usize, WikiError> {
        let elements = self
            .graph
            .all_elements()
            .map_err(|e| WikiError::Database(e.to_string()))?;
        let relationships = self
            .graph
            .all_relationships()
            .map_err(|e| WikiError::Database(e.to_string()))?;

        let mut connectivity: HashMap<String, usize> = HashMap::new();
        for rel in &relationships {
            *connectivity
                .entry(rel.source_qualified.clone())
                .or_insert(0) += 1;
            *connectivity
                .entry(rel.target_qualified.clone())
                .or_insert(0) += 1;
        }

        let mut god_nodes: Vec<_> = connectivity
            .iter()
            .filter(|(_, count)| **count >= 5)
            .collect();
        god_nodes.sort_by(|a, b| b.1.cmp(a.1));

        let mut content = String::from("# High-Connectivity Elements (God Nodes)\n\n");
        content.push_str(&format!(
            "Elements with {} or more connections. These are critical hubs in the codebase.\n\n",
            5
        ));
        content.push_str("| Element | Connections | Type |\n");
        content.push_str("|---------|-------------|------|\n");

        for (elem_qn, count) in god_nodes.iter().take(30) {
            let elem_type = elements
                .iter()
                .find(|e| &e.qualified_name == *elem_qn)
                .map(|e| e.element_type.as_str())
                .unwrap_or("unknown");
            content.push_str(&format!("| `{}` | {} | {} |\n", elem_qn, count, elem_type));
        }
        content.push('\n');

        content.push_str("## Connectivity Distribution\n\n");
        content.push_str("```mermaid\n");
        content.push_str("graph TD\n");
        content.push_str("    subgraph GodNodes\n");
        for (elem_qn, count) in god_nodes.iter().take(10) {
            let short_name = elem_qn.split("::").last().unwrap_or(elem_qn);
            content.push_str(&format!(
                "        {}[\"{}\\n{} connections\"]\n",
                elem_qn
                    .replace("::", "_")
                    .replace("-", "_")
                    .replace(".", "_"),
                short_name,
                count
            ));
        }
        content.push_str("    end\n");
        content.push_str("```\n\n");

        let path = self.output_path.join("god-nodes.md");
        fs::write(path, &content)?;
        Ok(1)
    }

    fn generate_annotations_pages(&self) -> Result<(usize, usize), WikiError> {
        let annotations = self
            .graph
            .all_annotations()
            .map_err(|e| WikiError::Database(e.to_string()))?;

        let mut content = String::from("# Annotations\n\n");
        content.push_str(&format!("{} total annotations.\n\n", annotations.len()));
        content.push_str("## All Annotations\n\n");
        content.push_str("| Element | Description | User Story | Feature |\n");
        content.push_str("|---------|-------------|------------|---------|\n");

        for ann in annotations.iter().take(50) {
            let story = ann.user_story_id.as_deref().unwrap_or("-");
            let feature = ann.feature_id.as_deref().unwrap_or("-");
            content.push_str(&format!(
                "| `{}` | {} | {} | {} |\n",
                ann.element_qualified, ann.description, story, feature
            ));
        }
        if annotations.len() > 50 {
            content.push_str(&format!(
                "\n_... and {} more annotations_\n",
                annotations.len() - 50
            ));
        }

        let path = self.output_path.join("annotations/index.md");
        fs::write(path, &content)?;

        let mut by_feature: HashMap<String, Vec<&BusinessLogic>> = HashMap::new();
        for ann in &annotations {
            if let Some(ref feat) = ann.feature_id {
                by_feature.entry(feat.clone()).or_default().push(ann);
            }
        }

        if !by_feature.is_empty() {
            content.clear();
            content.push_str("# Annotations by Feature\n\n");

            let mut sorted_features: Vec<_> = by_feature.iter().collect();
            sorted_features.sort_by_key(|(k, _)| *k);

            for (feature, anns) in &sorted_features {
                content.push_str(&format!("## {}\n\n", feature));
                for ann in *anns {
                    content.push_str(&format!(
                        "- `{}`: {}\n",
                        ann.element_qualified, ann.description
                    ));
                }
                content.push('\n');
            }

            let path = self.output_path.join("annotations/by-feature/index.md");
            fs::write(path, &content)?;
        }

        let mut diagrams_content =
            String::from("# Annotation Traceability\n\n```mermaid\nflowchart LR\n");
        diagrams_content.push_str(
            &annotations
                .iter()
                .take(20)
                .map(|ann| {
                    let feat = ann.feature_id.as_deref().unwrap_or("unlinked");
                    format!(
                        "    {}[\"{}\"] -->|{}| F[\"{}\"]",
                        ann.element_qualified
                            .split("::")
                            .last()
                            .unwrap_or("elem")
                            .replace("-", "_"),
                        ann.element_qualified.split("::").last().unwrap_or("elem"),
                        feat,
                        feat
                    )
                })
                .collect::<Vec<_>>()
                .join("\n"),
        );
        diagrams_content.push_str("\n```\n\n");

        let path = self.output_path.join("annotations/traceability-diagram.md");
        fs::write(path, &diagrams_content).ok();

        Ok((2, 1))
    }

    fn generate_mermaid_diagrams(&self) -> Result<HashMap<String, String>, WikiError> {
        let relationships = self
            .graph
            .all_relationships()
            .map_err(|e| WikiError::Database(e.to_string()))?;
        let elements = self
            .graph
            .all_elements()
            .map_err(|e| WikiError::Database(e.to_string()))?;

        let mut diagrams = HashMap::new();

        let call_graph = self.generate_call_graph_mermaid(&relationships);
        diagrams.insert("call-graph".to_string(), call_graph);

        let module_graph = self.generate_module_graph_mermaid(&elements, &relationships);
        diagrams.insert("module-dependencies".to_string(), module_graph);

        let cluster_graph = self.generate_cluster_graph_mermaid();
        diagrams.insert("cluster-relationships".to_string(), cluster_graph);

        let rel_type_dist = self.generate_rel_type_distribution_mermaid(&relationships);
        diagrams.insert("relationship-distribution".to_string(), rel_type_dist);

        Ok(diagrams)
    }

    fn generate_call_graph_mermaid(&self, relationships: &[Relationship]) -> String {
        let mut mermaid = String::from("graph TD\n");
        let call_rels: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "calls")
            .take(50)
            .collect();

        for rel in &call_rels {
            let source_id = rel
                .source_qualified
                .replace("::", "__")
                .replace("/", "_")
                .replace("-", "_");
            let target_id = rel
                .target_qualified
                .replace("::", "__")
                .replace("/", "_")
                .replace("-", "_");
            let source_short = rel.source_qualified.split("::").last().unwrap_or("src");
            let target_short = rel.target_qualified.split("::").last().unwrap_or("tgt");
            mermaid.push_str(&format!(
                "    {}[\"{}\"] --> {}[\"{}\"]\n",
                source_id, source_short, target_id, target_short
            ));
        }
        mermaid
    }

    fn generate_module_graph_mermaid(
        &self,
        elements: &[CodeElement],
        relationships: &[Relationship],
    ) -> String {
        let mut mermaid = String::from("graph TB\n");
        let mut module_elements: HashMap<String, Vec<&CodeElement>> = HashMap::new();
        for elem in elements {
            let module = elem
                .file_path
                .split('/')
                .nth(1)
                .unwrap_or("root")
                .to_string();
            module_elements.entry(module).or_default().push(elem);
        }

        let mut module_deps: HashMap<String, HashSet<String>> = HashMap::new();
        for rel in relationships {
            if rel.rel_type == "imports" {
                let source_module = rel
                    .source_qualified
                    .split('/')
                    .nth(1)
                    .unwrap_or("root")
                    .to_string();
                let target_module = rel
                    .target_qualified
                    .split('/')
                    .nth(1)
                    .unwrap_or("root")
                    .to_string();
                if source_module != target_module {
                    module_deps
                        .entry(source_module)
                        .or_default()
                        .insert(target_module);
                }
            }
        }

        for (module, elems) in &module_elements {
            let elem_count = elems.len();
            mermaid.push_str(&format!(
                "    {}[\"{}\\n{} elements\"]\n",
                module, module, elem_count
            ));
        }

        for (module, deps) in &module_deps {
            for dep in deps {
                mermaid.push_str(&format!("    {} --> {}\n", module, dep));
            }
        }
        mermaid
    }

    fn generate_cluster_graph_mermaid(&self) -> String {
        let db = self.graph.db();
        let detector = CommunityDetector::new(db);
        let clusters = detector.detect_communities().ok();

        let mut mermaid = String::from("graph LR\n");
        if let Some(ref clusters) = clusters {
            for cluster in clusters.values() {
                let members_short = cluster
                    .members
                    .iter()
                    .take(3)
                    .map(|m| m.split("::").last().unwrap_or(m))
                    .collect::<Vec<_>>()
                    .join(", ");
                mermaid.push_str(&format!(
                    "    subgraph {}[\"{}\\n{} members\"]\n",
                    cluster.id,
                    cluster.label,
                    cluster.members.len()
                ));
                mermaid.push_str(&format!("        {}[\"{}\"]\n", cluster.id, members_short));
                mermaid.push_str("    end\n");
            }
        }
        mermaid
    }

    fn generate_rel_type_distribution_mermaid(&self, relationships: &[Relationship]) -> String {
        let mut rel_types: HashMap<&str, usize> = HashMap::new();
        for rel in relationships {
            *rel_types.entry(rel.rel_type.as_str()).or_insert(0) += 1;
        }

        let mut mermaid = String::from("pie(title Relationship Types)\n");
        for (rel_type, count) in rel_types {
            let label = rel_type.replace("-", "_").replace(" ", "_");
            mermaid.push_str(&format!("    \"{}\" : {}\n", label, count));
        }
        mermaid
    }
}

use std::collections::HashSet;
