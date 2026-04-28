use leankg::graph::GraphEngine;
use leankg::orchestrator::QueryOrchestrator;
use std::env;
use std::fs;
use std::time::{Duration, Instant};

fn get_db_path() -> std::path::PathBuf {
    let path = env::temp_dir().join("leankg_bench.db");
    let _ = fs::remove_file(&path);
    path
}

fn cleanup_db(path: &std::path::PathBuf) {
    let _ = fs::remove_file(path);
}

struct BenchmarkResult {
    name: String,
    elapsed: Duration,
    cache_hit: bool,
    tokens: usize,
    total_tokens: usize,
    savings_percent: f64,
}

fn run_benchmark(
    name: &str,
    orchestrator: &QueryOrchestrator,
    intent: &str,
    file: Option<&str>,
    mode: Option<&str>,
    fresh: bool,
) -> BenchmarkResult {
    let start = Instant::now();
    let result = orchestrator.orchestrate(intent, file, mode, fresh).unwrap();
    let elapsed = start.elapsed();

    BenchmarkResult {
        name: name.to_string(),
        elapsed,
        cache_hit: result.is_cached,
        tokens: result.tokens,
        total_tokens: result.total_tokens,
        savings_percent: result.savings_percent,
    }
}

fn print_result(r: &BenchmarkResult) {
    let ms = r.elapsed.as_secs_f64() * 1000.0;
    println!(
        "{:40} | {:>10.4}ms | {} | tokens: {:5}/{:5} | savings: {:5.1}%",
        r.name,
        ms,
        if r.cache_hit { "HIT" } else { "MISS" },
        r.tokens,
        r.total_tokens,
        r.savings_percent
    );
}

fn main() {
    println!("============================================================");
    println!(" LeanKG Orchestrator Benchmark");
    println!("============================================================");
    println!();

    let db_path = get_db_path();
    let db = leankg::db::schema::init_db(&db_path).expect("failed to init db");
    let graph = GraphEngine::new(db);
    let orchestrator = QueryOrchestrator::new(graph);

    println!("Test file: src/lib.rs");
    println!("Mode: adaptive (auto-select)");
    println!();
    println!("------------------------------------------------------------");
    println!(
        "{:40} | {:>10} | {:^4} | {:^20} | {:^12}",
        "Test", "Time", "Cache", "Tokens", "Savings"
    );
    println!("------------------------------------------------------------");

    // Warm up - first call to populate cache
    let _ = orchestrator.orchestrate(
        "show me context for lib.rs",
        Some("src/lib.rs"),
        Some("adaptive"),
        true,
    );

    cleanup_db(&db_path);

    // Re-create for fresh start
    let db_path = get_db_path();
    let db = leankg::db::schema::init_db(&db_path).expect("failed to init db");
    let graph = GraphEngine::new(db);
    let orchestrator = QueryOrchestrator::new(graph);

    // 1. Cold start - first call (cache miss)
    let r1 = run_benchmark(
        "Cold: context query (adaptive)",
        &orchestrator,
        "show me context for lib.rs",
        Some("src/lib.rs"),
        Some("adaptive"),
        false,
    );
    print_result(&r1);

    // 2. Cache hit - same query
    let r2 = run_benchmark(
        "Cache HIT: same context query",
        &orchestrator,
        "show me context for lib.rs",
        Some("src/lib.rs"),
        Some("adaptive"),
        false,
    );
    print_result(&r2);

    // 3. Fresh query - bypass cache
    let r3 = run_benchmark(
        "Fresh: context query (bypass)",
        &orchestrator,
        "show me context for lib.rs",
        Some("src/lib.rs"),
        Some("adaptive"),
        true, // fresh = true
    );
    print_result(&r3);

    println!("------------------------------------------------------------");

    // Different modes
    println!();
    println!("Mode comparison (context query on lib.rs):");
    println!("------------------------------------------------------------");

    let modes = vec!["adaptive", "full", "map", "signatures"];
    for mode in modes {
        let r = run_benchmark(
            &format!("Mode: {:12}", mode),
            &orchestrator,
            "context for lib.rs",
            Some("src/lib.rs"),
            Some(mode),
            true,
        );
        print_result(&r);
    }

    println!("------------------------------------------------------------");

    // Different intents
    println!();
    println!("Intent type comparison (fresh queries):");
    println!("------------------------------------------------------------");

    let intents = vec![
        ("Context query", "show me context for lib.rs"),
        ("Impact query", "what's the impact of changing lib.rs"),
        ("Dependencies", "show dependencies of lib.rs"),
        ("Doc query", "get documentation for lib.rs"),
        ("Search query", "find function named QueryOrchestrator"),
    ];

    for (name, intent) in intents {
        let r = run_benchmark(
            name,
            &orchestrator,
            intent,
            Some("src/lib.rs"),
            Some("adaptive"),
            true,
        );
        print_result(&r);
    }

    println!("------------------------------------------------------------");

    // Caching efficiency test
    println!();
    println!("Cache efficiency test (100 repeated queries):");
    println!("------------------------------------------------------------");

    let iterations = 100;

    // Cold start
    let start = Instant::now();
    for i in 0..iterations {
        let _ = orchestrator.orchestrate("context for lib.rs", Some("src/lib.rs"), None, false);
    }
    let cold_time = start.elapsed();

    // Cached
    let start = Instant::now();
    for i in 0..iterations {
        let _ = orchestrator.orchestrate("context for lib.rs", Some("src/lib.rs"), None, false);
    }
    let cached_time = start.elapsed();

    let speedup = cold_time.as_secs_f64() / cached_time.as_secs_f64();

    println!(
        "{:40} | {:>10} | {:>10}",
        "100 cold queries",
        format!("{:.3}s", cold_time.as_secs_f64()),
        format!(
            "{:.3}ms/iter",
            cold_time.as_secs_f64() * 1000.0 / iterations as f64
        )
    );
    println!(
        "{:40} | {:>10} | {:>10}",
        "100 cached queries",
        format!("{:.3}s", cached_time.as_secs_f64()),
        format!(
            "{:.3}ms/iter",
            cached_time.as_secs_f64() * 1000.0 / iterations as f64
        )
    );
    println!(
        "{:40} | {:>10}",
        "Cache speedup",
        format!("{:.1}x faster", speedup)
    );

    println!("------------------------------------------------------------");

    cleanup_db(&db_path);

    println!();
    println!("Benchmark complete!");
}
