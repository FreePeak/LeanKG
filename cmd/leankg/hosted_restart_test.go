package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/projects"
	"github.com/FreePeak/LeanKG/internal/rest"
	"github.com/FreePeak/LeanKG/internal/store"
)

// openHomeStore opens the home store over dataDir the way serve does, so a
// second call over the same directory is a restart.
func openHomeStore(t *testing.T, dataDir string) store.Backend {
	t.Helper()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{ProjectDir: dataDir, Engine: store.EngineSQLite, Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

// hostedRouterOver is the router serve builds in hosted mode, with no extra
// projects: registrations come from the home store's claims.
func hostedRouterOver(def string) *projects.Router {
	return projects.NewRouter(def, projects.Config{Mode: store.RW, EngineName: store.EngineSQLite, Hosted: true})
}

// elementsFor asks GET /api/v1/status?project=<sel> as bearer and returns the
// HTTP status and the element count (-1 when the body is not a status).
func elementsFor(t *testing.T, h http.Handler, sel, bearer string) (int, int) {
	t.Helper()
	code, body := restStatus(t, h, sel, bearer)
	var out struct {
		Elements int `json:"elements"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &out) != nil {
		return code, -1
	}
	return code, out.Elements
}

// TestHostedRestartRestoresClaimedProjects: projects registered before a restart
// route again after it, with no re-registration. The org token reaches its own
// project, the other org stays isolated, a project deleted before the restart
// stays gone, and a claim whose directory vanished does not break startup.
func TestHostedRestartRestoresClaimedProjects(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	def := filepath.Join(root, "default-repo")
	alpha := filepath.Join(root, "alpha-repo")
	beta := filepath.Join(root, "beta-repo")
	gone := filepath.Join(root, "gone-repo")
	gamma := filepath.Join(root, "gamma-repo")
	seedProjectDir(t, def, "DefaultSym")
	seedProjectDir(t, alpha, "AlphaSym")
	seedProjectDir(t, beta, "BetaSym", "BetaSym2")
	seedProjectDir(t, gone, "GoneSym")
	seedProjectDir(t, gamma, "GammaSym")
	defEng := engineOver(t, def)
	reg := portfolioreg.Options{Engine: store.EngineSQLite}

	// First run: an admin registers four projects over the admin route, then
	// deletes gamma. Then the process stops.
	home := openHomeStore(t, dataDir)
	admin, _, err := auth.Mint(ctx, home, auth.MintRequest{Name: "restart-admin", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	org1 := mintTokenViaRoute(t, home, admin, "restart-org-1", "org-1", "contributor")
	org2 := mintTokenViaRoute(t, home, admin, "restart-org-2", "org-2", "contributor")
	router1 := hostedRouterOver(def)
	admitted := projectAdminRoute(home, router1, reg)
	for _, body := range []string{
		`{"dir":` + jsonString(t, alpha) + `,"org_id":"org-1"}`,
		`{"dir":` + jsonString(t, beta) + `,"org_id":"org-2"}`,
		`{"dir":` + jsonString(t, gone) + `,"org_id":"org-1"}`,
		`{"dir":` + jsonString(t, gamma) + `,"org_id":"org-1"}`,
	} {
		if code, resp := sendJSON(t, admitted, http.MethodPost, "/api/v1/projects", admin, body); code != http.StatusOK {
			t.Fatalf("register = %d %s; want 200", code, resp)
		}
	}
	if code, resp := sendJSON(t, admitted, http.MethodDelete, "/api/v1/projects", admin,
		`{"dir":`+jsonString(t, gamma)+`}`); code != http.StatusOK {
		t.Fatalf("DELETE gamma = %d %s; want 200", code, resp)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	router1.Close()
	if err := home.Close(); err != nil {
		t.Fatal(err)
	}

	// Restart: the same data dir and store, no registration request.
	home2 := openHomeStore(t, dataDir)
	t.Cleanup(func() { _ = home2.Close() })
	router2 := hostedRouterOver(def)
	t.Cleanup(func() { _ = router2.Close() })
	if err := restoreClaimedProjects(ctx, home2, router2); err != nil {
		t.Fatalf("restore after restart: %v", err)
	}
	h := auth.MiddlewareWithStore(home2, routeByProject(router2, home2, nil, rest.Handler(defEng, nil),
		func(p *projects.Project) http.Handler { return rest.Handler(p.Engine, p.Memory) }))

	if code, n := elementsFor(t, h, filepath.Base(alpha), org1); code != http.StatusOK || n != 1 {
		t.Fatalf("org-1 on its own project after restart = %d elements %d; want 200 with 1 element", code, n)
	}
	if code, n := elementsFor(t, h, filepath.Base(beta), org2); code != http.StatusOK || n != 2 {
		t.Fatalf("org-2 on its own project after restart = %d elements %d; want 200 with 2 elements", code, n)
	}
	for _, c := range []struct{ name, sel, bearer string }{
		{"org-2 on org-1's project", filepath.Base(alpha), org2},
		{"org-1 on org-2's project", filepath.Base(beta), org1},
		{"org-1 on the removed directory", filepath.Base(gone), org1},
		{"org-1 on the deleted project", filepath.Base(gamma), org1},
	} {
		if code, _ := elementsFor(t, h, c.sel, c.bearer); code != http.StatusNotFound {
			t.Fatalf("%s = %d; want 404 as unknown", c.name, code)
		}
	}
}
