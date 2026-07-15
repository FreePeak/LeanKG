pub mod cicd;
pub mod event_edges;
pub mod extractor;
pub mod git;
pub mod git_workspace;
pub mod microservice;
pub mod parser;
pub mod process_processor;
pub mod regex_cache;
pub mod route_extractor;
pub mod sfc;
pub mod sql;
pub mod swift;
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
pub mod android_workmanager;
pub mod call_graph;
pub mod config_extractor;
pub mod coroutine_dispatcher;
pub mod framework_detector;
pub mod gradle_extractor;
pub mod gradle_module_extractor;
pub mod kotlin_annotations;
pub mod kotlin_utils;
pub mod maven_extractor;
pub mod viewmodel_repository;
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
pub use android_workmanager::AndroidWorkManagerExtractor;
#[allow(unused_imports)]
pub use call_graph::{extract_calls_with_resolution, CallGraphBuilder};
pub use cicd::*;
pub use config_extractor::*;
pub use coroutine_dispatcher::CoroutineDispatcherExtractor;
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
pub use viewmodel_repository::ViewModelRepositoryExtractor;
pub use xml_generic::GenericXmlExtractor;
pub use xml_layout::*;

use crate::db::models::{CodeElement, Relationship};
use crate::graph::GraphEngine;
use ignore::WalkBuilder;
use rayon::prelude::*;
use std::collections::HashSet;
use std::path::Path;
use std::sync::Arc;

use std::collections::HashMap;
use std::sync::OnceLock;

/// Cache for go.mod module name -> directory mapping
static GO_MOD_CACHE: OnceLock<std::sync::Mutex<HashMap<String, Option<String>>>> = OnceLock::new();

fn go_mod_cache() -> &'static std::sync::Mutex<HashMap<String, Option<String>>> {
    GO_MOD_CACHE.get_or_init(|| std::sync::Mutex::new(HashMap::new()))
}

/// Find the nearest go.mod directory for a given file path
fn find_go_mod_dir(file_path: &str) -> Option<String> {
    let path = Path::new(file_path);
    for ancestor in path.ancestors() {
        let go_mod = ancestor.join("go.mod");
        if go_mod.exists() {
            return Some(ancestor.to_string_lossy().to_string());
        }
    }
    None
}

/// Parse module name from go.mod content
fn parse_go_module_name(go_mod_path: &str) -> Option<String> {
    let content = std::fs::read_to_string(go_mod_path).ok()?;
    for line in content.lines() {
        let trimmed = line.trim();
        if let Some(rest) = trimmed.strip_prefix("module ") {
            return Some(rest.trim().to_string());
        }
    }
    None
}

/// Get the module name for a given Go file path
fn get_go_module_name(file_path: &str) -> Option<String> {
    let go_mod_dir = find_go_mod_dir(file_path)?;
    let go_mod_path = format!("{}/go.mod", go_mod_dir);

    let mut cache = go_mod_cache().lock().unwrap();
    if let Some(cached) = cache.get(&go_mod_dir) {
        return cached.clone();
    }

    let module_name = parse_go_module_name(&go_mod_path);
    cache.insert(go_mod_dir.clone(), module_name.clone());
    module_name
}

/// Resolve a Go module import path to a filesystem path relative to the project root
fn resolve_go_import(import_path: &str, file_path: &str) -> Option<String> {
    // Standard library imports (no dots, no slashes before first path element)
    // Examples: "fmt", "net/http", "crypto/tls"
    if !import_path.contains('.')
        && import_path
            .chars()
            .next()
            .is_some_and(|c| c.is_ascii_lowercase())
    {
        // Could be stdlib, skip
        return None;
    }

    let go_mod_dir = find_go_mod_dir(file_path)?;
    let module_name = get_go_module_name(file_path)?;

    // Check if the import belongs to this module
    if let Some(sub_path) = import_path.strip_prefix(&module_name) {
        let sub_path = sub_path.trim_start_matches('/');
        let resolved = format!("{}/{}", go_mod_dir, sub_path);
        return Some(resolved);
    }

    // For external module imports, try resolving within the project tree
    // by matching the import path suffix against known directories
    let parts: Vec<&str> = import_path.rsplitn(3, '/').collect();
    if parts.len() >= 2 {
        // Check if the go_mod_dir ancestor has a matching subdirectory
        let go_mod_parent = Path::new(&go_mod_dir).parent()?;
        let candidate = go_mod_parent.join(parts[0]);
        if candidate.exists() {
            return Some(candidate.to_string_lossy().to_string());
        }
        if parts.len() >= 3 {
            let candidate2 = go_mod_parent.join(parts[1]).join(parts[0]);
            if candidate2.exists() {
                return Some(candidate2.to_string_lossy().to_string());
            }
        }
    }

    None
}

/// Resolve Go import targets in relationships to filesystem paths
fn resolve_go_imports(relationships: &mut [Relationship], file_path: &str, language: &str) {
    if language != "go" {
        return;
    }

    for rel in relationships.iter_mut() {
        if rel.rel_type == "imports" && !rel.target_qualified.is_empty() {
            // Skip if already a filesystem path (contains / or starts with .)
            if rel.target_qualified.starts_with('.') || rel.target_qualified.starts_with('/') {
                continue;
            }
            if let Some(resolved) = resolve_go_import(&rel.target_qualified, file_path) {
                rel.target_qualified = resolved;
            }
        }
    }
}

const DEFAULT_INDEX_IGNORED_DIRS: &[&str] = &[
    ".git",
    ".leankg",
    ".worktrees",
    "worktrees",
    "target",
    "node_modules",
    "vendor",
    "__pycache__",
    ".gradle",
    ".idea",
    ".vscode",
    // Build outputs / generated / caches
    "dist",
    "build",
    "out",
    "bin",
    "obj",
    "coverage",
    ".next",
    ".nuxt",
    ".svelte-kit",
    ".turbo",
    ".cache",
    ".parcel-cache",
    ".pytest_cache",
    ".ruff_cache",
    ".mypy_cache",
    ".tox",
    ".venv",
    "venv",
    "env",
    ".terraform",
    ".terragrunt-cache",
    "Godeps",
    "k8s",
    // Generated proto / SDK outputs
    "pb",
    "pb-go",
    "gen",
    "generated",
    "swagger",
    "openapi",
    ".openapi-generator",
    // Snapshots / fixtures / large data files
    "fixtures",
    "__snapshots__",
    "testdata",
    "docs",
    "tmp",
    "logs",
];

/// Maximum file size to read for parsing. Files larger than this are skipped to
/// bound memory + parse time. Default 2 MiB. Override with `LEANKG_MAX_FILE_SIZE` (bytes).
fn max_file_size() -> u64 {
    std::env::var("LEANKG_MAX_FILE_SIZE")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(2 * 1024 * 1024)
}

pub fn find_files_sync(root: &str) -> Result<Vec<String>, Box<dyn std::error::Error>> {
    let mut files = Vec::new();
    let extensions = [
        "go", "ts", "js", "py", "rs", "java", "kt", "kts", "tf", "yml", "yaml", "json", "toml",
        "mod", "xml", "dart",
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

    let root_path = Path::new(root).to_path_buf();
    let walker = WalkBuilder::new(root)
        .follow_links(true)
        .filter_entry(move |entry| !is_default_ignored_entry(&root_path, entry.path()))
        .build();

    let max_size = max_file_size();

    for entry in walker.flatten() {
        let path = entry.path();

        if !path.is_file() {
            continue;
        }

        // Skip files that are too large to parse efficiently.
        if let Ok(meta) = std::fs::metadata(path) {
            if meta.len() > max_size {
                tracing::debug!(
                    "Skipping oversized file ({} bytes): {}",
                    meta.len(),
                    path.display()
                );
                continue;
            }
        }

        let ext = path.extension().and_then(|e| e.to_str()).unwrap_or("");
        let file_name = path.file_name().and_then(|n| n.to_str()).unwrap_or("");

        let is_valid_file = config_files.contains(&file_name)
            || (path.to_string_lossy().contains("/res/") && ext == "xml")
            || extensions.contains(&ext)
            || is_cicd_yaml_file(path);

        if is_valid_file {
            files.push(path.to_string_lossy().to_string());
        }
    }

    Ok(files)
}

fn is_default_ignored_entry(root: &Path, path: &Path) -> bool {
    if path == root {
        return false;
    }

    let relative = path.strip_prefix(root).unwrap_or(path);
    relative.components().any(|component| {
        let segment = component.as_os_str().to_string_lossy();
        DEFAULT_INDEX_IGNORED_DIRS.contains(&segment.as_ref())
    })
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
        "kt" | "kts" => Some("kotlin"),
        "dart" => Some("dart"),
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
    if let Ok(meta) = std::fs::metadata(file_path) {
        if meta.len() > max_file_size() {
            tracing::debug!(
                "Skipping oversized file ({} bytes): {}",
                meta.len(),
                file_path
            );
            return Ok(ParsedFile {
                element_count: 0,
                elements: vec![],
                relationships: vec![],
            });
        }
    }
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
        static PARSERS: std::cell::RefCell<Vec<Option<tree_sitter::Parser>>> = std::cell::RefCell::new(vec![None, None, None, None, None, None, None, None]);
    }

    let parser_idx = match language {
        "go" => 0,
        "typescript" => 1,
        "python" => 2,
        "rust" => 3,
        "java" => 4,
        "kotlin" => 5,
        "dart" => 6,
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
                "dart" => tree_sitter_dart::LANGUAGE.into(),
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
    let mut workmanager_elements = Vec::new();
    let mut workmanager_relationships = Vec::new();
    let mut coroutine_dispatcher_elements = Vec::new();
    let mut coroutine_dispatcher_relationships = Vec::new();
    let mut vm_repo_elements = Vec::new();
    let mut vm_repo_relationships = Vec::new();
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

        // Extract WorkManager patterns
        let workmanager_extractor =
            crate::indexer::AndroidWorkManagerExtractor::new(source, file_path);
        let (we, wr) = workmanager_extractor.extract();
        workmanager_elements = we;
        workmanager_relationships = wr;

        // Extract coroutine dispatcher usage
        let coroutine_extractor =
            crate::indexer::CoroutineDispatcherExtractor::new(source, file_path);
        let (cde, cdr) = coroutine_extractor.extract();
        coroutine_dispatcher_elements = cde;
        coroutine_dispatcher_relationships = cdr;

        // Extract ViewModel/Repository patterns
        let vm_repo_extractor =
            crate::indexer::ViewModelRepositoryExtractor::new(source, file_path);
        let (vre, vrr) = vm_repo_extractor.extract();
        vm_repo_elements = vre;
        vm_repo_relationships = vrr;
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
    elements.extend(workmanager_elements);
    relationships.extend(workmanager_relationships);
    elements.extend(coroutine_dispatcher_elements);
    relationships.extend(coroutine_dispatcher_relationships);
    elements.extend(vm_repo_elements);
    relationships.extend(vm_repo_relationships);

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

        // Mark every touched element as embedding-stale so the next
        // `embed` run picks them up incrementally. Only fires when the
        // `embeddings` feature is compiled in; otherwise no-op.
        #[cfg(feature = "embeddings")]
        {
            let touched: Vec<String> = all_elements
                .iter()
                .map(|e| e.qualified_name.clone())
                .collect();
            if let Err(e) =
                crate::embeddings::state::mark_stale_for_qualified_names(graph.db(), &touched)
            {
                tracing::warn!("embedding_state stale-mark failed: {}", e);
            }
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
    if let Ok(meta) = std::fs::metadata(file_path) {
        if meta.len() > max_file_size() {
            tracing::debug!(
                "Skipping oversized file ({} bytes): {}",
                meta.len(),
                file_path
            );
            return Ok(0);
        }
    }
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
    } else if file_path.ends_with(".dart") {
        "dart"
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

        // Extract WorkManager patterns
        let workmanager_extractor =
            crate::indexer::AndroidWorkManagerExtractor::new(source, file_path);
        let (workmanager_elements, workmanager_relationships) = workmanager_extractor.extract();
        elements.extend(workmanager_elements);
        relationships.extend(workmanager_relationships);

        // Extract coroutine dispatcher usage
        let coroutine_extractor =
            crate::indexer::CoroutineDispatcherExtractor::new(source, file_path);
        let (coroutine_elements, coroutine_rels) = coroutine_extractor.extract();
        elements.extend(coroutine_elements);
        relationships.extend(coroutine_rels);

        // Extract ViewModel/Repository patterns
        let vm_repo_extractor =
            crate::indexer::ViewModelRepositoryExtractor::new(source, file_path);
        let (vm_repo_elements, vm_repo_relationships) = vm_repo_extractor.extract();
        elements.extend(vm_repo_elements);
        relationships.extend(vm_repo_relationships);
    }

    if elements.is_empty() && relationships.is_empty() {
        return Ok(0);
    }

    // Resolve Go import paths to filesystem paths
    if language == "go" {
        resolve_go_imports(&mut relationships, file_path, language);
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
    let root = std::path::Path::new(root_path);
    if !crate::indexer::git_workspace::has_git_context(root) {
        return Err(
            "Not a git repository (and no nested git repos found). Cannot perform incremental indexing."
                .into(),
        );
    }

    let workspace_root = if GitAnalyzer::is_git_repo_at(root) {
        GitAnalyzer::get_repo_root_at(root).unwrap_or_else(|| root_path.to_string())
    } else {
        root_path.to_string()
    };

    let changed = crate::indexer::git_workspace::workspace_changed_files(root)?;

    let deleted_files: Vec<String> = changed
        .deleted
        .iter()
        .map(|f| {
            if std::path::Path::new(f).is_absolute() {
                f.clone()
            } else {
                format!("{}/{}", workspace_root, f)
            }
        })
        .collect();

    let mut all_changed: Vec<String> = Vec::new();
    all_changed.extend(changed.modified);
    all_changed.extend(changed.added);
    all_changed.extend(changed.deleted);

    let untracked = crate::indexer::git_workspace::workspace_untracked_files(root)?;
    let indexable_untracked = filter_indexable_files(&untracked);
    all_changed.extend(indexable_untracked);

    let changed_files: Vec<String> = all_changed
        .iter()
        .map(|f| {
            if std::path::Path::new(f).is_absolute() {
                f.clone()
            } else {
                format!("{}/{}", workspace_root, f)
            }
        })
        .collect();

    for deleted_file in &deleted_files {
        graph.remove_elements_by_file(deleted_file)?;
        graph.remove_relationships_by_source(deleted_file)?;
    }

    // Mega-graphs (nested multi-repo workspaces like BE): never load all
    // relationships just to expand dependents — that alone can add ~0.5–0.8 GiB
    // and OOM a 6 GiB Docker container. See research_docker_memory_impact_2026-07-13.md.
    let mut dependent_files: Vec<String> = Vec::new();
    if crate::ontology::safe_discover::skip_incremental_dependents(graph) {
        tracing::warn!(
            target: "leankg::mem",
            changed = changed_files.len(),
            "incremental_index: skipping full-graph dependent expansion on mega-graph / LEANKG_INCREMENTAL_SKIP_DEPENDENTS"
        );
    } else {
        let all_relationships = graph.all_relationships()?;
        let rel_tuples: Vec<(String, String)> = all_relationships
            .iter()
            .map(|r| (r.source_qualified.clone(), r.target_qualified.clone()))
            .collect();

        for changed_file in &changed_files {
            let file_name = std::path::Path::new(changed_file)
                .file_name()
                .and_then(|n| n.to_str())
                .unwrap_or(changed_file);

            let deps = find_dependents(file_name, &rel_tuples);
            for dep in deps {
                let dep_path = std::path::Path::new(&dep);
                if !dep_path.is_absolute() {
                    dependent_files.push(format!("{}/{}", workspace_root, dep));
                } else {
                    dependent_files.push(dep);
                }
            }
        }
        dependent_files.dedup();
    }

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
                ..Default::default()
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
                        element_type: "directory".to_string(),
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
                        ..Default::default()
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
                ..Default::default()
            });
        }
    }

    populate_directory_metadata(&mut elements, files);

    let (rat_elements, rat_relationships) = extract_rationale_markers(files);
    elements.extend(rat_elements);
    relationships.extend(rat_relationships);

    // US-CBM-B6 / FR-B15: emit / listen event channel edges.
    for file in files {
        if let Ok(content) = std::fs::read_to_string(file) {
            let edges = crate::indexer::event_edges::detect_event_edges(&content);
            let edge_rels = crate::indexer::event_edges::to_relationships(&edges);
            relationships.extend(edge_rels);
        }
    }

    (elements, relationships)
}

/// US-GF-07 / FR-GF-15..16: Extract `# WHY:`, `# NOTE:`, `# HACK:`,
/// `// FIXME:`, `TODO(rationale):` markers and ADR/RFC citations
/// from source files. Each marker becomes a `rationale` element with
/// an `explains` edge to its enclosing function/class (or the file
/// itself when no enclosing element is found).
pub fn extract_rationale_markers(files: &[String]) -> (Vec<CodeElement>, Vec<Relationship>) {
    use std::collections::HashMap;
    let mut elements: Vec<CodeElement> = Vec::new();
    let mut relationships: Vec<Relationship> = Vec::new();
    let mut seen_keys: HashMap<String, usize> = HashMap::new();

    let markers = [
        ("WHY", "rationale_why"),
        ("NOTE", "rationale_note"),
        ("HACK", "rationale_hack"),
        ("FIXME", "rationale_fixme"),
        ("XXX", "rationale_xxx"),
    ];

    for file in files {
        let content = match std::fs::read_to_string(file) {
            Ok(c) => c,
            Err(_) => continue,
        };
        let mut current_function: Option<String> = None;
        let mut in_block_comment = false;
        for (line_idx, line) in content.lines().enumerate() {
            let trimmed = line.trim_start();
            // Track rough "function context" so markers can attach to the
            // enclosing function instead of always falling back to file.
            if trimmed.starts_with("fn ")
                || trimmed.starts_with("func ")
                || trimmed.starts_with("def ")
                || trimmed.starts_with("function ")
            {
                let name = trimmed
                    .split_whitespace()
                    .nth(1)
                    .unwrap_or("anonymous")
                    .split(|c: char| !c.is_alphanumeric() && c != '_')
                    .next()
                    .unwrap_or("anonymous")
                    .to_string();
                current_function = Some(format!("{}::{}", file, name));
            }
            let text = if in_block_comment { trimmed } else { line };
            for (marker, kind) in markers {
                // Match `# WHY:`, `// WHY:`, `/* WHY:`, `-- WHY:`, `# TODO(...)` patterns.
                let pat = format!("{}:", marker);
                let lower_text = text.to_uppercase();
                if let Some(pos) = lower_text.find(&pat) {
                    let raw = text[pos + pat.len()..].trim();
                    let summary = raw.chars().take(200).collect::<String>();
                    let key = format!("{}:{}:{}", file, marker, line_idx);
                    let idx = seen_keys.entry(key.clone()).or_insert(0);
                    let qn = if *idx == 0 {
                        format!("{}#{}@{}", file, marker, line_idx)
                    } else {
                        format!("{}#{}@{}#{}", file, marker, line_idx, idx)
                    };
                    *idx += 1;
                    elements.push(CodeElement {
                        qualified_name: qn.clone(),
                        element_type: "rationale".to_string(),
                        name: format!(
                            "{} ({})",
                            marker,
                            summary.chars().take(60).collect::<String>()
                        ),
                        file_path: file.clone(),
                        line_start: (line_idx + 1) as u32,
                        line_end: (line_idx + 1) as u32,
                        metadata: serde_json::json!({
                            "marker": marker,
                            "kind": kind,
                            "summary": summary,
                        }),
                        ..Default::default()
                    });
                    let target = current_function.clone().unwrap_or_else(|| file.clone());
                    relationships.push(Relationship {
                        id: None,
                        source_qualified: target,
                        target_qualified: qn,
                        rel_type: "explained_by".to_string(),
                        confidence: 1.0,
                        metadata: serde_json::json!({"marker": marker}),
                        ..Default::default()
                    });
                }
            }
            // Block comment toggle for /* */ C-style comments.
            in_block_comment =
                if !in_block_comment && trimmed.starts_with("/*") && !trimmed.ends_with("*/") {
                    true
                } else if in_block_comment && trimmed.ends_with("*/") {
                    false
                } else {
                    in_block_comment
                };
        }
    }
    (elements, relationships)
}

fn populate_directory_metadata(elements: &mut [CodeElement], files: &[String]) {
    use std::collections::HashMap;

    let mut dir_children: HashMap<String, usize> = HashMap::new();
    let mut dir_langs: HashMap<String, HashMap<String, usize>> = HashMap::new();
    let mut dir_lines: HashMap<String, usize> = HashMap::new();

    for file_path in files {
        let lang = file_path
            .rsplit('.')
            .next()
            .map(|ext| ext.to_lowercase())
            .unwrap_or_default();

        let line_count = std::fs::read_to_string(file_path)
            .map(|c| c.lines().count())
            .unwrap_or(0);

        if let Some(parent) = std::path::Path::new(file_path).parent() {
            let parent_str = parent.to_string_lossy().into_owned();
            *dir_children.entry(parent_str.clone()).or_insert(0) += 1;
            *dir_lines.entry(parent_str.clone()).or_insert(0) += line_count;
            dir_langs
                .entry(parent_str.clone())
                .or_default()
                .entry(lang.clone())
                .and_modify(|c| *c += 1)
                .or_insert(1);

            let mut ancestor = parent.parent();
            while let Some(a) = ancestor {
                if a.as_os_str().is_empty() {
                    break;
                }
                let a_str = a.to_string_lossy().into_owned();
                *dir_children.entry(a_str.clone()).or_insert(0) += 1;
                *dir_lines.entry(a_str.clone()).or_insert(0) += line_count;
                dir_langs
                    .entry(a_str)
                    .or_default()
                    .entry(lang.clone())
                    .and_modify(|c| *c += 1)
                    .or_insert(1);

                ancestor = a.parent();
            }
        }
    }

    for elem in elements.iter_mut() {
        if elem.element_type != "directory" {
            continue;
        }
        let child_count = dir_children.get(&elem.qualified_name).copied().unwrap_or(0);
        let total_lines = dir_lines.get(&elem.qualified_name).copied().unwrap_or(0);

        let lang_dist: serde_json::Map<String, serde_json::Value> = dir_langs
            .get(&elem.qualified_name)
            .map(|m| {
                m.iter()
                    .map(|(k, v)| (k.clone(), serde_json::Value::Number((*v).into())))
                    .collect()
            })
            .unwrap_or_default();

        elem.metadata = serde_json::json!({
            "child_count": child_count,
            "total_lines": total_lines,
            "language_distribution": lang_dist,
        });
    }
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

    // Serialize tests that mutate process-wide environment variables.
    // `std::env::set_var` / `remove_var` are not thread-safe; without this
    // lock, parallel `cargo test` invocations can race on `max_file_size`.
    static ENV_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

    #[test]
    fn test_max_file_size_default_is_2_mib() {
        // Acquire the lock so we don't race with
        // `test_max_file_size_env_override` which sets the env var.
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        // Make sure no other test left the env var set.
        std::env::remove_var("LEANKG_MAX_FILE_SIZE");
        // Sanity check: the default cap should be 2 MiB unless overridden by
        // env. This is the cap that protects the indexer from accidentally
        // slurping a 60 MB checked-in binary or a huge generated XML.
        assert_eq!(max_file_size(), 2 * 1024 * 1024);
    }

    #[test]
    fn test_max_file_size_env_override() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        // LEANKG_MAX_FILE_SIZE should override the default when set.
        // SAFETY: tests run single-threaded for this crate and no other test
        // reads the same env var; the previous value is restored at the end.
        let prev = std::env::var("LEANKG_MAX_FILE_SIZE").ok();
        std::env::set_var("LEANKG_MAX_FILE_SIZE", "1024");
        assert_eq!(max_file_size(), 1024);
        match prev {
            Some(v) => std::env::set_var("LEANKG_MAX_FILE_SIZE", v),
            None => std::env::remove_var("LEANKG_MAX_FILE_SIZE"),
        }
    }

    #[test]
    fn test_find_files_sync_skips_oversized_files() {
        let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        // Create a temp tree with one small .go file and one oversized one.
        let dir = tempfile::tempdir().expect("tempdir");
        let small = dir.path().join("small.go");
        std::fs::write(&small, b"package x\nfunc A() {}").expect("write small");

        let big = dir.path().join("big.go");
        // 64 KiB is well over the 1 KiB override we set below.
        let big_contents = vec![b'x'; 64 * 1024];
        std::fs::write(&big, &big_contents).expect("write big");

        let prev = std::env::var("LEANKG_MAX_FILE_SIZE").ok();
        std::env::set_var("LEANKG_MAX_FILE_SIZE", "1024");

        let files = find_files_sync(dir.path().to_str().unwrap()).expect("find");
        let names: Vec<&str> = files
            .iter()
            .map(|p| {
                std::path::Path::new(p)
                    .file_name()
                    .unwrap()
                    .to_str()
                    .unwrap()
            })
            .collect();

        assert!(
            names.contains(&"small.go"),
            "small.go should be indexed: {:?}",
            names
        );
        assert!(
            !names.contains(&"big.go"),
            "big.go should be skipped: {:?}",
            names
        );

        match prev {
            Some(v) => std::env::set_var("LEANKG_MAX_FILE_SIZE", v),
            None => std::env::remove_var("LEANKG_MAX_FILE_SIZE"),
        }
    }

    #[test]
    fn test_default_index_ignored_dirs_covers_common_build_dirs() {
        // Regression guard: the default exclude set must keep growing to cover
        // common monorepo build outputs, otherwise the indexer drags in
        // hundreds of MiB of generated code per service.
        for d in [
            "dist", "build", "out", "coverage", ".next", ".nuxt", ".cache", ".venv", "testdata",
            "fixtures", "gen", "pb",
        ] {
            assert!(
                DEFAULT_INDEX_IGNORED_DIRS.contains(&d),
                "missing default exclude dir: {}",
                d
            );
        }
    }

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

    // US-GF-07: rationale extraction
    #[test]
    fn rationale_extraction_picks_up_why_note_hack() {
        let dir = std::env::temp_dir().join(format!("leankg-rationale-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("a.rs");
        std::fs::write(
            &path,
            r#"
fn main() {
    // WHY: legacy contract required by the upstream client
    let x = 1;
    // NOTE: do not reorder; tests depend on side effects
    println!("{}", x);
    // HACK: workaround for upstream bug
    let y = 2;
}
"#,
        )
        .unwrap();
        let (elems, rels) = extract_rationale_markers(&[path.to_string_lossy().to_string()]);
        let kinds: Vec<String> = elems
            .iter()
            .filter_map(|e| {
                e.metadata
                    .get("marker")
                    .and_then(|v| v.as_str())
                    .map(String::from)
            })
            .collect();
        assert!(kinds.contains(&"WHY".to_string()));
        assert!(kinds.contains(&"NOTE".to_string()));
        assert!(kinds.contains(&"HACK".to_string()));
        assert!(rels.iter().any(|r| r.rel_type == "explained_by"));
        std::fs::remove_dir_all(&dir).ok();
    }
}
