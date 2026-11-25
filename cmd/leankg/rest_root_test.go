package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/portfolioreg"
)

// stubBody answers 200 with a fixed body. Behind the gate an unauthenticated
// request never reaches it, so a 200 from it means a path escaped the gate.
func stubBody(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(name))
	})
}

// restRootServer serves the REST root of a listener with a token configured,
// so the gate is on and an anonymous request must be refused.
func restRootServer(t *testing.T, hosted bool) *httptest.Server {
	t.Helper()
	t.Setenv("LEANKG_TOKEN_ADMIN", "configured-admin-token-for-test")
	projectAdmin := projectAdminRoute(nil, nil, portfolioreg.Options{})
	srv := httptest.NewServer(restRoot(nil, hosted, projectAdmin, stubBody("api")))
	t.Cleanup(srv.Close)
	return srv
}

func anonymous(t *testing.T, method, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(body))
}

// TestHostedRESTHealthAnswersWithoutATokenAndNothingElseDoes: a hosted REST
// listener answers /health with 200 and the same bare body as every other
// listener, so a probe needs no token and learns nothing about projects or
// paths. Every other unauthenticated request stays refused.
func TestHostedRESTHealthAnswersWithoutATokenAndNothingElseDoes(t *testing.T) {
	srv := restRootServer(t, true)

	if code, body := anonymous(t, http.MethodGet, srv.URL+"/health"); code != http.StatusOK || body != `{"ok":true}` {
		t.Fatalf("GET /health with no token = %d %q, want 200 {\"ok\":true}", code, body)
	}
	refused := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/status"},
		{http.MethodPost, "/health"},
		{http.MethodGet, "/health/extra"},
		{http.MethodGet, "/"},
		{http.MethodGet, "/api/v1/projects"},
		{http.MethodPost, "/api/v1/projects"},
	}
	for _, c := range refused {
		if code, body := anonymous(t, c.method, srv.URL+c.path); code != http.StatusUnauthorized {
			t.Errorf("%s %s with no token = %d %q, want 401", c.method, c.path, code, body)
		}
	}
}

// TestStandaloneRESTHealthStillNeedsAToken: the health route is open on a
// hosted listener only. A standalone server keeps today's gate.
func TestStandaloneRESTHealthStillNeedsAToken(t *testing.T) {
	srv := restRootServer(t, false)
	if code, body := anonymous(t, http.MethodGet, srv.URL+"/health"); code != http.StatusUnauthorized {
		t.Fatalf("standalone GET /health with no token = %d %q, want 401", code, body)
	}
}
