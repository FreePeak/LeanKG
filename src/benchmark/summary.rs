use crate::benchmark::data::{BenchmarkResult, OverheadResult};
use std::collections::HashMap;
use std::path::Path;

#[derive(Debug, Clone)]
pub struct SummaryReport {
    pub total_tasks: usize,
    pub successful_tasks: usize,
    pub token_savings: i32,
    pub token_savings_percent: f32,
    pub quality_avg_precision: f32,
    pub quality_avg_recall: f32,
    pub quality_avg_f1: f32,
    pub datastore_elements_checked: usize,
    pub datastore_elements_valid: usize,
    pub datastore_duplicates: usize,
    pub verdict: String,
}

pub fn calculate_token_savings(
    with_leankg: &BenchmarkResult,
    without_leankg: &BenchmarkResult,
) -> OverheadResult {
    with_leankg.overhead(without_leankg)
}

pub fn generate_summary_report(
    results: &HashMap<String, (BenchmarkResult, BenchmarkResult)>,
    quality_scores: &[(String, f32, f32, f32)],
    datastore_stats: &(usize, usize, usize),
) -> SummaryReport {
    let mut total_savings = 0i32;
    let mut successful = 0usize;
    let mut quality_precisions = Vec::new();
    let mut quality_recalls = Vec::new();
    let mut quality_f1s = Vec::new();

    for (_, (with_result, without_result)) in results {
        if with_result.success && without_result.success {
            successful += 1;
            let overhead = calculate_token_savings(with_result, without_result);
            total_savings += overhead.token_delta;
        }
    }

    let avg_savings = if !results.is_empty() {
        total_savings as f32 / results.len() as f32
    } else {
        0.0
    };

    for (_, precision, recall, f1) in quality_scores {
        quality_precisions.push(*precision);
        quality_recalls.push(*recall);
        quality_f1s.push(*f1);
    }

    let avg_precision = if !quality_precisions.is_empty() {
        quality_precisions.iter().sum::<f32>() / quality_precisions.len() as f32
    } else {
        0.0
    };

    let avg_recall = if !quality_recalls.is_empty() {
        quality_recalls.iter().sum::<f32>() / quality_recalls.len() as f32
    } else {
        0.0
    };

    let avg_f1 = if !quality_f1s.is_empty() {
        quality_f1s.iter().sum::<f32>() / quality_f1s.len() as f32
    } else {
        0.0
    };

    let verdict = determine_verdict(avg_f1, total_savings, successful);

    SummaryReport {
        total_tasks: results.len(),
        successful_tasks: successful,
        token_savings: total_savings,
        token_savings_percent: avg_savings,
        quality_avg_precision: avg_precision,
        quality_avg_recall: avg_recall,
        quality_avg_f1: avg_f1,
        datastore_elements_checked: datastore_stats.0,
        datastore_elements_valid: datastore_stats.1,
        datastore_duplicates: datastore_stats.2,
        verdict,
    }
}

pub fn determine_verdict(avg_f1: f32, total_savings: i32, _successful: usize) -> String {
    let savings_positive = total_savings <= 0;
    let f1_good = avg_f1 >= 0.6;
    let f1_excellent = avg_f1 >= 0.8;

    if savings_positive && f1_excellent {
        "LeanKG PROVIDES EXCELLENT context while saving tokens".to_string()
    } else if savings_positive && f1_good {
        "LeanKG PROVIDES GOOD context and saves tokens".to_string()
    } else if savings_positive {
        "LeanKG saves tokens but context quality needs improvement".to_string()
    } else if f1_excellent {
        "LeanKG provides EXCELLENT context but has token overhead".to_string()
    } else if f1_good {
        "LeanKG provides GOOD context but has token overhead".to_string()
    } else {
        "LeanKG context quality is POOR and has token overhead - needs improvement".to_string()
    }
}

pub fn generate_markdown_report(
    report: &SummaryReport,
    detailed_results: &HashMap<String, (BenchmarkResult, BenchmarkResult, i32)>,
    output_path: &Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut md = String::from("# LeanKG AB Testing Summary\n\n");

    md.push_str("## Overall Verdict\n\n");
    md.push_str(&format!("**{}**\n\n", report.verdict));

    md.push_str("## Token Efficiency\n\n");
    md.push_str("| Task | With LeanKG | Without | Savings |\n");
    md.push_str("|------|-------------|---------|--------|\n");
    for (task, (with_result, without_result, savings)) in detailed_results {
        md.push_str(&format!(
            "| {} | {} | {} | {} |\n",
            task, with_result.total_tokens, without_result.total_tokens, savings
        ));
    }
    md.push_str(&format!(
        "\n**Total Token Savings:** {} (avg {:.1} per task)\n\n",
        report.token_savings, report.token_savings_percent
    ));

    md.push_str("## Context Quality\n\n");
    md.push_str(&format!("| Metric | Score |\n|--------|-------|\n"));
    md.push_str(&format!(
        "| Precision | {:.2} |\n",
        report.quality_avg_precision
    ));
    md.push_str(&format!("| Recall | {:.2} |\n", report.quality_avg_recall));
    md.push_str(&format!("| F1 Score | {:.2} |\n\n", report.quality_avg_f1));

    md.push_str("## Data Store Correctness\n\n");
    md.push_str(&format!(
        "- Elements checked: {}\n",
        report.datastore_elements_checked
    ));
    md.push_str(&format!(
        "- Elements valid: {} ({:.1}%)\n",
        report.datastore_elements_valid,
        if report.datastore_elements_checked > 0 {
            (report.datastore_elements_valid as f32 / report.datastore_elements_checked as f32
                * 100.0)
        } else {
            0.0
        }
    ));
    md.push_str(&format!(
        "- Duplicate elements: {}\n\n",
        report.datastore_duplicates
    ));

    md.push_str("## Summary Statistics\n\n");
    md.push_str(&format!("- Tasks run: {}\n", report.total_tasks));
    md.push_str(&format!("- Successful: {}\n", report.successful_tasks));
    md.push_str(&format!(
        "- Token savings: {} tokens\n",
        report.token_savings
    ));

    std::fs::write(output_path, &md)?;

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_determine_verdict_excellent() {
        let verdict = determine_verdict(0.85, -500, 10);
        assert!(verdict.contains("EXCELLENT"));
        assert!(verdict.contains("saving tokens"));
    }

    #[test]
    fn test_determine_verdict_good_with_savings() {
        let verdict = determine_verdict(0.65, -200, 8);
        assert!(verdict.contains("GOOD"));
        assert!(verdict.contains("saves tokens"));
    }

    #[test]
    fn test_determine_verdict_overhead_but_excellent() {
        let verdict = determine_verdict(0.85, 500, 10);
        assert!(verdict.contains("EXCELLENT"));
        assert!(verdict.contains("token overhead"));
    }

    #[test]
    fn test_determine_verdict_poor() {
        let verdict = determine_verdict(0.35, 1000, 5);
        assert!(verdict.contains("POOR"));
        assert!(verdict.contains("needs improvement"));
    }

    #[test]
    fn test_calculate_token_savings() {
        let with_result = BenchmarkResult {
            total_tokens: 1000,
            input_tokens: 800,
            cached_tokens: 200,
            token_percent: 0.0,
            build_time_seconds: 0.0,
            success: true,
            context: None,
        };
        let without_result = BenchmarkResult {
            total_tokens: 1500,
            input_tokens: 1200,
            cached_tokens: 0,
            token_percent: 0.0,
            build_time_seconds: 0.0,
            success: true,
            context: None,
        };

        let overhead = calculate_token_savings(&with_result, &without_result);
        assert_eq!(overhead.token_delta, -500);
    }

    #[test]
    fn test_summary_report_fields() {
        let report = SummaryReport {
            total_tasks: 10,
            successful_tasks: 8,
            token_savings: -1000,
            token_savings_percent: -100.0,
            quality_avg_precision: 0.8,
            quality_avg_recall: 0.75,
            quality_avg_f1: 0.77,
            datastore_elements_checked: 100,
            datastore_elements_valid: 98,
            datastore_duplicates: 2,
            verdict: "Test verdict".to_string(),
        };

        assert_eq!(report.total_tasks, 10);
        assert_eq!(report.successful_tasks, 8);
        assert_eq!(report.datastore_duplicates, 2);
    }

    #[test]
    fn test_generate_summary_report() {
        let mut results = HashMap::new();
        results.insert(
            "task1".to_string(),
            (
                BenchmarkResult {
                    total_tokens: 1000,
                    input_tokens: 800,
                    cached_tokens: 200,
                    token_percent: 0.0,
                    build_time_seconds: 0.0,
                    success: true,
                    context: None,
                },
                BenchmarkResult {
                    total_tokens: 1500,
                    input_tokens: 1200,
                    cached_tokens: 0,
                    token_percent: 0.0,
                    build_time_seconds: 0.0,
                    success: true,
                    context: None,
                },
            ),
        );

        let quality_scores = vec![("task1".to_string(), 0.9, 0.85, 0.87)];
        let datastore_stats = (100, 98, 2);

        let report = generate_summary_report(&results, &quality_scores, &datastore_stats);

        assert_eq!(report.total_tasks, 1);
        assert_eq!(report.successful_tasks, 1);
        assert_eq!(report.token_savings, -500);
        assert_eq!(report.quality_avg_f1, 0.87);
    }
}
