// Debug get_call_graph issue
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;

#[tokio::test(flavor = "multi_thread")]
async fn test_debug_call_graph() {
    let db_path = std::path::PathBuf::from(".leankg");
    let db = init_db(db_path.as_path()).expect("Failed to init db");
    let graph = GraphEngine::new(db);

    // Test get_call_graph_bounded with depth 1
    println!("=== Testing get_call_graph_bounded('./src/main.rs::main', 1, 10) ===");
    match graph.get_call_graph_bounded("./src/main.rs::main", 1, 10) {
        Ok(results) => {
            println!("get_call_graph_bounded returned {} results", results.len());
            for (src, tgt, depth) in results.iter().take(5) {
                println!("  {} -> {} (depth={})", src, tgt, depth);
            }
        }
        Err(e) => println!("Error: {}", e),
    }

    // Also test get_callers
    println!("\n=== Testing get_callers('./src/main.rs::main') ===");
    match graph.get_callers("./src/main.rs::main", None) {
        Ok(callers) => {
            println!("get_callers returned {} results", callers.len());
            for c in callers.iter().take(5) {
                println!("  {} at {}:{}", c.name, c.file_path, c.line_start);
            }
        }
        Err(e) => println!("Error: {}", e),
    }
}
