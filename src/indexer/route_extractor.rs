// Phase 1: HTTP route extraction from Go and TypeScript frameworks
// FR-B10: route element type | FR-B11: >= 2 Go + >= 2 TS frameworks
// FR-B12: http_calls edges call-site -> route

use crate::db::models::{CodeElement, Relationship};
use serde_json::json;

#[derive(Debug, Clone)]
pub struct RouteInfo {
    pub method: String,
    pub path: String,
    pub handler: String,
    pub framework: String,
    pub file_path: String,
    pub line: u32,
}

pub struct RouteExtractor;

impl RouteExtractor {
    pub fn extract_routes(
        source: &[u8],
        tree: &tree_sitter::Tree,
        file_path: &str,
        language: &str,
    ) -> Vec<RouteInfo> {
        let mut routes = Vec::new();
        match language {
            "go" => Self::extract_go_routes(source, tree, file_path, &mut routes),
            "typescript" | "javascript" | "tsx" | "jsx" => {
                Self::extract_ts_routes(source, tree, file_path, &mut routes);
            }
            _ => {}
        }
        routes
    }

    pub fn routes_to_elements_and_rels(
        routes: &[RouteInfo],
    ) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        for route in routes {
            let qualified = format!(
                "{}::{} {} {}",
                route.file_path, route.method, route.path, route.handler
            );
            let route_name = format!("{} {}", route.method, route.path);

            elements.push(CodeElement {
                qualified_name: qualified.clone(),
                element_type: "route".to_string(),
                name: route_name,
                file_path: route.file_path.clone(),
                line_start: route.line,
                line_end: route.line,
                language: route.framework.clone(),
                metadata: json!({
                    "method": route.method,
                    "path": route.path,
                    "handler": route.handler,
                    "framework": route.framework,
                }),
                ..Default::default()
            });

            let handler_qualified = format!("{}::{}", route.file_path, route.handler);
            relationships.push(Relationship {
                source_qualified: handler_qualified,
                target_qualified: qualified.clone(),
                rel_type: "http_calls".to_string(),
                confidence: 0.90,
                metadata: json!({
                    "method": route.method,
                    "path": route.path,
                    "framework": route.framework,
                    "line": route.line,
                }),
                ..Default::default()
            });

            relationships.push(Relationship {
                source_qualified: route.file_path.to_string(),
                target_qualified: qualified,
                rel_type: "defines_route".to_string(),
                confidence: 0.90,
                metadata: json!({"method": route.method, "framework": route.framework}),
                ..Default::default()
            });
        }

        (elements, relationships)
    }

    // Go routes

    fn extract_go_routes(
        source: &[u8],
        tree: &tree_sitter::Tree,
        file_path: &str,
        routes: &mut Vec<RouteInfo>,
    ) {
        Self::walk_go_node(source, tree.root_node(), file_path, routes);
    }

    fn walk_go_node(
        source: &[u8],
        node: tree_sitter::Node,
        file_path: &str,
        routes: &mut Vec<RouteInfo>,
    ) {
        if node.kind() == "call_expression" {
            if let Some(route) = Self::try_go_route(source, node, file_path) {
                routes.push(route);
            }
        }
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            Self::walk_go_node(source, child, file_path, routes);
        }
    }

    fn try_go_route(source: &[u8], node: tree_sitter::Node, file_path: &str) -> Option<RouteInfo> {
        let mut selector: Option<(String, String)> = None;
        let mut args: Vec<String> = Vec::new();
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            let kind = child.kind();
            if kind == "selector_expression" {
                selector = Some(Self::extract_go_selector(source, child));
            }
            if kind == "argument_list" {
                args = Self::extract_go_args(source, child);
            }
        }
        let (receiver, method) = selector?;
        if args.is_empty() {
            return None;
        }
        let http_method = match method.to_lowercase().as_str() {
            "get" | "handlefunc" | "handle" => "GET",
            "post" => "POST",
            "put" => "PUT",
            "delete" | "del" => "DELETE",
            "patch" => "PATCH",
            "head" => "HEAD",
            "options" => "OPTIONS",
            _ => return None,
        };
        let handler = if args.len() > 1 {
            Self::normalize_go_handler(&args[1])
        } else {
            "anonymous".to_string()
        };

        Some(RouteInfo {
            method: http_method.to_string(),
            path: Self::clean_path(&args[0]),
            handler,
            framework: Self::detect_go_framework(&receiver, &method),
            file_path: file_path.to_string(),
            line: node.start_position().row as u32 + 1,
        })
    }

    fn extract_go_selector(source: &[u8], node: tree_sitter::Node) -> (String, String) {
        let mut receiver = String::new();
        let mut method = String::new();
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            let kind = child.kind();
            let text = Self::node_text(source, child);
            if kind == "identifier" || kind == "field_identifier" {
                if receiver.is_empty() {
                    receiver = text;
                } else {
                    method = text;
                }
            } else if kind == "selector_expression" {
                let (rec, _) = Self::extract_go_selector(source, child);
                receiver = rec;
            }
        }
        (receiver, method)
    }

    fn extract_go_args(source: &[u8], node: tree_sitter::Node) -> Vec<String> {
        let mut args = Vec::new();
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            let kind = child.kind();
            let text = Self::node_text(source, child).trim_matches('"').to_string();
            if text.is_empty() || text == "(" || text == ")" || text == "," {
                continue;
            }
            if kind == "interpreted_string_literal"
                || kind == "raw_string_literal"
                || kind == "identifier"
                || kind == "selector_expression"
            {
                args.push(text);
            }
        }
        args
    }

    fn detect_go_framework(receiver: &str, method: &str) -> String {
        let r = receiver.to_lowercase();
        if r == "r" || r == "router" {
            return "chi".to_string();
        }
        if r == "e" || r == "echo" {
            return "echo".to_string();
        }
        if r == "g" || r == "gin" {
            return "gin".to_string();
        }
        if receiver == "http" && (method == "HandleFunc" || method == "Handle") {
            return "net/http".to_string();
        }
        "net/http".to_string()
    }

    fn normalize_go_handler(raw: &str) -> String {
        if let Some(dot_pos) = raw.rfind('.') {
            let after = &raw[dot_pos + 1..];
            if let Some(p) = after.find('(') {
                after[..p].to_string()
            } else {
                after.to_string()
            }
        } else if let Some(p) = raw.find('(') {
            raw[..p].to_string()
        } else {
            raw.to_string()
        }
    }

    // TypeScript routes

    fn extract_ts_routes(
        source: &[u8],
        tree: &tree_sitter::Tree,
        file_path: &str,
        routes: &mut Vec<RouteInfo>,
    ) {
        Self::walk_ts_node(source, tree.root_node(), file_path, routes);
    }

    fn walk_ts_node(
        source: &[u8],
        node: tree_sitter::Node,
        file_path: &str,
        routes: &mut Vec<RouteInfo>,
    ) {
        let kind = node.kind();
        if kind == "call_expression" {
            if let Some(route) = Self::try_ts_route(source, node, file_path) {
                routes.push(route);
            }
            if let Some(route) = Self::try_ts_mount(source, node, file_path) {
                routes.push(route);
            }
        }
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            Self::walk_ts_node(source, child, file_path, routes);
        }
    }

    fn try_ts_route(source: &[u8], node: tree_sitter::Node, file_path: &str) -> Option<RouteInfo> {
        let method_call = match node.child(0) {
            Some(c) if c.kind() == "member_expression" => Self::node_text(source, c),
            _ => return None,
        };

        // Defer middleware `use(...)` calls to try_ts_mount to avoid duplication.
        let (_, http_method) = Self::parse_ts_member_expr(&method_call)?;
        if http_method == "USE" {
            return None;
        }

        let mut strings: Vec<String> = Vec::new();
        let mut handler: Option<String> = None;
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() != "arguments" {
                continue;
            }
            let mut inner = child.walk();
            let mut arg_index = 0usize;
            for arg in child.children(&mut inner) {
                if Self::is_punctuation(arg.kind()) {
                    continue;
                }
                let ak = arg.kind();
                match ak {
                    "string" | "template_string" => {
                        let raw = Self::node_text(source, arg);
                        strings.push(Self::unquote(&raw));
                    }
                    "identifier" | "member_expression"
                        if (arg_index == 1 || (arg_index == 0 && strings.is_empty()))
                            && handler.is_none() =>
                    {
                        handler = Some(Self::node_text(source, arg));
                    }
                    "arrow_function" | "function" | "function_expression"
                        if (arg_index == 1 || (arg_index == 0 && strings.is_empty()))
                            && handler.is_none() =>
                    {
                        handler = Some("anonymous".to_string());
                    }
                    _ => {}
                }
                arg_index += 1;
            }
        }

        if strings.is_empty() {
            return None;
        }

        let (receiver, method) = {
            let dot = method_call.rfind('.')?;
            (
                method_call[..dot].to_string(),
                method_call[dot + 1..].to_uppercase(),
            )
        };

        Some(RouteInfo {
            method,
            path: Self::clean_path(&strings[0]),
            handler: handler.unwrap_or_else(|| "anonymous".to_string()),
            framework: Self::detect_ts_framework(&receiver),
            file_path: file_path.to_string(),
            line: node.start_position().row as u32 + 1,
        })
    }

    fn try_ts_mount(source: &[u8], node: tree_sitter::Node, file_path: &str) -> Option<RouteInfo> {
        let first_child = node.child(0)?;
        if first_child.kind() != "member_expression" {
            return None;
        }
        let text = Self::node_text(source, first_child);
        let (receiver, method) = Self::parse_ts_member_expr(&text)?;
        if method != "USE" {
            return None;
        }

        let mut strings: Vec<String> = Vec::new();
        let mut handler: Option<String> = None;
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() != "arguments" {
                continue;
            }
            let mut inner = child.walk();
            let mut arg_index = 0usize;
            for arg in child.children(&mut inner) {
                if Self::is_punctuation(arg.kind()) {
                    continue;
                }
                match arg.kind() {
                    "string" | "template_string" => {
                        let raw = Self::node_text(source, arg);
                        strings.push(Self::unquote(&raw));
                    }
                    "identifier" | "member_expression"
                        if (arg_index == 1 || (arg_index == 0 && strings.is_empty()))
                            && handler.is_none() =>
                    {
                        handler = Some(Self::node_text(source, arg));
                    }
                    "arrow_function" | "function" | "function_expression"
                        if (arg_index == 1 || (arg_index == 0 && strings.is_empty()))
                            && handler.is_none() =>
                    {
                        handler = Some("anonymous".to_string());
                    }
                    _ => {}
                }
                arg_index += 1;
            }
        }

        let first_path = strings
            .into_iter()
            .next()
            .map(|p| Self::clean_path(&p))
            .unwrap_or_else(|| "/".to_string());
        Some(RouteInfo {
            method: "USE".to_string(),
            path: first_path,
            handler: handler.unwrap_or_else(|| "router".to_string()),
            framework: Self::detect_ts_framework(&receiver),
            file_path: file_path.to_string(),
            line: node.start_position().row as u32 + 1,
        })
    }

    fn unquote(raw: &str) -> String {
        raw.trim()
            .trim_matches('"')
            .trim_matches('\'')
            .trim_matches('`')
            .to_string()
    }

    fn is_punctuation(kind: &str) -> bool {
        matches!(kind, "(" | ")" | "," | ";" | "{" | "}")
    }

    fn parse_ts_member_expr(text: &str) -> Option<(String, String)> {
        if let Some(dot_pos) = text.rfind('.') {
            let receiver = text[..dot_pos].to_string();
            let method = text[dot_pos + 1..].to_uppercase();
            match method.as_str() {
                "GET" | "POST" | "PUT" | "DELETE" | "PATCH" | "HEAD" | "OPTIONS" | "USE" => {
                    Some((receiver, method))
                }
                _ => None,
            }
        } else {
            None
        }
    }

    fn detect_ts_framework(receiver: &str) -> String {
        let r = receiver.to_lowercase();
        if r == "app" || r == "application" || r == "router" {
            "express".to_string()
        } else if r == "fastify" || r == "server" {
            "fastify".to_string()
        } else {
            "express".to_string()
        }
    }

    fn node_text(source: &[u8], node: tree_sitter::Node) -> String {
        node.utf8_text(source).unwrap_or("").to_string()
    }

    fn clean_path(path: &str) -> String {
        let p = path.trim().trim_matches('"').trim_matches('\'');
        if !p.starts_with('/') && p != "*" && p != "/" {
            format!("/{}", p)
        } else {
            p.to_string()
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn parse_go(source: &str) -> tree_sitter::Tree {
        let mut parser = tree_sitter::Parser::new();
        parser
            .set_language(&tree_sitter_go::LANGUAGE.into())
            .unwrap();
        parser.parse(source, None).unwrap()
    }

    fn parse_ts(source: &str) -> tree_sitter::Tree {
        let mut parser = tree_sitter::Parser::new();
        parser
            .set_language(&tree_sitter_typescript::LANGUAGE_TYPESCRIPT.into())
            .unwrap();
        parser.parse(source, None).unwrap()
    }

    #[test]
    fn test_extract_chi_route() {
        let source = r#"
package main
import "github.com/go-chi/chi/v5"
func main() {
    r := chi.NewRouter()
    r.Get("/users/{id}", getUser)
    r.Post("/users", createUser)
}
func getUser(w http.ResponseWriter, r *http.Request) {}
func createUser(w http.ResponseWriter, r *http.Request) {}
"#;
        let tree = parse_go(source);
        let routes = RouteExtractor::extract_routes(source.as_bytes(), &tree, "src/main.go", "go");
        assert!(!routes.is_empty());
        let get_route = routes.iter().find(|r| r.method == "GET");
        assert!(get_route.is_some());
        if let Some(r) = get_route {
            assert!(r.path.contains("users"));
            assert_eq!(r.framework, "chi");
        }
    }

    #[test]
    fn test_extract_gin_route() {
        let source = r#"
package main
import "github.com/gin-gonic/gin"
func main() {
    g := gin.Default()
    g.GET("/health", healthCheck)
    g.POST("/orders", createOrder)
}
func healthCheck(c *gin.Context) {}
func createOrder(c *gin.Context) {}
"#;
        let tree = parse_go(source);
        let routes = RouteExtractor::extract_routes(source.as_bytes(), &tree, "src/main.go", "go");
        assert!(!routes.is_empty());
        let post = routes.iter().find(|r| r.method == "POST");
        assert!(post.is_some());
        if let Some(r) = post {
            assert_eq!(r.framework, "gin");
        }
    }

    #[test]
    fn test_extract_express_routes() {
        let source = r#"
const express = require('express');
const app = express();
app.get('/api/users', getUsers);
app.post('/api/users', createUser);
app.put('/api/users/:id', updateUser);
function getUsers(req, res) {}
function createUser(req, res) {}
function updateUser(req, res) {}
"#;
        let tree = parse_ts(source);
        let routes =
            RouteExtractor::extract_routes(source.as_bytes(), &tree, "src/app.ts", "typescript");
        assert!(!routes.is_empty());
        assert_eq!(routes.len(), 3);
        let put_route = routes.iter().find(|r| r.method == "PUT");
        assert!(put_route.is_some());
        if let Some(r) = put_route {
            assert!(r.path.contains(":id"));
            assert_eq!(r.framework, "express");
        }
    }

    #[test]
    fn test_extract_fastify_route() {
        let source = r#"
import Fastify from 'fastify';
const fastify = Fastify();
fastify.get('/status', async (req, reply) => { return { ok: true }; });
fastify.post('/items', createItem);
function createItem(req, reply) {}
"#;
        let tree = parse_ts(source);
        let routes =
            RouteExtractor::extract_routes(source.as_bytes(), &tree, "src/server.ts", "typescript");
        assert!(!routes.is_empty());
        let get_route = routes.iter().find(|r| r.method == "GET");
        assert!(get_route.is_some());
        if let Some(r) = get_route {
            assert_eq!(r.framework, "fastify");
        }
    }

    #[test]
    fn test_routes_to_elements() {
        let routes = vec![RouteInfo {
            method: "GET".to_string(),
            path: "/health".to_string(),
            handler: "healthCheck".to_string(),
            framework: "chi".to_string(),
            file_path: "src/handler.go".to_string(),
            line: 10,
        }];
        let (elements, relationships) = RouteExtractor::routes_to_elements_and_rels(&routes);
        assert_eq!(elements.len(), 1);
        assert_eq!(elements[0].element_type, "route");
        assert_eq!(elements[0].name, "GET /health");
        assert_eq!(relationships.len(), 2);
        assert_eq!(relationships[0].rel_type, "http_calls");
        assert_eq!(relationships[1].rel_type, "defines_route");
    }

    #[test]
    fn test_parse_ts_member_expr() {
        assert_eq!(
            RouteExtractor::parse_ts_member_expr("app.get"),
            Some(("app".to_string(), "GET".to_string()))
        );
        assert_eq!(
            RouteExtractor::parse_ts_member_expr("fastify.post"),
            Some(("fastify".to_string(), "POST".to_string()))
        );
        assert_eq!(RouteExtractor::parse_ts_member_expr("app.listen"), None);
    }
}
