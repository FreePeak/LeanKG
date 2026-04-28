use flate2::write::GzEncoder;
use flate2::Compression;
use std::collections::HashMap;
use std::io::Write;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CompressibilityClass {
    High,
    Medium,
    Low,
}

pub struct EntropyAnalyzer {
    _jaccard_threshold: f64,
}

impl Default for EntropyAnalyzer {
    fn default() -> Self {
        Self::new(0.7)
    }
}

impl EntropyAnalyzer {
    pub fn new(jaccard_threshold: f64) -> Self {
        Self {
            _jaccard_threshold: jaccard_threshold,
        }
    }

    pub fn shannon_entropy(&self, text: &str) -> f64 {
        if text.is_empty() {
            return 0.0;
        }

        let mut char_counts: HashMap<char, usize> = HashMap::new();
        for c in text.chars() {
            *char_counts.entry(c).or_insert(0) += 1;
        }

        let len = text.len() as f64;
        let mut entropy = 0.0;

        for count in char_counts.values() {
            let p = *count as f64 / len;
            if p > 0.0 {
                entropy -= p * p.log2();
            }
        }

        entropy
    }

    pub fn normalized_entropy(&self, text: &str) -> f64 {
        let entropy = self.shannon_entropy(text);
        let len = text.len();
        if len <= 1 {
            return entropy;
        }
        let max_entropy = (len as f64).log2();
        if max_entropy > 0.0 {
            entropy / max_entropy
        } else {
            0.0
        }
    }

    pub fn kolmogorov_proxy(text: &str) -> usize {
        if text.is_empty() {
            return 0;
        }
        let mut e = GzEncoder::new(Vec::new(), Compression::default());
        let _ = e.write_all(text.as_bytes());
        match e.finish() {
            Ok(compressed) => compressed.len(),
            Err(_) => text.len(), // Fallback
        }
    }

    pub fn compressibility_class(text: &str) -> CompressibilityClass {
        let bytes_len = text.len();
        if bytes_len == 0 {
            return CompressibilityClass::Low;
        }

        let k_size = Self::kolmogorov_proxy(text);
        let ratio = k_size as f64 / bytes_len as f64;

        if ratio < 0.3 {
            CompressibilityClass::High
        } else if ratio < 0.6 {
            CompressibilityClass::Medium
        } else {
            CompressibilityClass::Low
        }
    }

    pub fn line_entropies(&self, lines: &[&str]) -> Vec<f64> {
        lines
            .iter()
            .map(|line| self.normalized_entropy(line))
            .collect()
    }

    pub fn filter_low_entropy_lines<'a>(&self, lines: &[&'a str], threshold: f64) -> Vec<&'a str> {
        let mut filtered = Vec::new();
        let mut last_was_empty = false;

        // Fast paths for very uncompressible files
        let full_text = lines.join("\n");
        let class = Self::compressibility_class(&full_text);

        let dynamic_threshold = match class {
            CompressibilityClass::High => threshold * 1.5, // Aggressive prune if highly repetitive
            CompressibilityClass::Medium => threshold,
            CompressibilityClass::Low => threshold * 0.5, // Be gentle if it's already dense
        };

        for &line in lines {
            let t = line.trim();
            if t.is_empty() {
                if !last_was_empty {
                    filtered.push(line);
                    last_was_empty = true;
                }
                continue;
            }

            last_was_empty = false;

            // Always keep structural bounds
            if t.starts_with("fn ")
                || t.starts_with("class ")
                || t.starts_with("pub ")
                || t.ends_with('{')
                || t == "}"
            {
                filtered.push(line);
                continue;
            }

            let e = self.normalized_entropy(line);
            if e >= dynamic_threshold {
                filtered.push(line);
            }
        }

        filtered
    }
}

pub fn jaccard_similarity(set1: &[&str], set2: &[&str]) -> f64 {
    if set1.is_empty() && set2.is_empty() {
        return 1.0;
    }
    if set1.is_empty() || set2.is_empty() {
        return 0.0;
    }

    let set1: std::collections::HashSet<_> = set1.iter().collect();
    let set2: std::collections::HashSet<_> = set2.iter().collect();

    let intersection = set1.intersection(&set2).count();
    let union = set1.union(&set2).count();

    if union == 0 {
        return 0.0;
    }

    intersection as f64 / union as f64
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_kolmogorov() {
        let text = "a".repeat(1000);
        let k = EntropyAnalyzer::kolmogorov_proxy(&text);
        assert!(k < 100);

        assert_eq!(
            EntropyAnalyzer::compressibility_class(&text),
            CompressibilityClass::High
        );
    }
}
