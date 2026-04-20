// Find files that have valid indexed relationships
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;

#[tokio::test(flavor = "multi_thread")]
async fn test_find_valid_files_for_relationships() {
    let db_path = std::path::PathBuf::from(".leankg");
    let db = init_db(db_path.as_path()).expect("Failed to init db");
    let graph = GraphEngine::new(db);

    let relationships = graph.all_relationships().unwrap();
    let all_elements = graph.all_elements().unwrap();
    let element_qns: std::collections::HashSet<_> = all_elements
        .iter()
        .map(|e| e.qualified_name.clone())
        .collect();

    // Group calls by source file
    use std::collections::HashMap;
    let mut calls_by_source: HashMap<String, Vec<String>> = HashMap::new();
    for r in relationships.iter().filter(|r| r.rel_type == "calls") {
        if element_qns.contains(&r.target_qualified) {
            calls_by_source
                .entry(r.source_qualified.clone())
                .or_default()
                .push(r.target_qualified.clone());
        }
    }

    println!("=== Files with indexed call targets ===");
    let mut sorted: Vec<_> = calls_by_source.iter().collect();
    sorted.sort_by(|a, b| b.1.len().cmp(&a.1.len()));

    for (source, targets) in sorted.iter().take(10) {
        println!("{}: {} call targets", source, targets.len());
    }

    // Get first source that has calls
    if let Some((first_source, first_targets)) = sorted.first() {
        println!("\n=== First file with valid calls ===");
        println!("Source: {}", first_source);
        println!("Sample targets (first 5):");
        for t in first_targets.iter().take(5) {
            println!("  -> {}", t);
        }

        // Now test get_callers and get_call_graph on this
        println!("\n=== Testing get_callers and get_call_graph ===");
        println!("Would test with: {}", first_source);
    }
}
