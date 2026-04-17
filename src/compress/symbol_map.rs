use super::estimate_tokens;
use std::collections::{HashMap, HashSet};

const MIN_IDENT_LENGTH: usize = 6;

pub struct AnchorGenerator {
    index: usize,
}

impl Default for AnchorGenerator {
    fn default() -> Self {
        Self::new()
    }
}

impl AnchorGenerator {
    pub fn new() -> Self {
        Self { index: 0 }
    }
}

impl Iterator for AnchorGenerator {
    type Item = String;

    fn next(&mut self) -> Option<Self::Item> {
        let mut num = self.index;
        self.index += 1;

        let mut result = String::new();
        loop {
            let rem = num % 52;
            let chr = if rem < 26 {
                (b'A' + rem as u8) as char
            } else {
                (b'a' + (rem - 26) as u8) as char
            };
            result.insert(0, chr);

            if num < 52 {
                break;
            }
            num = (num / 52) - 1;
        }
        Some(result)
    }
}

#[derive(Debug, Clone)]
pub struct SymbolMap {
    forward: HashMap<String, String>,
    existing_words: HashSet<String>,
}

impl SymbolMap {
    pub fn new(content: &str) -> Self {
        let ident_re = regex::Regex::new(r"\b[a-zA-Z_][a-zA-Z0-9_]*\b").unwrap();
        let mut existing_words = HashSet::new();
        for mat in ident_re.find_iter(content) {
            existing_words.insert(mat.as_str().to_string());
        }

        Self {
            forward: HashMap::new(),
            existing_words,
        }
    }

    pub fn register(&mut self, identifier: &str) -> Option<String> {
        if identifier.len() < MIN_IDENT_LENGTH {
            return None;
        }

        if let Some(existing) = self.forward.get(identifier) {
            return Some(existing.clone());
        }

        let mut generator = AnchorGenerator::new();
        loop {
            let short_id = generator.next().unwrap();
            if !self.existing_words.contains(&short_id) {
                self.forward
                    .insert(identifier.to_string(), short_id.clone());
                self.existing_words.insert(short_id.clone());
                return Some(short_id);
            }
        }
    }

    pub fn apply(&self, text: &str) -> String {
        if self.forward.is_empty() {
            return text.to_string();
        }

        let mut sorted: Vec<(&String, &String)> = self.forward.iter().collect();
        sorted.sort_by_key(|b| std::cmp::Reverse(b.0.len()));

        let mut result = text.to_string();
        for (long, short) in &sorted {
            result = result.replace(long.as_str(), short.as_str());
        }
        result
    }

    pub fn format_table(&self) -> String {
        if self.forward.is_empty() {
            return String::new();
        }

        let mut entries: Vec<(&String, &String)> = self.forward.iter().collect();
        entries.sort_by(|(_, a), (_, b)| a.len().cmp(&b.len()).then(a.cmp(b)));

        let mut table = String::from("\n[MAP]:");
        for (long, short) in &entries {
            table.push_str(&format!("\n  {}={}", short, long));
        }
        table
    }

    pub fn len(&self) -> usize {
        self.forward.len()
    }

    pub fn is_empty(&self) -> bool {
        self.forward.is_empty()
    }
}

const MAP_ENTRY_OVERHEAD: usize = 2; // "  A=identifier\n" ~= tokens(ident) + 1 + 2

pub fn should_register(identifier: &str, occurrences: usize) -> bool {
    if identifier.len() < MIN_IDENT_LENGTH {
        return false;
    }
    let ident_tokens = estimate_tokens(identifier);
    let short_tokens = 1; // Pure alphabets are 1 token

    let token_saving_per_use = ident_tokens.saturating_sub(short_tokens);
    if token_saving_per_use == 0 {
        return false;
    }

    let total_savings = occurrences * token_saving_per_use;
    let entry_cost = ident_tokens + short_tokens + MAP_ENTRY_OVERHEAD;

    total_savings > entry_cost
}

fn is_keyword(word: &str, ext: &str) -> bool {
    match ext {
        "rs" => matches!(
            word,
            "continue" | "default" | "return" | "struct" | "unsafe" | "where" | "match" | "impl"
        ),
        "ts" | "tsx" | "js" | "jsx" => matches!(
            word,
            "constructor" | "arguments" | "undefined" | "prototype" | "instanceof" | "function"
        ),
        "py" => matches!(
            word,
            "continue" | "lambda" | "return" | "import" | "class" | "def"
        ),
        "go" => matches!(
            word,
            "continue" | "default" | "return" | "struct" | "interface" | "func"
        ),
        _ => false,
    }
}

pub fn extract_identifiers(content: &str, ext: &str) -> Vec<String> {
    let ident_re = regex::Regex::new(r"\b[a-zA-Z_][a-zA-Z0-9_]*\b").unwrap();

    let mut seen = HashMap::new();
    for mat in ident_re.find_iter(content) {
        let word = mat.as_str();
        if word.len() >= MIN_IDENT_LENGTH && !is_keyword(word, ext) {
            *seen.entry(word.to_string()).or_insert(0usize) += 1;
        }
    }

    let mut idents: Vec<(String, usize)> = seen
        .into_iter()
        .filter(|(ident, count)| should_register(ident, *count))
        .collect();

    idents.sort_by(|a, b| {
        let savings_a = a.0.len() * a.1;
        let savings_b = b.0.len() * b.1;
        savings_b.cmp(&savings_a)
    });

    idents.into_iter().map(|(s, _)| s).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_anchor_generator() {
        let mut gen = AnchorGenerator::new();
        assert_eq!(gen.next().unwrap(), "A");
        assert_eq!(gen.next().unwrap(), "B");

        let mut gen2 = AnchorGenerator::new();
        // Skip first 26 (A-Z)
        for _ in 0..26 {
            gen2.next();
        }
        assert_eq!(gen2.next().unwrap(), "a");
        // Skip next 25 (b-z)
        for _ in 0..25 {
            gen2.next();
        }
        assert_eq!(gen2.next().unwrap(), "AA");
        assert_eq!(gen2.next().unwrap(), "AB");
    }

    #[test]
    fn test_symbol_map_apply() {
        let content = "fn my_long_function_name() {}";
        let mut map = SymbolMap::new(content);
        map.register("my_long_function_name");

        let result = map.apply(content);
        // Assuming 'A' is not used in "fn" and "my_long_function_name"
        // Wait, "fn" is extracted, "my_long_function_name" is extracted. "A" is perfectly free!
        assert_eq!(result, "fn A() {}");
        assert!(map.format_table().contains("A=my_long_function_name"));
    }

    #[test]
    fn test_collision_prevention() {
        // If A, B, C are used, it should pick D.
        let content = "let A = 0; let B = 1; let C = 2; let custom_long_var = 5;";
        let mut map = SymbolMap::new(content);
        let assigned = map.register("custom_long_var").unwrap();
        assert_eq!(assigned, "D"); // A, B, C skip.
    }

    #[test]
    fn test_keyword_exclusion() {
        assert!(is_keyword("continue", "rs"));
        assert!(is_keyword("interface", "go"));
        assert!(!is_keyword("my_var", "rs"));

        let content = "fn continue() {} fn my_custom_ident() {}";
        let idents = extract_identifiers(content, "rs");
        assert!(!idents.contains(&"continue".to_string()));
    }
}
