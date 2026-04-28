use super::entropy::EntropyAnalyzer;
use super::litm::reorder_for_lcurve;
use super::modes::{parse_lines_spec, LinesRange, ReadMode};
use super::session_cache::SessionCache;
use super::signatures::extract_signatures;
use parking_lot::RwLock;
use std::fs;
use std::path::Path;
use std::sync::Arc;

pub struct FileReader {
    entropy_analyzer: EntropyAnalyzer,
    session_cache: Arc<RwLock<SessionCache>>,
}

impl FileReader {
    pub fn new(session_cache: Arc<RwLock<SessionCache>>) -> Self {
        Self {
            entropy_analyzer: EntropyAnalyzer::default(),
            session_cache,
        }
    }
}

impl Default for FileReader {
    fn default() -> Self {
        Self::new(Arc::new(RwLock::new(SessionCache::new())))
    }
}

impl FileReader {
    pub fn read(
        &mut self,
        path: &str,
        mode: ReadMode,
        lines_spec: Option<&str>,
        fresh: bool,
    ) -> Result<ReadResult, String> {
        let content = match fs::read_to_string(path) {
            Ok(c) => c,
            Err(e) => {
                if let std::io::ErrorKind::NotFound = e.kind() {
                    self.session_cache.write().invalidate(path);
                }
                return Err(format!("Failed to read file {}: {}", path, e));
            }
        };

        let total_tokens = super::estimate_tokens(&content);

        let (entry, is_hit, old_content, file_ref) = {
            let mut cache = self.session_cache.write();
            let (entry, hit, old) = cache.store(path, content.clone());
            let r = cache.get_file_ref(path);
            (entry, hit, old, r)
        };

        // Cache Return Pre-emption
        if is_hit && !fresh && mode != ReadMode::Diff && mode != ReadMode::Lines {
            let short_name = Path::new(path)
                .file_name()
                .unwrap_or_default()
                .to_string_lossy();
            let msg = format!(
                "{}={} cached {}t {}L\n[File unchanged in SessionCache. Use fresh=true to pull absolute text]",
                file_ref, short_name, entry.read_count, entry.line_count
            );
            return Ok(ReadResult {
                path: path.to_string(),
                mode,
                content: msg.clone(),
                tokens: super::estimate_tokens(&msg),
                total_tokens: entry.original_tokens,
                savings_percent: 99.0, // Cache hits are roughly 99% efficient
                total_lines: entry.line_count,
                output_lines: 2,
                is_cached: true,
                lines_included: None,
            });
        }

        let lines: Vec<&str> = content.lines().collect();
        let total_lines = lines.len();

        let result = match mode {
            ReadMode::Adaptive => {
                return Err("Adaptive mode should be resolved before calling read()".into());
            }
            ReadMode::Full => self.read_full(path, &content),
            ReadMode::Map => self.read_map(path, &content, &lines)?,
            ReadMode::Signatures => self.read_signatures(path, &content, &lines)?,
            ReadMode::Diff => {
                let diff_res = self.read_diff(path, &file_ref, &content, old_content.as_deref());
                // Override tokens tracking on read_diff
                return Ok(diff_res);
            }
            ReadMode::Aggressive => self.read_aggressive(&content, &lines),
            ReadMode::Entropy => self.read_entropy(&content, &lines),
            ReadMode::Lines => {
                let ranges = lines_spec.map(parse_lines_spec).unwrap_or_default();
                self.read_lines(&lines, &ranges)
            }
        };

        let tokens = self.estimate_tokens(&result.content);
        let savings_percent = if total_tokens > 0 && tokens <= total_tokens {
            (total_tokens - tokens) as f64 / total_tokens as f64 * 100.0
        } else {
            0.0
        };
        let output_lines = result.content.lines().count();

        Ok(ReadResult {
            path: path.to_string(),
            mode,
            content: result.content,
            tokens,
            total_tokens,
            savings_percent,
            total_lines,
            output_lines,
            is_cached: is_hit,
            lines_included: result.lines_included,
        })
    }

    fn read_full(&self, path: &str, content: &str) -> ReadResult {
        let ext = Path::new(path)
            .extension()
            .unwrap_or_default()
            .to_string_lossy();

        let mut final_content = content.to_string();

        let mut sym_map = super::symbol_map::SymbolMap::new(content);
        let idents = super::symbol_map::extract_identifiers(content, &ext);
        for ident in &idents {
            sym_map.register(ident);
        }

        if sym_map.len() >= 3 {
            let table = sym_map.format_table();
            let compressed = sym_map.apply(content);
            let orig_tokens = super::estimate_tokens(content);
            let new_tokens = super::estimate_tokens(&compressed) + super::estimate_tokens(&table);
            let net_savings = orig_tokens.saturating_sub(new_tokens);

            if orig_tokens > 0 && net_savings * 100 / orig_tokens >= 5 {
                final_content = format!("{}{}", compressed, table);
            }
        }

        let total_lines = content.lines().count();

        ReadResult {
            path: path.to_string(),
            mode: ReadMode::Full,
            content: final_content.clone(),
            tokens: 0,
            total_tokens: 0,
            savings_percent: 0.0,
            total_lines,
            output_lines: final_content.lines().count(),
            is_cached: false,
            lines_included: Some(total_lines),
        }
    }

    fn read_map(
        &mut self,
        path: &str,
        content: &str,
        lines: &[&str],
    ) -> Result<ReadResult, String> {
        let mut result_lines = Vec::new();
        let mut imports = Vec::new();
        let mut exports = Vec::new();

        // Scan basic imports matching
        for (i, line) in lines.iter().enumerate() {
            let trimmed = line.trim();
            if is_import_line(trimmed) {
                imports.push(format!("L{}: {}", i + 1, trimmed));
            }
            if is_export_line(trimmed) {
                exports.push(format!("L{}: {}", i + 1, trimmed));
            }
        }

        result_lines.push(format!(
            "# {} [{}L]",
            Path::new(path)
                .file_name()
                .unwrap_or_default()
                .to_string_lossy(),
            lines.len()
        ));
        result_lines.push(String::new());

        if !imports.is_empty() {
            result_lines.push(format!("deps: {}", imports.join(", ")));
        }

        if !exports.is_empty() {
            result_lines.push(format!("exports: {}", exports.join(", ")));
        }

        // Apply strict signature mapping rules instead of braces math
        let ext = Path::new(path)
            .extension()
            .unwrap_or_default()
            .to_string_lossy();
        let sigs = extract_signatures(content, &ext);

        result_lines.push(String::new());
        result_lines.push("API:".to_string());
        for sig in &sigs {
            result_lines.push(format!("  {}", sig.to_compact()));
        }

        // Output formatting: We run L-curve optimization purely on exports payload when required
        let map_output = result_lines.join("\n");
        let map_optimal = reorder_for_lcurve(&map_output, &[]);

        Ok(ReadResult {
            path: path.to_string(),
            mode: ReadMode::Map,
            content: map_optimal,
            tokens: 0,
            total_tokens: 0,
            savings_percent: 0.0,
            total_lines: lines.len(),
            output_lines: result_lines.len(),
            is_cached: false,
            lines_included: Some(sigs.len()),
        })
    }

    fn read_signatures(
        &mut self,
        path: &str,
        content: &str,
        lines: &[&str],
    ) -> Result<ReadResult, String> {
        let ext = Path::new(path)
            .extension()
            .unwrap_or_default()
            .to_string_lossy();
        let sigs = extract_signatures(content, &ext);

        let mut out = Vec::new();
        out.push(format!(
            "{} [{}L]\nsignatures: {}\n",
            Path::new(path)
                .file_name()
                .unwrap_or_default()
                .to_string_lossy(),
            lines.len(),
            sigs.len()
        ));

        for sig in &sigs {
            out.push(sig.to_tdd());
        }

        Ok(ReadResult {
            path: path.to_string(),
            mode: ReadMode::Signatures,
            content: out.join("\n"),
            tokens: 0,
            total_tokens: 0,
            savings_percent: 0.0,
            total_lines: lines.len(),
            output_lines: out.len(),
            is_cached: false,
            lines_included: Some(sigs.len()),
        })
    }

    fn read_diff(
        &self,
        path: &str,
        file_ref: &str,
        current_content: &str,
        old_content: Option<&str>,
    ) -> ReadResult {
        let short_name = Path::new(path)
            .file_name()
            .unwrap_or_default()
            .to_string_lossy();

        let lines: Vec<&str> = current_content.lines().collect();
        let total_lines = lines.len();

        let old = match old_content {
            Some(o) => o,
            None => {
                // If it's the very first time being read, we can't emit a diff. We fall back to outputting the full file.
                return ReadResult {
                    path: path.to_string(),
                    mode: ReadMode::Diff,
                    content: format!(
                        "{}={} [New in Cache => Showing Full {}L]\n{}",
                        file_ref, short_name, total_lines, current_content
                    ),
                    tokens: super::estimate_tokens(current_content),
                    total_tokens: super::estimate_tokens(current_content),
                    savings_percent: 0.0,
                    total_lines,
                    output_lines: total_lines,
                    is_cached: false,
                    lines_included: Some(total_lines),
                };
            }
        };

        if old == current_content {
            let msg = format!("{}={} [No changes since last read]", file_ref, short_name);
            return ReadResult {
                path: path.to_string(),
                mode: ReadMode::Diff,
                content: msg.clone(),
                tokens: super::estimate_tokens(&msg),
                total_tokens: super::estimate_tokens(current_content),
                savings_percent: 99.0,
                total_lines,
                output_lines: 1,
                is_cached: true,
                lines_included: None,
            };
        }

        let diff = similar::TextDiff::from_lines(old, current_content);
        let unified = diff.unified_diff().context_radius(3).to_string();
        let msg = format!(
            "{}={} [auto-delta] ∆{}L\n{}",
            file_ref, short_name, total_lines, unified
        );

        let output_lines = unified.lines().count();
        let tokens = super::estimate_tokens(&msg);
        let total_tokens = super::estimate_tokens(current_content);
        let savings_percent = if total_tokens > 0 && tokens <= total_tokens {
            (total_tokens - tokens) as f64 / total_tokens as f64 * 100.0
        } else {
            0.0
        };

        ReadResult {
            path: path.to_string(),
            mode: ReadMode::Diff,
            content: msg,
            tokens,
            total_tokens,
            savings_percent,
            total_lines,
            output_lines,
            is_cached: false,
            lines_included: None,
        }
    }

    fn read_aggressive(&self, _content: &str, lines: &[&str]) -> ReadResult {
        let filtered: Vec<String> = lines
            .iter()
            .filter(|line| !is_noise_line(line.trim()))
            .map(|line| remove_syntax_noise(line))
            .collect();

        ReadResult {
            path: String::new(),
            mode: ReadMode::Aggressive,
            content: filtered.join("\n"),
            tokens: 0,
            total_tokens: 0,
            savings_percent: 0.0,
            total_lines: lines.len(),
            output_lines: filtered.len(),
            is_cached: false,
            lines_included: Some(filtered.len()),
        }
    }

    fn read_entropy(&self, _content: &str, lines: &[&str]) -> ReadResult {
        let threshold = 0.3;
        let filtered = self
            .entropy_analyzer
            .filter_low_entropy_lines(lines, threshold);

        ReadResult {
            path: String::new(),
            mode: ReadMode::Entropy,
            content: filtered.join("\n"),
            tokens: 0,
            total_tokens: 0,
            savings_percent: 0.0,
            total_lines: lines.len(),
            output_lines: filtered.len(),
            is_cached: false,
            lines_included: Some(filtered.len()),
        }
    }

    fn read_lines(&self, lines: &[&str], ranges: &[LinesRange]) -> ReadResult {
        let mut selected = Vec::new();

        for range in ranges {
            let start = range.start.saturating_sub(1);
            let end = range.end.min(lines.len());
            for line in lines.iter().take(end).skip(start) {
                selected.push(*line);
            }
        }

        ReadResult {
            path: String::new(),
            mode: ReadMode::Lines,
            content: selected.join("\n"),
            tokens: 0,
            total_tokens: 0,
            savings_percent: 0.0,
            total_lines: lines.len(),
            output_lines: selected.len(),
            is_cached: false,
            lines_included: Some(selected.len()),
        }
    }

    fn estimate_tokens(&self, text: &str) -> usize {
        text.len() / 4
    }
}

pub struct ReadResult {
    pub path: String,
    pub mode: ReadMode,
    pub content: String,
    pub tokens: usize,
    pub total_tokens: usize,
    pub savings_percent: f64,
    pub total_lines: usize,
    pub output_lines: usize,
    pub is_cached: bool,
    pub lines_included: Option<usize>,
}

fn is_import_line(line: &str) -> bool {
    let imports = [
        "import ",
        "use ",
        "require(",
        "from ",
        "#include",
        "use crate::",
        "use self::",
    ];
    imports.iter().any(|p| line.starts_with(p))
}

fn is_export_line(line: &str) -> bool {
    line.starts_with("pub ") || line.starts_with("export ") || line.starts_with("module.exports")
}

fn is_noise_line(line: &str) -> bool {
    let noise = [
        "// ", "/* ", "*/", "# ", "##", "---", "***", "<!--", "-->", "```", "\"\"\"",
    ];
    noise.iter().any(|p| line.starts_with(p)) || line.is_empty()
}

fn remove_syntax_noise(line: &str) -> String {
    let mut result = String::new();
    let mut in_string = false;
    let chars: Vec<char> = line.chars().collect();

    let mut i = 0;
    while i < chars.len() {
        let c = chars[i];

        if c == '"' || c == '\'' {
            in_string = !in_string;
            result.push(c);
        } else if in_string {
            result.push(c);
        } else if c == '/' && i + 1 < chars.len() && chars[i + 1] == '/' {
            break;
        } else if c == '#' && i + 1 < chars.len() && chars[i + 1].is_numeric() {
            i += 1;
            while i < chars.len() && chars[i].is_numeric() {
                i += 1;
            }
            continue;
        } else {
            result.push(c);
        }
        i += 1;
    }

    result.trim().to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_is_import_line() {
        assert!(is_import_line("import foo from 'bar'"));
        assert!(is_import_line("use std::collections"));
        assert!(is_import_line("require('./module')"));
        assert!(!is_import_line("let x = 1"));
    }

    #[test]
    fn test_read_signatures() {
        let mut reader = FileReader::default();
        let content = "pub fn execute() {}\nfn helper() {}\npub struct Point { x: i32 }";
        let lines: Vec<&str> = content.lines().collect();
        let result = reader.read_signatures("test.rs", content, &lines).unwrap();

        assert_eq!(result.mode, ReadMode::Signatures);
        assert_eq!(result.total_lines, 3);
        assert_eq!(result.lines_included, Some(3));
        assert!(result.content.contains("λ+execute()"));
        assert!(result.content.contains("§+Point"));
    }

    #[test]
    fn test_read_map() {
        let mut reader = FileReader::default();
        let content = "use std::io;\npub fn main() {}";
        let lines: Vec<&str> = content.lines().collect();
        let result = reader.read_map("test.rs", content, &lines).unwrap();

        assert_eq!(result.mode, ReadMode::Map);
        assert!(result.content.contains("deps: L1: use std::io;"));
        assert!(result.content.contains("fn main()"));
    }

    #[test]
    fn test_read_entropy_filtering() {
        let reader = FileReader::default();
        let content = "let x = 1;\n\n// a very low entropy string\naaaaaaaaaaaaa\npub fn run() {}";
        let lines: Vec<&str> = content.lines().collect();
        let result = reader.read_entropy(content, &lines);

        assert_eq!(result.mode, ReadMode::Entropy);
        assert!(result.content.contains("run()"));
        // 'aaaaaaaaaaaaa' should be filtered out by entropy analyzer
        assert!(!result.content.contains("aaaaaaaaaaaaa"));
    }
}
