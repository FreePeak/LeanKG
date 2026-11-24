package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	leankgmcp "github.com/FreePeak/LeanKG/internal/mcp"
	"github.com/FreePeak/LeanKG/internal/projects"
	"github.com/FreePeak/LeanKG/internal/rest"
	restauto "github.com/FreePeak/LeanKG/internal/rest/auto"
	"github.com/FreePeak/LeanKG/internal/store"
)

// grantRig is one serving process over three registered projects behind one
// DB token store. gamma is the serving (default) project; alpha and beta are
// routed. Dirs carry their names so a grant names them by basename.
type grantRig struct {
	t      *testing.T
	st     store.Backend
	router *projects.Router
	alpha  string
	beta   string
	gamma  string
}

func newGrantRig(t *testing.T) grantRig {
	t.Helper()
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
	t.Setenv("LEANKG_TOKEN_VIEWER", "")
	base := t.TempDir()
	r := grantRig{t: t, gamma: filepath.Join(base, "gamma"), alpha: filepath.Join(base, "alpha"), beta: filepath.Join(base, "beta")}
	for _, d := range []string{r.gamma, r.alpha, r.beta} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seedProjectDir(t, r.gamma, "GammaSym")
	seedProjectDir(t, r.alpha, "AlphaSym")
	seedProjectDir(t, r.beta, "BetaSym", "BetaSym2")
	if err := os.MkdirAll(filepath.Join(r.beta, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := openFixtureStore(filepath.Join(base, "tokens", ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.st = st
	r.router = projects.NewRouter(r.gamma, projects.Config{
		ExtraDirs: []string{r.alpha, r.beta}, Mode: store.RW, EngineName: "sqlite",
	})
	r.router.SeedDefault(&projects.Project{Dir: r.gamma, Name: "gamma", Engine: engineOver(t, r.gamma)})
	t.Cleanup(func() { _ = r.router.Close() })
	return r
}

// mint issues a DB token with role and scopes and returns the bearer secret.
func (r grantRig) mint(role string, scopes ...string) string {
	r.t.Helper()
	secret, _, err := auth.Mint(context.Background(), r.st, auth.MintRequest{Name: r.t.Name(), Role: role, Scopes: scopes})
	if err != nil {
		r.t.Fatal(err)
	}
	return secret
}

// mcpHandler mounts the MCP listener the way serve does: ?project= routing
// over a project-scoped server, the default server behind it.
func (r grantRig) mcpHandler() http.Handler {
	srv := leankgmcp.New(engineOver(r.t, r.gamma), leankgmcp.WithProjectRequired(true))
	srv.SetProjectRouter(r.router)
	srv.SetAuthStore(r.st)
	return routeByProject(r.router, resolveCaller(r.st), srv.HTTPHandler(), func(p *projects.Project) http.Handler {
		pSrv := newScopedMCPServer(p.Engine, r.router)
		pSrv.SetAuthStore(r.st)
		return pSrv.HTTPHandler()
	})
}

// restHandler mounts the REST listener the way serve does: the auth gate
// outside the ?project= routing.
func (r grantRig) restHandler() http.Handler {
	h := routeByProject(r.router, nil, rest.Handler(engineOver(r.t, r.gamma), nil), func(p *projects.Project) http.Handler {
		return rest.Handler(p.Engine, p.Memory)
	})
	return auth.MiddlewareWithStore(r.st, h)
}

// callTool posts one tools/call with an optional bearer and returns the body.
func callTool(h http.Handler, bearer, tool, args string) string {
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tool, args)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Body.String()
}

// get returns the status and body of a REST GET with an optional bearer.
func get(t *testing.T, h http.Handler, bearer, target string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

// TestProjectGrantMCP pins the grant on the MCP listener: a token scoped to
// project:alpha reads alpha, is refused beta with PermissionDenied (by name,
// by a path inside beta, and by an unknown name alike), and the refusal leaks
// neither beta's path nor the other projects.
func TestProjectGrantMCP(t *testing.T) {
	r := newGrantRig(t)
	h := r.mcpHandler()
	alphaTok := r.mint("viewer", "project:alpha")

	if body := callTool(h, alphaTok, "status", `{"project":"alpha"}`); !strings.Contains(body, "elements") {
		t.Fatalf("granted project must answer; got %s", body)
	}
	denied := callTool(h, alphaTok, "status", `{"project":"beta"}`)
	if !strings.Contains(denied, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("a project outside the grant must be refused with PermissionDenied; got %s", denied)
	}
	for _, leak := range []string{r.beta, r.gamma, "gamma"} {
		if strings.Contains(denied, leak) {
			t.Fatalf("refusal leaks %q: %s", leak, denied)
		}
	}
	if unknown := callTool(h, alphaTok, "status", `{"project":"no-such-project"}`); !strings.Contains(unknown, "LEANKG_ERROR_PERMISSION_DENIED") ||
		strings.Contains(unknown, "known projects") {
		t.Fatalf("an unknown name must get the same refusal, not the known-project list; got %s", unknown)
	}
	if inside := callTool(h, alphaTok, "status", fmt.Sprintf(`{"project":%q}`, filepath.Join(r.beta, "src"))); !strings.Contains(inside, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("a path inside beta must not bypass the grant; got %s", inside)
	}
	if q := callTool(h, alphaTok, "query", `{"project":"beta","action":"search","query":"BetaSym"}`); !strings.Contains(q, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("query on beta must be refused; got %s", q)
	}
	if viaQuery := callTool(h, alphaTok, "status", `{"project":"alpha"}`); strings.Contains(viaQuery, "BetaSym") {
		t.Fatalf("granted call leaked beta content: %s", viaQuery)
	}

	// ?project= is a second selector and is checked before the server is built.
	if body := postWithQuery(h, alphaTok, "status", "beta"); !strings.Contains(body, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("?project=beta must be refused for an alpha token; got %s", body)
	}

	// An unscoped token, and a token that names no project, keep full access.
	for name, tok := range map[string]string{
		"unscoped":          r.mint("admin"),
		"non-project scope": r.mint("viewer", "graphs:read"),
	} {
		if body := callTool(h, tok, "status", `{"project":"beta"}`); !strings.Contains(body, "elements") {
			t.Fatalf("%s token must reach beta; got %s", name, body)
		}
	}
}

// postWithQuery is callTool with the project carried in ?project= and no
// project argument in the body.
func postWithQuery(h http.Handler, bearer, tool, project string) string {
	req := httptest.NewRequest("POST", "/mcp?project="+project, strings.NewReader(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":{}}}`, tool)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Body.String()
}

// TestProjectGrantREST pins the grant on the REST listener: ?project= for a
// project outside the grant answers 403, and an unknown name answers the same
// 403 rather than the 404 that lists the known projects.
func TestProjectGrantREST(t *testing.T) {
	r := newGrantRig(t)
	h := r.restHandler()
	alphaTok := r.mint("viewer", "project:alpha")

	if code, body := get(t, h, alphaTok, "/api/v1/status?project=alpha"); code != http.StatusOK || !strings.Contains(body, `"elements"`) {
		t.Fatalf("granted project = %d, want 200 with elements; got %s", code, body)
	}
	code, denied := get(t, h, alphaTok, "/api/v1/status?project=beta")
	if code != http.StatusForbidden || !strings.Contains(denied, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("denied project = %d, want 403 PermissionDenied; got %s", code, denied)
	}
	if strings.Contains(denied, r.beta) || strings.Contains(denied, "gamma") {
		t.Fatalf("403 leaks another project: %s", denied)
	}
	if code, unknown := get(t, h, alphaTok, "/api/v1/status?project=no-such-project"); code != http.StatusForbidden || strings.Contains(unknown, "known projects") {
		t.Fatalf("unknown name for a scoped token = %d, want 403 without the project list; got %s", code, unknown)
	}
	if code, _ := get(t, h, alphaTok, "/api/v1/status?project="+filepath.Join(r.beta, "src")); code != http.StatusForbidden {
		t.Fatalf("path inside beta = %d, want 403", code)
	}
	if code, body := get(t, h, r.mint("admin"), "/api/v1/status?project=beta"); code != http.StatusOK || !strings.Contains(body, `"elements"`) {
		t.Fatalf("unscoped token must reach beta = %d; got %s", code, body)
	}
}

// TestProjectGrantAutoConfig pins the grant on the auto-config write route,
// which resolves ?project= through the same router: a contributor scoped to
// alpha is refused beta with 403 and never starts an index there.
func TestProjectGrantAutoConfig(t *testing.T) {
	r := newGrantRig(t)
	h := auth.MiddlewareWithStore(r.st, restauto.RegisterAutoConfig(http.NotFoundHandler(), engineOver(t, r.gamma), restauto.WithRouter(r.router)))
	tok := r.mint("contributor", "project:alpha")
	post := func(project string) (int, string) {
		req := httptest.NewRequest("POST", "/api/v1/mcp/auto-config?project="+project, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Body)
		return rec.Code, string(body)
	}
	if code, body := post("alpha"); code != http.StatusOK {
		t.Fatalf("granted auto-config = %d; got %s", code, body)
	}
	if code, body := post("beta"); code != http.StatusForbidden || !strings.Contains(body, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("auto-config on beta = %d, want 403; got %s", code, body)
	}
}

// TestProjectGrantNoTokenHTTPUnaffected pins the open-gate case over HTTP: a
// request with no token while no token is configured has no grant and reaches
// every registered project. The no-caller context itself is
// TestProjectGrantNoCallerContext.
func TestProjectGrantNoTokenHTTPUnaffected(t *testing.T) {
	r := newGrantRig(t)
	srv := newScopedMCPServer(engineOver(t, r.gamma), r.router)
	if body := callTool(srv.HTTPHandler(), "", "status", `{"project":"beta"}`); !strings.Contains(body, "elements") {
		t.Fatalf("a caller with no grant must reach beta; got %s", body)
	}
}
