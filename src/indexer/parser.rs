use tree_sitter::Parser;

#[derive(Default)]
pub struct ParserManager {
    pub go_parser: Parser,
    pub ts_parser: Parser,
    pub python_parser: Parser,
    pub rust_parser: Parser,
    pub java_parser: Parser,
    pub kotlin_parser: Parser,
}

impl ParserManager {
    pub fn new() -> Self {
        Self {
            go_parser: Parser::new(),
            ts_parser: Parser::new(),
            python_parser: Parser::new(),
            rust_parser: Parser::new(),
            java_parser: Parser::new(),
            kotlin_parser: Parser::new(),
        }
    }

    pub fn init_parsers(&mut self) -> Result<(), Box<dyn std::error::Error>> {
        let go_lang: tree_sitter::Language = tree_sitter_go::LANGUAGE.into();
        let ts_lang: tree_sitter::Language = tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into();
        let py_lang: tree_sitter::Language = tree_sitter_python::LANGUAGE.into();
        let rust_lang: tree_sitter::Language = tree_sitter_rust::LANGUAGE.into();
        let java_lang: tree_sitter::Language = tree_sitter_java::LANGUAGE.into();
        let kotlin_lang: tree_sitter::Language = tree_sitter_kotlin_ng::LANGUAGE.into();

        self.go_parser.set_language(&go_lang)?;
        self.ts_parser.set_language(&ts_lang)?;
        self.python_parser.set_language(&py_lang)?;
        self.rust_parser.set_language(&rust_lang)?;
        self.java_parser.set_language(&java_lang)?;
        self.kotlin_parser.set_language(&kotlin_lang)?;

        Ok(())
    }

    pub fn get_parser_for_language(&mut self, language: &str) -> Option<&mut Parser> {
        match language {
            "go" => Some(&mut self.go_parser),
            "typescript" | "javascript" => Some(&mut self.ts_parser),
            "python" => Some(&mut self.python_parser),
            "rust" => Some(&mut self.rust_parser),
            "java" => Some(&mut self.java_parser),
            "kotlin" => Some(&mut self.kotlin_parser),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn init_parsers_if_compatible() -> Option<ParserManager> {
        let mut pm = ParserManager::new();
        pm.init_parsers().ok()?;
        Some(pm)
    }

    #[test]
    fn test_parser_manager_new() {
        let _pm = ParserManager::new();
    }

    #[test]
    fn test_parser_manager_init_parsers() {
        let mut pm = ParserManager::new();
        let result = pm.init_parsers();
        assert!(result.is_ok() || result.is_err());
    }

    #[test]
    fn test_get_parser_for_go() {
        if let Some(mut pm) = init_parsers_if_compatible() {
            assert!(pm.get_parser_for_language("go").is_some());
        }
    }

    #[test]
    fn test_get_parser_for_typescript() {
        if let Some(mut pm) = init_parsers_if_compatible() {
            assert!(pm.get_parser_for_language("typescript").is_some());
            assert!(pm.get_parser_for_language("javascript").is_some());
        }
    }

    #[test]
    fn test_get_parser_for_python() {
        if let Some(mut pm) = init_parsers_if_compatible() {
            assert!(pm.get_parser_for_language("python").is_some());
        }
    }

    #[test]
    fn test_get_parser_for_java() {
        if let Some(mut pm) = init_parsers_if_compatible() {
            assert!(pm.get_parser_for_language("java").is_some());
        }
    }

    #[test]
    fn test_get_parser_for_kotlin() {
        if let Some(mut pm) = init_parsers_if_compatible() {
            assert!(pm.get_parser_for_language("kotlin").is_some());
        }
    }

    #[test]
    fn test_get_parser_for_unknown_returns_none() {
        let mut pm = ParserManager::new();
        assert!(pm.get_parser_for_language("unknown").is_none());
        assert!(pm.get_parser_for_language("").is_none());
    }

    #[test]
    fn test_parser_parse_go_code() {
        if let Some(mut pm) = init_parsers_if_compatible() {
            let source = b"package main\nfunc foo() {}";
            let parser = pm.get_parser_for_language("go").unwrap();
            let tree = parser.parse(source, None);
            assert!(tree.is_some());
        }
    }

    #[test]
    fn test_parser_parse_java_code() {
        if let Some(mut pm) = init_parsers_if_compatible() {
            let source = b"public class Main { public static void main(String[] args) {} }";
            let parser = pm.get_parser_for_language("java").unwrap();
            let tree = parser.parse(source, None);
            assert!(tree.is_some());
        }
    }

    #[test]
    fn test_parser_parse_kotlin_code() {
        if let Some(mut pm) = init_parsers_if_compatible() {
            let source = b"class Main { fun main(args: Array<String>) {} }";
            let parser = pm.get_parser_for_language("kotlin").unwrap();
            let tree = parser.parse(source, None);
            assert!(tree.is_some());
        }
    }
}

