package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func reqWith(token string) *http.Request {
	r := httptest.NewRequest("POST", "/mcp", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

// TestNoTokensConfiguredIsAdmin pins the local default: with no tokens set,
// everything is allowed (single-user laptop posture).
func TestNoTokensConfiguredIsAdmin(t *testing.T) {
	for _, k := range []string{"LEANKG_TOKEN_ADMIN", "LEANKG_TOKEN_CONTRIBUTOR", "LEANKG_TOKEN_VIEWER"} {
		t.Setenv(k, "")
	}
	role, err := RoleForRequest(reqWith(""))
	if err != nil || role != Admin {
		t.Fatalf("role=%v err=%v, want Admin", role, err)
	}
	if !AllowedTool(role, "import") {
		t.Fatal("admin must be allowed to write")
	}
}

// TestTokensEnforced pins the gated behavior: missing/unknown tokens are
// rejected, viewer cannot write, contributor can.
func TestTokensEnforced(t *testing.T) {
	t.Setenv("LEANKG_TOKEN_ADMIN", "admin-tok")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "contrib-tok")
	t.Setenv("LEANKG_TOKEN_VIEWER", "viewer-tok")

	if _, err := RoleForRequest(reqWith("")); err == nil {
		t.Fatal("missing token must be rejected when tokens are configured")
	}
	if _, err := RoleForRequest(reqWith("nope")); err == nil {
		t.Fatal("unknown token must be rejected")
	}

	viewer, err := RoleForRequest(reqWith("viewer-tok"))
	if err != nil || viewer != Viewer {
		t.Fatalf("viewer: %v %v", viewer, err)
	}
	if AllowedTool(viewer, "import") {
		t.Fatal("viewer must NOT be allowed to import (write)")
	}
	if !AllowedTool(viewer, "query") || !AllowedTool(viewer, "status") {
		t.Fatal("viewer must be allowed to read")
	}

	contrib, _ := RoleForRequest(reqWith("contrib-tok"))
	if !AllowedTool(contrib, "import") {
		t.Fatal("contributor must be allowed to write")
	}
	admin, _ := RoleForRequest(reqWith("admin-tok"))
	if !AllowedTool(admin, "import") {
		t.Fatal("admin must be allowed to write")
	}
}

// TestCompatRetainAllowedForContributor is the other half: tightening the gate
// must not lock a Contributor out of the compat mount.
func TestCompatRetainAllowedForContributor(t *testing.T) {
	t.Setenv("LEANKG_TOKEN_VIEWER", "viewer-tok")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "contrib-tok")
	t.Setenv("LEANKG_TOKEN_ADMIN", "")

	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/default/banks/omp/memories", nil)
	req.Header.Set("Authorization", "Bearer contrib-tok")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("contributor retain: %d, want 200", rec.Code)
	}
}

// namedWriteRoutes are POST routes that mutate the store: session retain, the
// ontology KV write, the auto-config reindex, and the federation push (same
// class: it upserts elements). None of them matched isWritePath before CX-02.
var namedWriteRoutes = []string{
	"/api/v1/memory/session/retain",
	"/api/v1/ontology/match",
	"/api/v1/mcp/auto-config",
	"/api/v2/graph/push",
}

// TestNamedWriteRoutesAllowedForContributor is the other half: the CX-02 gate
// must not lock a Contributor out of the same routes.
func TestNamedWriteRoutesAllowedForContributor(t *testing.T) {
	t.Setenv("LEANKG_TOKEN_VIEWER", "viewer-tok")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "contrib-tok")
	t.Setenv("LEANKG_TOKEN_ADMIN", "")

	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, p := range namedWriteRoutes {
		t.Run(p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", p, nil)
			req.Header.Set("Authorization", "Bearer contrib-tok")
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("contributor POST %s: %d, want 200", p, rec.Code)
			}
		})
	}
}
