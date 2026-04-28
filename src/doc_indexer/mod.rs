#![allow(dead_code)]
use crate::db::models::{CodeElement, Relationship};
use crate::db::schema::CozoDb;
use crate::graph::GraphEngine;
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use walkdir::WalkDir;

#[derive(Debug, Clone)]
pub struct DocIndexResult {
    pub documents: Vec<CodeElement>,
    pub sections: Vec<CodeElement>,
    pub relationships: Vec<Relationship>,
}

pub struct DocIndexer {
    _db: CozoDb,
}

impl DocIndexer {
    pub fn new(db: CozoDb) -> Self {
        Self { _db: db }
    }

    pub fn index_docs(
        &self,
        docs_path: &Path,
    ) -> Result<DocIndexResult, Box<dyn std::error::Error>> {
        let mut documents = Vec::new();
        let mut sections = Vec::new();
        let mut relationships = Vec::new();
        let mut doc_hierarchy: HashMap<PathBuf, Vec<PathBuf>> = HashMap::new();

        if !docs_path.exists() {
            return Ok(DocIndexResult {
                documents,
                sections,
                relationships,
            });
        }

        for entry in WalkDir::new(docs_path)
            .follow_links(true)
            .into_iter()
            .filter_map(|e| e.ok())
        {
            let path = entry.path();
            if path.is_file() {
                if let Some(ext) = path.extension() {
                    if ext == "md" || ext == "markdown" || ext == "mdown" || ext == "mkd" {
                        match self.parse_doc_file(path, docs_path) {
                            Ok((doc, secs, rels, _children)) => {
                                documents.push(doc);
                                sections.extend(secs);
                                relationships.extend(rels);
                                if let Some(parent) = path.parent() {
                                    doc_hierarchy
                                        .entry(parent.to_path_buf())
                                        .or_default()
                                        .push(path.to_path_buf());
                                }
                            }
                            Err(e) => {
                                eprintln!("Warning: Failed to parse {:?}: {}", path, e);
                            }
                        }
                    }
                }
            }
        }

        for (parent_path, children) in doc_hierarchy {
            for child_path in children {
                let parent_name = format!("{}", parent_path.display());
                let child_name = format!("{}", child_path.display());
                relationships.push(Relationship {
                    id: None,
                    source_qualified: parent_name,
                    target_qualified: child_name,
                    rel_type: "contains".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                });
            }
        }

        Ok(DocIndexResult {
            documents,
            sections,
            relationships,
        })
    }

    #[allow(clippy::type_complexity)]
    fn parse_doc_file(
        &self,
        path: &Path,
        docs_root: &Path,
    ) -> Result<
        (
            CodeElement,
            Vec<CodeElement>,
            Vec<Relationship>,
            Vec<PathBuf>,
        ),
        Box<dyn std::error::Error>,
    > {
        let content = std::fs::read_to_string(path)?;
        let relative_path = path.strip_prefix(docs_root).unwrap_or(path);
        let qualified_name = format!(
            "docs/{}",
            relative_path.display().to_string().replace('\\', "/")
        );

        let category = self.detect_category(path, docs_root);
        let title = self.extract_title(&content).unwrap_or_else(|| {
            path.file_stem()
                .and_then(|s| s.to_str())
                .unwrap_or("Untitled")
                .to_string()
        });

        let headings = self.extract_headings(&content);
        let doc = CodeElement {
            qualified_name: qualified_name.clone(),
            element_type: "document".to_string(),
            name: title.clone(),
            file_path: format!("{}", path.display()),
            line_start: 1,
            line_end: content.lines().count() as u32,
            language: "markdown".to_string(),
            parent_qualified: None,
            metadata: serde_json::json!({
                "category": category,
                "title": title,
                "headings": headings,
            }),
            ..Default::default()
        };

        let (sections, heading_rels) = self.extract_sections(&content, &qualified_name, path);

        let code_refs = self.extract_code_references(&content);
        let mut relationships = heading_rels;

        for (target, _context) in code_refs {
            let target_clone = target.clone();
            relationships.push(Relationship {
                id: None,
                source_qualified: qualified_name.clone(),
                target_qualified: target,
                rel_type: "references".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({}),
            });

            relationships.push(Relationship {
                id: None,
                source_qualified: target_clone,
                target_qualified: qualified_name.clone(),
                rel_type: "documented_by".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({}),
            });
        }

        Ok((doc, sections, relationships, Vec::new()))
    }

    fn detect_category(&self, path: &Path, docs_root: &Path) -> String {
        let relative = path.strip_prefix(docs_root).unwrap_or(path);
        relative
            .components()
            .next()
            .and_then(|c| c.as_os_str().to_str())
            .unwrap_or("root")
            .to_string()
    }

    fn extract_title(&self, content: &str) -> Option<String> {
        for line in content.lines() {
            let trimmed = line.trim();
            if let Some(stripped) = trimmed.strip_prefix("# ") {
                return Some(stripped.trim().to_string());
            }
        }
        None
    }

    fn extract_headings(&self, content: &str) -> Vec<String> {
        let mut headings = Vec::new();
        for line in content.lines() {
            let trimmed = line.trim();
            if trimmed.starts_with("## ") || trimmed.starts_with("### ") {
                headings.push(trimmed.trim_start_matches('#').trim().to_string());
            }
        }
        headings
    }

    fn extract_sections(
        &self,
        content: &str,
        doc_qualified: &str,
        path: &Path,
    ) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut sections = Vec::new();
        let mut relationships = Vec::new();
        let mut current_section: Option<(&str, u32)> = None;
        let mut line_num = 0u32;

        for line in content.lines() {
            line_num += 1;
            let trimmed = line.trim();

            if trimmed.starts_with("## ") || trimmed.starts_with("### ") {
                if let Some((sec_name, sec_start)) = current_section {
                    let section_qualified = format!("{}::{}", doc_qualified, sec_name);
                    sections.push(CodeElement {
                        qualified_name: section_qualified.clone(),
                        element_type: "doc_section".to_string(),
                        name: sec_name.to_string(),
                        file_path: format!("{}", path.display()),
                        line_start: sec_start,
                        line_end: line_num - 1,
                        language: "markdown".to_string(),
                        parent_qualified: Some(doc_qualified.to_string()),
                        metadata: serde_json::json!({}),
                        ..Default::default()
                    });

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: doc_qualified.to_string(),
                        target_qualified: section_qualified,
                        rel_type: "contains".to_string(),
                        confidence: 1.0,
                        metadata: serde_json::json!({}),
                    });
                }

                let _heading_level = if trimmed.starts_with("## ") { 2 } else { 3 };
                current_section = Some((trimmed.trim_start_matches('#').trim(), line_num));
            }
        }

        if let Some((sec_name, sec_start)) = current_section {
            let section_qualified = format!("{}::{}", doc_qualified, sec_name);
            sections.push(CodeElement {
                qualified_name: section_qualified.clone(),
                element_type: "doc_section".to_string(),
                name: sec_name.to_string(),
                file_path: format!("{}", path.display()),
                line_start: sec_start,
                line_end: line_num,
                language: "markdown".to_string(),
                parent_qualified: Some(doc_qualified.to_string()),
                metadata: serde_json::json!({}),
                ..Default::default()
            });

            relationships.push(Relationship {
                id: None,
                source_qualified: doc_qualified.to_string(),
                target_qualified: section_qualified,
                rel_type: "contains".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({}),
            });
        }

        (sections, relationships)
    }

    fn extract_code_references(&self, content: &str) -> Vec<(String, String)> {
        let mut refs = Vec::new();
        let file_pattern =
            regex::Regex::new(r"\b([\w\-/]+\.(?:go|rs|ts|tsx|js|jsx|py))\b").unwrap();
        let mut in_code_block = false;

        for line in content.lines() {
            let trimmed = line.trim();
            if trimmed.starts_with("```") {
                in_code_block = !in_code_block;
                continue;
            }
            if in_code_block {
                continue;
            }

            for cap in file_pattern.captures_iter(trimmed) {
                if let Some(m) = cap.get(1) {
                    let target = m.as_str().to_string();
                    if target.len() >= 5 {
                        let context = trimmed.chars().take(100).collect::<String>();
                        refs.push((target, context));
                    }
                }
            }
        }

        refs
    }

    #[allow(dead_code)]
    pub fn get_doc_structure(
        &self,
        docs_path: &Path,
    ) -> Result<Vec<DocTreeNode>, Box<dyn std::error::Error>> {
        let mut root = DocTreeNode::new("docs".to_string(), "directory".to_string());

        if !docs_path.exists() {
            return Ok(vec![root]);
        }

        for entry in WalkDir::new(docs_path)
            .max_depth(3)
            .into_iter()
            .filter_map(|e| e.ok())
        {
            let path = entry.path();
            if path.is_dir() {
                let relative = path.strip_prefix(docs_path).unwrap_or(path);
                let parts: Vec<&str> = relative
                    .components()
                    .filter_map(|c| c.as_os_str().to_str())
                    .collect();
                if !parts.is_empty() {
                    root.add_path(&parts);
                }
            }
        }

        for entry in WalkDir::new(docs_path)
            .follow_links(true)
            .into_iter()
            .filter_map(|e| e.ok())
        {
            let path = entry.path();
            if path.is_file() {
                if let Some(ext) = path.extension() {
                    if ext == "md" || ext == "markdown" {
                        let relative = path.strip_prefix(docs_path).unwrap_or(path);
                        let parts: Vec<&str> = relative
                            .components()
                            .filter_map(|c| c.as_os_str().to_str())
                            .collect();
                        if !parts.is_empty() {
                            root.add_path(&parts);
                        }
                    }
                }
            }
        }

        Ok(vec![root])
    }
}

#[allow(dead_code)]
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct DocTreeNode {
    pub name: String,
    pub node_type: String,
    pub children: Vec<DocTreeNode>,
}

impl DocTreeNode {
    #[allow(dead_code)]
    pub fn new(name: String, node_type: String) -> Self {
        Self {
            name,
            node_type,
            children: Vec::new(),
        }
    }

    pub fn add_path(&mut self, parts: &[&str]) {
        if parts.is_empty() {
            return;
        }

        let first = parts[0].to_string();
        let is_dir = parts.len() > 1;

        let node_type = if is_dir { "directory" } else { "document" };

        if let Some(existing) = self.children.iter_mut().find(|c| c.name == first) {
            if parts.len() > 1 {
                existing.add_path(&parts[1..]);
            }
        } else {
            let mut new_node = Self::new(first, node_type.to_string());
            if parts.len() > 1 {
                new_node.add_path(&parts[1..]);
            }
            self.children.push(new_node);
        }
    }
}

pub fn index_docs_directory(
    docs_path: &Path,
    graph: &GraphEngine,
) -> Result<DocIndexResult, Box<dyn std::error::Error>> {
    let result = {
        let db = graph.db();
        let indexer = DocIndexer::new(db.clone());
        indexer.index_docs(docs_path)?
    };

    if !result.documents.is_empty() {
        graph.insert_elements(&result.documents)?;
    }

    if !result.sections.is_empty() {
        graph.insert_elements(&result.sections)?;
    }

    if !result.relationships.is_empty() {
        graph.insert_relationships(&result.relationships)?;
    }

    Ok(result)
}
