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
	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/projects"
	restauto "github.com/FreePeak/LeanKG/internal/rest/auto"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestProjectGrantNoSelector pins the default project on the routed REST and
// auto-config listeners. A request with no ?project= serves the default
// project (gamma in the rig), so a token scoped to another project is refused
// there too; a token scoped to the default or unscoped is served.
func TestProjectGrantNoSelector(t *testing.T) {
	r := newGrantRig(t)
	rest := r.restHandler()
	autoCfg := auth.MiddlewareWithStore(r.st, restauto.RegisterAutoConfig(http.NotFoundHandler(), engineOver(t, r.gamma), restauto.WithRouter(r.router)))
	autoConfig := func(bearer string) (int, string) {
		req := httptest.NewRequest("POST", "/api/v1/mcp/auto-config", strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		autoCfg.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Body)
		return rec.Code, string(body)
	}

	if code, body := get(t, rest, r.mint("viewer", "project:alpha"), "/api/v1/status"); code != http.StatusForbidden || !strings.Contains(body, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("REST no selector, token scoped to alpha (default is gamma) = %d, want 403 PermissionDenied; got %s", code, body)
	}
	if code, body := get(t, rest, r.mint("viewer", "project:gamma"), "/api/v1/status"); code != http.StatusOK || !strings.Contains(body, `"elements"`) {
		t.Fatalf("REST no selector, token scoped to the default = %d, want 200; got %s", code, body)
	}
	if code, body := get(t, rest, r.mint("admin"), "/api/v1/status"); code != http.StatusOK || !strings.Contains(body, `"elements"`) {
		t.Fatalf("REST no selector, unscoped token = %d, want 200; got %s", code, body)
	}

	if code, body := autoConfig(r.mint("contributor", "project:alpha")); code != http.StatusForbidden || !strings.Contains(body, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("auto-config no selector, token scoped to alpha = %d, want 403 PermissionDenied; got %s", code, body)
	}
	if code, body := autoConfig(r.mint("contributor", "project:gamma")); code != http.StatusOK {
		t.Fatalf("auto-config no selector, token scoped to the default = %d, want 200; got %s", code, body)
	}
	if code, body := autoConfig(r.mint("contributor")); code != http.StatusOK {
		t.Fatalf("auto-config no selector, unscoped token = %d, want 200; got %s", code, body)
	}
}

// TestProjectGrantNoSelectorMCP confirms the MCP listener refuses an omitted
// selector when one project is registered: the default engine is served with
// no router check of its own, so the grant must apply to it there.
func TestProjectGrantNoSelectorMCP(t *testing.T) {
	r := newGrantRig(t).withOneProject(t)
	h := r.mcpHandler()

	if body := callTool(h, r.mint("viewer", "project:alpha"), "status", `{}`); !strings.Contains(body, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("no selector, token scoped to alpha (default is gamma) must be refused; got %s", body)
	}
	if body := callTool(h, r.mint("viewer", "project:gamma"), "status", `{}`); !strings.Contains(body, "elements") {
		t.Fatalf("no selector, token scoped to the default must answer; got %s", body)
	}
	if body := callTool(h, r.mint("admin"), "status", `{}`); !strings.Contains(body, "elements") {
		t.Fatalf("no selector, unscoped token must answer; got %s", body)
	}
}

// TestProjectGrantNoCallerContext is the stdio case at the router: a context
// with no caller (no gate ran) carries no grant, so every registered project,
// the default included, is reachable.
func TestProjectGrantNoCallerContext(t *testing.T) {
	r := newGrantRig(t)
	for _, sel := range []string{"", "alpha", "beta"} {
		if _, err := r.router.EngineFor(context.Background(), sel); err != nil {
			t.Fatalf("no-caller EngineFor(%q) = %v, want an engine", sel, err)
		}
	}
}

// TestProjectGrantMCPSelectorNeedsBearer pins the 401 on the MCP listener: with
// the gate on, ?project= without a bearer is refused before any project opens.
func TestProjectGrantMCPSelectorNeedsBearer(t *testing.T) {
	r := newGrantRig(t)
	r.mint("viewer") // a live token turns the gate on
	req := httptest.NewRequest("POST", "/mcp?project=alpha", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":{}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	r.mcpHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("?project= with no bearer and the gate on = %d, want 401; got %s", rec.Code, rec.Body.String())
	}
}

// dashboardSearch is a dashboard data read that resolves the default project:
// GammaSym is seeded in gamma, the default.
const dashboardSearch = "/api/search?q=GammaSym"

// TestProjectGrantDashboardAPI pins the grant on the dashboard listener. The
// /api/ data routes resolve the caller and apply the grant to the default
// project; the static shell stays public with the gate on.
func TestProjectGrantDashboardAPI(t *testing.T) {
	r := newGrantRig(t)
	dash := dashboardHandler(r.st, "127.0.0.1:8081", r.router, engineOver(t, r.gamma), nil)

	if code, body := get(t, dash, "", dashboardSearch); code != http.StatusOK || !strings.Contains(body, "GammaSym") {
		t.Fatalf("gate off, no token: dashboard /api/ = %d, want 200 with the default project; got %s", code, body)
	}

	r.mint("viewer") // a live token turns the gate on
	if code, body := get(t, dash, "", dashboardSearch); code != http.StatusUnauthorized {
		t.Fatalf("gate on, no token: dashboard /api/ = %d, want 401; got %s", code, body)
	}
	if code, body := get(t, dash, "not-a-token", dashboardSearch); code != http.StatusUnauthorized {
		t.Fatalf("gate on, unknown token: dashboard /api/ = %d, want 401; got %s", code, body)
	}
	if code, body := get(t, dash, r.mint("viewer", "project:alpha"), dashboardSearch); code != http.StatusForbidden || !strings.Contains(body, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("token scoped to alpha (default is gamma): dashboard /api/ = %d, want 403 PermissionDenied; got %s", code, body)
	}
	if code, body := get(t, dash, r.mint("viewer", "project:gamma"), dashboardSearch); code != http.StatusOK || !strings.Contains(body, "GammaSym") {
		t.Fatalf("token scoped to the default: dashboard /api/ = %d, want 200; got %s", code, body)
	}
	if code, body := get(t, dash, r.mint("viewer"), dashboardSearch); code != http.StatusOK || !strings.Contains(body, "GammaSym") {
		t.Fatalf("unscoped token: dashboard /api/ = %d, want 200; got %s", code, body)
	}
	if code, body := get(t, dash, "", "/"); code != http.StatusOK || body == "" {
		t.Fatalf("gate on, no token: static shell = %d, want 200 (public); got %d bytes", code, len(body))
	}
}

// switchProject posts the dashboard's project switch for path, with an optional
// bearer and an optional ?project= selector (the project the switch is made
// from), and returns the status and body.
func switchProject(h http.Handler, bearer, from, path string) (int, string) {
	target := "/api/project/switch"
	if from != "" {
		target += "?project=" + from
	}
	body, _ := json.Marshal(map[string]string{"path": path})
	req := httptest.NewRequest("POST", target, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out, _ := io.ReadAll(rec.Body)
	return rec.Code, string(out)
}

// TestProjectGrantDashboardSwitch pins the grant on the dashboard's project
// switch, which names a project by path. A token scoped to alpha may not switch
// to beta (registered but withheld) or to an unregistered directory, and the
// refusal carries no project path or name beyond the path the caller sent.
func TestProjectGrantDashboardSwitch(t *testing.T) {
	r := newGrantRig(t)
	dash := dashboardHandler(r.st, "127.0.0.1:8081", r.router, engineOver(t, r.gamma), nil)
	unregistered := t.TempDir()

	if code, body := switchProject(dash, "", "", r.beta); code != http.StatusOK {
		t.Fatalf("gate off: switch to beta = %d, want 200; got %s", code, body)
	}

	r.mint("viewer") // a live token turns the gate on
	alphaTok := r.mint("viewer", "project:alpha")
	for _, denied := range []string{r.beta, unregistered} {
		code, body := switchProject(dash, alphaTok, "alpha", denied)
		if code != http.StatusForbidden || !strings.Contains(body, "LEANKG_ERROR_PERMISSION_DENIED") {
			t.Fatalf("token scoped to alpha: switch to %s = %d, want 403 PermissionDenied; got %s", denied, code, body)
		}
		if rest := strings.ReplaceAll(body, denied, ""); strings.Contains(rest, "beta") || strings.Contains(rest, "project_path") {
			t.Fatalf("refusal names or locates a withheld project beyond the caller's own path: %s", body)
		}
	}
	if code, body := switchProject(dash, r.mint("viewer", "project:gamma"), "", r.beta); code != http.StatusForbidden || strings.Contains(body, "project_path") {
		t.Fatalf("token scoped to the default (gamma): switch to beta = %d, want 403 with no project_path; got %s", code, body)
	}

	betaCanon, err := filepath.EvalSymlinks(r.beta)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := switchProject(dash, alphaTok, "alpha", r.alpha); code != http.StatusOK || !strings.Contains(body, "project_path") {
		t.Fatalf("token scoped to alpha: switch to alpha = %d, want 200 with project_path; got %s", code, body)
	}
	if code, body := switchProject(dash, r.mint("viewer"), "", r.beta); code != http.StatusOK || !strings.Contains(body, betaCanon) {
		t.Fatalf("unscoped token: switch to beta = %d, want 200 naming beta; got %s", code, body)
	}
}

// rpcCall posts one ConnectRPC unary JSON call and returns the status and body.
func rpcCall(h http.Handler, bearer, procedure, body string) (int, string) {
	req := httptest.NewRequest("POST", procedure, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out, _ := io.ReadAll(rec.Body)
	return rec.Code, string(out)
}

// rpcQuery is the ConnectRPC Query procedure.
const rpcQuery = "/leankg.v1.LeanKG/Query"

// TestProjectGrantRPC pins the grant on the ConnectRPC listener, which serves
// the default project with no selector of its own.
func TestProjectGrantRPC(t *testing.T) {
	r := newGrantRig(t)
	h := rpcHandler(r.st, r.router, engineOver(t, r.gamma), nil)
	search := `{"action":"search","query":"GammaSym"}`

	if code, body := rpcCall(h, "", rpcQuery, search); code != http.StatusOK || !strings.Contains(body, "GammaSym") {
		t.Fatalf("gate off, no token: rpc Query = %d, want 200 with the default project; got %s", code, body)
	}

	r.mint("viewer") // a live token turns the gate on
	if code, body := rpcCall(h, "", rpcQuery, search); code != http.StatusUnauthorized {
		t.Fatalf("gate on, no token: rpc Query = %d, want 401; got %s", code, body)
	}
	if code, body := rpcCall(h, r.mint("viewer", "project:alpha"), rpcQuery, search); code != http.StatusForbidden || !strings.Contains(body, "LEANKG_ERROR_PERMISSION_DENIED") {
		t.Fatalf("token scoped to alpha (default is gamma): rpc Query = %d, want 403 PermissionDenied; got %s", code, body)
	}
	if code, body := rpcCall(h, r.mint("viewer", "project:gamma"), rpcQuery, search); code != http.StatusOK || !strings.Contains(body, "GammaSym") {
		t.Fatalf("token scoped to the default: rpc Query = %d, want 200; got %s", code, body)
	}
	if code, body := rpcCall(h, r.mint("viewer"), rpcQuery, search); code != http.StatusOK || !strings.Contains(body, "GammaSym") {
		t.Fatalf("unscoped token: rpc Query = %d, want 200; got %s", code, body)
	}
}

// TestProjectGrantRPCPortfolio pins that the ConnectRPC portfolio fan-out
// applies the caller's grant: the identity the gate stores in the request
// context reaches the fan-out, so a withheld project is neither named nor
// searched.
func TestProjectGrantRPCPortfolio(t *testing.T) {
	r := newGrantRig(t)
	regDB := filepath.Join(t.TempDir(), "portfolio.db")
	t.Setenv(portfolioreg.DBPathEnv, regDB)
	opts := portfolioreg.Options{DBPath: regDB}
	for _, p := range []struct{ dir, name string }{{r.gamma, "gamma"}, {r.alpha, "alpha"}, {r.beta, "beta"}} {
		if _, err := portfolioreg.Register(context.Background(), opts, p.dir, p.name, 1, 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	h := rpcHandler(r.st, r.router, engineOver(t, r.gamma), nil)
	portfolio := `{"action":"portfolio","query":"Sym"}`
	r.mint("viewer") // a live token turns the gate on

	code, body := rpcCall(h, r.mint("viewer", "project:gamma"), rpcQuery, portfolio)
	if code != http.StatusOK {
		t.Fatalf("gamma-scoped portfolio = %d, want 200; got %s", code, body)
	}
	answer := rpcAnswer(t, body)
	if answer["projects_withheld"] != float64(2) || answer["projects_served"] != float64(1) {
		t.Fatalf("gamma-scoped portfolio withheld %v served %v, want 2 and 1", answer["projects_withheld"], answer["projects_served"])
	}
	for _, leak := range []string{"AlphaSym", "BetaSym"} {
		if strings.Contains(body, leak) {
			t.Fatalf("gamma-scoped portfolio reached a withheld project (%s): %s", leak, body)
		}
	}
	if !strings.Contains(body, "GammaSym") {
		t.Fatalf("gamma-scoped portfolio must answer from gamma: %s", body)
	}

	_, body = rpcCall(h, r.mint("viewer"), rpcQuery, portfolio)
	if answer := rpcAnswer(t, body); answer["projects_served"] != float64(3) {
		t.Fatalf("unscoped portfolio served %v, want 3", answer["projects_served"])
	}
}

// rpcAnswer unwraps a ConnectRPC QueryResponse, whose json field carries the
// answer as a JSON string, and returns the decoded answer.
func rpcAnswer(t *testing.T, body string) map[string]any {
	t.Helper()
	var wire struct {
		JSON string `json:"json"`
	}
	if err := json.Unmarshal([]byte(body), &wire); err != nil {
		t.Fatalf("rpc response is not a QueryResponse: %v; got %s", err, body)
	}
	var answer map[string]any
	if err := json.Unmarshal([]byte(wire.JSON), &answer); err != nil {
		t.Fatalf("rpc answer is not JSON: %v; got %s", err, wire.JSON)
	}
	return answer
}

// withOneProject returns the rig with only its default project registered.
func (r grantRig) withOneProject(t *testing.T) grantRig {
	t.Helper()
	r.router = projects.NewRouter(r.gamma, projects.Config{Mode: store.RW, EngineName: "sqlite"})
	r.router.SeedDefault(&projects.Project{Dir: r.gamma, Name: "gamma", Engine: engineOver(t, r.gamma)})
	t.Cleanup(func() { _ = r.router.Close() })
	return r
}

// TestProjectGrantSwitchNoExistenceOracle pins that a restricted token cannot
// tell a missing directory from an existing one it is not granted: both get the
// same 403 refusal, differing only in the path the caller sent. An unrestricted
// caller still gets 404 for a missing directory.
func TestProjectGrantSwitchNoExistenceOracle(t *testing.T) {
	r := newGrantRig(t)
	dash := dashboardHandler(r.st, "127.0.0.1:8081", r.router, engineOver(t, r.gamma), nil)
	missing := filepath.Join(t.TempDir(), "missing")
	existing := t.TempDir()
	r.mint("viewer") // a live token turns the gate on
	alphaTok := r.mint("viewer", "project:alpha")

	mCode, mBody := switchProject(dash, alphaTok, "alpha", missing)
	eCode, eBody := switchProject(dash, alphaTok, "alpha", existing)
	if mCode != http.StatusForbidden || eCode != http.StatusForbidden {
		t.Fatalf("token scoped to alpha: switch missing = %d, existing = %d; want both 403", mCode, eCode)
	}
	if strings.ReplaceAll(mBody, missing, "PATH") != strings.ReplaceAll(eBody, existing, "PATH") {
		t.Fatalf("missing and existing ungranted paths answer differently:\n%s\n%s", mBody, eBody)
	}
	if code, body := switchProject(dash, r.mint("viewer"), "", missing); code != http.StatusNotFound {
		t.Fatalf("unscoped token: switch to a missing directory = %d, want 404; got %s", code, body)
	}
}

// TestProjectGrantRPCHealth pins /health on the ConnectRPC listener as a
// liveness probe outside the project grant: a token scoped to a project other
// than the default gets 200, the bearer requirement still holds, and the
// service stays behind the grant.
func TestProjectGrantRPCHealth(t *testing.T) {
	r := newGrantRig(t)
	h := rpcHandler(r.st, r.router, engineOver(t, r.gamma), nil)
	r.mint("viewer") // a live token turns the gate on

	if code, body := get(t, h, "", "/health"); code != http.StatusUnauthorized {
		t.Fatalf("gate on, no token: rpc /health = %d, want 401; got %s", code, body)
	}
	if code, body := get(t, h, r.mint("viewer", "project:alpha"), "/health"); code != http.StatusOK || body != `{"ok":true}` {
		t.Fatalf("token scoped to alpha (default is gamma): rpc /health = %d, want 200 {\"ok\":true}; got %s", code, body)
	}
	if code, body := rpcCall(h, r.mint("viewer", "project:alpha"), rpcQuery, `{"action":"search","query":"GammaSym"}`); code != http.StatusForbidden {
		t.Fatalf("token scoped to alpha: rpc Query = %d, want 403; got %s", code, body)
	}
}
