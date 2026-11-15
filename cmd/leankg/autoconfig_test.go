package main

import (
	"fmt"
	"github.com/FreePeak/LeanKG/internal/auth"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRESTListenerServesAutoConfig guards a dropped return value:
// cmd/leankg/main.go called restauto.RegisterAutoConfig(h, engine) for its
// side effect, but the function returns the mux it wrapped, so the endpoint
// it mounted lived in a mux nobody served. Both listeners that register it
// answered 404 for POST /api/v1/mcp/auto-config while the log line and the
// package claimed otherwise — the auto-index/auto-embed handshake a fresh
// install relies on was dead.
func TestRESTListenerServesAutoConfig(t *testing.T) {
	bin := buildLeanKG(t)
	proj := seedProject(t)
	if out, err := exec.Command(bin, "index", proj, "--auto").CombinedOutput(); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "serve", "--read-only", "--rest", addr, "--project", proj)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	url := fmt.Sprintf("http://%s/api/v1/mcp/auto-config", addr)
	var code int
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Post(url, "application/json", strings.NewReader(
			`{"index_enabled":false,"embed_enabled":false}`))
		if err == nil {
			code = resp.StatusCode
			_ = resp.Body.Close()
			// 200 = mounted; 404 = the mux that mounted it was discarded.
			if code == http.StatusOK {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if code != http.StatusOK {
		t.Fatalf("POST %s: %d (want 200 — the auto-config endpoint must be mounted)", url, code)
	}
}

// TestMCPListenerGatesAutoConfig pins CX-02 on the --http MCP listener: with
// the gate on, auto-config answers 401 without a token, 403 for a Viewer and
// reaches the handler for an Admin, while the MCP route stays open to an
// anonymous handshake. With the gate off, auto-config is open as before.
func TestMCPListenerGatesAutoConfig(t *testing.T) {
	mcp := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	// The real route (restauto.RegisterAutoConfig) declares Contributor; this stub
	// declares the same, so the listener wiring is what this test pins.
	auto := auth.Require(auth.Contributor, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	t.Run("gate on", func(t *testing.T) {
		t.Setenv("LEANKG_TOKEN_ADMIN", "admin-tok")
		t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
		t.Setenv("LEANKG_TOKEN_VIEWER", "viewer-tok")
		h := gateAutoConfig(nil, mcp, auto)
		cases := []struct {
			name, path, token string
			want              int
		}{
			{"auto-config no token", "/api/v1/mcp/auto-config", "", http.StatusUnauthorized},
			{"auto-config viewer", "/api/v1/mcp/auto-config", "viewer-tok", http.StatusForbidden},
			{"auto-config admin", "/api/v1/mcp/auto-config", "admin-tok", http.StatusOK},
			{"mcp anonymous handshake", "/mcp", "", http.StatusNoContent},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, c.path, nil)
				if c.token != "" {
					req.Header.Set("Authorization", "Bearer "+c.token)
				}
				h.ServeHTTP(rec, req)
				if rec.Code != c.want {
					t.Fatalf("POST %s (%s): %d, want %d", c.path, c.name, rec.Code, c.want)
				}
			})
		}
	})

	t.Run("gate off", func(t *testing.T) {
		t.Setenv("LEANKG_TOKEN_ADMIN", "")
		t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
		t.Setenv("LEANKG_TOKEN_VIEWER", "")
		h := gateAutoConfig(nil, mcp, auto)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/auto-config", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("gate off auto-config: %d, want 200", rec.Code)
		}
	})
}
