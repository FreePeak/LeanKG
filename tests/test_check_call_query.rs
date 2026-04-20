// Debug get_call_graph query
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;

#[tokio::test(flavor = "multi_thread")]
async fn test_check_call_graph_query() {
    let db_path = std::path::PathBuf::from(".leankg");
    let db = init_db(db_path.as_path()).expect("Failed to init db");
    let graph = GraphEngine::new(db);

    // Check what calls exist for main
    let source = "./src/main.rs::main";
    let safe_src = source.replace('"', "\\\"");

    // Try depth 1 query
    let query = format!(
        r#"?[src, tgt, depth] :=
           *relationships[src, tgt, "calls", _, _],
           (src = "{}" or src = "./{}"), depth = 1
           :limit 30"#,
        safe_src, safe_src
    );

    println!("Query: {}", query);

    let result = graph.db().run_script(&query, Default::default()).unwrap();
    println!("\nResults: {} rows", result.rows.len());
    for row in result.rows.iter().take(5) {
        println!("  {:?} -> {:?} (depth={})", row[0], row[1], row[2]);
    }

    // Also check if the function exists in relationships at all
    let all_calls_query = r#"?[src, tgt] := *relationships[src, tgt, "calls", _, _] :limit 10"#;
    let all_result = graph
        .db()
        .run_script(all_calls_query, Default::default())
        .unwrap();
    println!("\nSample calls (first 10):");
    for row in all_result.rows.iter() {
        println!("  {:?} -> {:?}", row[0], row[1]);
    }

    // Check if main exists as source
    let main_check = format!(
        r#"?[tgt] := *relationships[src, tgt, "calls", _, _], src = "{}""#,
        safe_src
    );
    let main_result = graph
        .db()
        .run_script(&main_check, Default::default())
        .unwrap();
    println!("\nCalls FROM {}: {} rows", source, main_result.rows.len());
}
