// More edge case tests to find bugs
use leankg::indexer::route_extractor::RouteExtractor;
use tree_sitter::Parser;

fn parse_go(src: &str) -> tree_sitter::Tree {
    let mut p = Parser::new();
    p.set_language(&tree_sitter_go::LANGUAGE.into()).unwrap();
    p.parse(src, None).unwrap()
}
fn parse_ts(src: &str) -> tree_sitter::Tree {
    let mut p = Parser::new();
    p.set_language(&tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into())
        .unwrap();
    p.parse(src, None).unwrap()
}

#[test]
fn route_qualified_name_uniqueness() {
    let src = r#"
const app = require('express')();
app.get('/users', getUsers);
app.post('/users', createUser);
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    let (elements, rels) = RouteExtractor::routes_to_elements_and_rels(&routes);
    println!("elements: {:#?}", elements);
    println!("rels: {:#?}", rels);
    let qns: Vec<_> = elements.iter().map(|e| e.qualified_name.clone()).collect();
    let unique: std::collections::HashSet<_> = qns.iter().collect();
    assert_eq!(
        qns.len(),
        unique.len(),
        "qualified_names must be unique: {:?}",
        qns
    );
}

#[test]
fn go_qualified_name_uniqueness() {
    let src = r#"
package main
import "github.com/go-chi/chi/v5"
func main() {
    r := chi.NewRouter()
    r.Get("/users", getUser)
    r.Post("/users", createUser)
}
"#;
    let routes = RouteExtractor::extract_routes(src.as_bytes(), &parse_go(src), "main.go", "go");
    let (elements, _rels) = RouteExtractor::routes_to_elements_and_rels(&routes);
    let qns: Vec<_> = elements.iter().map(|e| e.qualified_name.clone()).collect();
    let unique: std::collections::HashSet<_> = qns.iter().collect();
    assert_eq!(
        qns.len(),
        unique.len(),
        "go routes must be unique: {:?}",
        qns
    );
}

#[test]
fn http_calls_target_qualified_format() {
    // The handler qualified name should match what other elements (functions) use
    let src = r#"
const app = require('express')();
app.get('/health', getHealth);
function getHealth(req, res) {}
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    let (_elements, rels) = RouteExtractor::routes_to_elements_and_rels(&routes);
    let http_call = rels.iter().find(|r| r.rel_type == "http_calls").unwrap();
    println!("http_call: {:#?}", http_call);
    // handler qualified should be `app.ts::getHealth`
    assert_eq!(http_call.source_qualified, "app.ts::getHealth");
    assert!(http_call.target_qualified.starts_with("app.ts::"));
}

#[test]
fn static_string_in_clean_path() {
    // Verify static path: "*", "/" etc work
    use leankg::indexer::route_extractor::RouteExtractor;
    let src = r#"
const app = require('express')();
app.get('/', rootHandler);
app.get('*', catchAllHandler);
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    let paths: Vec<_> = routes.iter().map(|r| &r.path).collect();
    println!("paths: {:?}", paths);
    assert!(
        paths.contains(&&"/".to_string())
            || paths.contains(&&"/*".to_string())
            || paths.contains(&&"*".to_string())
    );
}
