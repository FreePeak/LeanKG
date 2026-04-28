//! Load tests with 100K nodes for performance benchmarking
//!
//! Run with: cargo test --release -- --nocapture load_test

use leankg::db::models::{CodeElement, Relationship};
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use std::time::Instant;
use tempfile::TempDir;

fn make_test_engine() -> (GraphEngine, TempDir) {
    let tmp = tempfile::tempdir().unwrap();
    let db_path = tmp.path().join("loadtest.db");
    let db = init_db(&db_path).unwrap();
    let engine = GraphEngine::new(db);
    (engine, tmp)
}

const COUNT: usize = 100_000;

fn insert_elements_batched(engine: &GraphEngine, count: usize) -> std::time::Duration {
    let elements: Vec<CodeElement> = (0..count)
        .map(|i| CodeElement {
            qualified_name: format!("src/mod{}::fn_{}", i / 100, i),
            element_type: if i % 10 == 0 {
                "file".to_string()
            } else if i % 5 == 0 {
                "class".to_string()
            } else {
                "function".to_string()
            },
            name: format!("fn_{}", i),
            file_path: format!("src/mod{}.rs", i / 100),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::json!({}),
        })
        .collect();

    let t0 = Instant::now();
    for chunk in elements.chunks(1000) {
        engine.insert_elements(chunk).unwrap();
    }
    t0.elapsed()
}

fn insert_relationships_batched(engine: &GraphEngine, count: usize) -> std::time::Duration {
    let relationships: Vec<Relationship> = (0..count)
        .map(|i| Relationship {
            id: None,
            source_qualified: format!("src/mod{}::caller_{}", i / 1000, i),
            target_qualified: format!("src/mod{}::callee_{}", i / 1000, (i + 1) % 1000),
            rel_type: if i % 3 == 0 {
                "calls".to_string()
            } else if i % 3 == 1 {
                "imports".to_string()
            } else {
                "references".to_string()
            },
            confidence: 0.5 + (i % 50) as f64 / 100.0,
            metadata: serde_json::json!({}),
        })
        .collect();

    let t0 = Instant::now();
    for chunk in relationships.chunks(1000) {
        engine.insert_relationships(chunk).unwrap();
    }
    t0.elapsed()
}

// =============================================================================
// BULK INSERT TESTS
// =============================================================================

#[test]
fn load_test_insert_100k_elements() {
    let (engine, _tmp) = make_test_engine();

    println!("\n=== Load Test: Insert {} Elements ===", COUNT);

    let t0 = Instant::now();
    let duration = insert_elements_batched(&engine, COUNT);
    let rate = COUNT as f64 / duration.as_secs_f64();

    println!(
        "Inserted {} elements in {:.3}s",
        COUNT,
        duration.as_secs_f64()
    );
    println!("Insert rate: {:.0} elements/sec", rate);

    // Verify count
    let count = engine.all_elements().unwrap().len();
    assert_eq!(
        count, COUNT,
        "Should have inserted exactly {} elements",
        COUNT
    );

    println!("Verified: {} elements in database", count);
    println!("=== PASS ===\n");
}

#[test]
fn load_test_insert_100k_relationships() {
    let (engine, _tmp) = make_test_engine();

    // First insert elements
    insert_elements_batched(&engine, 10_000);

    println!("\n=== Load Test: Insert {} Relationships ===", COUNT);

    let t0 = Instant::now();
    let duration = insert_relationships_batched(&engine, COUNT);
    let rate = COUNT as f64 / duration.as_secs_f64();

    println!(
        "Inserted {} relationships in {:.3}s",
        COUNT,
        duration.as_secs_f64()
    );
    println!("Insert rate: {:.0} relationships/sec", rate);

    // Verify count
    let count = engine.all_relationships().unwrap().len();
    assert_eq!(
        count, COUNT,
        "Should have inserted exactly {} relationships",
        COUNT
    );

    println!("Verified: {} relationships in database", count);
    println!("=== PASS ===\n");
}

#[test]
fn load_test_combined() {
    let (engine, _tmp) = make_test_engine();

    println!(
        "\n=== Load Test: {} Elements + 50K Relationships ===",
        COUNT
    );

    let t0 = Instant::now();
    let elem_duration = insert_elements_batched(&engine, COUNT);
    let rel_duration = insert_relationships_batched(&engine, 50_000);
    let total = t0.elapsed();

    println!(
        "Inserted {} elements in {:.3}s",
        COUNT,
        elem_duration.as_secs_f64()
    );
    println!(
        "Inserted 50K relationships in {:.3}s",
        rel_duration.as_secs_f64()
    );
    println!("Total time: {:.3}s", total.as_secs_f64());

    let elem_count = engine.all_elements().unwrap().len();
    let rel_count = engine.all_relationships().unwrap().len();

    assert_eq!(elem_count, COUNT);
    assert_eq!(rel_count, 50_000);

    println!(
        "Verified: {} elements, {} relationships",
        elem_count, rel_count
    );
    println!("=== PASS ===\n");
}

// =============================================================================
// SEARCH PERFORMANCE TESTS
// =============================================================================

#[test]
fn load_test_search_cold_cache() {
    let (engine, _tmp) = make_test_engine();

    println!(
        "\n=== Load Test: Search on {} Elements (Cold Cache) ===",
        COUNT
    );

    // Insert elements
    insert_elements_batched(&engine, COUNT);

    // Perform searches without cache benefit
    let search_terms = ["fn_5", "fn_50", "fn_500", "fn_5000"];

    for term in search_terms {
        let t0 = Instant::now();
        let results = engine.search_by_name(term).unwrap();
        let elapsed = t0.elapsed();

        println!(
            "Search '{}': {} results in {:.3}s",
            term,
            results.len(),
            elapsed.as_secs_f64()
        );
    }

    println!("=== PASS ===\n");
}

#[test]
fn load_test_search_warm_cache() {
    let (engine, _tmp) = make_test_engine();

    println!(
        "\n=== Load Test: Search on {} Elements (Warm Cache) ===",
        COUNT
    );

    // Insert elements
    insert_elements_batched(&engine, COUNT);

    // First search (cold)
    let t0 = Instant::now();
    let _ = engine.search_by_name("fn_5").unwrap();
    let cold = t0.elapsed();
    println!("First search (cold): {:.3}s", cold.as_secs_f64());

    // Repeat searches (should hit cache)
    let search_terms = ["fn_5", "fn_50", "fn_500", "fn_5000"];

    for term in search_terms {
        let t0 = Instant::now();
        let results = engine.search_by_name(term).unwrap();
        let elapsed = t0.elapsed();

        println!(
            "Search '{}': {} results in {:.3}s (cached)",
            term,
            results.len(),
            elapsed.as_secs_f64()
        );
    }

    println!("=== PASS ===\n");
}

// =============================================================================
// ALL ELEMENTS/RELATIONSHIPS TESTS
// =============================================================================

#[test]
fn load_test_all_elements() {
    let (engine, _tmp) = make_test_engine();

    insert_elements_batched(&engine, COUNT);

    println!("\n=== Load Test: all_elements() on {} ===", COUNT);

    let t0 = Instant::now();
    let elements = engine.all_elements().unwrap();
    let elapsed = t0.elapsed();

    println!(
        "Retrieved {} elements in {:.3}s",
        elements.len(),
        elapsed.as_secs_f64()
    );
    println!(
        "Throughput: {:.0} elements/sec",
        COUNT as f64 / elapsed.as_secs_f64()
    );

    assert_eq!(elements.len(), COUNT);
    println!("=== PASS ===\n");
}

#[test]
fn load_test_all_relationships() {
    let (engine, _tmp) = make_test_engine();

    insert_elements_batched(&engine, 10_000);
    insert_relationships_batched(&engine, COUNT);

    println!("\n=== Load Test: all_relationships() on {} ===", COUNT);

    let t0 = Instant::now();
    let relationships = engine.all_relationships().unwrap();
    let elapsed = t0.elapsed();

    println!(
        "Retrieved {} relationships in {:.3}s",
        relationships.len(),
        elapsed.as_secs_f64()
    );
    println!(
        "Throughput: {:.0} relationships/sec",
        COUNT as f64 / elapsed.as_secs_f64()
    );

    assert_eq!(relationships.len(), COUNT);
    println!("=== PASS ===\n");
}

// =============================================================================
// SEARCH BY TYPE TESTS
// =============================================================================

#[test]
fn load_test_search_by_type() {
    let (engine, _tmp) = make_test_engine();

    // Insert elements (10% files, 10% classes, 80% functions)
    insert_elements_batched(&engine, COUNT);

    println!(
        "\n=== Load Test: search_by_type() on {} Elements ===",
        COUNT
    );

    for elem_type in &["file", "class", "function"] {
        let t0 = Instant::now();
        let results = engine.search_by_type(elem_type).unwrap();
        let elapsed = t0.elapsed();

        println!(
            "search_by_type('{}'): {} results in {:.3}s",
            elem_type,
            results.len(),
            elapsed.as_secs_f64()
        );
    }

    println!("=== PASS ===\n");
}

// =============================================================================
// MIXED OPERATIONS TEST
// =============================================================================

#[test]
fn load_test_mixed_operations() {
    let (engine, _tmp) = make_test_engine();

    println!(
        "\n=== Load Test: Mixed Operations on {} Elements ===",
        COUNT
    );

    // Insert elements
    let t0 = Instant::now();
    insert_elements_batched(&engine, COUNT);
    println!("Bulk insert: {:.3}s", t0.elapsed().as_secs_f64());

    // Perform various operations
    let t0 = Instant::now();
    let count = engine.all_elements().unwrap().len();
    println!(
        "all_elements: {} results in {:.3}s",
        count,
        t0.elapsed().as_secs_f64()
    );

    let t0 = Instant::now();
    let count = engine.search_by_type("function").unwrap().len();
    println!(
        "search_by_type(function): {} results in {:.3}s",
        count,
        t0.elapsed().as_secs_f64()
    );

    let t0 = Instant::now();
    let count = engine.search_by_type("class").unwrap().len();
    println!(
        "search_by_type(class): {} results in {:.3}s",
        count,
        t0.elapsed().as_secs_f64()
    );

    let t0 = Instant::now();
    let count = engine.search_by_name("fn_5").unwrap().len();
    println!(
        "search_by_name(fn_5): {} results in {:.3}s",
        count,
        t0.elapsed().as_secs_f64()
    );

    println!("=== PASS ===\n");
}

// =============================================================================
// CACHE EFFECTIVENESS TEST
// =============================================================================

#[test]
fn load_test_cache_effectiveness() {
    let (engine, _tmp) = make_test_engine();

    insert_elements_batched(&engine, COUNT);

    println!(
        "\n=== Load Test: Cache Effectiveness on {} Elements ===",
        COUNT
    );

    // First pass - cold
    let terms = ["fn_1", "fn_2", "fn_3", "fn_4", "fn_5"];
    let mut cold_times = Vec::new();

    for term in &terms {
        let t0 = Instant::now();
        let _ = engine.search_by_name(term).unwrap();
        cold_times.push(t0.elapsed());
    }

    println!("Cold cache searches:");
    for (i, term) in terms.iter().enumerate() {
        println!("  {}: {:.3}s", term, cold_times[i].as_secs_f64());
    }

    // Second pass - warm
    let mut warm_times = Vec::new();
    for term in &terms {
        let t0 = Instant::now();
        let _ = engine.search_by_name(term).unwrap();
        warm_times.push(t0.elapsed());
    }

    println!("\nWarm cache searches:");
    for (i, term) in terms.iter().enumerate() {
        let speedup = cold_times[i].as_secs_f64() / warm_times[i].as_secs_f64().max(0.001);
        println!(
            "  {}: {:.3}s (speedup: {:.1}x)",
            term,
            warm_times[i].as_secs_f64(),
            speedup
        );
    }

    println!("=== PASS ===\n");
}

// =============================================================================
// REFERENCE TEST (10K for fast validation)
// =============================================================================

#[test]
fn load_test_reference_10k() {
    let (engine, _tmp) = make_test_engine();

    println!("\n=== Reference: 10K Elements ===");

    let t0 = Instant::now();
    insert_elements_batched(&engine, 10_000);
    let insert_time = t0.elapsed();

    let t0 = Instant::now();
    let count = engine.all_elements().unwrap().len();
    let retrieve_time = t0.elapsed();

    println!(
        "Insert 10K: {:.3}s ({:.0}/sec)",
        insert_time.as_secs_f64(),
        10_000.0 / insert_time.as_secs_f64()
    );
    println!(
        "Retrieve 10K: {:.3}s ({:.0}/sec)",
        retrieve_time.as_secs_f64(),
        10_000.0 / retrieve_time.as_secs_f64()
    );

    assert_eq!(count, 10_000);
    println!("=== PASS ===\n");
}
