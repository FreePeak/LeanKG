//! Guard: no legacy storage-engine or APM-vendor terms anywhere in the repo.
//!
//! LeanKG is PostgreSQL-only (`PostgresBackend`, plan D4). References to the
//! retired embedded engine ("cozo") or vendor-specific telemetry ("datadog")
//! must not appear in code, configs, tool descriptions, UI text, or living
//! documentation. Dated historical records are explicitly allowlisted below —
//! they are audit evidence, not active surface.

use std::fs;
use std::path::{Path, PathBuf};

const FORBIDDEN: &[&str] = &["cozo", "datadog"];

/// Historical / generated-record paths excluded from the scan.
/// Everything else — src, tests, benches, examples, e2e, scripts, npm, ui,
/// ui-v2, docs, generated_docs, .github, ontology, config, hooks, and every
/// root-level file — must be term-free.
const ALLOWLIST_PREFIXES: &[&str] = &[
    // release-please generated history
    "CHANGELOG.md",
    // migration audit trail (dated engineering evidence)
    "docs/plan-migrate-cozo-to-postgres-pgvector.md",
    // SQL-migration plan + dated cycle handoff records (historical evidence
    // of the engine removal itself — the terms ARE the subject matter)
    "docs/plan-remove-cozo-datalog-sql-migration.md",
    "docs/cycles/",
    "docs/analysis/",
    "docs/plans/",
    "docs/planning/",
    "docs/implementation/",
    "docs/reports/",
    "docs/testing/",
    "docs/validation/",
    "docs/prs/",
    "docs/superpowers/",
    "docs/pg-migration-kanban.md",
    // competitor market research — terms refer to third-party products
    "docs/competitive-analysis.md",
    "docs/competitive-analysis.html",
    ".docs/",
    "benchmark/results/",
    // checked-in built UI bundle (regenerates from ui-v2 sources)
    "src/embed/assets/",
    // append-only work logs
    "docs/prd-task-tracker.md",
    "docs/prd-task-tracker.json",
    "docs/interview-highlights.md",
    "HACKATHON.md",
    // local scratch / build artifacts / VCS internals
    ".leankg/",
    ".git/",
    "target/",
    "node_modules/",
];

const TEXT_EXTENSIONS: &[&str] = &[
    "rs", "ts", "tsx", "js", "jsx", "mjs", "cjs", "py", "sh", "zsh", "md", "json", "yaml", "yml",
    "toml", "html", "css", "scss", "sql", "txt", "xml", "rb", "go", "kt", "kts", "java", "php",
    "pl", "r", "lua", "ex", "exs",
];

fn is_allowlisted(rel: &str) -> bool {
    if ALLOWLIST_PREFIXES
        .iter()
        .any(|p| rel == p.trim_end_matches('/') || rel.starts_with(p))
    {
        return true;
    }
    // dated records (…-2026-07-14.md etc.) are historical audit evidence
    let name = Path::new(rel)
        .file_name()
        .map(|n| n.to_string_lossy().into_owned())
        .unwrap_or_default();
    date_marker(&name).is_some()
}

/// Returns the position of a `-YYYY-` style date marker in the file name.
fn date_marker(name: &str) -> Option<usize> {
    let bytes = name.as_bytes();
    let mut i = 0;
    while i + 6 <= bytes.len() {
        if (bytes[i] == b'-' || bytes[i] == b'_')
            && bytes[i + 1] == b'2'
            && bytes[i + 2] == b'0'
            && bytes[i + 3].is_ascii_digit()
            && bytes[i + 4].is_ascii_digit()
        {
            return Some(i);
        }
        i += 1;
    }
    None
}

fn collect_files(dir: &Path, base: &Path, out: &mut Vec<PathBuf>) {
    let Ok(entries) = fs::read_dir(dir) else {
        return;
    };
    for entry in entries.flatten() {
        let path = entry.path();
        let Ok(meta) = fs::metadata(&path) else {
            continue;
        };
        let rel = path
            .strip_prefix(base)
            .unwrap_or(&path)
            .to_string_lossy()
            .replace('\\', "/");
        if meta.is_dir() {
            if matches!(rel.as_str(), ".git" | "target" | "node_modules" | ".leankg") {
                continue;
            }
            collect_files(&path, base, out);
        } else if is_text_file(&rel)
            && !is_allowlisted(&rel)
            && rel != "tests/no_legacy_terms_test.rs"
        {
            out.push(path);
        }
    }
}

fn is_text_file(rel: &str) -> bool {
    Path::new(rel)
        .extension()
        .and_then(|e| e.to_str())
        .map(|e| TEXT_EXTENSIONS.contains(&e))
        // extensionless root files (Makefile, Dockerfile, entrypoint.sh, …)
        .unwrap_or_else(|| {
            !Path::new(rel)
                .file_name()
                .unwrap_or_default()
                .to_string_lossy()
                .contains('.')
        })
}

#[test]
fn no_legacy_engine_or_vendor_terms() {
    let manifest = env!("CARGO_MANIFEST_DIR");
    let base = Path::new(manifest);
    let mut files = Vec::new();
    collect_files(base, base, &mut files);
    assert!(
        files.len() > 500,
        "sanity: scanned only {} files — walker broken?",
        files.len()
    );

    let mut violations: Vec<String> = Vec::new();
    for path in &files {
        let Ok(content) = fs::read_to_string(path) else {
            continue;
        };
        let rel = path.strip_prefix(base).unwrap_or(path).to_string_lossy();
        for (line_no, line) in content.lines().enumerate() {
            for term in FORBIDDEN {
                if line.to_lowercase().contains(term) {
                    violations.push(format!(
                        "{}:{}: [{}] {}",
                        rel,
                        line_no + 1,
                        term,
                        line.trim().chars().take(140).collect::<String>()
                    ));
                }
            }
        }
    }

    assert!(
        violations.is_empty(),
        "found {} legacy-term violations (rewrite to PostgreSQL-present phrasing):\n{}",
        violations.len(),
        violations.join("\n")
    );
}
