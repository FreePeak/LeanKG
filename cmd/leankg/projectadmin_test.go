package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/projects"
	"github.com/FreePeak/LeanKG/internal/store"
)

type adminFixture struct {
	handler  http.Handler
	router   *projects.Router
	st       store.Backend
	registry portfolioreg.Options
	admin    string
	viewer   string
	alpha    string
}

func newAdminFixture(t *testing.T) adminFixture {
	t.Helper()
	ctx := context.Background()
	t.Setenv("LEANKG_PORTFOLIO_DB", filepath.Join(t.TempDir(), "portfolio.db"))
	st, err := store.OpenBackend(ctx, store.OpenOptions{ProjectDir: t.TempDir(), Engine: store.EngineSQLite, Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	admin, _, err := auth.Mint(ctx, st, auth.MintRequest{Name: "ops", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	viewer, _, err := auth.Mint(ctx, st, auth.MintRequest{Name: "ro", Role: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	registry := portfolioreg.Options{Engine: store.EngineSQLite}
	router := projects.NewRouter(t.TempDir(), projects.Config{})
	return adminFixture{
		handler:  projectAdminRoute(st, router, registry),
		router:   router,
		st:       st,
		registry: registry,
		admin:    admin,
		viewer:   viewer,
		alpha:    t.TempDir(),
	}
}

func (f adminFixture) call(t *testing.T, method, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f adminFixture) registeredDirs(t *testing.T) []string {
	t.Helper()
	recs, err := portfolioreg.Projects(context.Background(), f.registry)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, r := range recs {
		dirs = append(dirs, r.Dir)
	}
	return dirs
}

// TestProjectAdminRequiresAdmin: the route is Admin-only, so a viewer token is
// refused before anything is registered.
func TestProjectAdminRequiresAdmin(t *testing.T) {
	f := newAdminFixture(t)
	body := `{"dir":"` + f.alpha + `","org_id":"org-1"}`
	if rec := f.call(t, http.MethodPost, f.viewer, body); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer POST = %d, want 403", rec.Code)
	}
	if rec := f.call(t, http.MethodPost, "", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous POST = %d, want 401", rec.Code)
	}
	if got := f.router.List(); len(got) != 1 {
		t.Fatalf("refused registration changed the router: %v", got)
	}
}

// TestProjectAdminRegistersAndUnregistersInStep: one POST routes the project,
// claims it for the org and writes the registry; one DELETE undoes all three.
func TestProjectAdminRegistersAndUnregistersInStep(t *testing.T) {
	ctx := context.Background()
	f := newAdminFixture(t)
	body := `{"dir":"` + f.alpha + `","org_id":"org-1"}`
	canon, _ := portfolioreg.Canonical(f.alpha)

	if rec := f.call(t, http.MethodPost, f.admin, body); rec.Code != http.StatusOK {
		t.Fatalf("POST = %d body %s", rec.Code, rec.Body.String())
	}
	if got := f.router.List(); !slices.Contains(got, canon) {
		t.Fatalf("router after POST = %v, want %s", got, canon)
	}
	if ids, _ := f.st.ResourceIDs(ctx, auth.ResourceProject, "org-1"); !slices.Equal(ids, []string{canon}) {
		t.Fatalf("org-1 claims = %v, want [%s]", ids, canon)
	}
	if dirs := f.registeredDirs(t); !slices.Contains(dirs, canon) {
		t.Fatalf("registry after POST = %v, want %s", dirs, canon)
	}

	// Re-registering for the same org is idempotent.
	if rec := f.call(t, http.MethodPost, f.admin, body); rec.Code != http.StatusOK {
		t.Fatalf("second POST = %d", rec.Code)
	}

	if rec := f.call(t, http.MethodDelete, f.admin, `{"dir":"`+f.alpha+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d body %s", rec.Code, rec.Body.String())
	}
	if got := f.router.List(); slices.Contains(got, canon) {
		t.Fatalf("router after DELETE still routes %s", canon)
	}
	if ids, _ := f.st.ResourceIDs(ctx, auth.ResourceProject, "org-1"); len(ids) != 0 {
		t.Fatalf("claims after DELETE = %v, want none", ids)
	}
	if dirs := f.registeredDirs(t); slices.Contains(dirs, canon) {
		t.Fatalf("registry after DELETE still lists %s", canon)
	}
}

// TestProjectAdminRefusesASecondOrg: a project belongs to one org. A second
// org claiming it is refused, and the first org keeps it.
func TestProjectAdminRefusesASecondOrg(t *testing.T) {
	ctx := context.Background()
	f := newAdminFixture(t)
	if rec := f.call(t, http.MethodPost, f.admin, `{"dir":"`+f.alpha+`","org_id":"org-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("first POST = %d", rec.Code)
	}
	rec := f.call(t, http.MethodPost, f.admin, `{"dir":"`+f.alpha+`","org_id":"org-2"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second org POST = %d, want 409", rec.Code)
	}
	canon, _ := portfolioreg.Canonical(f.alpha)
	if ids, _ := f.st.ResourceIDs(ctx, auth.ResourceProject, "org-2"); len(ids) != 0 {
		t.Fatalf("org-2 claimed %v despite the 409", ids)
	}
	if orgs, _ := f.st.ResourceOrgs(ctx, auth.ResourceProject, canon); !slices.Equal(orgs, []string{"org-1"}) {
		t.Fatalf("claiming orgs = %v, want [org-1]", orgs)
	}
}

// TestProjectAdminValidatesInput: a missing org, a missing or non-directory
// path, and removing the serving process's own project are all refused.
func TestProjectAdminValidatesInput(t *testing.T) {
	f := newAdminFixture(t)
	file := filepath.Join(f.alpha, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, method, body string }{
		{"no org", http.MethodPost, `{"dir":"` + f.alpha + `"}`},
		{"no dir", http.MethodPost, `{"org_id":"org-1"}`},
		{"missing dir", http.MethodPost, `{"dir":"` + filepath.Join(f.alpha, "nope") + `","org_id":"org-1"}`},
		{"a file", http.MethodPost, `{"dir":"` + file + `","org_id":"org-1"}`},
		{"remove default", http.MethodDelete, `{"dir":"` + f.router.Default() + `"}`},
		{"unknown method", http.MethodPut, `{}`},
	}
	for _, c := range cases {
		if rec := f.call(t, c.method, f.admin, c.body); rec.Code == http.StatusOK {
			t.Fatalf("%s: got 200, want a refusal", c.name)
		}
	}
	if got := f.router.List(); len(got) != 1 {
		t.Fatalf("refused requests changed the router: %v", got)
	}
}
