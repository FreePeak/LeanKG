// Check if import targets exist in code_elements
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use serde_json::json;

#[tokio::test(flavor = "multi_thread")]
async fn test_check_import_targets() {
    let db_path = std::path::PathBuf::from(".leankg");
    let db = init_db(db_path.as_path()).expect("Failed to init db");
    let graph = GraphEngine::new(db);

    // Get all relationships that are imports
    let relationships = graph.all_relationships().unwrap();
    let imports: Vec<_> = relationships
        .iter()
        .filter(|r| r.rel_type == "imports")
        .collect();

    println!("Total imports: {}", imports.len());

    // Check if targets exist in code_elements
    let all_elements = graph.all_elements().unwrap();
    let element_qns: std::collections::HashSet<_> = all_elements
        .iter()
        .map(|e| e.qualified_name.clone())
        .collect();

    println!("\nTotal elements: {}", all_elements.len());

    let mut found = 0;
    let mut not_found = 0;
    let mut sample_not_found = Vec::new();

    for r in &imports {
        if element_qns.contains(&r.target_qualified) {
            found += 1;
        } else {
            not_found += 1;
            if sample_not_found.len() < 10 {
                sample_not_found.push(r.target_qualified.clone());
            }
        }
    }

    println!("Import targets found in code_elements: {}", found);
    println!("Import targets NOT found in code_elements: {}", not_found);
    println!("\nSample NOT found targets:");
    for t in &sample_not_found {
        println!("  {}", t);
    }

    // Now check calls relationships
    let calls: Vec<_> = relationships
        .iter()
        .filter(|r| r.rel_type == "calls")
        .collect();
    println!("\n=== Calls relationships ===");
    println!("Total calls: {}", calls.len());

    let mut callers_found = 0;
    let mut callers_not_found = 0;
    let mut sample_call_targets = Vec::new();

    for r in &calls {
        if element_qns.contains(&r.target_qualified) {
            callers_found += 1;
        } else {
            callers_not_found += 1;
            if sample_call_targets.len() < 10 {
                sample_call_targets.push(r.target_qualified.clone());
            }
        }
    }

    println!("Call targets found in code_elements: {}", callers_found);
    println!(
        "Call targets NOT found in code_elements: {}",
        callers_not_found
    );
    if !sample_call_targets.is_empty() {
        println!("\nSample NOT found call targets:");
        for t in &sample_call_targets {
            println!("  {}", t);
        }
    }
}
