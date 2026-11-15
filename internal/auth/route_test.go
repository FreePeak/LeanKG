package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// roleCtx returns a request whose context carries role, as the gate sets it.
func roleCtx(role Role) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	return req.WithContext(WithRole(context.Background(), role))
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// TestRequireDeniesBelowDeclaredRole: a route that declares Contributor
// refuses a Viewer and serves a Contributor.
func TestRequireDeniesBelowDeclaredRole(t *testing.T) {
	h := Require(Contributor, okHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, roleCtx(Viewer))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer on contributor route: %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, roleCtx(Contributor))
	if rec.Code != http.StatusOK {
		t.Fatalf("contributor on contributor route: %d, want 200", rec.Code)
	}
}

// TestRequireDefaultDeny: a request that no gate ran for has no role, and
// Require refuses it rather than serving it as Admin.
func TestRequireDefaultDeny(t *testing.T) {
	rec := httptest.NewRecorder()
	Require(Viewer, okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no role in context: %d, want 401", rec.Code)
	}
}

// TestPublicPassesThrough: Public adds no check of its own.
func TestPublicPassesThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	Public(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("public route without a role: %d, want 200", rec.Code)
	}
}

// TestMiddlewareStoresRoleForRoutes: the gate resolves the bearer once and the
// route sees the role in its context.
func TestMiddlewareStoresRoleForRoutes(t *testing.T) {
	t.Setenv("LEANKG_TOKEN_VIEWER", "viewer-tok")
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")

	var seen Role
	var found bool
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, found = RoleFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query", nil)
	req.Header.Set("Authorization", "Bearer viewer-tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !found || seen != Viewer {
		t.Fatalf("status %d, role %v (found %v), want 200 and viewer", rec.Code, seen, found)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/query", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d, want 401", rec.Code)
	}
}
