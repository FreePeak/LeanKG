//! `leankg cost` — LOCOMO-style token-cost estimate (strategy §18.4 /
//! landscape sweep: `kg_cost estimate` is a unique niche).
//!
//! Given an impact radius (or a plain file set), estimate what it would cost
//! an agent to *reason over* that blast radius vs *rewrite* it:
//!
//! - `out` tokens: the source lines that would enter context.
//! - `in`  tokens: the model's reply budget (estimated 0.4× output).
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
    pub bytes: usize,
    pub out_tokens: usize,
    pub in_tokens: usize,
}

/// Aggregate cost estimate.
#[derive(Debug, Clone, serde::Serialize)]
pub struct CostEstimate {
    pub files: Vec<FileCost>,
    pub total_lines: usize,
    pub total_bytes: usize,
    pub out_tokens: usize,
    pub in_tokens: usize,
}

/// Rough tokens-per-char heuristic (English + code ≈ 4 chars/token). This is
/// intentionally coarse — it exists to *orient* the agent, not to bill.
fn estimate_tokens(bytes: usize) -> usize {
    bytes.div_ceil(4)
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

/// Compute a cost estimate for a set of file paths.
///
/// Files that are missing on disk (e.g. generated or unindexed) are skipped
/// and reported implicitly by absence from `files`.
pub fn estimate(
    files: &[String],
    base_dir: &Path,
) -> Result<CostEstimate, Box<dyn std::error::Error>> {
    let mut out = CostEstimate {
        files: Vec::with_capacity(files.len()),
        total_lines: 0,
        total_bytes: 0,
        out_tokens: 0,
        in_tokens: 0,
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
        // A file ending in `\n` has one line per newline; a non-terminated file
        // has one more line than newlines.
        let lines = newline_count + usize::from(!bytes.is_empty() && !bytes.ends_with(b"\n"));
        let out_tokens = estimate_tokens(bytes.len());
        let in_tokens = (out_tokens as u64 * 4 / 10) as usize; // 0.4× output
        out.total_lines += lines;
        out.total_bytes += bytes.len();
        out.out_tokens += out_tokens;
        out.in_tokens += in_tokens;
        out.files.push(FileCost {
            file: f.clone(),
            lines,
            bytes: bytes.len(),
            out_tokens,
            in_tokens,
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
    fn counts_lines_bytes_tokens() {
        let tmp = TempDir::new().unwrap();
        write(tmp.path(), "src/a.rs", "fn a() {}\nfn b() {}\n");
        let files = vec!["src/a.rs".to_string()];
        let est = estimate(&files, tmp.path()).expect("estimate");
        assert_eq!(est.files.len(), 1);
        assert_eq!(est.files[0].lines, 2);
        assert!(est.files[0].bytes > 0);
        assert_eq!(est.files[0].out_tokens, (est.files[0].bytes + 3) / 4);
        assert_eq!(est.files[0].in_tokens, est.files[0].out_tokens * 4 / 10);
        assert_eq!(est.total_lines, 2);
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
