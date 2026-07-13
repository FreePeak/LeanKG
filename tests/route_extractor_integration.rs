// Real-world integration tests for route_extractor
// Uses realistic patterns from actual frameworks (chi, gin, echo, net/http, express, fastify, koa)
use leankg::indexer::route_extractor::RouteExtractor;
use tree_sitter::Parser;

fn parse_go(source: &str) -> tree_sitter::Tree {
    let mut p = Parser::new();
    p.set_language(&tree_sitter_go::LANGUAGE.into()).unwrap();
    p.parse(source, None).unwrap()
}

fn parse_ts(source: &str) -> tree_sitter::Tree {
    let mut p = Parser::new();
    p.set_language(&tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into())
        .unwrap();
    p.parse(source, None).unwrap()
}

#[test]
fn real_world_chi_routes() {
    let source = r#"
package main
import "github.com/go-chi/chi/v5"
func main() {
    r := chi.NewRouter()
    r.Get("/users/{id}", getUser)
    r.Post("/users", createUser)
    r.Put("/users/{id}", updateUser)
    r.Delete("/users/{id}", deleteUser)
    r.Patch("/users/{id}", patchUser)
    r.Head("/users", headUsers)
    r.HandleFunc("/static", staticHandler)
}
"#;
    let routes =
        RouteExtractor::extract_routes(source.as_bytes(), &parse_go(source), "main.go", "go");
    assert_eq!(
        routes.len(),
        7,
        "should extract 7 chi routes: {:#?}",
        routes
    );
    let get = routes.iter().find(|r| r.method == "GET").unwrap();
    assert!(get.path.contains("users"));
    assert_eq!(get.framework, "chi");
}

#[test]
fn real_world_gin_routes() {
    let source = r#"
package main
import "github.com/gin-gonic/gin"
func main() {
    g := gin.Default()
    g.GET("/health", healthCheck)
    g.POST("/orders", createOrder)
    g.PUT("/orders/:id", updateOrder)
    g.DELETE("/orders/:id", deleteOrder)
    g.PATCH("/orders/:id", patchOrder)
    g.OPTIONS("/orders", optionsOrder)
}
"#;
    let routes =
        RouteExtractor::extract_routes(source.as_bytes(), &parse_go(source), "main.go", "go");
    assert_eq!(
        routes.len(),
        6,
        "should extract 6 gin routes: {:#?}",
        routes
    );
    for r in &routes {
        assert_eq!(r.framework, "gin");
    }
}

#[test]
fn real_world_echo_routes() {
    let source = r#"
package main
import "github.com/labstack/echo/v4"
func main() {
    e := echo.New()
    e.GET("/health", health)
    e.POST("/login", login)
    e.PUT("/users/:id", updateUser)
    e.DELETE("/users/:id", deleteUser)
}
"#;
    let routes =
        RouteExtractor::extract_routes(source.as_bytes(), &parse_go(source), "main.go", "go");
    assert_eq!(
        routes.len(),
        4,
        "should extract 4 echo routes: {:#?}",
        routes
    );
    for r in &routes {
        assert_eq!(r.framework, "echo");
    }
}

#[test]
fn real_world_net_http_routes() {
    let source = r#"
package main
import "net/http"
func main() {
    http.HandleFunc("/static", staticHandler)
    http.Handle("/health", httpHandler)
    http.HandleFunc("/api/users", usersHandler)
}
func staticHandler(w http.ResponseWriter, r *http.Request) {}
"#;
    let routes =
        RouteExtractor::extract_routes(source.as_bytes(), &parse_go(source), "main.go", "go");
    println!("net/http routes: {:#?}", routes);
    assert!(!routes.is_empty(), "should extract net/http routes");
    for r in &routes {
        assert_eq!(r.framework, "net/http");
    }
}

#[test]
fn real_world_express_routes() {
    let source = r#"
const express = require('express');
const app = express();
const router = express.Router();

app.get('/api/users', (req, res) => {});
app.post('/api/users', (req, res) => {});
app.put('/api/users/:id', (req, res) => {});
app.delete('/api/users/:id', (req, res) => {});
app.patch('/api/users/:id', (req, res) => {});
app.options('/api/users', (req, res) => {});
app.head('/api/users', (req, res) => {});

router.get('/api/v1/items', (req, res) => {});
"#;
    let routes = RouteExtractor::extract_routes(
        source.as_bytes(),
        &parse_ts(source),
        "app.ts",
        "typescript",
    );
    println!("express routes count: {}", routes.len());
    assert!(
        routes.len() >= 8,
        "should extract >= 8 express routes, got: {:#?}",
        routes
    );
    for r in &routes {
        assert_eq!(r.framework, "express");
    }
}

#[test]
fn real_world_fastify_routes() {
    let source = r#"
import Fastify from 'fastify';
const fastify = Fastify();

fastify.get('/status', async (req, reply) => ({}));
fastify.post('/items', async (req, reply) => ({}));
fastify.put('/items/:id', async (req, reply) => ({}));
fastify.delete('/items/:id', async (req, reply) => ({}));
"#;
    let routes = RouteExtractor::extract_routes(
        source.as_bytes(),
        &parse_ts(source),
        "app.ts",
        "typescript",
    );
    assert_eq!(
        routes.len(),
        4,
        "should extract 4 fastify routes: {:#?}",
        routes
    );
    for r in &routes {
        assert_eq!(r.framework, "fastify");
    }
}

#[test]
fn real_world_express_use_routes() {
    let source = r#"
const app = require('express')();
app.use('/api/middleware', (req, res, next) => next());
app.use('/static', express.static('public'));
"#;
    let routes = RouteExtractor::extract_routes(
        source.as_bytes(),
        &parse_ts(source),
        "app.ts",
        "typescript",
    );
    let use_routes: Vec<_> = routes.iter().filter(|r| r.method == "USE").collect();
    assert_eq!(
        use_routes.len(),
        2,
        "should detect 2 USE routes: {:#?}",
        routes
    );
}
