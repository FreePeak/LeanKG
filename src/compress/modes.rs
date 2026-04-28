use serde::{Deserialize, Serialize};
use std::fmt;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
#[derive(Default)]
pub enum ReadMode {
    #[default]
    Adaptive,
    Full,
    Map,
    Signatures,
    Diff,
    Aggressive,
    Entropy,
    Lines,
}

impl ReadMode {
    #[allow(clippy::should_implement_trait)]
    pub fn from_str(s: &str) -> Option<Self> {
        match s.to_lowercase().as_str() {
            "adaptive" => Some(ReadMode::Adaptive),
            "full" => Some(ReadMode::Full),
            "map" => Some(ReadMode::Map),
            "signatures" => Some(ReadMode::Signatures),
            "diff" => Some(ReadMode::Diff),
            "aggressive" => Some(ReadMode::Aggressive),
            "entropy" => Some(ReadMode::Entropy),
            "lines" => Some(ReadMode::Lines),
            _ => None,
        }
    }

    pub fn description(&self) -> &'static str {
        match self {
            ReadMode::Adaptive => "Auto-select best mode based on file type/size/cache",
            ReadMode::Full => "Complete file content (cached re-reads ≈ 13 tokens)",
            ReadMode::Map => "Dependencies + exports + API signatures (~85-95% savings)",
            ReadMode::Signatures => "Function/class signatures only (~90-95% savings)",
            ReadMode::Diff => "Only changed hunks via Myers diff",
            ReadMode::Aggressive => "Syntax-stripped, removes boilerplate (~60-70% savings)",
            ReadMode::Entropy => {
                "Shannon entropy filtered for repetitive patterns (~70-80% savings)"
            }
            ReadMode::Lines => "Specific line ranges (proportional savings)",
        }
    }

    pub fn estimated_savings(&self) -> &'static str {
        match self {
            ReadMode::Adaptive => "~75-95% (auto-selected)",
            ReadMode::Full => "~0% cached",
            ReadMode::Map => "~85-95%",
            ReadMode::Signatures => "~90-95%",
            ReadMode::Diff => "proportional",
            ReadMode::Aggressive => "~60-70%",
            ReadMode::Entropy => "~70-80%",
            ReadMode::Lines => "proportional",
        }
    }

    pub fn select_adaptive(file_path: &str, _file_size: usize, lines: usize) -> Self {
        let extension = std::path::Path::new(file_path)
            .extension()
            .and_then(|e| e.to_str())
            .unwrap_or("");

        let code_extensions = ["rs", "go", "ts", "js", "py", "java", "c", "cpp", "h"];
        let is_code = code_extensions.contains(&extension);

        if !is_code {
            return ReadMode::Full;
        }

        if lines > 500 {
            ReadMode::Map
        } else if lines > 200 {
            ReadMode::Signatures
        } else {
            ReadMode::Map
        }
    }
}

impl fmt::Display for ReadMode {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let s = match self {
            ReadMode::Adaptive => "adaptive",
            ReadMode::Full => "full",
            ReadMode::Map => "map",
            ReadMode::Signatures => "signatures",
            ReadMode::Diff => "diff",
            ReadMode::Aggressive => "aggressive",
            ReadMode::Entropy => "entropy",
            ReadMode::Lines => "lines",
        };
        write!(f, "{}", s)
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct LinesRange {
    pub start: usize,
    pub end: usize,
}

impl LinesRange {
    pub fn parse(s: &str) -> Option<Self> {
        let parts: Vec<&str> = s.split('-').collect();
        if parts.len() != 2 {
            return None;
        }
        let start = parts[0].parse().ok()?;
        let end = parts[1].parse().ok()?;
        if start > end {
            return None;
        }
        Some(LinesRange { start, end })
    }
}

pub fn parse_lines_spec(spec: &str) -> Vec<LinesRange> {
    let mut ranges = Vec::new();
    for part in spec.split(',') {
        if let Some(range) = LinesRange::parse(part.trim()) {
            ranges.push(range);
        }
    }
    ranges
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_read_mode_from_str() {
        assert_eq!(ReadMode::from_str("full"), Some(ReadMode::Full));
        assert_eq!(ReadMode::from_str("map"), Some(ReadMode::Map));
        assert_eq!(ReadMode::from_str("SIGNATURES"), Some(ReadMode::Signatures));
        assert_eq!(ReadMode::from_str("invalid"), None);
    }

    #[test]
    fn test_lines_range_parse() {
        assert_eq!(
            LinesRange::parse("10-20"),
            Some(LinesRange { start: 10, end: 20 })
        );
        assert_eq!(
            LinesRange::parse("5-5"),
            Some(LinesRange { start: 5, end: 5 })
        );
        assert_eq!(LinesRange::parse("20-10"), None);
        assert_eq!(LinesRange::parse("invalid"), None);
    }

    #[test]
    fn test_parse_lines_spec() {
        let ranges = parse_lines_spec("10-20,30-40,50-60");
        assert_eq!(ranges.len(), 3);
        assert_eq!(ranges[0].start, 10);
        assert_eq!(ranges[2].end, 60);
    }
}
