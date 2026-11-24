package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/core"
	leankgmcp "github.com/FreePeak/LeanKG/internal/mcp"
	"github.com/FreePeak/LeanKG/internal/projects"
	"github.com/FreePeak/LeanKG/internal/rest"
	"github.com/FreePeak/LeanKG/internal/store"
)

// orgFixture is a hosted-style serve over three indexed projects: a default
// project nobody claims, alpha claimed by org-1 and beta claimed by org-2. It
// mints an org-1 token carrying org_id only (no account), and a legacy token
// with no org, which must keep seeing every project.
type orgFixture struct {
	router     *projects.Router
	home       store.Backend
	defaultEng *core.Engine
	alpha      string
	beta       string
	org1       string
	legacy     string
}

func newOrgFixture(t *testing.T) orgFixture {
	t.Helper()
	ctx := context.Background()
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
	t.Setenv("LEANKG_TOKEN_VIEWER", "")

	// Named directories (not bare temp numbers) so the selector matrix can
	// exercise letter case and percent-encoded letters.
	root := t.TempDir()
	defaultDir, alpha, beta := filepath.Join(root, "default-repo"), filepath.Join(root, "alpha-repo"), filepath.Join(root, "beta-repo")
	seedProjectDir(t, defaultDir, "DefaultSym")
	seedProjectDir(t, alpha, "AlphaSym")
	seedProjectDir(t, beta, "BetaSym", "BetaSym2")
	defaultEng := engineOver(t, defaultDir)

	home, err := store.OpenBackend(ctx, store.OpenOptions{ProjectDir: t.TempDir(), Engine: "sqlite", Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = home.Close() })
	if err := home.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := auth.ClaimResource(ctx, home, auth.ResourceProject, realOrSelf(alpha), "org:org-1", "org-1"); err != nil {
		t.Fatal(err)
	}
	if err := auth.ClaimResource(ctx, home, auth.ResourceProject, realOrSelf(beta), "org:org-2", "org-2"); err != nil {
		t.Fatal(err)
	}
	org1, _, err := auth.Mint(ctx, home, auth.MintRequest{Name: "cp-org1", Role: "contributor", OrgID: "org-1"})
	if err != nil {
		t.Fatal(err)
	}
	legacy, _, err := auth.Mint(ctx, home, auth.MintRequest{Name: "legacy", Role: "viewer"})
	if err != nil {
		t.Fatal(err)
	}

	router := projects.NewRouter(defaultDir, projects.Config{
		ExtraDirs: []string{alpha, beta}, Mode: store.RW, EngineName: "sqlite",
	})
	router.SeedDefault(&projects.Project{Dir: defaultDir, Name: filepath.Base(defaultDir), Engine: defaultEng})
	t.Cleanup(func() { _ = router.Close() })
	return orgFixture{router: router, home: home, defaultEng: defaultEng,
		alpha: alpha, beta: beta, org1: org1, legacy: legacy}
}

// restHandler is the REST listener as serve mounts it: the bearer gate, then
// the project routing.
func (f orgFixture) restHandler() http.Handler { return f.restHandlerOver(f.router) }

// restHandlerOver is restHandler over the given router.
func (f orgFixture) restHandlerOver(router *projects.Router) http.Handler {
	return auth.MiddlewareWithStore(f.home, routeByProject(router, f.home, nil, rest.Handler(f.defaultEng, nil),
		func(p *projects.Project) http.Handler { return rest.Handler(p.Engine, p.Memory) }))
}

// get issues one status request for selector with bearer as the caller.
func (f orgFixture) get(t *testing.T, h http.Handler, selector, bearer string) (int, string) {
	t.Helper()
	target := "/api/v1/status"
	if selector != "" {
		target += "?project=" + selector
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

// blankSelector removes the caller's own selector from a refusal body, so
// refusals for different selectors can be compared.
func blankSelector(body, selector string) string {
	return strings.ReplaceAll(body, selector, "<selector>")
}

// TestOrgTokenSeesOnlyItsOwnProjectOverREST: an org-1 token reads its own
// project by name and by path. Another org's project, by name or by path, gets
// the same 404 body as a project that does not exist.
func TestOrgTokenSeesOnlyItsOwnProjectOverREST(t *testing.T) {
	f := newOrgFixture(t)
	h := f.restHandler()
	alphaName, betaName := filepath.Base(f.alpha), filepath.Base(f.beta)

	if code, body := f.get(t, h, alphaName, f.org1); code != http.StatusOK {
		t.Fatalf("own project by name = %d %s; want 200", code, body)
	}
	if code, body := f.get(t, h, realOrSelf(f.alpha), f.org1); code != http.StatusOK {
		t.Fatalf("own project by path = %d %s; want 200", code, body)
	}

	unknownSel := "does-not-exist"
	codeU, bodyU := f.get(t, h, unknownSel, f.org1)
	if codeU != http.StatusNotFound {
		t.Fatalf("unknown project = %d; want 404", codeU)
	}
	for _, sel := range []string{betaName, realOrSelf(f.beta), ""} {
		code, body := f.get(t, h, sel, f.org1)
		if code != http.StatusNotFound {
			t.Fatalf("org-1 reached %q with %d %s; want 404", sel, code, body)
		}
		if rest := blankSelector(body, sel); strings.Contains(rest, betaName+" (") || strings.Contains(rest, realOrSelf(f.beta)) {
			t.Fatalf("refusal names another org's project: %s", body)
		}
		if sel != "" && blankSelector(body, sel) != blankSelector(bodyU, unknownSel) {
			t.Fatalf("refusal for %q = %s, want the unknown-project body %s", sel, body, bodyU)
		}
	}
}

// TestLegacyTokenKeepsEveryProject: a token with no org is unscoped, so the
// single-user behaviour is unchanged.
func TestLegacyTokenKeepsEveryProject(t *testing.T) {
	f := newOrgFixture(t)
	h := f.restHandler()
	if code, body := f.get(t, h, filepath.Base(f.beta), f.legacy); code != http.StatusOK {
		t.Fatalf("legacy token on another project = %d %s; want 200", code, body)
	}
}

// mcpStatus posts one status tools/call over the MCP HTTP handler with the
// given arguments and bearer.
func mcpStatus(t *testing.T, h http.Handler, args, bearer string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":`+args+`}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Body.String()
}

// TestOrgTokenIsScopedOverMCP: the MCP tool argument `project` is held to the
// same rule as the REST selector: another org's project answers exactly like
// an unknown one, by name and by path, and the caller's own project answers.
func TestOrgTokenIsScopedOverMCP(t *testing.T) {
	f := newOrgFixture(t)
	srv := leankgmcp.New(f.defaultEng, leankgmcp.WithProjectRequired(true))
	srv.SetProjectRouter(f.router)
	srv.SetAuthStore(f.home)
	h := srv.HTTPHandler()

	own := mcpStatus(t, h, `{"project":"`+filepath.Base(f.alpha)+`"}`, f.org1)
	if !strings.Contains(own, `\"elements\":1`) {
		t.Fatalf("own project over MCP must answer alpha's status: %s", own)
	}

	unknown := mcpStatus(t, h, `{"project":"does-not-exist"}`, f.org1)
	if !strings.Contains(unknown, "unknown project") {
		t.Fatalf("unknown project over MCP = %s", unknown)
	}
	for _, sel := range []string{filepath.Base(f.beta), realOrSelf(f.beta)} {
		body := mcpStatus(t, h, `{"project":`+jsonString(t, sel)+`}`, f.org1)
		if blankSelector(body, sel) != blankSelector(unknown, "does-not-exist") {
			t.Fatalf("MCP refusal for %q = %s, want the unknown-project answer %s", sel, body, unknown)
		}
	}
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
