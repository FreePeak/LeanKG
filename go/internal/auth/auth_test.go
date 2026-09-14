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

// TestMiddlewarePathGating covers the REST/RPC path-level gate.
func TestMiddlewarePathGating(t *testing.T) {
	t.Setenv("LEANKG_TOKEN_VIEWER", "viewer-tok")
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")

	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// reads pass for a viewer
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/query", nil)
	req.Header.Set("Authorization", "Bearer viewer-tok")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer read: %d", rec.Code)
	}
	// writes are forbidden for a viewer
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/import", nil)
	req.Header.Set("Authorization", "Bearer viewer-tok")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer write: %d, want 403", rec.Code)
	}
	// missing token is unauthorized
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/query", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d, want 401", rec.Code)
	}
}

// TestParameterizedWriteRoutesGated pins the fix for the path-equality hole:
// hindsight memory retain lives at a parameterized route and must still be
// forbidden for a Viewer.
func TestParameterizedWriteRoutesGated(t *testing.T) {
	t.Setenv("LEANKG_TOKEN_VIEWER", "viewer-tok")
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")

	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, p := range []string{
		"/api/v1/import",
		"/api/v1/memory/banks/my-bank/memories",
		"/leankg.v1.LeanKG/Import",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", p, nil)
		req.Header.Set("Authorization", "Bearer viewer-tok")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s as viewer: %d, want 403", p, rec.Code)
		}
	}
	// reads stay open
	for _, p := range []string{"/api/v1/query", "/api/v1/status", "/api/v1/memory/banks/b/recall"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", p, nil)
		req.Header.Set("Authorization", "Bearer viewer-tok")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s as viewer: %d, want 200", p, rec.Code)
		}
	}
}
