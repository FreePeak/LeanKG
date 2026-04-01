use leankg::benchmark::data::{PromptCategory, PromptTask};
use std::collections::HashSet;
use std::path::Path;

#[derive(Debug, Clone)]
pub struct ContextQualityResult {
    pub task_id: String,
    pub correct_files: Vec<String>,
    pub incorrect_files: Vec<String>,
    pub missing_files: Vec<String>,
    pub precision: f32,
    pub recall: f32,
    pub f1_score: f32,
}

pub fn validate_context_quality(
    task: &PromptTask,
    ai_referenced_files: &[String],
) -> ContextQualityResult {
    let expected: HashSet<_> = task.expected_files.iter().collect();
    let referenced: HashSet<_> = ai_referenced_files.iter().collect();

    let correct_files: Vec<_> = expected.intersection(&referenced).collect();
    let missing_files: Vec<_> = expected.difference(&referenced).collect();
    let incorrect_files: Vec<_> = referenced.difference(&expected).collect();

    let correct_count = correct_files.len() as f32;
    let incorrect_count = incorrect_files.len() as f32;
    let missing_count = missing_files.len() as f32;

    let precision = if correct_count + incorrect_count > 0.0 {
        correct_count / (correct_count + incorrect_count)
    } else {
        0.0
    };

    let recall = if correct_count + missing_count > 0.0 {
        correct_count / (correct_count + missing_count)
    } else {
        0.0
    };

    let f1_score = if precision + recall > 0.0 {
        2.0 * (precision * recall) / (precision + recall)
    } else {
        0.0
    };

    ContextQualityResult {
        task_id: task.id.clone(),
        correct_files: correct_files
            .into_iter()
            .map(|s| (*s).to_string())
            .collect(),
        incorrect_files: incorrect_files
            .into_iter()
            .map(|s| (*s).to_string())
            .collect(),
        missing_files: missing_files
            .into_iter()
            .map(|s| (*s).to_string())
            .collect(),
        precision,
        recall,
        f1_score,
    }
}

pub fn load_prompt_categories(
    prompts_dir: &Path,
) -> Result<Vec<PromptCategory>, Box<dyn std::error::Error>> {
    PromptCategory::load_all(prompts_dir)
}

pub fn calculate_overall_quality(qualities: &[ContextQualityResult]) -> (f32, f32, f32) {
    if qualities.is_empty() {
        return (0.0, 0.0, 0.0);
    }

    let total_precision: f32 = qualities.iter().map(|q| q.precision).sum();
    let total_recall: f32 = qualities.iter().map(|q| q.recall).sum();
    let total_f1: f32 = qualities.iter().map(|q| q.f1_score).sum();

    let count = qualities.len() as f32;
    (
        total_precision / count,
        total_recall / count,
        total_f1 / count,
    )
}

pub fn generate_quality_report(qualities: &[ContextQualityResult]) -> String {
    let mut report = String::from("# Context Quality Report\n\n");
    report.push_str("| Task | Precision | Recall | F1 | Correct | Incorrect | Missing |\n");
    report.push_str("|------|-----------|--------|-----|---------|----------|--------|\n");

    for q in qualities {
        report.push_str(&format!(
            "| {} | {:.2} | {:.2} | {:.2} | {} | {} | {} |\n",
            q.task_id,
            q.precision,
            q.recall,
            q.f1_score,
            q.correct_files.len(),
            q.incorrect_files.len(),
            q.missing_files.len()
        ));
    }

    let (avg_precision, avg_recall, avg_f1) = calculate_overall_quality(qualities);
    report.push_str(&format!(
        "\n**Average** | {:.2} | {:.2} | {:.2} |\n",
        avg_precision, avg_recall, avg_f1
    ));

    report
}

pub fn verdict_from_quality(qualities: &[ContextQualityResult]) -> String {
    let (_, _, avg_f1) = calculate_overall_quality(qualities);
    if avg_f1 >= 0.8 {
        "LeanKG provides EXCELLENT context correctness".to_string()
    } else if avg_f1 >= 0.6 {
        "LeanKG provides GOOD context correctness".to_string()
    } else if avg_f1 >= 0.4 {
        "LeanKG provides MODERATE context correctness - review needed".to_string()
    } else {
        "LeanKG context correctness is POOR - significant improvements needed".to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn create_test_task(id: &str, expected_files: Vec<&str>) -> PromptTask {
        PromptTask {
            id: id.to_string(),
            prompt: format!("Test prompt for {}", id),
            expected: expected_files.iter().map(|s| s.to_string()).collect(),
            expected_files: expected_files.iter().map(|s| s.to_string()).collect(),
        }
    }

    #[test]
    fn test_perfect_precision_and_recall() {
        let task = create_test_task("perfect", vec!["src/a.rs", "src/b.rs"]);
        let referenced = vec!["src/a.rs".to_string(), "src/b.rs".to_string()];

        let result = validate_context_quality(&task, &referenced);

        assert_eq!(result.precision, 1.0);
        assert_eq!(result.recall, 1.0);
        assert_eq!(result.f1_score, 1.0);
        assert!(result.correct_files.len() == 2);
        assert!(result.incorrect_files.is_empty());
        assert!(result.missing_files.is_empty());
    }

    #[test]
    fn test_partial_match() {
        let task = create_test_task("partial", vec!["src/a.rs", "src/b.rs", "src/c.rs"]);
        let referenced = vec!["src/a.rs".to_string(), "src/b.rs".to_string()];

        let result = validate_context_quality(&task, &referenced);

        assert_eq!(result.precision, 1.0);
        assert!((result.recall - 0.666).abs() < 0.01);
        assert!((result.f1_score - 0.8).abs() < 0.01);
        assert!(result.correct_files.len() == 2);
        assert!(result.missing_files.len() == 1);
    }

    #[test]
    fn test_incorrect_files_increase() {
        let task = create_test_task("incorrect", vec!["src/a.rs"]);
        let referenced = vec![
            "src/a.rs".to_string(),
            "src/b.rs".to_string(),
            "src/c.rs".to_string(),
        ];

        let result = validate_context_quality(&task, &referenced);

        assert!((result.precision - 0.333).abs() < 0.01);
        assert_eq!(result.recall, 1.0);
        assert!((result.f1_score - 0.5).abs() < 0.01);
        assert!(result.correct_files.len() == 1);
        assert!(result.incorrect_files.len() == 2);
    }

    #[test]
    fn test_empty_references() {
        let task = create_test_task("empty", vec!["src/a.rs", "src/b.rs"]);
        let referenced: Vec<String> = vec![];

        let result = validate_context_quality(&task, &referenced);

        assert_eq!(result.precision, 0.0);
        assert_eq!(result.recall, 0.0);
        assert_eq!(result.f1_score, 0.0);
        assert!(result.missing_files.len() == 2);
    }

    #[test]
    fn test_empty_expected() {
        let task = create_test_task("no-expected", vec![]);
        let referenced = vec!["src/a.rs".to_string()];

        let result = validate_context_quality(&task, &referenced);

        assert_eq!(result.precision, 0.0);
        assert_eq!(result.recall, 0.0);
        assert_eq!(result.f1_score, 0.0);
    }

    #[test]
    fn test_overall_quality_calculation() {
        let qualities = vec![
            ContextQualityResult {
                task_id: "task1".to_string(),
                correct_files: vec![],
                incorrect_files: vec![],
                missing_files: vec![],
                precision: 1.0,
                recall: 1.0,
                f1_score: 1.0,
            },
            ContextQualityResult {
                task_id: "task2".to_string(),
                correct_files: vec![],
                incorrect_files: vec![],
                missing_files: vec![],
                precision: 0.5,
                recall: 0.5,
                f1_score: 0.5,
            },
        ];

        let (avg_precision, avg_recall, avg_f1) = calculate_overall_quality(&qualities);

        assert_eq!(avg_precision, 0.75);
        assert_eq!(avg_recall, 0.75);
        assert_eq!(avg_f1, 0.75);
    }

    #[test]
    fn test_verdict_thresholds() {
        let excellent = vec![ContextQualityResult {
            task_id: "t".to_string(),
            correct_files: vec![],
            incorrect_files: vec![],
            missing_files: vec![],
            precision: 1.0,
            recall: 1.0,
            f1_score: 0.85,
        }];
        assert!(verdict_from_quality(&excellent).contains("EXCELLENT"));

        let moderate = vec![ContextQualityResult {
            task_id: "t".to_string(),
            correct_files: vec![],
            incorrect_files: vec![],
            missing_files: vec![],
            precision: 0.5,
            recall: 0.5,
            f1_score: 0.5,
        }];
        assert!(verdict_from_quality(&moderate).contains("MODERATE"));
    }
}
