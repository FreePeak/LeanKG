pub mod extractor;
pub mod git;
pub mod parser;

pub use extractor::*;
pub use git::*;
pub use parser::*;

use crate::graph::GraphEngine;
use std::collections::HashSet;
use walkdir::WalkDir;

pub async fn find_files(root: &str) -> Result<Vec<String>, Box<dyn std::error::Error>> {
    let mut files = Vec::new();
    let extensions = ["go", "ts", "js", "py"];

    for entry in WalkDir::new(root)
        .follow_links(true)
        .into_iter()
        .filter_map(|e| e.ok())
    {
        let path = entry.path();
        if path.is_file()
            && path.extension().is_some_and(|ext| extensions.contains(&ext.to_str().unwrap_or("")))
            && !path.to_string_lossy().contains("node_modules")
            && !path.to_string_lossy().contains("vendor")
            && !path.to_string_lossy().contains(".git")
        {
            files.push(path.to_string_lossy().to_string());
        }
    }

    Ok(files)
}

pub async fn index_file(
    graph: &GraphEngine,
    parser_manager: &mut ParserManager,
    file_path: &str,
) -> Result<usize, Box<dyn std::error::Error>> {
    let content = tokio::fs::read(file_path).await?;
    let source = content.as_slice();

    let language = if file_path.ends_with(".go") {
        "go"
    } else if file_path.ends_with(".ts") || file_path.ends_with(".js") {
        "typescript"
    } else if file_path.ends_with(".py") {
        "python"
    } else {
        return Ok(0);
    };

    let parser = parser_manager.get_parser_for_language(language);
    let parser = match parser {
        Some(p) => p,
        None => return Ok(0),
    };

    let tree = parser.parse(source, None).ok_or("Failed to parse")?;

    let extractor = EntityExtractor::new(source, file_path, language);
    let (elements, relationships) = extractor.extract(&tree);

    if elements.is_empty() && relationships.is_empty() {
        return Ok(0);
    }

    let _ = graph.insert_elements(&elements).await;
    let _ = graph.insert_relationships(&relationships).await;

    Ok(elements.len())
}

pub async fn reindex_file(
    graph: &GraphEngine,
    parser_manager: &mut ParserManager,
    file_path: &str,
) -> Result<usize, Box<dyn std::error::Error>> {
    graph.remove_elements_by_file(file_path).await?;
    graph.remove_relationships_by_source(file_path).await?;

    index_file(graph, parser_manager, file_path).await
}

pub struct IncrementalIndexResult {
    pub changed_files: Vec<String>,
    pub dependent_files: Vec<String>,
    pub total_files_processed: usize,
    pub elements_indexed: usize,
}

pub async fn incremental_index(
    graph: &GraphEngine,
    parser_manager: &mut ParserManager,
    root_path: &str,
) -> Result<IncrementalIndexResult, Box<dyn std::error::Error>> {
    if !GitAnalyzer::is_git_repo() {
        return Err("Not a git repository. Cannot perform incremental indexing.".into());
    }

    let repo_root = GitAnalyzer::get_repo_root().unwrap_or_else(|| root_path.to_string());

    let changed = GitAnalyzer::get_changed_files_since_last_commit()?;

    let deleted_files: Vec<String> = changed
        .deleted
        .iter()
        .map(|f| {
            if std::path::Path::new(f).is_absolute() {
                f.clone()
            } else {
                format!("{}/{}", repo_root, f)
            }
        })
        .collect();

    let mut all_changed: Vec<String> = Vec::new();
    all_changed.extend(changed.modified);
    all_changed.extend(changed.added);
    all_changed.extend(changed.deleted);

    let untracked = GitAnalyzer::get_untracked_files()?;
    let indexable_untracked = filter_indexable_files(&untracked);
    all_changed.extend(indexable_untracked);

    let changed_files: Vec<String> = all_changed
        .iter()
        .map(|f| {
            if std::path::Path::new(f).is_absolute() {
                f.clone()
            } else {
                format!("{}/{}", repo_root, f)
            }
        })
        .collect();

    for deleted_file in &deleted_files {
        graph.remove_elements_by_file(deleted_file).await?;
        graph.remove_relationships_by_source(deleted_file).await?;
    }

    let all_relationships = graph.all_relationships().await?;
    let rel_tuples: Vec<(String, String)> = all_relationships
        .iter()
        .map(|r| (r.source_qualified.clone(), r.target_qualified.clone()))
        .collect();

    let mut dependent_files: Vec<String> = Vec::new();
    for changed_file in &changed_files {
        let file_name = std::path::Path::new(changed_file)
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or(changed_file);

        let deps = find_dependents(file_name, &rel_tuples);
        for dep in deps {
            let dep_path = std::path::Path::new(&dep);
            if !dep_path.is_absolute() {
                dependent_files.push(format!("{}/{}", repo_root, dep));
            } else {
                dependent_files.push(dep);
            }
        }
    }

    dependent_files.dedup();

    let mut all_files_to_process: Vec<String> = Vec::new();
    let mut seen: HashSet<String> = HashSet::new();

    for f in &changed_files {
        if !seen.contains(f) {
            all_files_to_process.push(f.clone());
            seen.insert(f.clone());
        }
    }
    for f in &dependent_files {
        if !seen.contains(f) {
            all_files_to_process.push(f.clone());
            seen.insert(f.clone());
        }
    }

    let mut total_elements = 0;
    let mut files_processed = 0;

    for file_path in &all_files_to_process {
        if std::path::Path::new(file_path).exists() {
            match reindex_file(graph, parser_manager, file_path).await {
                Ok(count) => {
                    if count > 0 {
                        total_elements += count;
                        files_processed += 1;
                    }
                }
                Err(e) => {
                    tracing::warn!("Failed to reindex {}: {}", file_path, e);
                }
            }
        }
    }

    Ok(IncrementalIndexResult {
        changed_files,
        dependent_files,
        total_files_processed: files_processed,
        elements_indexed: total_elements,
    })
}
