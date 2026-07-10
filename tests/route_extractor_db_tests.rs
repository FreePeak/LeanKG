// Integration test: indexer pipeline indexes Go + TS files
// and produces route elements with correct metadata + relationships

use leankg::indexer::route_extractor::RouteExtractor;
use std::fs;
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
fn route_metadata_is_valid_json() {
    // The metadata should contain method, path, handler, framework
    let src = r#"
package main
import "github.com/go-chi/chi/v5"
func main() {
    r := chi.NewRouter()
    r.Get("/users", getUsers)
    r.Post("/users", createUser)
}
"#;
    let routes = RouteExtractor::extract_routes(src.as_bytes(), &parse_go(src), "main.go", "go");
    let (elements, rels) = RouteExtractor::routes_to_elements_and_rels(&routes);
    assert_eq!(elements.len(), 2);
    for e in &elements {
        assert_eq!(e.element_type, "route");
        assert!(e.metadata.is_object());
        let m = e.metadata.as_object().unwrap();
        assert!(m.get("method").is_some());
        assert!(m.get("path").is_some());
        assert!(m.get("handler").is_some());
        assert!(m.get("framework").is_some());
    }
    assert_eq!(rels.len(), 4); // 2 routes x 2 rels each
}

#[test]
fn route_handler_name_extracted_correctly_for_each_pattern() {
    let src = r#"
const app = require('express')();
app.get('/a', identifierHandler);
app.post('/b', obj.method);
app.put('/c', (req, res) => {});
app.delete('/d', function (req, res) {});
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    println!("Routes: {:#?}", routes);
    assert_eq!(routes.len(), 4);
    let a = routes.iter().find(|r| r.path == "/a").unwrap();
    assert_eq!(a.handler, "identifierHandler");
    let b = routes.iter().find(|r| r.path == "/b").unwrap();
    assert_eq!(b.handler, "obj.method");
    let c = routes.iter().find(|r| r.path == "/c").unwrap();
    assert_eq!(c.handler, "anonymous");
    let d = routes.iter().find(|r| r.path == "/d").unwrap();
    assert_eq!(d.handler, "anonymous");
}

#[test]
fn route_without_handler() {
    // app.get('/x') without handler should still be detected
    let src = r#"
const app = require('express')();
app.get('/x');
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    let x = routes.iter().find(|r| r.path == "/x");
    assert!(x.is_some(), "Route /x should be detected");
}

#[test]
fn use_handler_with_identifier() {
    let src = r#"
const app = require('express')();
app.use('/api/auth', authMiddleware);
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    assert_eq!(routes.len(), 1, "should not duplicate use routes");
    let r = &routes[0];
    assert_eq!(r.method, "USE");
    assert_eq!(r.handler, "authMiddleware");
}

#[test]
fn use_handler_with_function() {
    let src = r#"
const app = require('express')();
app.use((req, res, next) => next());
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    assert_eq!(routes.len(), 1);
    assert_eq!(routes[0].handler, "anonymous");
}

#[test]
fn go_handler_with_method_call() {
    // gin sometimes: g.GET("/path", h.MethodName)
    let src = r#"
package main
import "github.com/gin-gonic/gin"
func main() {
    g := gin.Default()
    g.GET("/items", h.List)
    g.POST("/items", h.Create)
}
"#;
    let routes = RouteExtractor::extract_routes(src.as_bytes(), &parse_go(src), "main.go", "go");
    assert_eq!(routes.len(), 2);
    let get = routes.iter().find(|r| r.method == "GET").unwrap();
    println!("get handler: {:?}", get.handler);
    assert!(get.handler.contains("List") || get.handler.contains("h"));
}

#[test]
fn http_calls_edges_point_to_route_element() {
    let src = r#"
const app = require('express')();
app.get('/users', getUsers);
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    let (elements, rels) = RouteExtractor::routes_to_elements_and_rels(&routes);
    let route_qualified = &elements[0].qualified_name;
    let http_call = rels.iter().find(|r| r.rel_type == "http_calls").unwrap();
    assert_eq!(http_call.target_qualified, *route_qualified);
    let defines = rels.iter().find(|r| r.rel_type == "defines_route").unwrap();
    assert_eq!(defines.target_qualified, *route_qualified);
    assert_eq!(defines.source_qualified, "app.ts");
}

#[test]
fn html_special_paths() {
    // Test the clean_path logic edge cases
    let src = r#"
const app = require('express')();
app.get('/', rootHandler);
app.get('/*', wildcardHandler);
app.get('users', noSlashHandler);  // becomes /users
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    let paths: Vec<_> = routes.iter().map(|r| r.path.as_str()).collect();
    println!("Paths: {:?}", paths);
    assert!(paths.contains(&"/") || paths.contains(&"/*"));
}

#[test]
fn templates_in_path() {
    // Template strings in paths should be unquoted
    let src = r#"
const app = require('express')();
const ver = 'v1';
app.get(`/api/${ver}/users`, getUsers);
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    println!("Template routes: {:#?}", routes);
    assert!(!routes.is_empty(), "Should detect template string paths");
}

#[test]
fn full_pipeline_simulation() {
    // Write a real Go file to disk, parse it via tree-sitter, then verify routes
    let temp = std::env::temp_dir().join("leankg_route_test_pipeline");
    let _ = fs::remove_dir_all(&temp);
    fs::create_dir_all(&temp).unwrap();
    let go_file = temp.join("api.go");
    fs::write(
        &go_file,
        r#"
package main
import (
    "github.com/go-chi/chi/v5"
    "github.com/gin-gonic/gin"
)
func main() {
    r := chi.NewRouter()
    r.Get("/api/users/{id}", GetUser)
    r.Post("/api/users", CreateUser)

    g := gin.Default()
    g.GET("/health", Health)
    g.POST("/webhook", Webhook)
}
"#,
    )
    .unwrap();

    let src = fs::read(&go_file).unwrap();
    let tree = parse_go(std::str::from_utf8(&src).unwrap());
    let routes = RouteExtractor::extract_routes(&src, &tree, go_file.to_str().unwrap(), "go");

    println!("File-path test routes:");
    for r in &routes {
        println!(
            "  {} {} handler={} framework={}",
            r.method, r.path, r.handler, r.framework
        );
    }

    assert_eq!(routes.len(), 4, "Should find 4 routes");
    assert!(routes
        .iter()
        .any(|r| r.method == "GET" && r.path.contains("users/{id}") && r.framework == "chi"));
    assert!(routes
        .iter()
        .any(|r| r.method == "POST" && r.path.contains("users") && r.framework == "chi"));
    assert!(routes
        .iter()
        .any(|r| r.method == "GET" && r.path.contains("health") && r.framework == "gin"));
    assert!(routes
        .iter()
        .any(|r| r.method == "POST" && r.path.contains("webhook") && r.framework == "gin"));

    let _ = fs::remove_dir_all(&temp);
}
