use leankg::benchmark::context_parser::{ContextParser, QualityMetrics};

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_quality_metrics_perfect_match() {
        let expected = vec!["src/a.rs".to_string(), "src/b.rs".to_string()];
        let actual = vec!["src/a.rs".to_string(), "src/b.rs".to_string()];

        let metrics = QualityMetrics::calculate(&expected, &actual);

        assert_eq!(metrics.precision, 1.0);
        assert_eq!(metrics.recall, 1.0);
        assert_eq!(metrics.f1_score, 1.0);
        assert!(metrics.incorrect_files.is_empty());
        assert!(metrics.missing_files.is_empty());
    }

    #[test]
    fn test_quality_metrics_partial_match() {
        let expected = vec!["src/a.rs".to_string(), "src/b.rs".to_string()];
        let actual = vec!["src/a.rs".to_string(), "src/c.rs".to_string()];

        let metrics = QualityMetrics::calculate(&expected, &actual);

        assert!((metrics.precision - 0.5).abs() < 0.01);
        assert!((metrics.recall - 0.5).abs() < 0.01);
        assert!((metrics.f1_score - 0.5).abs() < 0.01);
        assert_eq!(metrics.correct_files.len(), 1);
        assert_eq!(metrics.incorrect_files.len(), 1);
        assert_eq!(metrics.missing_files.len(), 1);
    }

    #[test]
    fn test_quality_metrics_no_match() {
        let expected = vec!["src/a.rs".to_string()];
        let actual = vec!["src/b.rs".to_string()];

        let metrics = QualityMetrics::calculate(&expected, &actual);

        assert_eq!(metrics.precision, 0.0);
        assert_eq!(metrics.recall, 0.0);
        assert_eq!(metrics.f1_score, 0.0);
    }

    #[test]
    fn test_quality_metrics_empty_expected() {
        let expected: Vec<String> = vec![];
        let actual = vec!["src/a.rs".to_string()];

        let metrics = QualityMetrics::calculate(&expected, &actual);

        assert_eq!(metrics.precision, 0.0);
        assert_eq!(metrics.recall, 0.0);
        assert_eq!(metrics.f1_score, 0.0);
    }

    #[test]
    fn test_quality_metrics_empty_actual() {
        let expected = vec!["src/a.rs".to_string()];
        let actual: Vec<String> = vec![];

        let metrics = QualityMetrics::calculate(&expected, &actual);

        assert_eq!(metrics.precision, 0.0);
        assert_eq!(metrics.recall, 0.0);
        assert_eq!(metrics.f1_score, 0.0);
    }

    #[test]
    fn test_quality_metrics_verdict_excellent() {
        let metrics = QualityMetrics {
            precision: 0.95,
            recall: 0.95,
            f1_score: 0.95,
            correct_files: vec![],
            incorrect_files: vec![],
            missing_files: vec![],
        };
        assert_eq!(metrics.verdict(), "EXCELLENT");
    }

    #[test]
    fn test_quality_metrics_verdict_good() {
        let metrics = QualityMetrics {
            precision: 0.8,
            recall: 0.8,
            f1_score: 0.8,
            correct_files: vec![],
            incorrect_files: vec![],
            missing_files: vec![],
        };
        assert_eq!(metrics.verdict(), "GOOD");
    }

    #[test]
    fn test_quality_metrics_verdict_moderate() {
        let metrics = QualityMetrics {
            precision: 0.6,
            recall: 0.6,
            f1_score: 0.6,
            correct_files: vec![],
            incorrect_files: vec![],
            missing_files: vec![],
        };
        assert_eq!(metrics.verdict(), "MODERATE");
    }

    #[test]
    fn test_quality_metrics_verdict_poor() {
        let metrics = QualityMetrics {
            precision: 0.3,
            recall: 0.3,
            f1_score: 0.3,
            correct_files: vec![],
            incorrect_files: vec![],
            missing_files: vec![],
        };
        assert_eq!(metrics.verdict(), "POOR");
    }

    #[test]
    fn test_context_parser_extracts_src_paths() {
        let stdout = "You should look at src/main.rs and src/lib.rs for the implementation";
        let files = ContextParser::parse_file_paths(stdout);

        assert!(files.contains(&"src/main.rs".to_string()));
        assert!(files.contains(&"src/lib.rs".to_string()));
    }

    #[test]
    fn test_context_parser_handles_multiple_paths() {
        let stdout = "Found in src/db/models.rs and lib/helper.rs";
        let files = ContextParser::parse_file_paths(stdout);

        assert!(files.contains(&"src/db/models.rs".to_string()));
        assert!(files.contains(&"lib/helper.rs".to_string()));
    }

    #[test]
    fn test_context_parser_deduplicates() {
        let stdout = "src/main.rs appears twice in the code. Also see src/main.rs for more.";
        let files = ContextParser::parse_file_paths(stdout);

        let main_count = files.iter().filter(|f| *f == "src/main.rs").count();
        assert_eq!(main_count, 1);
    }

    #[test]
    fn test_context_parser_handles_nested_paths() {
        let stdout = "Check src/graph/query.rs and src/indexer/extractor.rs";
        let files = ContextParser::parse_file_paths(stdout);

        assert!(files.contains(&"src/graph/query.rs".to_string()));
        assert!(files.contains(&"src/indexer/extractor.rs".to_string()));
    }

    #[test]
    fn test_context_parser_handles_tests_paths() {
        let stdout = "Tests are in tests/mcp_tests.rs";
        let files = ContextParser::parse_file_paths(stdout);

        assert!(files.contains(&"tests/mcp_tests.rs".to_string()));
    }
}
