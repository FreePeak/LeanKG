pub mod cicd;
pub mod extractor;
pub mod git;
pub mod microservice;
pub mod parser;
pub mod process_processor;
pub mod terraform;

pub mod android_hilt;
pub mod android_manifest;
pub mod android_nav_fragments;
pub mod android_nav_jetpack;
pub mod android_nav_leanback;
pub mod android_nav_model;
pub mod android_resource_linker;
pub mod android_resource_refs;
pub mod android_resources;
pub mod android_room;
pub mod call_graph;
pub mod config_extractor;
pub mod framework_detector;
pub mod gradle_extractor;
pub mod gradle_module_extractor;
pub mod kotlin_annotations;
pub mod kotlin_utils;
pub mod maven_extractor;
pub mod xml_generic;
pub mod xml_layout;

pub use android_hilt::AndroidHiltExtractor;
pub use android_manifest::*;
pub use android_nav_fragments::FragmentNavExtractor;
pub use android_nav_jetpack::JetpackNavExtractor;
pub use android_nav_leanback::LeanbackNavExtractor;
pub use android_resource_linker::AndroidResourceLinker;
pub use android_resource_refs::AndroidResourceRefExtractor;
pub use android_resources::*;
pub use android_room::AndroidRoomExtractor;
#[allow(unused_imports)]
pub use call_graph::{extract_calls_with_resolution, CallGraphBuilder};
pub use cicd::*;
pub use config_extractor::*;
pub use extractor::*;
pub use framework_detector::*;
pub use git::*;
pub use gradle_extractor::*;
pub use gradle_module_extractor::GradleModuleExtractor;
pub use kotlin_annotations::KotlinAnnotationExtractor;
pub use maven_extractor::*;
pub use microservice::*;
pub use parser::*;
pub use process_processor::*;
pub use terraform::*;
pub use xml_generic::GenericXmlExtractor;
pub use xml_layout::*;

use crate::db::models::{CodeElement, Relationship};
use crate::graph::GraphEngine;
use ignore::WalkBuilder;
use rayon::prelude::*;
use std::collections::HashSet;
use std::sync::Arc;

pub fn find_files_sync(root: &str) -> Result<Vec<String>, Box<dyn std::error::Error>> {
    let mut files = Vec::new();
    let extensions = [
        "go", "ts", "js", "py", "rs", "java", "kt", "kts", "tf", "yml", "yaml", "json", "toml",
        "mod", "xml",
    ];
    let config_files = [
        "package.json",
        "tsconfig.json",
        "Cargo.toml",
        "go.mod",
        "build.gradle",
        "build.gradle.kts",
        "settings.gradle",
        "settings.gradle.kts",
        "pom.xml",
        "AndroidManifest.xml",
    ];

    let walker = WalkBuilder::new(root).follow_links(true).build();

    for entry in walker.flatten() {
        let path = entry.path();

        let ext = path.extension().and_then(|e| e.to_str()).unwrap_or("");
        let file_name = path.file_name().and_then(|n| n.to_str()).unwrap_or("");

        let is_valid_file = config_files.contains(&file_name)
            || (path.to_string_lossy().contains("/res/") && ext == "xml")
            || extensions.contains(&ext)
            || is_cicd_yaml_file(path);

        if path.is_file() && is_valid_file && !path.to_string_lossy().contains("/node_modules/") {
            files.push(path.to_string_lossy().to_string());
        }
    }

    Ok(files)
}

/// Returns true if the path should be skipped during indexing.
/// Covers build outputs, dependency caches, VCS, and generated dirs for all languages.
fn is_cicd_yaml_file(path: &std::path::Path) -> bool {
    let path_str = path.to_string_lossy();
    path_str.contains(".github/workflows")
        || path_str.contains(".gitlab-ci")
        || path_str.contains("azure-pipelines")
        || path_str.ends_with(".yml")
        || path_str.ends_with(".yaml")
}

struct ParsedFile {
    elements: Vec<CodeElement>,
    relationships: Vec<Relationship>,
    element_count: usize,
}

fn get_language(file_path: &str) -> Option<&'static str> {
    let ext = std::path::Path::new(file_path)
        .extension()
        .and_then(|e| e.to_str())?;
    match ext {
        "go" => Some("go"),
        "ts" | "js" => Some("typescript"),
        "py" => Some("python"),
        "rs" => Some("rust"),
        "java" => Some("java"),
        "kt" => Some("kotlin"),
        _ => None,
    }
}

fn try_extract_android(
    source: &[u8],
    file_path: &str,
) -> Option<(Vec<CodeElement>, Vec<Relationship>)> {
    let file_name = std::path::Path::new(file_path)
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("");
    if file_name == "AndroidManifest.xml" {
        let extractor = crate::indexer::AndroidManifestExtractor::new(source, file_path);
        return Some(extractor.extract());
    }
    if file_path.contains("/res/values/") && file_path.ends_with(".xml") {
        let extractor = crate::indexer::AndroidResourcesExtractor::new(source, file_path);
        return Some(extractor.extract());
    }
    if file_path.contains("/res/navigation/") && file_path.ends_with(".xml") {
        let extractor = crate::indexer::JetpackNavExtractor::new(source, file_path);
        return Some(extractor.extract_xml());
    }
    if file_path.contains("/res/") && file_path.ends_with(".xml") {
        let extractor = crate::indexer::XmlLayoutExtractor::new(source, file_path);
        return Some(extractor.extract());
    }
    None
}

fn extract_elements_for_file(
    file_path: &str,
) -> Result<ParsedFile, Box<dyn std::error::Error + Send + Sync>> {
    let content = std::fs::read(file_path)?;
    let source = content.as_slice();

    if file_path.ends_with(".tf") {
        let extractor = crate::indexer::TerraformExtractor::new(source, file_path);
        let (elements, relationships) = extractor.extract();
        return Ok(ParsedFile {
            element_count: elements.len(),
            elements,
            relationships,
        });
    }

    if is_cicd_yaml_file(std::path::Path::new(file_path))
        && (file_path.ends_with(".yml") || file_path.ends_with(".yaml"))
    {
        let extractor = crate::indexer::CicdYamlExtractor::new(source, file_path);
        let (elements, relationships) = extractor.extract();
        return Ok(ParsedFile {
            element_count: elements.len(),
            elements,
            relationships,
        });
    }

    let file_name = std::path::Path::new(file_path)
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("");
    if file_name == "package.json" || file_name == "tsconfig.json" {
        let file_type = if file_name == "package.json" {
            "package_json"
        } else {
            "tsconfig_json"
        };
        let extractor = crate::indexer::ConfigExtractor::new(source, file_path, file_type);
        let (elements, relationships) = extractor.extract();
        return Ok(ParsedFile {
            element_count: elements.len(),
            elements,
            relationships,
        });
    } else if file_name == "Cargo.toml" {
        let extractor = crate::indexer::ConfigExtractor::new(source, file_path, "cargo_toml");
        let (elements, relationships) = extractor.extract();
        return Ok(ParsedFile {
            element_count: elements.len(),
            elements,
            relationships,
        });
    } else if file_name == "go.mod" {
        let extractor = crate::indexer::ConfigExtractor::new(source, file_path, "go_mod");
        let (elements, relationships) = extractor.extract();
        return Ok(ParsedFile {
            element_count: elements.len(),
            elements,
            relationships,
        });
    } else if file_name == "build.gradle"
        || file_name == "build.gradle.kts"
        || file_name == "settings.gradle"
        || file_name == "settings.gradle.kts"
    {
        let extractor = crate::indexer::GradleExtractor::new(source, file_path);
        let (elements, mut relationships) = extractor.extract();

        // Also extract module dependencies
        let module_extractor = crate::indexer::GradleModuleExtractor::new(source, file_path);
        let (_, mod_rels) = module_extractor.extract();
        relationships.extend(mod_rels);

        return Ok(ParsedFile {
            element_count: elements.len(),
            elements,
            relationships,
        });
    } else if file_name == "pom.xml" {
        let extractor = crate::indexer::MavenExtractor::new(source, file_path);
        let (elements, relationships) = extractor.extract();
        return Ok(ParsedFile {
            element_count: elements.len(),
            elements,
            relationships,
        });
    } else if let Some((elements, relationships)) = try_extract_android(source, file_path) {
        return Ok(ParsedFile {
            element_count: elements.len(),
            elements,
            relationships,
        });
    } else if file_path.ends_with(".xml") {
        // Handle generic XML files not caught by Android extractors
        let extractor = crate::indexer::GenericXmlExtractor::new(source, file_path);
        let (elements, relationships) = extractor.extract();
        return Ok(ParsedFile {
            element_count: elements.len(),
            elements,
            relationships,
        });
    }

    let language = match get_language(file_path) {
        Some(l) => l,
        None => {
            return Ok(ParsedFile {
                element_count: 0,
                elements: vec![],
                relationships: vec![],
            })
        }
    };

    thread_local! {
        static PARSERS: std::cell::RefCell<Vec<Option<tree_sitter::Parser>>> = std::cell::RefCell::new(vec![None, None, None, None, None, None]);
    }

    let parser_idx = match language {
        "go" => 0,
        "typescript" => 1,
        "python" => 2,
        "rust" => 3,
        "java" => 4,
        "kotlin" => 5,
        _ => {
            return Ok(ParsedFile {
                element_count: 0,
                elements: vec![],
                relationships: vec![],
            })
        }
    };

    let tree = PARSERS.with(|parsers| {
        let mut parsers = parsers.borrow_mut();
        let parser = parsers[parser_idx].get_or_insert_with(|| {
            let mut p = tree_sitter::Parser::new();
            let lang: tree_sitter::Language = match language {
                "go" => tree_sitter_go::LANGUAGE.into(),
                "typescript" => tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into(),
                "python" => tree_sitter_python::LANGUAGE.into(),
                "rust" => tree_sitter_rust::LANGUAGE.into(),
                "java" => tree_sitter_java::LANGUAGE.into(),
                "kotlin" => tree_sitter_kotlin_ng::LANGUAGE.into(),
                _ => return p,
            };
            let _ = p.set_language(&lang);
            p
        });
        parser.parse(source, None).ok_or("parse failed")
    })?;

    let mut room_elements = Vec::new();
    let mut room_relationships = Vec::new();
    let mut hilt_elements = Vec::new();
    let mut hilt_relationships = Vec::new();
    let mut res_ref_relationships = Vec::new();
    let mut annotation_elements = Vec::new();
    let mut annotation_relationships = Vec::new();
    let mut resource_link_rels = Vec::new();
    let mut nav_elements = Vec::new();
    let mut nav_relationships = Vec::new();
    if language == "kotlin" {
        let room_extractor = crate::indexer::AndroidRoomExtractor::new(source, file_path);
        let (re, rr) = room_extractor.extract();
        room_elements = re;
        room_relationships = rr;

        let hilt_extractor = crate::indexer::AndroidHiltExtractor::new(source, file_path);
        let (he, hr) = hilt_extractor.extract();
        hilt_elements = he;
        hilt_relationships = hr;

        let res_ref_extractor = crate::indexer::AndroidResourceRefExtractor::new(source, file_path);
        let (_, rr) = res_ref_extractor.extract();
        res_ref_relationships = rr;

        // Extract Kotlin annotations
        let annotation_extractor =
            crate::indexer::KotlinAnnotationExtractor::new(source, file_path);
        let (ae, ar) = annotation_extractor.extract(&tree);
        annotation_elements = ae;
        annotation_relationships = ar;

        // Extract enhanced resource linking
        let resource_linker = crate::indexer::AndroidResourceLinker::new(source, file_path);
        let (_, rl) = resource_linker.extract();
        resource_link_rels = rl;

        // Extract navigation patterns
        let frag_nav_extractor = crate::indexer::FragmentNavExtractor::new(source, file_path);
        let (_, fnr) = frag_nav_extractor.extract();
        nav_relationships.extend(fnr);

        let leanback_extractor = crate::indexer::LeanbackNavExtractor::new(source, file_path);
        let (lne, lnr) = leanback_extractor.extract();
        nav_elements.extend(lne);
        nav_relationships.extend(lnr);

        // JetpackNavExtractor Kotlin DSL
        if content
            .windows(b"NavGraphBuilder".len())
            .any(|w| w == b"NavGraphBuilder")
            || content
                .windows(b"composable(".len())
                .any(|w| w == b"composable(")
        {
            let nav_dsl_extractor = crate::indexer::JetpackNavExtractor::new(source, file_path);
            let (ne, nr) = nav_dsl_extractor.extract_kotlin_dsl();
            nav_elements.extend(ne);
            nav_relationships.extend(nr);
        }
    }

    let extractor = crate::indexer::EntityExtractor::new(source, file_path, language);
    let (mut elements, mut relationships) = extractor.extract(&tree);

    // Extract calls with resolution using CallGraphBuilder
    let call_rels = crate::indexer::call_graph::extract_calls_with_resolution(
        &tree, source, file_path, language,
    );
    relationships.extend(call_rels);

    elements.extend(room_elements);
    relationships.extend(room_relationships);
    elements.extend(hilt_elements);
    relationships.extend(hilt_relationships);
    relationships.extend(res_ref_relationships);
    elements.extend(annotation_elements);
    relationships.extend(annotation_relationships);
    relationships.extend(resource_link_rels);
    elements.extend(nav_elements);
    relationships.extend(nav_relationships);

    Ok(ParsedFile {
        element_count: elements.len(),
        elements,
        relationships,
    })
}

pub fn index_files_parallel(
    graph: &GraphEngine,
    files: &[String],
    verbose: bool,
) -> Result<usize, Box<dyn std::error::Error>> {
    if files.is_empty() {
        return Ok(0);
    }

    let total_count = files.len();
    let progress = Arc::new(std::sync::atomic::AtomicUsize::new(0));

    eprintln!("Parsing {} files in parallel...", total_count);

    let results: Vec<Result<ParsedFile, Box<dyn std::error::Error + Send + Sync>>> = files
        .par_iter()
        .map(|file_path| {
            let count = progress.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
            if count.is_multiple_of(1000) {
                eprint!("\r  Parsed {}/{} files", count, total_count);
            }
            extract_elements_for_file(file_path)
        })
        .collect();

    eprintln!("\r  Parsed {}/{} files", total_count, total_count);

    let (mut structure_elements, mut structure_rels) = generate_physical_structure(
        std::env::current_dir()
            .unwrap_or_default()
            .to_str()
            .unwrap_or("."),
        files,
    );

    let mut all_elements = Vec::new();
    let mut all_relationships = Vec::new();

    all_elements.append(&mut structure_elements);
    all_relationships.append(&mut structure_rels);

    let mut total = 0;

    for result in results {
        match result {
            Ok(parsed) => {
                total += parsed.element_count;
                all_elements.extend(parsed.elements);
                all_relationships.extend(parsed.relationships);
            }
            Err(e) => {
                tracing::debug!("Failed to parse file: {}", e);
            }
        }
    }

    if verbose {
        eprintln!("Detecting execution flows and processes...");
    }

    let process_result = detect_processes(&all_elements, &all_relationships, None);
    if verbose {
        eprintln!(
            "  Detected {} execution flows spanning {} relationships",
            process_result.process_elements.len(),
            process_result.process_relationships.len()
        );
    }
    all_elements.extend(process_result.process_elements);
    all_relationships.extend(process_result.process_relationships);

    if verbose {
        eprintln!("Detecting frameworks...");
    }
    let (fw_elements, fw_rels) =
        FrameworkDetector::detect_frameworks(&all_elements, &all_relationships);
    if verbose {
        eprintln!("  Detected {} frameworks", fw_elements.len());
    }
    all_elements.extend(fw_elements);
    all_relationships.extend(fw_rels);

    resolve_call_edges_inline(&mut all_elements, &mut all_relationships);

    // Extract microservice relationships (gRPC service-to-service calls)
    if verbose {
        eprintln!("Detecting microservice calls...");
    }
    let microservice_rels = extract_microservice_relationships(
        std::env::current_dir()
            .unwrap_or_default()
            .to_str()
            .unwrap_or("."),
    );
    if verbose {
        eprintln!("  Detected {} microservice calls", microservice_rels.len());
    }
    all_relationships.extend(microservice_rels);

    eprintln!(
        "Inserting {} elements and {} relationships...",
        all_elements.len(),
        all_relationships.len()
    );

    if !all_elements.is_empty() {
        let total_elements = all_elements.len();
        const ELEM_BATCH_SIZE: usize = 5000;
        for (i, chunk) in all_elements.chunks(ELEM_BATCH_SIZE).enumerate() {
            graph.insert_elements(chunk)?;
            if verbose {
                let progress = ((i + 1) * ELEM_BATCH_SIZE).min(total_elements);
                eprint!("\r  Inserted {}/{} elements", progress, total_elements);
            }
        }
        if verbose {
            eprintln!(
                "\r  Inserted {}/{} elements",
                total_elements, total_elements
            );
        }
    }

    if !all_relationships.is_empty() {
        let total_rels = all_relationships.len();
        const REL_BATCH_SIZE: usize = 5000;
        for (i, chunk) in all_relationships.chunks(REL_BATCH_SIZE).enumerate() {
            graph.insert_relationships(chunk)?;
            if verbose {
                let progress = ((i + 1) * REL_BATCH_SIZE).min(total_rels);
                eprint!("\r  Inserted {}/{} relationships", progress, total_rels);
            }
        }
        if verbose {
            eprintln!("\r  Inserted {}/{} relationships", total_rels, total_rels);
        }
    }

    Ok(total)
}

pub fn index_file_sync(
    graph: &GraphEngine,
    parser_manager: &mut ParserManager,
    file_path: &str,
) -> Result<usize, Box<dyn std::error::Error>> {
    let content = std::fs::read(file_path)?;
    let source = content.as_slice();

    if file_path.ends_with(".tf") {
        let extractor = TerraformExtractor::new(source, file_path);
        let (elements, relationships) = extractor.extract();
        if elements.is_empty() && relationships.is_empty() {
            return Ok(0);
        }
        let _ = graph.insert_elements(&elements);
        let _ = graph.insert_relationships(&relationships);
        return Ok(elements.len());
    }

    if is_cicd_yaml_file(std::path::Path::new(file_path))
        && (file_path.ends_with(".yml") || file_path.ends_with(".yaml"))
    {
        let extractor = CicdYamlExtractor::new(source, file_path);
        let (elements, relationships) = extractor.extract();
        if elements.is_empty() && relationships.is_empty() {
            return Ok(0);
        }
        let _ = graph.insert_elements(&elements);
        let _ = graph.insert_relationships(&relationships);
        return Ok(elements.len());
    }

    let file_name = std::path::Path::new(file_path)
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("");

    if let Some((elements, relationships)) = try_extract_android(source, file_path) {
        if elements.is_empty() && relationships.is_empty() {
            return Ok(0);
        }
        let _ = graph.insert_elements(&elements);
        let _ = graph.insert_relationships(&relationships);
        return Ok(elements.len());
    }

    if file_path.ends_with("pom.xml") {
        let extractor = crate::indexer::MavenExtractor::new(source, file_path);
        let (elements, relationships) = extractor.extract();
        if elements.is_empty() && relationships.is_empty() {
            return Ok(0);
        }
        let _ = graph.insert_elements(&elements);
        let _ = graph.insert_relationships(&relationships);
        return Ok(elements.len());
    }

    if file_name == "build.gradle"
        || file_name == "build.gradle.kts"
        || file_name == "settings.gradle"
        || file_name == "settings.gradle.kts"
    {
        let extractor = crate::indexer::GradleExtractor::new(source, file_path);
        let (elements, mut relationships) = extractor.extract();

        // Also extract module dependencies
        let module_extractor = crate::indexer::GradleModuleExtractor::new(source, file_path);
        let (_, mod_rels) = module_extractor.extract();
        relationships.extend(mod_rels);

        if elements.is_empty() && relationships.is_empty() {
            return Ok(0);
        }
        let _ = graph.insert_elements(&elements);
        let _ = graph.insert_relationships(&relationships);
        return Ok(elements.len());
    }

    let language = if file_path.ends_with(".go") {
        "go"
    } else if file_path.ends_with(".ts") || file_path.ends_with(".js") {
        "typescript"
    } else if file_path.ends_with(".py") {
        "python"
    } else if file_path.ends_with(".rs") {
        "rust"
    } else if file_path.ends_with(".java") {
        "java"
    } else if file_path.ends_with(".kt") || file_path.ends_with(".kts") {
        "kotlin"
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
    let (mut elements, mut relationships) = extractor.extract(&tree);

    // Extract calls with resolution using CallGraphBuilder
    let call_rels = crate::indexer::call_graph::extract_calls_with_resolution(
        &tree, source, file_path, language,
    );
    relationships.extend(call_rels);

    // Android-specific extractors for Kotlin files
    if language == "kotlin" {
        let room_extractor = crate::indexer::AndroidRoomExtractor::new(source, file_path);
        let (room_elements, room_relationships) = room_extractor.extract();
        elements.extend(room_elements);
        relationships.extend(room_relationships);

        let hilt_extractor = crate::indexer::AndroidHiltExtractor::new(source, file_path);
        let (hilt_elements, hilt_relationships) = hilt_extractor.extract();
        elements.extend(hilt_elements);
        relationships.extend(hilt_relationships);

        let res_ref_extractor = crate::indexer::AndroidResourceRefExtractor::new(source, file_path);
        let (_, res_ref_relationships) = res_ref_extractor.extract();
        relationships.extend(res_ref_relationships);

        // Extract Kotlin annotations
        let annotation_extractor =
            crate::indexer::KotlinAnnotationExtractor::new(source, file_path);
        let (annotation_elements, annotation_relationships) = annotation_extractor.extract(&tree);
        elements.extend(annotation_elements);
        relationships.extend(annotation_relationships);

        // Extract enhanced resource linking
        let resource_linker = crate::indexer::AndroidResourceLinker::new(source, file_path);
        let (_, resource_link_rels) = resource_linker.extract();
        relationships.extend(resource_link_rels);

        // Extract navigation patterns
        let frag_nav_extractor = crate::indexer::FragmentNavExtractor::new(source, file_path);
        let (_, frag_nav_relationships) = frag_nav_extractor.extract();
        relationships.extend(frag_nav_relationships);

        let leanback_extractor = crate::indexer::LeanbackNavExtractor::new(source, file_path);
        let (leanback_elements, leanback_relationships) = leanback_extractor.extract();
        elements.extend(leanback_elements);
        relationships.extend(leanback_relationships);

        // JetpackNavExtractor Kotlin DSL
        if content
            .windows(b"NavGraphBuilder".len())
            .any(|w| w == b"NavGraphBuilder")
            || content
                .windows(b"composable(".len())
                .any(|w| w == b"composable(")
        {
            let nav_dsl_extractor = crate::indexer::JetpackNavExtractor::new(source, file_path);
            let (nav_elements, nav_relationships) = nav_dsl_extractor.extract_kotlin_dsl();
            elements.extend(nav_elements);
            relationships.extend(nav_relationships);
        }
    }

    if elements.is_empty() && relationships.is_empty() {
        return Ok(0);
    }

    let _ = graph.insert_elements(&elements);
    let _ = graph.insert_relationships(&relationships);

    Ok(elements.len())
}

pub fn reindex_file_sync(
    graph: &GraphEngine,
    parser_manager: &mut ParserManager,
    file_path: &str,
) -> Result<usize, Box<dyn std::error::Error>> {
    graph.remove_elements_by_file(file_path)?;
    graph.remove_relationships_by_source(file_path)?;

    index_file_sync(graph, parser_manager, file_path)
}

pub struct IncrementalIndexResult {
    pub changed_files: Vec<String>,
    pub dependent_files: Vec<String>,
    pub total_files_processed: usize,
    pub elements_indexed: usize,
}

pub async fn incremental_index_sync(
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
        graph.remove_elements_by_file(deleted_file)?;
        graph.remove_relationships_by_source(deleted_file)?;
    }

    let all_relationships = graph.all_relationships()?;
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
            match reindex_file_sync(graph, parser_manager, file_path) {
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

#[allow(dead_code)]
pub async fn index_file(
    graph: &GraphEngine,
    parser_manager: &mut ParserManager,
    file_path: &str,
) -> Result<usize, Box<dyn std::error::Error>> {
    index_file_sync(graph, parser_manager, file_path)
}

#[allow(dead_code)]
pub async fn reindex_file(
    graph: &GraphEngine,
    parser_manager: &mut ParserManager,
    file_path: &str,
) -> Result<usize, Box<dyn std::error::Error>> {
    reindex_file_sync(graph, parser_manager, file_path)
}

#[allow(dead_code)]
pub async fn incremental_index(
    graph: &GraphEngine,
    parser_manager: &mut ParserManager,
    root_path: &str,
) -> Result<IncrementalIndexResult, Box<dyn std::error::Error>> {
    incremental_index_sync(graph, parser_manager, root_path).await
}

pub struct IndexWithProgressResult {
    pub total_files: usize,
    pub indexed_files: usize,
    pub skipped_files: usize,
}

pub async fn index_with_progress<F>(
    graph: &GraphEngine,
    _parser_manager: &mut ParserManager,
    path: &str,
    progress_callback: F,
) -> Result<IndexWithProgressResult, Box<dyn std::error::Error + Send + Sync + 'static>>
where
    F: Fn(usize, &str) + Send + Sync,
{
    let files = match find_files_sync(path) {
        Ok(f) => f,
        Err(e) => {
            return Err(Box::new(std::io::Error::other(e.to_string()))
                as Box<dyn std::error::Error + Send + Sync>)
        }
    };
    let total_files = files.len();
    let progress = Arc::new(std::sync::atomic::AtomicUsize::new(0));

    #[allow(clippy::type_complexity)]
    let results: Vec<(
        String,
        Result<ParsedFile, Box<dyn std::error::Error + Send + Sync>>,
    )> = files
        .par_iter()
        .map(|file_path| {
            let count = progress.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
            progress_callback(count, file_path);
            let parsed = extract_elements_for_file(file_path);
            (file_path.clone(), parsed)
        })
        .collect();

    let mut indexed_files = 0;
    let mut skipped_files = 0;

    let (mut structure_elements, mut structure_rels) = generate_physical_structure(
        std::env::current_dir()
            .unwrap_or_default()
            .to_str()
            .unwrap_or("."),
        &files,
    );

    let mut all_elements = Vec::new();
    let mut all_relationships = Vec::new();

    all_elements.append(&mut structure_elements);
    all_relationships.append(&mut structure_rels);

    for (file_path, result) in results {
        match result {
            Ok(parsed) => {
                if parsed.element_count > 0
                    || !parsed.elements.is_empty()
                    || !parsed.relationships.is_empty()
                {
                    indexed_files += 1;
                    all_elements.extend(parsed.elements);
                    all_relationships.extend(parsed.relationships);
                } else {
                    skipped_files += 1;
                }
            }
            Err(e) => {
                tracing::warn!("Failed to index {}: {}", file_path, e);
                skipped_files += 1;
            }
        }
    }

    if !all_elements.is_empty() {
        if let Err(e) = graph.insert_elements(&all_elements) {
            tracing::warn!("Failed to batch insert elements: {}", e);
        }
    }

    if !all_relationships.is_empty() {
        if let Err(e) = graph.insert_relationships(&all_relationships) {
            tracing::warn!("Failed to batch insert relationships: {}", e);
        }
    }

    // Extract microservice relationships (gRPC service-to-service calls)
    let microservice_rels = extract_microservice_relationships(path);
    if !microservice_rels.is_empty() {
        if let Err(e) = graph.insert_relationships(&microservice_rels) {
            tracing::warn!("Failed to insert microservice relationships: {}", e);
        }
    }

    if let Err(e) = graph.resolve_call_edges() {
        tracing::warn!("Failed to resolve call edges: {}", e);
    }

    Ok(IndexWithProgressResult {
        total_files,
        indexed_files,
        skipped_files,
    })
}

pub fn generate_physical_structure(
    repo_root: &str,
    files: &[String],
) -> (Vec<CodeElement>, Vec<Relationship>) {
    let mut elements = Vec::new();
    let mut relationships = Vec::new();
    let mut seen_folders = std::collections::HashSet::new();

    let root_name = std::path::Path::new(repo_root)
        .file_name()
        .map(|n| n.to_string_lossy().into_owned())
        .unwrap_or_else(|| repo_root.to_string());

    elements.push(CodeElement {
        qualified_name: repo_root.to_string(),
        element_type: "Project".to_string(),
        name: root_name,
        file_path: repo_root.to_string(),
        ..Default::default()
    });

    for file in files {
        let path = std::path::Path::new(file);

        elements.push(CodeElement {
            qualified_name: file.to_string(),
            element_type: "File".to_string(),
            name: path
                .file_name()
                .unwrap_or_default()
                .to_string_lossy()
                .into_owned(),
            file_path: file.to_string(),
            ..Default::default()
        });

        let current_dir = path.parent();
        if let Some(parent) = current_dir {
            let parent_str = parent.to_string_lossy().into_owned();

            relationships.push(Relationship {
                id: None,
                source_qualified: if parent_str.is_empty() {
                    repo_root.to_string()
                } else {
                    parent_str.clone()
                },
                target_qualified: file.to_string(),
                rel_type: "contains".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({}),
            });

            let mut node_dir = parent;
            while let Some(current_str) = node_dir.to_str() {
                if current_str.is_empty() {
                    break;
                }

                if !seen_folders.contains(current_str) {
                    seen_folders.insert(current_str.to_string());

                    let dir_name = node_dir
                        .file_name()
                        .map(|n| n.to_string_lossy().into_owned())
                        .unwrap_or_else(|| current_str.to_string());

                    elements.push(CodeElement {
                        qualified_name: current_str.to_string(),
                        element_type: "Folder".to_string(),
                        name: dir_name,
                        file_path: current_str.to_string(),
                        ..Default::default()
                    });

                    let parent_of_dir = node_dir.parent().unwrap_or(std::path::Path::new(""));
                    let target = if parent_of_dir.as_os_str().is_empty() {
                        repo_root.to_string()
                    } else {
                        parent_of_dir.to_string_lossy().into_owned()
                    };

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: target,
                        target_qualified: current_str.to_string(),
                        rel_type: "contains".to_string(),
                        confidence: 1.0,
                        metadata: serde_json::json!({}),
                    });
                }

                node_dir = match node_dir.parent() {
                    Some(p) => {
                        if p.as_os_str().is_empty() {
                            break;
                        }
                        p
                    }
                    None => break,
                };
            }
        } else {
            relationships.push(Relationship {
                id: None,
                source_qualified: repo_root.to_string(),
                target_qualified: file.to_string(),
                rel_type: "contains".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({}),
            });
        }
    }

    (elements, relationships)
}

pub fn resolve_call_edges_inline(elements: &mut [CodeElement], relationships: &mut [Relationship]) {
    if relationships.is_empty() {
        return;
    }

    let mut by_name: std::collections::HashMap<&str, &str> = std::collections::HashMap::new();
    let mut by_name_and_file: std::collections::HashMap<(&str, &str), &str> =
        std::collections::HashMap::new();

    for elem in elements.iter() {
        if elem.element_type == "function" {
            let key = (elem.name.as_str(), elem.file_path.as_str());
            by_name_and_file.insert(key, elem.qualified_name.as_str());
            if !by_name.contains_key(elem.name.as_str()) {
                by_name.insert(&elem.name, &elem.qualified_name);
            }
        }
    }

    let mut resolved = 0;
    let mut unresolved = Vec::new();

    for rel in relationships.iter_mut() {
        if rel.rel_type == "calls" && rel.target_qualified.starts_with("__unresolved__") {
            let bare_name = rel.target_qualified.trim_start_matches("__unresolved__");
            let file_hint = rel
                .metadata
                .get("callee_file_hint")
                .and_then(|v| v.as_str());

            let target_qn = if let Some(hint) = file_hint {
                by_name_and_file
                    .get(&(bare_name, hint))
                    .or_else(|| by_name.get(bare_name))
                    .map(|s| s.to_string())
                    .unwrap_or_else(|| bare_name.to_string())
            } else {
                by_name
                    .get(bare_name)
                    .map(|s| s.to_string())
                    .unwrap_or_else(|| bare_name.to_string())
            };

            rel.target_qualified = target_qn;
            rel.confidence = 1.0;
            rel.metadata = serde_json::json!({});
            resolved += 1;
        } else if rel.rel_type == "calls" {
            unresolved.push(rel.target_qualified.clone());
        }
    }

    if resolved > 0 {
        eprintln!(
            "Resolved {} call edges inline (no DB pass needed)",
            resolved
        );
    }
}

pub fn detect_gradle_submodules(settings_content: &[u8]) -> Vec<String> {
    let content = std::str::from_utf8(settings_content).unwrap_or("");
    let re = regex::Regex::new(r#"include\(["']([^"']+)["']\)"#).unwrap();
    re.captures_iter(content)
        .filter_map(|cap| cap.get(1).map(|m| m.as_str().to_string()))
        .collect()
}

pub fn detect_maven_submodules(pom_content: &[u8]) -> Vec<String> {
    let content = std::str::from_utf8(pom_content).unwrap_or("");
    let re = regex::Regex::new(r"<module>([^<]+)</module>").unwrap();
    re.captures_iter(content)
        .filter_map(|cap| cap.get(1).map(|m| m.as_str().trim().to_string()))
        .collect()
}

/// Extract microservice relationships from Go services
/// Scans internal/external/ directories for gRPC client calls
pub fn extract_microservice_relationships(project_path: &str) -> Vec<Relationship> {
    let extractor = MicroserviceExtractor::new();
    extractor.extract(project_path)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_detect_gradle_submodules() {
        let content = br#"include("api")
include("core")
include("web-app")"#;
        let submodules = detect_gradle_submodules(content);
        assert!(submodules.contains(&"api".to_string()));
        assert!(submodules.contains(&"core".to_string()));
        assert!(submodules.contains(&"web-app".to_string()));
    }

    #[test]
    fn test_detect_maven_submodules() {
        let content = br#"<?xml version="1.0"?>
<project>
    <modules>
        <module>api</module>
        <module>core</module>
    </modules>
</project>"#;
        let submodules = detect_maven_submodules(content);
        assert!(submodules.contains(&"api".to_string()));
        assert!(submodules.contains(&"core".to_string()));
    }
}
