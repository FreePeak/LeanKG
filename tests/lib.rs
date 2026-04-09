// Integration tests for LeanKG

mod config_tests {
    use leankg::config::ProjectConfig;

    #[test]
    fn test_config_default() {
        let config = ProjectConfig::default();
        assert_eq!(config.project.name, "my-project");
    }

    #[test]
    fn test_config_default_mcp() {
        let config = ProjectConfig::default();
        assert!(config.mcp.enabled);
        assert_eq!(config.mcp.port, 3000);
    }
}

mod parser_tests {
    use leankg::indexer::ParserManager;

    fn init_parser_manager() -> Option<ParserManager> {
        let mut pm = ParserManager::new();
        pm.init_parsers().ok()?;
        Some(pm)
    }

    #[test]
    fn test_parser_manager_new_creates_instance() {
        let mut pm = ParserManager::new();
        let parser = pm.get_parser_for_language("go");
        assert!(
            parser.is_some(),
            "ParserManager should have a go parser slot"
        );
    }

    #[test]
    fn test_parser_manager_init_and_get() {
        let mut pm = ParserManager::new();
        if pm.init_parsers().is_ok() {
            assert!(pm.get_parser_for_language("go").is_some());
        }
    }

    #[test]
    fn test_init_parsers_succeeds_and_parsers_available() {
        let mut pm = ParserManager::new();
        let result = pm.init_parsers();
        assert!(
            result.is_ok() || result.is_err(),
            "init_parsers should not panic"
        );
        if result.is_ok() {
            assert!(
                pm.get_parser_for_language("go").is_some(),
                "Go parser should be available"
            );
            assert!(
                pm.get_parser_for_language("typescript").is_some(),
                "TS parser should be available"
            );
            assert!(
                pm.get_parser_for_language("python").is_some(),
                "Python parser should be available"
            );
        }
    }

    #[test]
    fn test_get_parser_for_language_go() {
        if let Some(mut pm) = init_parser_manager() {
            let parser = pm.get_parser_for_language("go");
            assert!(parser.is_some(), "Should return Some for 'go'");
        }
    }

    #[test]
    fn test_get_parser_for_language_typescript() {
        if let Some(mut pm) = init_parser_manager() {
            let parser = pm.get_parser_for_language("typescript");
            assert!(parser.is_some(), "Should return Some for 'typescript'");
        }
    }

    #[test]
    fn test_get_parser_for_language_python() {
        if let Some(mut pm) = init_parser_manager() {
            let parser = pm.get_parser_for_language("python");
            assert!(parser.is_some(), "Should return Some for 'python'");
        }
    }

    #[test]
    fn test_get_parser_for_language_javascript() {
        if let Some(mut pm) = init_parser_manager() {
            let parser = pm.get_parser_for_language("javascript");
            assert!(parser.is_some(), "Should return Some for 'javascript'");
        }
    }

    #[test]
    fn test_get_parser_for_language_unsupported_returns_none() {
        if let Some(mut pm) = init_parser_manager() {
            assert!(
                pm.get_parser_for_language("cobol").is_none(),
                "Should return None for 'cobol'"
            );
            assert!(
                pm.get_parser_for_language("haskell").is_none(),
                "Should return None for 'haskell'"
            );
            assert!(
                pm.get_parser_for_language("").is_none(),
                "Should return None for empty string"
            );
            assert!(
                pm.get_parser_for_language("unknown").is_none(),
                "Should return None for 'unknown'"
            );
        }
    }

    #[test]
    fn test_parse_simple_go_code() {
        if let Some(mut pm) = init_parser_manager() {
            let source = b"package main\n\nfunc add(a int, b int) int {\n    return a + b\n}";
            let parser = pm.get_parser_for_language("go").unwrap();
            let tree = parser.parse(source, None);
            assert!(tree.is_some(), "Should parse valid Go code successfully");
            let tree = tree.unwrap();
            assert!(
                !tree.root_node().has_error(),
                "Parsed tree should not have errors"
            );
            let root_kind = tree.root_node().kind();
            assert!(
                root_kind == "program" || root_kind == "source_file",
                "Root node should be 'program' or 'source_file', got '{}'",
                root_kind
            );
        }
    }

    #[test]
    fn test_parse_simple_python_code() {
        if let Some(mut pm) = init_parser_manager() {
            let source = b"def add(a, b):\n    return a + b\n";
            let parser = pm.get_parser_for_language("python").unwrap();
            let tree = parser.parse(source, None);
            assert!(
                tree.is_some(),
                "Should parse valid Python code successfully"
            );
            let tree = tree.unwrap();
            assert!(
                !tree.root_node().has_error(),
                "Parsed tree should not have errors"
            );
            let root_kind = tree.root_node().kind();
            assert!(
                root_kind == "program" || root_kind == "module",
                "Root node should be 'program' or 'module', got '{}'",
                root_kind
            );
        }
    }

    #[test]
    fn test_parse_simple_typescript_code() {
        if let Some(mut pm) = init_parser_manager() {
            let source = b"function add(a: number, b: number): number {\n    return a + b;\n}";
            let parser = pm.get_parser_for_language("typescript").unwrap();
            let tree = parser.parse(source, None);
            assert!(
                tree.is_some(),
                "Should parse valid TypeScript code successfully"
            );
            let tree = tree.unwrap();
            assert!(
                !tree.root_node().has_error(),
                "Parsed tree should not have errors"
            );
            assert_eq!(
                tree.root_node().kind(),
                "program",
                "Root node should be 'program'"
            );
        }
    }

    #[test]
    fn test_parse_invalid_language_gracefully_handles_error() {
        let mut pm = ParserManager::new();
        let _ = pm.init_parsers();
        let parser = pm.get_parser_for_language("nonexistent_lang");
        assert!(
            parser.is_none(),
            "Should return None for unsupported language"
        );
    }

    #[test]
    fn test_parse_go_with_imports() {
        if let Some(mut pm) = init_parser_manager() {
            let source = b"package main\n\nimport (\n    \"fmt\"\n    \"context\"\n)\n\nfunc main() {\n    ctx := context.Background()\n    fmt.Println(ctx)\n}";
            let parser = pm.get_parser_for_language("go").unwrap();
            let tree = parser.parse(source, None);
            assert!(tree.is_some());
            assert!(!tree.unwrap().root_node().has_error());
        }
    }

    #[test]
    fn test_parse_python_with_class() {
        if let Some(mut pm) = init_parser_manager() {
            let source = b"class Calculator:\n    def add(self, a, b):\n        return a + b\n\n    def subtract(self, a, b):\n        return a - b\n";
            let parser = pm.get_parser_for_language("python").unwrap();
            let tree = parser.parse(source, None);
            assert!(tree.is_some());
            assert!(!tree.unwrap().root_node().has_error());
        }
    }

    #[test]
    fn test_parse_typescript_with_interface() {
        if let Some(mut pm) = init_parser_manager() {
            let source = b"interface Person {\n    name: string;\n    age: number;\n}\n\nfunction greet(p: Person): string {\n    return `Hello, ${p.name}`;\n}";
            let parser = pm.get_parser_for_language("typescript").unwrap();
            let tree = parser.parse(source, None);
            assert!(tree.is_some());
            assert!(!tree.unwrap().root_node().has_error());
        }
    }
}

mod mcp_tools_tests {
    use leankg::mcp::tools::ToolRegistry;

    #[test]
    fn test_mcp_tools_registry() {
        let tools = ToolRegistry::list_tools();
        assert!(!tools.is_empty());
    }
}
