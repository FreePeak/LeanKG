pub mod context_parser;
pub mod data;
pub mod runner;
pub mod summary;

pub use context_parser::QualityMetrics;

use std::path::PathBuf;

pub use runner::{BenchmarkRunner, CliTool};

pub fn run(category: Option<String>, cli: CliTool) -> Result<(), Box<dyn std::error::Error>> {
    let prompts_dir = PathBuf::from("benchmark/prompts");
    let output_dir = PathBuf::from("benchmark/results");

    let categories = if let Some(cat) = category {
        vec![data::PromptCategory::from_yaml(
            &prompts_dir.join(format!("{}.yaml", cat)),
        )?]
    } else {
        data::PromptCategory::load_all(&prompts_dir)?
    };

    let runner = BenchmarkRunner::new(output_dir, cli);

    for cat in &categories {
        println!("\n=== Category: {} ===\n", cat.name);
        for task in &cat.tasks {
            println!("Running: {}", task.id);

            let with_leankg = runner.run_with_leankg(&task.prompt);
            let without_leankg = runner.run_without_leankg(&task.prompt);

            let overhead = with_leankg.overhead(&without_leankg);

            println!(
                "  With LeanKG: {} tokens (input: {}, cached: {})",
                with_leankg.total_tokens, with_leankg.input_tokens, with_leankg.cached_tokens
            );
            println!(
                "  Without: {} tokens (input: {}, cached: {})",
                without_leankg.total_tokens,
                without_leankg.input_tokens,
                without_leankg.cached_tokens
            );
            println!("  Overhead: {} tokens", overhead.token_delta);

            if !task.expected_files.is_empty() {
                let with_quality = with_leankg
                    .context
                    .as_ref()
                    .map(|c| QualityMetrics::calculate(&task.expected_files, &c.files_referenced));
                let without_quality = without_leankg
                    .context
                    .as_ref()
                    .map(|c| QualityMetrics::calculate(&task.expected_files, &c.files_referenced));

                if let Some(wq) = &with_quality {
                    println!(
                        "  LeanKG Quality: Precision={:.2} | Recall={:.2} | F1={:.2} | {}",
                        wq.precision,
                        wq.recall,
                        wq.f1_score,
                        wq.verdict()
                    );
                    println!("    Correct Files: {:?}", wq.correct_files);
                    if !wq.incorrect_files.is_empty() {
                        println!("    Incorrect (false positives): {:?}", wq.incorrect_files);
                    }
                    if !wq.missing_files.is_empty() {
                        println!("    Missing (false negatives): {:?}", wq.missing_files);
                    }
                } else {
                    println!("  LeanKG Quality: (context not available)");
                }

                if let Some(uq) = &without_quality {
                    println!(
                        "  Without LeanKG Quality: Precision={:.2} | Recall={:.2} | F1={:.2} | {}",
                        uq.precision,
                        uq.recall,
                        uq.f1_score,
                        uq.verdict()
                    );
                } else {
                    println!("  Without LeanKG Quality: (context not available)");
                }
            }
            println!();

            let _ = runner.save_comparison(&with_leankg, &without_leankg, &task.id);
        }
    }

    Ok(())
}
