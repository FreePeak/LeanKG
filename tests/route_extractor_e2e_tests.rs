// End-to-end: exercise the full EntityExtractor.extract() pipeline
// to verify routes are correctly extracted during real indexing.

use leankg::indexer::extractor::EntityExtractor;

#[test]
fn entity_extractor_extracts_routes_for_go_file() {
    let src = r#"
package main
import "github.com/go-chi/chi/v5"
func main() {
    r := chi.NewRouter()
    r.Get("/users/{id}", getUser)
    r.Post("/users", createUser)
}
"#;
    let extractor = EntityExtractor::new(src.as_bytes(), "main.go", "go");
    let tree = {
        let mut p = tree_sitter::Parser::new();
        p.set_language(&tree_sitter_go::LANGUAGE.into()).unwrap();
        p.parse(src, None).unwrap()
    };
    let (elements, rels) = extractor.extract(&tree);
    let routes: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "route")
        .collect();
    assert_eq!(routes.len(), 2);
    let http_calls: Vec<_> = rels.iter().filter(|r| r.rel_type == "http_calls").collect();
    assert_eq!(http_calls.len(), 2);
    let defines_route: Vec<_> = rels
        .iter()
        .filter(|r| r.rel_type == "defines_route")
        .collect();
    assert_eq!(defines_route.len(), 2);
}

#[test]
fn entity_extractor_extracts_routes_for_ts_file() {
    let src = r#"
const app = require('express')();
app.get('/api/users', getUsers);
app.post('/api/users', createUser);
app.use('/api/middleware', mw);
"#;
    let extractor = EntityExtractor::new(src.as_bytes(), "app.ts", "typescript");
    let tree = {
        let mut p = tree_sitter::Parser::new();
        p.set_language(&tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into())
            .unwrap();
        p.parse(src, None).unwrap()
    };
    let (elements, rels) = extractor.extract(&tree);
    let routes: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "route")
        .collect();
    println!("Routes: {:#?}", routes);
    println!("Rels count: {}", rels.len());
    println!(
        "http_calls: {}",
        rels.iter().filter(|r| r.rel_type == "http_calls").count()
    );
    println!(
        "defines_route: {}",
        rels.iter()
            .filter(|r| r.rel_type == "defines_route")
            .count()
    );
    // Should be 3 (GET, POST, USE), NOT 4 (no duplication of USE)
    assert_eq!(routes.len(), 3, "Should not duplicate USE routes");

    let paths: Vec<_> = routes.iter().map(|r| &r.metadata["path"]).collect();
    println!("Paths: {:?}", paths);
}

#[test]
fn entity_extractor_skips_routes_for_rust_file() {
    let src = r#"
fn main() {
    println!("hello");
}
"#;
    let extractor = EntityExtractor::new(src.as_bytes(), "main.rs", "rust");
    let tree = {
        let mut p = tree_sitter::Parser::new();
        p.set_language(&tree_sitter_rust::LANGUAGE.into()).unwrap();
        p.parse(src, None).unwrap()
    };
    let (elements, _rels) = extractor.extract(&tree);
    let routes: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "route")
        .collect();
    assert!(routes.is_empty(), "Rust files should not produce routes");
}

#[test]
fn entity_extractor_handles_javascript_extension() {
    let src = r#"
const app = require('express')();
app.get('/js-endpoint', jsHandler);
"#;
    let extractor = EntityExtractor::new(src.as_bytes(), "app.js", "javascript");
    let tree = {
        let mut p = tree_sitter::Parser::new();
        p.set_language(&tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into())
            .unwrap();
        p.parse(src, None).unwrap()
    };
    let (elements, _rels) = extractor.extract(&tree);
    let routes: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "route")
        .collect();
    assert_eq!(
        routes.len(),
        1,
        "JS files should produce routes via typescript parser"
    );
}

#[test]
fn no_duplicate_routes_in_extractor_for_express_use() {
    let src = r#"
const app = require('express')();
app.use('/x', fn1);
app.use('/y', fn2);
"#;
    let extractor = EntityExtractor::new(src.as_bytes(), "app.ts", "typescript");
    let tree = {
        let mut p = tree_sitter::Parser::new();
        p.set_language(&tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into())
            .unwrap();
        p.parse(src, None).unwrap()
    };
    let (elements, _rels) = extractor.extract(&tree);
    let routes: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "route")
        .collect();
    let qns: std::collections::HashSet<_> = routes.iter().map(|r| &r.qualified_name).collect();
    println!("USE routes: {:#?}", routes);
    assert_eq!(
        routes.len(),
        qns.len(),
        "All qualified_names must be unique"
    );
    assert_eq!(routes.len(), 2);
}
