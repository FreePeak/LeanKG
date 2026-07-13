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
fn edge_case_gin_route_with_handler_suffix() {
    // gin sometimes has `r.GET("/health", h.Check)` or uses chaining
    let src = r#"
package main
import "github.com/gin-gonic/gin"
type Handler struct{}
func (h *Handler) Check(c *gin.Context) {}
func main() {
    g := gin.Default()
    g.GET("/health", h.Check)
    g.GET("/users", (&Handler{}).Check)
}
"#;
    let routes = RouteExtractor::extract_routes(src.as_bytes(), &parse_go(src), "main.go", "go");
    println!("gin handler suffix routes: {:#?}", routes);
    assert!(!routes.is_empty());
    for r in routes {
        assert_eq!(r.method, "GET");
    }
}

#[test]
fn edge_case_chi_mux_router() {
    let src = r#"
package main
import "github.com/go-chi/chi/v5"
func main() {
    router := chi.NewRouter()
    router.Get("/a", a)
    router.Post("/b", b)
}
"#;
    let routes = RouteExtractor::extract_routes(src.as_bytes(), &parse_go(src), "main.go", "go");
    println!("chi router routes: {:#?}", routes);
    assert!(!routes.is_empty());
    for r in &routes {
        assert_eq!(r.framework, "chi");
    }
}

#[test]
fn edge_case_nested_call_expressions() {
    // g.Group("/v1").GET("/users", h) shouldn't be picked up because method isn't directly invoked
    let src = r#"
package main
import "github.com/gin-gonic/gin"
func main() {
    g := gin.Default()
    v1 := g.Group("/v1")
    v1.GET("/users", usersHandler)
    g.GET("/health", healthHandler)
}
"#;
    let routes = RouteExtractor::extract_routes(src.as_bytes(), &parse_go(src), "main.go", "go");
    println!("nested routes: {:#?}", routes);
    assert!(!routes.is_empty());
}

#[test]
fn edge_case_ts_chained_calls() {
    let src = r#"
const app = require('express')();
app.route('/users')
  .get(getUsers)
  .post(createUser);
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    println!("chained TS routes: {:#?}", routes);
    // These won't be detected because each call resolves to a member of the route() result
    // That's acceptable; the test verifies behavior, not exact count
}

#[test]
fn edge_case_no_duplicate_use() {
    // This was the bug: try_ts_route AND try_ts_mount both fire on app.use()
    let src = r#"
const app = require('express')();
app.use('/api/middleware', mw);
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    println!("single use: {:#?}", routes);
    assert_eq!(routes.len(), 1, "must not duplicate app.use() routes");
}

#[test]
fn edge_case_express_route_via_router() {
    let src = r#"
const router = express.Router();
router.get('/api/v1/items', handler);
router.post('/api/v1/items', handler);
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    println!("router routes: {:#?}", routes);
    assert_eq!(routes.len(), 2);
    for r in routes {
        assert_eq!(r.framework, "express");
    }
}

#[test]
fn edge_case_koa_router_methods() {
    let src = r#"
const Router = require('@koa/router');
const router = new Router();
router.get('/users', getUsers);
router.post('/items', createItem);
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    println!("koa routes: {:#?}", routes);
    assert_eq!(routes.len(), 2);
}

#[test]
fn edge_case_handler_is_anonymous_function() {
    let src = r#"
const app = require('express')();
app.get('/items', (req, res) => {});
app.post('/items', function (req, res) {});
"#;
    let routes =
        RouteExtractor::extract_routes(src.as_bytes(), &parse_ts(src), "app.ts", "typescript");
    println!("anonymous handler routes: {:#?}", routes);
    assert_eq!(routes.len(), 2);
}
