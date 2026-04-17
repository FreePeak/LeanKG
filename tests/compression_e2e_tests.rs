use leankg::compress::modes::ReadMode;
use leankg::compress::reader::FileReader;
use leankg::compress::session_cache::SessionCache;
use parking_lot::RwLock;
use std::fs;
use std::sync::Arc;

fn setup_dummy_file(name: &str, content: &str) -> String {
    let mut path = std::env::temp_dir();
    path.push(name);
    fs::write(&path, content).unwrap();
    path.to_str().unwrap().to_string()
}

#[test]
fn test_symbol_map_compression_savings() {
    let cache = Arc::new(RwLock::new(SessionCache::new()));
    let mut reader = FileReader::new(cache);

    let content = r#"
        pub struct AbstractStrategyFactorySingleton {
            pub extremely_long_enterprise_variable_name_one: String,
            pub extremely_long_enterprise_variable_name_two: String,
        }

        impl AbstractStrategyFactorySingleton {
            pub fn perform_highly_complex_algorithmic_computation(
                &self,
                extremely_long_enterprise_variable_name_one: String
            ) {
                println!("{}", extremely_long_enterprise_variable_name_one);
                println!("{}", self.extremely_long_enterprise_variable_name_two);
            }
        }
    "#;

    let path = setup_dummy_file("symbol_map_test.rs", content);

    let result = reader.read(&path, ReadMode::Full, None, false).unwrap();

    let orig_tokens = result.total_tokens;
    let new_tokens = result.tokens;

    // Assert significant LLM-token savings
    assert!(
        orig_tokens > new_tokens,
        "Original {} should be > Compressed {}",
        orig_tokens,
        new_tokens
    );
    assert!(
        result.savings_percent > 5.0,
        "Savings percent should be > 5.0, got {}",
        result.savings_percent
    );

    println!("--- SymbolMap Comparison ---");
    println!("Original Tokens: {}", orig_tokens);
    println!("Compressed Tokens: {}", new_tokens);
    println!("Savings: {:.2}%", result.savings_percent);

    assert!(result.content.contains("[MAP]:"));
}

#[test]
fn test_diff_mode_compression_savings() {
    let cache = Arc::new(RwLock::new(SessionCache::new()));
    let mut reader = FileReader::new(cache.clone());

    // Create a 50-line boilerplate file
    let mut content = String::new();
    for i in 0..50 {
        content.push_str(&format!(
            "fn boilerplate_function_number_{}() {{ println!(\"Boilerplate code line {}\"); }}\n",
            i, i
        ));
    }

    let path = setup_dummy_file("diff_test.rs", &content);

    // Baseline Full Cache Ingestion
    let result1 = reader.read(&path, ReadMode::Full, None, false).unwrap();
    let original_total_tokens = result1.total_tokens;

    // Mutate line 25
    let mut new_content = content.clone();
    new_content = new_content.replace(
        "fn boilerplate_function_number_25() { println!(\"Boilerplate code line 25\"); }",
        "fn boilerplate_function_number_25() { println!(\"THIS LINE HAS CHANGED!\"); }",
    );
    fs::write(&path, new_content).unwrap();

    // Secondary Read - Triggering Fast-Delta Diff Mode
    let result2 = reader.read(&path, ReadMode::Diff, None, false).unwrap();
    let diff_tokens = result2.tokens;

    assert!(
        diff_tokens < original_total_tokens / 5,
        "Diff tokens {} should be significantly smaller than original {}",
        diff_tokens,
        original_total_tokens
    );
    assert!(result2.content.contains("THIS LINE HAS CHANGED!"));
    assert!(result2.content.contains("@@")); // Git unified diff marker

    println!("--- Diff Mode Comparison ---");
    println!("Original Tokens: {}", original_total_tokens);
    println!("Diff Mode Tokens: {}", diff_tokens);
    println!(
        "Diff Savings: {:.2}%",
        100.0 - (diff_tokens as f64 / original_total_tokens as f64 * 100.0)
    );
}

#[test]
fn test_session_cache_preemption() {
    let cache = Arc::new(RwLock::new(SessionCache::new()));
    let mut reader = FileReader::new(cache.clone());

    let content = "fn standard_function_routine() { let x = 0; }";
    let path = setup_dummy_file("cache_preempt_test.rs", content);

    // Initial read
    let result1 = reader.read(&path, ReadMode::Full, None, false).unwrap();
    assert!(!result1.is_cached);

    // Exact unchanged recall
    let result2 = reader.read(&path, ReadMode::Full, None, false).unwrap();
    assert!(result2.is_cached);

    assert!(
        result2.savings_percent > 90.0,
        "Cache hits should yield 90%+ savings, got {}",
        result2.savings_percent
    );
    println!("--- Cache Preemption ---");
    println!("Original Tokens: {}", result2.total_tokens);
    println!("Preemption Msg Tokens: {}", result2.tokens);
    println!("Preemption Savings: {}%", result2.savings_percent);
}
