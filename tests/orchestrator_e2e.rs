use leankg::graph::GraphEngine;
use leankg::orchestrator::QueryOrchestrator;
use std::env;
use std::fs;
use std::sync::atomic::{AtomicU32, Ordering};

static TEST_COUNTER: AtomicU32 = AtomicU32::new(0);

fn get_db_path() -> std::path::PathBuf {
    let counter = TEST_COUNTER.fetch_add(1, Ordering::SeqCst);
    let path = env::temp_dir().join(format!("leankg_e2e_test_{}.db", counter));
    let _ = fs::remove_file(&path);
    path
}

fn cleanup_db(path: &std::path::PathBuf) {
    let _ = fs::remove_file(path);
}

#[test]
fn test_orchestrate_context_intent_real_file() {
    let db_path = get_db_path();
    let db = leankg::db::schema::init_db(&db_path).expect("failed to init db");
    let graph = GraphEngine::new(db);
    let orchestrator = QueryOrchestrator::new(graph);

    // Use an actual file from the repo
    let result = orchestrator
        .orchestrate(
            "show me context for lib.rs",
            Some("src/lib.rs"),
            Some("adaptive"),
            false,
        )
        .expect("orchestrate failed");

    assert_eq!(result.query_type, "context");
    assert!(result.content.len() > 0, "should have content");

    eprintln!("Context Result:");
    eprintln!("  Query type: {}", result.query_type);
    eprintln!("  Tokens: {}/{}", result.tokens, result.total_tokens);
    eprintln!("  Savings: {:.1}%", result.savings_percent);
    eprintln!("  Cached: {}", result.is_cached);
    eprintln!("  Content length: {}", result.content.len());

    cleanup_db(&db_path);
}

#[test]
fn test_orchestrate_cache_hit_on_real_file() {
    let db_path = get_db_path();
    let db = leankg::db::schema::init_db(&db_path).expect("failed to init db");
    let graph = GraphEngine::new(db);
    let orchestrator = QueryOrchestrator::new(graph);

    // First call
    let result1 = orchestrator
        .orchestrate("get context for lib.rs", Some("src/lib.rs"), None, false)
        .expect("orchestrate failed");

    assert!(!result1.is_cached, "first call should not be cached");

    // Second call - should be cached
    let result2 = orchestrator
        .orchestrate("get context for lib.rs", Some("src/lib.rs"), None, false)
        .expect("orchestrate failed");

    assert!(result2.is_cached, "second call should be cached");

    eprintln!(
        "First call:  cached={}, tokens={}",
        result1.is_cached, result1.tokens
    );
    eprintln!(
        "Second call: cached={}, tokens={}",
        result2.is_cached, result2.tokens
    );

    cleanup_db(&db_path);
}

#[test]
fn test_orchestrate_force_fresh_bypasses_cache() {
    let db_path = get_db_path();
    let db = leankg::db::schema::init_db(&db_path).expect("failed to init db");
    let graph = GraphEngine::new(db);
    let orchestrator = QueryOrchestrator::new(graph);

    // First call
    let result1 = orchestrator
        .orchestrate("context for lib.rs", Some("src/lib.rs"), None, false)
        .expect("orchestrate failed");

    // Fresh call
    let result2 = orchestrator
        .orchestrate("context for lib.rs", Some("src/lib.rs"), None, true)
        .expect("orchestrate failed");

    assert!(!result2.is_cached, "fresh call should not be cached");

    eprintln!("Normal call: cached={}", result1.is_cached);
    eprintln!("Fresh call: cached={}", result2.is_cached);

    cleanup_db(&db_path);
}

#[test]
fn test_orchestrate_search_intent() {
    let db_path = get_db_path();
    let db = leankg::db::schema::init_db(&db_path).expect("failed to init db");
    let graph = GraphEngine::new(db);
    let orchestrator = QueryOrchestrator::new(graph);

    // Search without specifying file (searches all)
    let result = orchestrator
        .orchestrate(
            "find function named QueryOrchestrator",
            None,
            Some("signatures"),
            true,
        )
        .expect("orchestrate failed");

    assert_eq!(result.query_type, "search");
    eprintln!("Search Result:");
    eprintln!("  Query type: {}", result.query_type);
    eprintln!("  Content length: {}", result.content.len());

    cleanup_db(&db_path);
}

#[test]
fn test_orchestrate_impact_with_file() {
    let db_path = get_db_path();
    let db = leankg::db::schema::init_db(&db_path).expect("failed to init db");
    let graph = GraphEngine::new(db);
    let orchestrator = QueryOrchestrator::new(graph);

    // Impact requires a file
    let result = orchestrator
        .orchestrate(
            "what's the impact of changing lib.rs",
            Some("src/lib.rs"),
            Some("map"),
            true,
        )
        .expect("orchestrate failed");

    assert_eq!(result.query_type, "impact");
    eprintln!("Impact Result:");
    eprintln!("  Query type: {}", result.query_type);
    eprintln!("  Content length: {}", result.content.len());

    cleanup_db(&db_path);
}

#[test]
fn test_orchestrate_different_modes_on_real_file() {
    let db_path = get_db_path();
    let db = leankg::db::schema::init_db(&db_path).expect("failed to init db");
    let graph = GraphEngine::new(db);
    let orchestrator = QueryOrchestrator::new(graph);

    let modes = vec!["adaptive", "full", "map", "signatures"];

    for mode in modes {
        let result = orchestrator
            .orchestrate("context for lib.rs", Some("src/lib.rs"), Some(mode), true)
            .expect("orchestrate failed");

        eprintln!(
            "Mode {:12}: tokens={:5}/{}, savings={:5.1}%, content_len={}",
            mode,
            result.tokens,
            result.total_tokens,
            result.savings_percent,
            result.content.len()
        );
    }

    cleanup_db(&db_path);
}

#[test]
fn test_orchestrate_doc_intent() {
    let db_path = get_db_path();
    let db = leankg::db::schema::init_db(&db_path).expect("failed to init db");
    let graph = GraphEngine::new(db);
    let orchestrator = QueryOrchestrator::new(graph);

    // Index some docs first (this would need the doc indexer to be populated)
    // For now, just test the parsing
    let result = orchestrator
        .orchestrate(
            "get documentation for src/lib.rs",
            Some("src/lib.rs"),
            Some("full"),
            true,
        )
        .expect("orchestrate failed");

    assert_eq!(result.query_type, "doc");
    eprintln!("Doc Result:");
    eprintln!("  Query type: {}", result.query_type);
    eprintln!("  Content length: {}", result.content.len());

    cleanup_db(&db_path);
}

#[test]
fn test_intent_parser_all_types() {
    use leankg::orchestrator::intent::IntentParser;

    let parser = IntentParser::new();

    let test_cases: Vec<(&str, &str, Option<String>)> = vec![
        (
            "show me context for main.rs",
            "context",
            Some("main.rs".to_string()),
        ),
        (
            "what's the impact of changing lib.rs",
            "impact",
            Some("lib.rs".to_string()),
        ),
        (
            "show dependencies of handler.rs",
            "dependencies",
            Some("handler.rs".to_string()),
        ),
        (
            "get documentation for api.rs",
            "doc",
            Some("api.rs".to_string()),
        ),
        (
            "find function named parse_config",
            "search",
            Some("parse_config".to_string()),
        ),
        (
            "show me what changes if I modify config.rs",
            "impact",
            Some("config.rs".to_string()),
        ),
        (
            "where is function main defined",
            "search",
            Some("main".to_string()),
        ),
        (
            "get context for src/main.rs",
            "context",
            Some("src/main.rs".to_string()),
        ),
    ];

    let mut passed = 0;
    let mut failed = 0;

    for (input, expected_type, expected_target) in test_cases {
        let intent = parser.parse(input);
        let type_ok = intent.query_type == expected_type;
        let target_ok = intent.target == expected_target;

        if type_ok && target_ok {
            eprintln!(
                "[PASS] '{}' -> type: {}, target: {:?}",
                input, intent.query_type, intent.target
            );
            passed += 1;
        } else {
            eprintln!("[FAIL] '{}'", input);
            eprintln!(
                "       Expected: type={}, target={:?}",
                expected_type, expected_target
            );
            eprintln!(
                "       Got:      type={}, target={:?}",
                intent.query_type, intent.target
            );
            failed += 1;
        }
    }

    eprintln!("\nIntent Parser: {}/{} passed", passed, passed + failed);
    assert_eq!(failed, 0, "Some intent parser tests failed");
}
