//! End-to-end tests for budget guards, LSH, and LSP registry. These
//! tests don't need a graph DB — they exercise the building blocks in
//! isolation.

use leankg::budget::{current_rss_mb, BudgetExceeded, BudgetGuard};
use leankg::lsp::registry::{
    default_server_config, detect_language, extension_table, LspServerSpec,
};
use leankg::minhash::{minhash, minhash_jaccard, LshIndex, MinHashConfig};

#[test]
fn budget_guard_aborts_on_iteration_cap() {
    std::env::remove_var("LEANKG_TOOL_BUDGET_OFF");
    let mut g = BudgetGuard::with_caps("e2e_test", u64::MAX, u64::MAX, 5);
    for _ in 0..5 {
        g.tick();
    }
    match g.check() {
        Err(BudgetExceeded::Iterations { count, cap, .. }) => {
            assert_eq!(count, 5);
            assert_eq!(cap, 5);
        }
        other => panic!("expected iteration breach, got {:?}", other),
    }
}

#[test]
fn budget_guard_iter_only_never_fires_on_rss() {
    std::env::remove_var("LEANKG_TOOL_BUDGET_OFF");
    // iter_only sets cap_rss_mb = MAX, so even huge RSS is OK.
    let g = BudgetGuard::iter_only("e2e", 1_000_000);
    // Force one RSS read so we exercise the platform branch.
    let _ = current_rss_mb();
    assert!(!g.is_exhausted());
}

#[test]
fn budget_guard_unlimited_never_aborts() {
    std::env::remove_var("LEANKG_TOOL_BUDGET_OFF");
    let mut g = BudgetGuard::unlimited("e2e");
    for _ in 0..10_000 {
        g.tick();
    }
    assert!(g.check().is_ok());
}

#[test]
fn lsp_registry_has_go_python_rust_typescript() {
    for lang in &[
        "go",
        "python",
        "rust",
        "typescript",
        "javascript",
        "kotlin",
        "ruby",
    ] {
        assert!(
            LspServerSpec::for_language(lang).is_some(),
            "missing {lang}"
        );
    }
}

#[test]
fn lsp_registry_detects_language_from_path() {
    use std::path::Path;
    assert_eq!(detect_language(Path::new("src/main.rs")), Some("rust"));
    assert_eq!(detect_language(Path::new("foo.go")), Some("go"));
    assert_eq!(detect_language(Path::new("foo.tsx")), Some("typescript"));
    assert_eq!(detect_language(Path::new("foo.kt")), Some("kotlin"));
    assert_eq!(detect_language(Path::new("foo.sol")), Some("solidity"));
    assert_eq!(detect_language(Path::new("foo.unknown_ext")), None);
}

#[test]
fn lsp_registry_extension_table_maps_common_extensions() {
    let t = extension_table();
    assert_eq!(t.get("go").copied(), Some("go"));
    assert_eq!(t.get("py").copied(), Some("python"));
    assert_eq!(t.get("ts").copied(), Some("typescript"));
    assert_eq!(t.get("tsx").copied(), Some("typescript"));
    assert_eq!(t.get("kt").copied(), Some("kotlin"));
    assert_eq!(t.get("sol").copied(), Some("solidity"));
}

#[test]
fn lsp_default_server_config_has_command() {
    let cfg = default_server_config("typescript").unwrap();
    assert!(!cfg.command.is_empty());
    assert!(!cfg.args.is_empty());
    assert!(cfg.extensions.contains(&"ts".to_string()));
}

#[test]
fn lsh_finds_near_duplicate_across_paths() {
    let cfg = MinHashConfig::default();
    let mut idx = LshIndex::new(cfg);

    // Two unrelated docs.
    let _ = idx.insert("lorem ipsum dolor sit amet consectetur adipiscing elit");
    let _ = idx.insert("completely different words here, no overlap whatsoever");

    // Two near-duplicate docs.
    let id_a = idx.insert("fn handle(req http.Request) http.Response { return process(req) }");
    let id_b = idx.insert("fn handle(req http.Request) http.Response { return process(req) }");

    let pairs = idx.candidate_pairs();
    assert!(
        pairs
            .iter()
            .any(|(a, b)| { (*a == id_a && *b == id_b) || (*a == id_b && *b == id_a) }),
        "LSH should flag the identical docs as candidates, got {:?}",
        pairs
    );
}

#[test]
fn minhash_jaccard_self_is_one() {
    let cfg = MinHashConfig::default();
    let s = minhash("fn add(a:int,b:int){return a+b;}", &cfg);
    let score = minhash_jaccard(&s, &s);
    assert!(
        (score - 1.0).abs() < 1e-9,
        "self Jaccard must be 1.0, got {score}"
    );
}

#[test]
fn minhash_jaccard_disjoint_is_below_threshold() {
    let cfg = MinHashConfig::default();
    let a = minhash("the quick brown fox jumps over the lazy dog", &cfg);
    let b = minhash("foo bar baz qux corge quux grault", &cfg);
    let score = minhash_jaccard(&a, &b);
    assert!(score < 0.1, "disjoint should be near 0, got {score}");
}

#[test]
fn minhash_jaccard_near_clone_is_above_threshold() {
    let cfg = MinHashConfig::default();
    let a = minhash(
        "fn add(a:int,b:int){return a+b;}\nfn sub(a:int,b:int){return a-b;}",
        &cfg,
    );
    let b = minhash(
        "fn add(a:int,b:int){return a+b;}\nfn sub(a:int,b:int){return a-b;}\n// extra line",
        &cfg,
    );
    let score = minhash_jaccard(&a, &b);
    assert!(score > 0.5, "near-clone should score > 0.5, got {score}");
}

#[test]
fn budget_guard_with_rss_cap_reads_current_rss() {
    std::env::remove_var("LEANKG_TOOL_BUDGET_OFF");
    // Set cap to absurdly high so we don't actually trip it.
    let g = BudgetGuard::with_caps("e2e_rss", u64::MAX, u64::MAX / 2, 0);
    let rss = current_rss_mb().unwrap_or(0);
    assert!(!g.is_exhausted() || rss >= u64::MAX / 2);
}
