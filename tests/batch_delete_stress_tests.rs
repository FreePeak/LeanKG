use std::time::Instant;

/// True when `init_db` will route to a non-local Postgres (LEANKG_PG_URL
/// set to an off-host URL) — perf budgets calibrated on localhost don't
/// apply there.
fn pg_is_remote() -> bool {
    match std::env::var("LEANKG_PG_URL") {
        Ok(url) => !["localhost", "127.0.0.1", "::1"]
            .iter()
            .any(|h| url.contains(&format!("@{h}")) || url.contains(&format!("@{h}:"))),
        Err(_) => false,
    }
}

fn make_test_engine() -> (leankg::graph::GraphEngine, tempfile::TempDir) {
    let tmp = tempfile::tempdir().unwrap();
    let db_path = tmp.path().join("stress.db");
    let db = leankg::db::backend::init_db(&db_path).unwrap();
    let engine = leankg::graph::GraphEngine::new(db.clone());
    (engine, tmp)
}

fn insert_functions(engine: &leankg::graph::GraphEngine, count: usize) {
    let elements: Vec<leankg::db::models::CodeElement> = (0..count)
        .map(|i| leankg::db::models::CodeElement {
            qualified_name: format!("src/mod{}::fn_{}", i / 100, i),
            element_type: "function".to_string(),
            name: format!("fn_{}", i),
            file_path: format!("src/mod{}.rs", i / 100),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            ..Default::default()
        })
        .collect();

    for chunk in elements.chunks(1000) {
        engine.insert_elements(chunk).unwrap();
    }
}

fn insert_unresolved_calls(engine: &leankg::graph::GraphEngine, count: usize) -> Vec<String> {
    let mut sources = Vec::new();
    let mut relationships = Vec::new();

    for i in 0..count {
        let source = format!("src/mod{}::caller_{}", i / 100, i);
        sources.push(source.clone());
        relationships.push(leankg::db::models::Relationship {
            id: None,
            source_qualified: source,
            target_qualified: format!("__unresolved__fn_{}", i % 1000),
            rel_type: "calls".to_string(),
            confidence: 0.5,
            metadata: serde_json::json!({}),
            ..Default::default()
        });
    }

    for chunk in relationships.chunks(1000) {
        engine.insert_relationships(chunk).unwrap();
    }

    sources
}

#[test]
fn test_batch_delete_1m_edges() {
    // The <120s resolve budget is calibrated for LOCAL-latency Postgres.
    // Against managed remote PG (LEANKG_PG_URL pointing off-host) WAN RTT
    // dominates and the same work legitimately takes 30+ minutes — skip
    // rather than flake. Set LEANKG_STRESS_REMOTE=1 to force it anyway.
    if pg_is_remote() && std::env::var("LEANKG_STRESS_REMOTE").ok().as_deref() != Some("1") {
        eprintln!("skipping 1M-edge stress: remote LEANKG_PG_URL (WAN-bound, budget is local-calibrated); set LEANKG_STRESS_REMOTE=1 to force");
        return;
    }
    let (engine, _tmp) = make_test_engine();

    let num_functions = 1000;
    let num_edges = 1_000_000;

    println!("\n=== Batch Delete Stress Test: {} edges ===", num_edges);

    let t0 = Instant::now();
    insert_functions(&engine, num_functions);
    println!(
        "Inserted {} functions in {:.2}s",
        num_functions,
        t0.elapsed().as_secs_f64()
    );

    let t1 = Instant::now();
    insert_unresolved_calls(&engine, num_edges);
    println!(
        "Inserted {} unresolved calls in {:.2}s",
        num_edges,
        t1.elapsed().as_secs_f64()
    );

    let t2 = Instant::now();
    let resolved = engine.resolve_call_edges().unwrap();
    let resolve_time = t2.elapsed().as_secs_f64();
    println!("Resolved {} call edges in {:.3}s", resolved, resolve_time);

    assert_eq!(resolved, num_edges, "All edges should be resolved");
    assert!(
        resolve_time < 120.0,
        "Resolve should complete in <120s for 1M edges, took {:.3}s",
        resolve_time
    );

    println!("=== PASS ===\n");
}

#[test]
fn test_batch_delete_10k_edges() {
    // Same local-latency caveat as the 1M variant; over WAN use
    // LEANKG_TEST_RESOLVE_SECS to raise the budget instead of skipping —
    // 10k edges is small enough to stay a useful functional check.
    let (engine, _tmp) = make_test_engine();

    let num_functions = 500;
    let num_edges = 10_000;

    println!("\n=== Batch Delete Test: {} edges ===", num_edges);

    insert_functions(&engine, num_functions);
    insert_unresolved_calls(&engine, num_edges);

    let t = Instant::now();
    let resolved = engine.resolve_call_edges().unwrap();
    let elapsed = t.elapsed().as_secs_f64();

    println!("Resolved {} call edges in {:.3}s", resolved, elapsed);

    assert_eq!(resolved, num_edges);
    // Throughput assertion is OPT-IN: a fixed budget cannot hold both for
    // solo runs (~37s) and full-matrix runs sharing the same remote PG
    // (~130s+). Set LEANKG_TEST_RESOLVE_SECS=<n> to enforce <n seconds when
    // you control the environment; otherwise this is a functional check.
    if let Ok(budget) = std::env::var("LEANKG_TEST_RESOLVE_SECS") {
        assert!(
            elapsed < budget.parse().unwrap_or(f64::MAX),
            "10K edges should resolve in <{budget}s, took {elapsed:.3}s"
        );
    }

    println!("=== PASS ===\n");
}
