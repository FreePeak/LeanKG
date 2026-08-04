//! `leankg cost` — LOCOMO-style token-cost estimate (strategy §18.4 /
//! landscape sweep: `kg_cost estimate` is a unique niche).
//!
//! Given an impact radius (or a plain file set), estimate what it would cost
//! an agent to *reason over* that blast radius vs *rewrite* it:
//!
//! - `in`  tokens: the source lines that would enter the prompt (LOCOMO/scc
//!   SLOC count × ~13 tokens/SLOC, code-aware).
//! - `out` tokens: the model's reply budget (per-model `model_rate`).
//! - per-file totals and the whole-set sum.
//!
//! Pure read; no DB writes. Estimation is token *counting* on source text,
//! not a per-model price — callers multiply by their provider rate.

use std::path::Path;

use crate::db::models::CodeElement;
use crate::graph::GraphEngine;

/// A single file's cost line.
#[derive(Debug, Clone, serde::Serialize)]
pub struct FileCost {
    pub file: String,
    pub lines: usize,
    pub sloc: usize,
    pub bytes: usize,
    pub in_tokens: usize,
    pub out_tokens: usize,
}

/// Aggregate cost estimate.
#[derive(Debug, Clone, serde::Serialize)]
pub struct CostEstimate {
    pub model: &'static str,
    pub files: Vec<FileCost>,
    pub total_lines: usize,
    pub total_sloc: usize,
    pub total_bytes: usize,
    pub in_tokens: usize,
    pub out_tokens: usize,
}

/// Per-model knobs. Defaults track the LOCOMO / scc heuristic for source
/// code (≈13 tokens per source line of code, mid-size model reply budget).
#[derive(Debug, Clone, Copy)]
pub struct ModelRate {
    /// Tokens per source line of code (in-context). LOCOMO cites ~13.
    pub tokens_per_sloc: u32,
    /// Model reply budget (completion) per affected file.
    pub out_tokens_per_file: u32,
}

impl Default for ModelRate {
    fn default() -> Self {
        Self {
            tokens_per_sloc: 13,
            out_tokens_per_file: 256,
        }
    }
}

/// Collect the distinct source files behind a set of impacted elements.
pub fn files_for_elements(elements: &[CodeElement]) -> Vec<String> {
    let mut seen = std::collections::BTreeSet::new();
    for el in elements {
        if !el.file_path.is_empty() {
            let rel = el.file_path.trim_start_matches("./");
            seen.insert(rel.to_string());
        }
    }
    seen.into_iter().collect()
}

/// Count source lines of code in a byte buffer. Mirrors the scc heuristic:
/// blank-only lines and lines that are only `{ } / whitespace` are stripped;
/// everything else counts. The result is the in-context (prompt) volume.
fn count_sloc(bytes: &[u8]) -> usize {
    let mut sloc = 0usize;
    for line in bytes.split(|&b| b == b'\n') {
        let trimmed: Vec<u8> = line
            .iter()
            .skip_while(|&&b| b == b' ' || b == b'\t' || b == b'\r')
            .copied()
            .collect();
        if trimmed.is_empty() {
            continue;
        }
        if trimmed
            .iter()
            .all(|&b| matches!(b, b'{' | b'}' | b'/' | b' ' | b'\t' | b'\r'))
        {
            continue;
        }
        sloc += 1;
    }
    sloc
}

/// Compute a cost estimate for a set of file paths.
///
/// Files that are missing on disk (e.g. generated or unindexed) are skipped
/// and reported implicitly by absence from `files`.
pub fn estimate(
    files: &[String],
    base_dir: &Path,
) -> Result<CostEstimate, Box<dyn std::error::Error>> {
    estimate_with_model(files, base_dir, &ModelRate::default())
}

/// As [`estimate`], with explicit model-rate knobs. Use this when you need
/// to match a specific provider's token-per-SLOC or reply-budget figures.
pub fn estimate_with_model(
    files: &[String],
    base_dir: &Path,
    rate: &ModelRate,
) -> Result<CostEstimate, Box<dyn std::error::Error>> {
    let mut out = CostEstimate {
        model: "locomo-default",
        files: Vec::with_capacity(files.len()),
        total_lines: 0,
        total_sloc: 0,
        total_bytes: 0,
        in_tokens: 0,
        out_tokens: 0,
    };
    for f in files {
        let path = if f.starts_with('/') {
            Path::new(f).to_path_buf()
        } else {
            base_dir.join(f)
        };
        let Ok(meta) = std::fs::metadata(&path) else {
            continue;
        };
        if !meta.is_file() {
            continue;
        }
        let bytes = match std::fs::read(&path) {
            Ok(b) => b,
            Err(_) => continue,
        };
        let newline_count = bytes.iter().filter(|&&b| b == b'\n').count();
        let lines = newline_count + usize::from(!bytes.is_empty() && !bytes.ends_with(b"\n"));
        let sloc = count_sloc(&bytes);
        let in_tokens = sloc.saturating_mul(rate.tokens_per_sloc as usize);
        let out_tokens = rate.out_tokens_per_file as usize;
        out.total_lines += lines;
        out.total_sloc += sloc;
        out.total_bytes += bytes.len();
        out.in_tokens += in_tokens;
        out.out_tokens += out_tokens;
        out.files.push(FileCost {
            file: f.clone(),
            lines,
            sloc,
            bytes: bytes.len(),
            in_tokens,
            out_tokens,
        });
    }
    Ok(out)
}

/// Estimate the cost of an impact radius: run the impact scan, then price the
/// affected files.
pub fn estimate_impact(
    start_file: &str,
    depth: u32,
    max_affected: usize,
    db_path: &Path,
    base_dir: &Path,
) -> Result<(crate::graph::ImpactResult, CostEstimate), Box<dyn std::error::Error>> {
    let db = crate::db::schema::init_db(db_path)?;
    let graph_engine = GraphEngine::new(db);
    let analyzer = crate::graph::ImpactAnalyzer::new(&graph_engine);
    let opts = crate::graph::ImpactScanOptions { max_affected };
    let result = analyzer.calculate_impact_radius_with_options(start_file, depth, 0.0, &opts)?;
    let files = files_for_elements(&result.affected_elements);
    let cost = estimate(&files, base_dir)?;
    Ok((result, cost))
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    fn write(base: &Path, rel: &str, content: &str) {
        let p = base.join(rel);
        std::fs::create_dir_all(p.parent().unwrap()).unwrap();
        std::fs::write(p, content).unwrap();
    }

    #[test]
    fn counts_lines_sloc_tokens() {
        // LOCOMO model: in_tokens = sloc × tokens_per_sloc, out_tokens = reply budget.
        let tmp = TempDir::new().unwrap();
        write(
            tmp.path(),
            "src/a.rs",
            "fn a() {}\nfn b() {}\n// comment\n\n",
        );
        let files = vec!["src/a.rs".to_string()];
        let est = estimate(&files, tmp.path()).expect("estimate");
        assert_eq!(est.files.len(), 1);
        assert_eq!(est.files[0].lines, 4);
        // 3 SLOC: `fn a() {}`, `fn b() {}`, `// comment`; the blank line is dropped.
        assert_eq!(est.files[0].sloc, 3);
        // Default rate: 13 tokens/SLOC, 256 reply tokens per file.
        assert_eq!(est.files[0].in_tokens, 3 * 13);
        assert_eq!(est.files[0].out_tokens, 256);
        assert_eq!(est.in_tokens, 3 * 13);
        assert_eq!(est.out_tokens, 256);
        assert_eq!(est.total_lines, 4);
        assert_eq!(est.total_sloc, 3);
    }

    #[test]
    fn in_out_direction_matches_locomo() {
        // Source text is INPUT (prompt), not output. The 0.4× ratio
        // was the old wrong-direction model; regression guard.
        let tmp = TempDir::new().unwrap();
        write(tmp.path(), "src/a.rs", "fn x() {}\n");
        let est = estimate(&["src/a.rs".into()], tmp.path()).expect("est");
        // in_tokens = sloc × rate.tokens_per_sloc, NOT derived from out_tokens.
        assert!(est.in_tokens > 0);
        // out_tokens is the reply budget (constant per file), not bytes/4.
        assert_eq!(est.out_tokens, 256);
    }

    #[test]
    fn model_rate_hook_overrides() {
        let tmp = TempDir::new().unwrap();
        write(tmp.path(), "src/a.rs", "fn x() {}\n");
        let rate = ModelRate {
            tokens_per_sloc: 7,
            out_tokens_per_file: 100,
        };
        let est = estimate_with_model(&["src/a.rs".into()], tmp.path(), &rate).expect("est");
        assert_eq!(est.in_tokens, 7);
        assert_eq!(est.out_tokens, 100);
        assert_eq!(est.model, "locomo-default");
    }

    #[test]
    fn blank_and_brace_only_lines_dropped_from_sloc() {
        assert_eq!(count_sloc(b"fn a() {}\n\nfn b() {}\n"), 2);
        assert_eq!(count_sloc(b"   \n{\n}\n// hi\nfn c() {}\n"), 2);
        assert_eq!(count_sloc(b""), 0);
    }

    #[test]
    fn missing_files_are_skipped() {
        let tmp = TempDir::new().unwrap();
        write(tmp.path(), "src/a.rs", "x");
        let files = vec!["src/a.rs".to_string(), "src/ghost.rs".to_string()];
        let est = estimate(&files, tmp.path()).expect("estimate");
        assert_eq!(est.files.len(), 1, "missing file skipped");
    }

    #[test]
    fn files_for_elements_dedups_and_strips_dot_slash() {
        let mk = |file: &str| CodeElement {
            qualified_name: format!("{file}::f"),
            element_type: "function".into(),
            name: "f".into(),
            file_path: file.into(),
            line_start: 1,
            line_end: 2,
            language: "rust".into(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::Value::Null,
            env: "local".into(),
        };
        let els = vec![mk("./src/a.rs"), mk("./src/a.rs"), mk("src/b.rs")];
        let files = files_for_elements(&els);
        assert_eq!(files, vec!["src/a.rs".to_string(), "src/b.rs".to_string()]);
    }

    #[test]
    fn empty_set_is_zero_cost() {
        let tmp = TempDir::new().unwrap();
        let est = estimate(&[], tmp.path()).expect("estimate");
        assert_eq!(est.out_tokens, 0);
        assert!(est.files.is_empty());
    }
}
