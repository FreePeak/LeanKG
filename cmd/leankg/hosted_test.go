package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/core"
	leankgmcp "github.com/FreePeak/LeanKG/internal/mcp"
	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/projects"
	"github.com/FreePeak/LeanKG/internal/rest"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestCheckHostedFlags: --hosted refuses the flags it cannot serve, and leaves
// every combination alone when it is off.
func TestCheckHostedFlags(t *testing.T) {
	ok := hostedOpts{rest: "127.0.0.1:8080"}
	cases := []struct {
		name   string
		hosted bool
		opts   hostedOpts
		refuse bool
	}{
		{"hosted, rest only", true, ok, false},
		{"hosted, read-only", true, hostedOpts{rest: ok.rest, readOnly: true}, true},
		{"hosted, memory-global", true, hostedOpts{rest: ok.rest, memoryGlobal: true}, true},
		{"hosted, ui", true, hostedOpts{rest: ok.rest, ui: "127.0.0.1:8081"}, true},
		{"hosted, rpc", true, hostedOpts{rest: ok.rest, rpc: "127.0.0.1:9090"}, true},
		{"hosted, stdio", true, hostedOpts{rest: ok.rest, stdio: true}, true},
		{"hosted, no rest", true, hostedOpts{}, true},
		{"not hosted, read-only", false, hostedOpts{readOnly: true, ui: "x", memoryGlobal: true}, false},
	}
	for _, c := range cases {
		err := checkHostedFlags(c.hosted, c.opts)
		if (err != nil) != c.refuse {
			t.Fatalf("%s: err = %v, refuse = %v", c.name, err, c.refuse)
		}
	}
}

// TestServeHostedNeedsATokenSource: a hosted serve with an open gate is refused
// before any listener starts. Nothing is opened, so no store file appears.
func TestServeHostedNeedsATokenSource(t *testing.T) {
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
	t.Setenv("LEANKG_TOKEN_VIEWER", "")
	dir := t.TempDir()
	err := serve(context.Background(), []string{"--hosted", "--rest", "127.0.0.1:0", "--project", dir})
	if err == nil || !strings.Contains(err.Error(), "token source") {
		t.Fatalf("serve --hosted with no token = %v; want the token-source refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".leankg", "leankg.db")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused hosted serve must not create the store: %v", statErr)
	}
}

// TestHostedRoutedStoresAreReadOnlyAndHomeIsWritable: in hosted mode a routed
// project's store refuses a graph write, while the home store still mints
// tokens and the admin API still registers a project.
func TestHostedRoutedStoresAreReadOnlyAndHomeIsWritable(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	alpha := t.TempDir()
	beta := t.TempDir()
	seedProjectDir(t, alpha, "AlphaSym")

	home, err := store.OpenBackend(ctx, store.OpenOptions{ProjectDir: t.TempDir(), Engine: store.EngineSQLite, Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = home.Close() })
	if err := home.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	admin, _, err := auth.Mint(ctx, home, auth.MintRequest{Name: "ops", Role: "admin"})
	if err != nil {
		t.Fatalf("home store must mint tokens in hosted mode: %v", err)
	}

	// Mode RW is asked for, yet hosted forces the routed stores read-only.
	router := projects.NewRouter(t.TempDir(), projects.Config{
		ExtraDirs: []string{alpha}, Mode: store.RW, EngineName: store.EngineSQLite, Hosted: true,
	})
	defer router.Close()
	if !router.Hosted() {
		t.Fatal("router must report hosted")
	}
	p, release, err := router.Open(ctx, alpha)
	if err != nil {
		t.Fatalf("open routed project: %v", err)
	}
	defer release()
	routed, ok := p.Store.(store.Backend)
	if !ok {
		t.Fatalf("routed store %T is not a store.Backend", p.Store)
	}
	if routed.Mode() != store.RO {
		t.Fatalf("routed store mode = %v, want read-only", routed.Mode())
	}
	if err := routed.UpsertElements(ctx, []store.Element{{QualifiedName: "pkg.Write", ElementType: "func", Name: "Write", FilePath: "w.go", LineStart: 1, LineEnd: 1, Language: "go"}}); err == nil {
		t.Fatal("a graph write to a routed project in hosted mode must be refused")
	}

	reg := portfolioreg.Options{Engine: store.EngineSQLite}
	register := projectAdminRoute(home, router, reg)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects",
		bytes.NewBufferString(`{"dir":"`+beta+`","org_id":"org-1"}`))
	req.Header.Set("Authorization", "Bearer "+admin)
	rec := httptest.NewRecorder()
	register.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("project registration in hosted mode = %d %s; want 200", rec.Code, rec.Body.String())
	}
}

// TestHostedRefusesAnOmittedProject: REST and MCP both refuse a call that names
// no project, instead of answering from a default.
func TestHostedRefusesAnOmittedProject(t *testing.T) {
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
	t.Setenv("LEANKG_TOKEN_VIEWER", "")
	def := t.TempDir()
	seedProjectDir(t, def, "DefaultSym")
	defEng := engineOver(t, def)
	router := projects.NewRouter(def, projects.Config{Mode: store.RW, EngineName: store.EngineSQLite, Hosted: true})
	router.SeedDefault(&projects.Project{Dir: def, Name: filepath.Base(def), Engine: defEng})
	defer router.Close()

	h := routeByProject(router, nil, nil, rest.Handler(defEng, nil), func(p *projects.Project) http.Handler {
		return rest.Handler(p.Engine, p.Memory)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "must name its project") {
		t.Fatalf("hosted REST without project = %d %s; want 400 naming the project", rec.Code, rec.Body.String())
	}

	srv := leankgmcp.New(defEng, leankgmcp.WithProjectRequired(true))
	srv.SetProjectRouter(router)
	body := mcpStatus(t, srv.HTTPHandler(), `{}`, "")
	if !strings.Contains(body, "LEANKG_ERROR_MISSING_PARAM") {
		t.Fatalf("hosted MCP without project must be refused, got %s", body)
	}
}

// TestHostedEngineRefusesPortfolioFanOut: a hosted engine answers no portfolio
// query, so no call can fan out across the registered projects.
func TestHostedEngineRefusesPortfolioFanOut(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	dir := t.TempDir()
	seedProjectDir(t, dir, "Sym")
	st, err := store.OpenBackend(ctx, store.OpenOptions{ProjectDir: dir, Engine: store.EngineSQLite, Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	eng := core.New(st, core.Options{ProjectDir: dir, Hosted: true})
	if _, err := eng.QueryAnswer(ctx, core.QueryRequest{Action: "portfolio", Query: "sym"}); err == nil || !strings.Contains(err.Error(), "hosted mode") {
		t.Fatalf("hosted portfolio query = %v; want the hosted-mode refusal", err)
	}
}

// TestAutoConfigRefusesAnOrgScopedToken: the auto-config route resolves its
// project without the org scope, so an org token gets 403 there, while a token
// with no org still reaches it.
func TestAutoConfigRefusesAnOrgScopedToken(t *testing.T) {
	f := newOrgFixture(t)
	reached := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := gateAutoConfig(f.home, reached, reached)
	call := func(bearer string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/auto-config", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := call(f.org1); code != http.StatusForbidden {
		t.Fatalf("org token on auto-config = %d; want 403", code)
	}
	if code := call(f.legacy); code != http.StatusOK {
		t.Fatalf("token with no org on auto-config = %d; want 200", code)
	}
}
