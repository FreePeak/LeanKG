#![allow(dead_code)]
mod paths;

pub use paths::{resolve_code_ref, resolve_doc_key, resolve_file_key};

use crate::db::models::{CodeElement, Relationship};

use crate::graph::GraphEngine;
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use walkdir::WalkDir;

/// Maximum per-symbol `references` edges emitted per resolved file.
/// Prevents edge-count blowup when a doc references a file with
/// hundreds of functions (FR-SEM-08 + FR-SEM-09).
const PER_SYMBOL_FANOUT_CAP: usize = 8;

/// Maximum markdown doc file size to parse. Larger files are skipped
/// to bound memory + parse time on huge generated markdown. Default 512 KiB.
/// Override with `LEANKG_DOC_MAX_FILE_SIZE` (bytes).
/// FR-DOC-CAP-512K: multi-MiB generated .md files can stall the doc walker
/// past the 10-min index budget.
fn doc_max_file_size() -> u64 {
    std::env::var("LEANKG_DOC_MAX_FILE_SIZE")
        .ok()
        .and_then(|v| v.parse().ok())
        .filter(|n| *n > 0)
        .unwrap_or(512 * 1024)
}

/// Maximum `code_refs` resolved per markdown doc. Each resolution can fire
/// multiple CozoDB round-trips; on the be mega-graph (721k elements) a
/// single doc with hundreds of references blows the 10-min budget.
/// Default 25 — doc joins are best-effort; a handful of resolved refs per
/// file is plenty for get_files_for_doc / get_traceability.
/// Setting 0 disables code-ref resolution entirely (sections/headings only);
/// the doc walker still indexes documents + sections + contains edges.
/// Override with `LEANKG_DOC_MAX_CODE_REFS`.
/// FR-DOC-REF-CAP-100: docs/analysis/ files reference 500+ symbols.
fn doc_max_code_refs() -> usize {
    std::env::var("LEANKG_DOC_MAX_CODE_REFS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(25)
}

#[derive(Debug, Clone)]
pub struct DocIndexResult {
    pub documents: Vec<CodeElement>,
    pub sections: Vec<CodeElement>,
    pub relationships: Vec<Relationship>,
}

pub struct DocIndexer {
    _db: crate::db::backend::SharedDb,
}

impl DocIndexer {
    pub fn new(db: crate::db::backend::SharedDb) -> Self {
        Self { _db: db }
    }

    pub fn index_docs(
        &self,
        docs_path: &Path,
    ) -> Result<DocIndexResult, Box<dyn std::error::Error>> {
        self.index_docs_with_graph(docs_path, None)
    }

    pub fn index_docs_with_graph(
        &self,
        docs_path: &Path,
        graph: Option<&GraphEngine>,
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
            // FR-INDEX-NO-HANG: don't follow symlinks (cycles hang the walk).
            .follow_links(false)
            .into_iter()
            .filter_map(|e| e.ok())
        {
            let path = entry.path();
            if path.is_file() {
                // FR-INDEX-NO-HANG: skip symlinked files (duplicates / deps).
                if path.is_symlink() {
                    continue;
                }
                if let Some(ext) = path.extension() {
                    if ext == "md" || ext == "markdown" || ext == "mdown" || ext == "mkd" {
                        // FR-DOC-CAP-512K: skip oversized md files so a
                        // multi-MiB generated .md can't stall the whole
                        // doc indexer past the 10-min budget.
                        let max_size = doc_max_file_size();
                        match std::fs::metadata(path) {
                            Ok(meta) if meta.len() > max_size => {
                                eprintln!(
                                    "Skipping oversize doc ({} bytes > {} cap): {}",
                                    meta.len(),
                                    max_size,
                                    path.display()
                                );
                                continue;
                            }
                            Ok(_) => {}
                            Err(e) => {
                                eprintln!("Warning: stat failed for {:?}: {}", path, e);
                                continue;
                            }
                        }
                        match self.parse_doc_file(path, docs_path, graph) {
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
                    ..Default::default()
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
        graph: Option<&GraphEngine>,
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
        // Storage-mapped absolute path (LEANKG_PATH_REWRITE) for the stored
        // `file_path`; the doc `qualified_name` stays `docs/...` (already
        // relative to docs_root, no rewrite).
        let stored_path = crate::indexer::rewrite_path_for_storage(&path.display().to_string());

        let category = self.detect_category(path, docs_root);
        let title = self.extract_title(&content).unwrap_or_else(|| {
            path.file_stem()
                .and_then(|s| s.to_str())
                .unwrap_or("Untitled")
                .to_string()
        });

        let headings = self.extract_headings(&content);
        let first_paragraph = Self::extract_first_paragraph(&content);
        let doc = CodeElement {
            qualified_name: qualified_name.clone(),
            element_type: "document".to_string(),
            name: title.clone(),
            file_path: stored_path.clone(),
            line_start: 1,
            line_end: content.lines().count() as u32,
            language: "markdown".to_string(),
            parent_qualified: None,
            metadata: serde_json::json!({
                "category": category,
                "title": title,
                "headings": headings,
                "first_paragraph": first_paragraph,
                "heading_path": [title.clone()],
            }),
            ..Default::default()
        };

        let (sections, heading_rels) =
            self.extract_sections(&content, &qualified_name, &stored_path, &title);

        let code_refs = self.extract_code_references(&content);
        let mut relationships = heading_rels;

        let mut resolved_count = 0u32;
        let mut skipped_count = 0u32;
        let cap = doc_max_code_refs();

        // FR-DOC-REF-CAP-100: stop resolving refs once we hit the per-doc
        // budget so a single doc can't blow the 10-min index budget on
        // thousands of CozoDB queries.
        for (target, context) in code_refs.into_iter().take(cap) {
            let resolved_target = match graph {
                Some(g) => match resolve_code_ref(g, &target) {
                    Some(qn) => {
                        if qn != target {
                            resolved_count += 1;
                        }
                        qn
                    }
                    None => {
                        skipped_count += 1;
                        tracing::debug!(
                            target: "leankg::docjoin",
                            doc = %qualified_name,
                            raw_ref = %target,
                            "doc join: unresolved markdown code ref"
                        );
                        continue;
                    }
                },
                None => target.clone(),
            };

            // FR-SEM-08 per-symbol fanout: capture the set of
            // function-bearing symbols inside the resolved file BEFORE
            // resolved_target is moved into the file-granular edge.
            // Bounded fanout (PER_SYMBOL_FANOUT_CAP) prevents blowup on
            // docs that reference large files. Sorted by line_start so
            // the earliest definitions (most-likely-relevant) win when
            // the cap kicks in.
            let per_symbol_targets: Vec<String> = match graph {
                Some(g) => g
                    .get_elements_by_file(&resolved_target)
                    .ok()
                    .map(|syms| {
                        let mut fns: Vec<_> = syms
                            .into_iter()
                            .filter(|e| {
                                matches!(
                                    e.element_type.as_str(),
                                    "function" | "method" | "constructor"
                                )
                            })
                            .collect();
                        fns.sort_by_key(|e| e.line_start);
                        fns.into_iter()
                            .take(PER_SYMBOL_FANOUT_CAP)
                            .map(|e| e.qualified_name)
                            .collect()
                    })
                    .unwrap_or_default(),
                None => Vec::new(),
            };

            let snippet: String = context.chars().take(100).collect();
            let edge_meta = serde_json::json!({
                "context": snippet,
                "confidence_label": "EXTRACTED",
            });

            relationships.push(Relationship {
                id: None,
                source_qualified: qualified_name.clone(),
                target_qualified: resolved_target.clone(),
                rel_type: "references".to_string(),
                confidence: 1.0,
                metadata: edge_meta.clone(),
                ..Default::default()
            });

            relationships.push(Relationship {
                id: None,
                source_qualified: resolved_target,
                target_qualified: qualified_name.clone(),
                rel_type: "documented_by".to_string(),
                confidence: 1.0,
                metadata: edge_meta,
                ..Default::default()
            });

            // Per-symbol fanout for FR-SEM-08 (additive). The
            // file-granular references + documented_by edges above are
            // preserved so legacy callers (kg_context, get_traceability)
            // are unaffected; the per-symbol edges below give the
            // ontology-guided top-down traversal function targets to
            // walk to. Metadata carries `granularity: "per-symbol"` so
            // callers can distinguish.
            for sym_qn in per_symbol_targets {
                let sym_meta = serde_json::json!({
                    "context": snippet,
                    "confidence_label": "EXTRACTED",
                    "granularity": "per-symbol",
                    "via_doc": qualified_name,
                    "via_edge": "references",
                });
                relationships.push(Relationship {
                    id: None,
                    source_qualified: qualified_name.clone(),
                    target_qualified: sym_qn.clone(),
                    rel_type: "references".to_string(),
                    confidence: 1.0,
                    metadata: sym_meta.clone(),
                    ..Default::default()
                });
                relationships.push(Relationship {
                    id: None,
                    source_qualified: sym_qn,
                    target_qualified: qualified_name.clone(),
                    rel_type: "documented_by".to_string(),
                    confidence: 1.0,
                    metadata: sym_meta,
                    ..Default::default()
                });
            }
        }

        if graph.is_some() && (resolved_count > 0 || skipped_count > 0) {
            tracing::debug!(
                target: "leankg::docjoin",
                doc = %qualified_name,
                resolved = resolved_count,
                skipped = skipped_count,
                "doc join: ref resolve summary"
            );
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

    fn extract_first_paragraph(content: &str) -> String {
        let mut past_title = false;
        for line in content.lines() {
            let trimmed = line.trim();
            if trimmed.is_empty() {
                continue;
            }
            if trimmed.starts_with("# ") {
                past_title = true;
                continue;
            }
            if trimmed.starts_with('#') {
                continue;
            }
            if !past_title {
                continue;
            }
            return trimmed.chars().take(500).collect();
        }
        String::new()
    }

    fn section_first_paragraph(content: &str, start_line: u32, end_line: u32) -> String {
        let mut line_num = 0u32;
        for line in content.lines() {
            line_num += 1;
            if line_num < start_line || line_num > end_line {
                continue;
            }
            let trimmed = line.trim();
            if trimmed.is_empty() || trimmed.starts_with('#') {
                continue;
            }
            return trimmed.chars().take(500).collect();
        }
        String::new()
    }

    fn extract_sections(
        &self,
        content: &str,
        doc_qualified: &str,
        stored_path: &str,
        doc_title: &str,
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
                        file_path: stored_path.to_string(),
                        line_start: sec_start,
                        line_end: line_num - 1,
                        language: "markdown".to_string(),
                        parent_qualified: Some(doc_qualified.to_string()),
                        metadata: serde_json::json!({
                            "title": sec_name,
                            "heading_path": [doc_title, sec_name],
                            "first_paragraph": Self::section_first_paragraph(content, sec_start, line_num - 1),
                        }),
                        ..Default::default()
                    });

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: doc_qualified.to_string(),
                        target_qualified: section_qualified,
                        rel_type: "contains".to_string(),
                        confidence: 1.0,
                        metadata: serde_json::json!({}),
                        ..Default::default()
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
                file_path: stored_path.to_string(),
                line_start: sec_start,
                line_end: line_num,
                language: "markdown".to_string(),
                parent_qualified: Some(doc_qualified.to_string()),
                metadata: serde_json::json!({
                    "title": sec_name,
                    "heading_path": [doc_title, sec_name],
                    "first_paragraph": Self::section_first_paragraph(content, sec_start, line_num),
                }),
                ..Default::default()
            });

            relationships.push(Relationship {
                id: None,
                source_qualified: doc_qualified.to_string(),
                target_qualified: section_qualified,
                rel_type: "contains".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({}),
                ..Default::default()
            });
        }

        (sections, relationships)
    }

    fn extract_code_references(&self, content: &str) -> Vec<(String, String)> {
        use regex::Regex;
        let mut refs = Vec::new();

        // Pattern 1: raw filenames in prose (e.g. "see handler.rs for details")
        let file_pattern = Regex::new(r"\b([\w\-/]+\.(?:go|rs|ts|tsx|js|jsx|py))\b").unwrap();

        // Pattern 2: markdown links [text](path/to/file.rs)
        let md_link_pattern =
            Regex::new(r"\[([^\]]+)\]\(([\w\-/.]+\.(?:go|rs|ts|tsx|js|jsx|py))\)").unwrap();

        // Pattern 3: backtick-enclosed code references `file.rs` or
        // `file.rs::symbol` (FR-DOCJOIN-06 keeps the `::symbol` suffix so the
        // resolver can upgrade to the symbol key when unique).
        let code_ref_pattern =
            Regex::new(r"`([\w\-/]+\.(?:go|rs|ts|tsx|js|jsx|py)(?:::[A-Za-z_][\w]*)?)`").unwrap();

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

            // Extract from markdown links
            for cap in md_link_pattern.captures_iter(trimmed) {
                if let Some(m) = cap.get(2) {
                    let target = m.as_str().to_string();
                    let context = trimmed.chars().take(100).collect::<String>();
                    refs.push((target, context));
                }
            }

            // Extract from backtick code references
            for cap in code_ref_pattern.captures_iter(trimmed) {
                if let Some(m) = cap.get(1) {
                    let target = m.as_str().to_string();
                    let context = trimmed.chars().take(100).collect::<String>();
                    // A `file.rs::symbol` mention also matches the bare file
                    // pattern below on the same line; keep the richer
                    // symbol-qualified target once per line.
                    if let Some((file_part, _)) = target.rsplit_once("::") {
                        if refs.iter().any(|(t, _)| t == file_part) {
                            continue;
                        }
                    }
                    refs.push((target, context));
                }
            }

            // Extract from bare filenames
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
            // FR-INDEX-NO-HANG: don't follow symlinks (cycles hang the walk).
            .follow_links(false)
            .into_iter()
            .filter_map(|e| e.ok())
        {
            let path = entry.path();
            if path.is_file() {
                // FR-INDEX-NO-HANG: skip symlinked files (duplicates / deps).
                if path.is_symlink() {
                    continue;
                }
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
        let db = graph.db_arc().clone();
        let indexer = DocIndexer::new(db);
        indexer.index_docs_with_graph(docs_path, Some(graph))?
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

    #[cfg(feature = "embeddings")]
    {
        let items: Vec<(String, String)> = result
            .documents
            .iter()
            .chain(result.sections.iter())
            .filter_map(|e| {
                let blob = crate::embeddings::text_blob::build_blob(e)?;
                let hash = crate::embeddings::text_blob::content_hash_for(&blob);
                Some((e.qualified_name.clone(), hash))
            })
            .collect();
        if !items.is_empty() {
            let _ = crate::embeddings::state::mark_stale_if_changed(graph.db(), &items);
        }
    }

    if let Err(e) = crate::graph::inventory::refresh_index_inventory(graph, "doc_index") {
        tracing::warn!("index_inventory refresh after doc_index failed: {}", e);
    }

    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;
    // Serialize env-var tests; std::env is not thread-safe.
    static ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    #[test]
    fn doc_max_file_size_default_is_512_kib() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let prev = std::env::var("LEANKG_DOC_MAX_FILE_SIZE").ok();
        std::env::remove_var("LEANKG_DOC_MAX_FILE_SIZE");
        assert_eq!(doc_max_file_size(), 512 * 1024);
        match prev {
            Some(v) => std::env::set_var("LEANKG_DOC_MAX_FILE_SIZE", v),
            None => std::env::remove_var("LEANKG_DOC_MAX_FILE_SIZE"),
        }
    }

    #[test]
    fn doc_max_code_refs_default_is_25() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let prev = std::env::var("LEANKG_DOC_MAX_CODE_REFS").ok();
        std::env::remove_var("LEANKG_DOC_MAX_CODE_REFS");
        assert_eq!(doc_max_code_refs(), 25);
        match prev {
            Some(v) => std::env::set_var("LEANKG_DOC_MAX_CODE_REFS", v),
            None => std::env::remove_var("LEANKG_DOC_MAX_CODE_REFS"),
        }
    }

    #[test]
    fn doc_max_code_refs_env_override() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let prev = std::env::var("LEANKG_DOC_MAX_CODE_REFS").ok();
        std::env::set_var("LEANKG_DOC_MAX_CODE_REFS", "25");
        assert_eq!(doc_max_code_refs(), 25);
        match prev {
            Some(v) => std::env::set_var("LEANKG_DOC_MAX_CODE_REFS", v),
            None => std::env::remove_var("LEANKG_DOC_MAX_CODE_REFS"),
        }
    }

    #[test]
    fn doc_max_code_refs_zero_disables_resolution() {
        // FR-DOC-REF-CAP-0: 0 means "skip doc code-ref resolution" so the
        // mega-graph fresh index stays under the 10-min budget.
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let prev = std::env::var("LEANKG_DOC_MAX_CODE_REFS").ok();
        std::env::set_var("LEANKG_DOC_MAX_CODE_REFS", "0");
        assert_eq!(doc_max_code_refs(), 0);
        match prev {
            Some(v) => std::env::set_var("LEANKG_DOC_MAX_CODE_REFS", v),
            None => std::env::remove_var("LEANKG_DOC_MAX_CODE_REFS"),
        }
    }
}
