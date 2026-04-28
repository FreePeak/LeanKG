// Check what relationship types exist
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use serde_json::json;

#[tokio::test(flavor = "multi_thread")]
async fn test_check_relationship_types() {
    let db_path = std::path::PathBuf::from(".leankg");
    let db = init_db(db_path.as_path()).expect("Failed to init db");
    let graph = GraphEngine::new(db);

    // Get all relationships to see what types exist
    let relationships = graph.all_relationships().unwrap();
    println!("Total relationships: {}", relationships.len());

    // Count by type
    use std::collections::HashMap;
    let mut type_counts: HashMap<String, usize> = HashMap::new();
    for r in &relationships {
        *type_counts.entry(r.rel_type.clone()).or_insert(0) += 1;
    }

    println!("\nRelationship types:");
    for (rel_type, count) in &type_counts {
        println!("  {}: {}", rel_type, count);
    }

    // Check if there are any imports or calls
    println!("\n=== Sample relationships by type ===");
    for (rel_type, count) in &type_counts {
        if rel_type == "imports" || rel_type == "calls" || rel_type == "contains" {
            println!("\n{} ({} total):", rel_type, count);
            let samples: Vec<_> = relationships
                .iter()
                .filter(|r| &r.rel_type == rel_type)
                .take(3)
                .collect();
            for r in samples {
                println!(
                    "  {} -> {} ({})",
                    r.source_qualified, r.target_qualified, r.rel_type
                );
            }
        }
    }

    // Check what files have imports relationships
    let imports: Vec<_> = relationships
        .iter()
        .filter(|r| r.rel_type == "imports")
        .take(5)
        .collect();
    println!("\n=== Sample imports relationships ===");
    for r in imports {
        println!("  {} -> {}", r.source_qualified, r.target_qualified);
    }
}
