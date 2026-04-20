// Find functions that have callers
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use leankg::mcp::handler::ToolHandler;
use serde_json::json;

#[tokio::test(flavor = "multi_thread")]
async fn test_verify_call_tools() {
    let db_path = std::path::PathBuf::from(".leankg");
    let db = init_db(db_path.as_path()).expect("Failed to init db");
    let graph = GraphEngine::new(db);
    let handler = ToolHandler::new(graph.clone(), db_path);

    // Test get_call_graph on main
    println!("=== Testing get_call_graph for ./src/main.rs::main ===");
    let cg = handler
        .execute_tool(
            "get_call_graph",
            &json!({"function": "./src/main.rs::main", "depth": 2}),
        )
        .await
        .unwrap();
    let calls_count = cg
        .get("calls")
        .and_then(|c| c.as_array())
        .map(|a| a.len())
        .unwrap_or(0);
    println!("get_call_graph: {} calls", calls_count);

    // Test get_callers for main - find a target that has callers
    let relationships = graph.all_relationships().unwrap();
    use std::collections::HashMap;
    let mut callers_by_target: HashMap<String, Vec<String>> = HashMap::new();
    for r in relationships.iter().filter(|r| r.rel_type == "calls") {
        callers_by_target
            .entry(r.target_qualified.clone())
            .or_default()
            .push(r.source_qualified.clone());
    }

    // Find targets that have callers
    let mut sorted_callers: Vec<_> = callers_by_target.iter().collect();
    sorted_callers.sort_by(|a, b| b.1.len().cmp(&a.1.len()));

    println!("\n=== Functions with most callers ===");
    for (target, callers) in sorted_callers.iter().take(5) {
        // Only show if target is a function (contains ::)
        if target.contains("::") {
            println!("{}: {} callers", target, callers.len());
        }
    }

    // Test with a function that has callers
    if let Some((target, _)) = sorted_callers.iter().find(|(t, _)| t.contains("::main")) {
        println!("\n=== Testing get_callers for {} ===", target);
        let callers = handler
            .execute_tool("get_callers", &json!({"function": target}))
            .await
            .unwrap();
        let callers_count = callers
            .get("callers")
            .and_then(|c| c.as_array())
            .map(|a| a.len())
            .unwrap_or(0);
        println!("get_callers: {} callers", callers_count);
    }
}
