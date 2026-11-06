package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// clearEnvTokens unsets the static env tokens for one test, so only the store
// decides whether the gate is on.
func clearEnvTokens(t *testing.T) {
	t.Helper()
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
	t.Setenv("LEANKG_TOKEN_VIEWER", "")
}

// newGateStore opens a migrated store in a fresh temp dir.
func newGateStore(t *testing.T) store.Backend {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

// newGateHandler serves an HTTP MCP handler over st, the way serve does.
func newGateHandler(t *testing.T, st store.Backend) http.Handler {
	t.Helper()
	engine := core.New(st, nil, nil)
	engine.SetProjectDir(t.TempDir())
	srv := New(engine)
	srv.SetAuthStore(st)
	return srv.HTTPHandler()
}

// rpc posts one JSON-RPC request to the handler, with an optional
// Authorization header ("" sends none).
func rpc(h http.Handler, body, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func toolCall(name string, args string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, name, args)
}

const (
	rpcInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"gate-test","version":"0"}}}`
	rpcToolsList  = `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`
	rpcPing       = `{"jsonrpc":"2.0","id":4,"method":"ping"}`
)

// requireAuthenticateHint asserts an anonymous caller got the unauthorized error that names authenticate.
func requireAuthenticateHint(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, "LEANKG_ERROR_UNAUTHORIZED") || !strings.Contains(body, "authenticate") {
		t.Fatalf("want the unauthorized error naming the authenticate tool, got: %s", body)
	}
}

// TestGateOnAnonymousMatrix pins slice 3: with only a site admin in the store
// (no env or DB token), a request with no Authorization header reaches only the
// protocol methods and the login tool, and a bad or expired token is HTTP 401.
func TestGateOnAnonymousMatrix(t *testing.T) {
	clearEnvTokens(t)
	st := newGateStore(t)
	if _, err := auth.CreateSiteAdmin(st, "matrix-admin", testAdminPassword); err != nil {
		t.Fatal(err)
	}
	h := newGateHandler(t, st)

	t.Run("anonymous tools/list is served", func(t *testing.T) {
		rec := rpc(h, rpcToolsList, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"authenticate"`) {
			t.Fatalf("anonymous tools/list: %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("anonymous initialize and ping are served", func(t *testing.T) {
		for name, body := range map[string]string{"initialize": rpcInitialize, "ping": rpcPing} {
			rec := rpc(h, body, "")
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "LEANKG_ERROR") {
				t.Fatalf("anonymous %s: %d %s", name, rec.Code, rec.Body.String())
			}
		}
	})
	t.Run("anonymous gated tools are refused with the authenticate hint", func(t *testing.T) {
		for _, call := range []string{
			toolCall("status", `{}`),
			toolCall("query", `{"query":"Foo"}`),
			toolCall("import", `{"action":"repo","path":"."}`),
		} {
			rec := rpc(h, call, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("anonymous gated call answered %d, want a structured JSON-RPC error: %s", rec.Code, rec.Body.String())
			}
			requireAuthenticateHint(t, rec.Body.String())
			if strings.Contains(rec.Body.String(), `"freshness"`) {
				t.Fatalf("anonymous caller reached a handler: %s", rec.Body.String())
			}
		}
	})
	t.Run("anonymous authenticate is served", func(t *testing.T) {
		rec := rpc(h, toolCall("authenticate", `{"username":"matrix-admin","password":"`+testAdminPassword+`"}`), "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "lkg_") {
			t.Fatalf("anonymous authenticate: %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("a bad bearer token is HTTP 401", func(t *testing.T) {
		rec := rpc(h, toolCall("status", `{}`), "Bearer lkg_not_a_real_token")
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `{"error":"unauthorized"}`) {
			t.Fatalf("bad token: %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("an expired token is HTTP 401", func(t *testing.T) {
		expired, _, err := auth.Mint(st, auth.MintRequest{Name: "old", Role: "admin", TTL: -time.Second})
		if err != nil {
			t.Fatal(err)
		}
		rec := rpc(h, toolCall("status", `{}`), "Bearer "+expired)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expired token: %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("a valid token keeps RBAC and reaches the tool", func(t *testing.T) {
		token, _, err := auth.Mint(st, auth.MintRequest{Name: "live", Role: "admin", TTL: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		rec := rpc(h, toolCall("status", `{}`), "Bearer "+token)
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "LEANKG_ERROR_UNAUTHORIZED") || !strings.Contains(rec.Body.String(), `"result"`) {
			t.Fatalf("valid token status: %d %s", rec.Code, rec.Body.String())
		}
	})
}

// TestGateOffEverythingIsAdmin pins the local default: with no env token, no
// DB token and no site admin, every caller (no header or a stray header) is
// Admin, exactly as before the gate moved.
func TestGateOffEverythingIsAdmin(t *testing.T) {
	clearEnvTokens(t)
	h := newGateHandler(t, newGateStore(t))
	for name, authz := range map[string]string{"no header": "", "stray token": "Bearer lkg_whatever"} {
		rec := rpc(h, toolCall("status", `{}`), authz)
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "LEANKG_ERROR") || !strings.Contains(rec.Body.String(), `"result"`) {
			t.Fatalf("gate off, %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

// TestGateOnFromEnvToken pins that a static env token is a gate source too:
// the anonymous restriction applies without any store account.
func TestGateOnFromEnvToken(t *testing.T) {
	clearEnvTokens(t)
	t.Setenv("LEANKG_TOKEN_VIEWER", "env-viewer-token")
	h := newGateHandler(t, newGateStore(t))
	rec := rpc(h, toolCall("status", `{}`), "")
	requireAuthenticateHint(t, rec.Body.String())
	rec = rpc(h, toolCall("status", `{}`), "Bearer env-viewer-token")
	if strings.Contains(rec.Body.String(), "LEANKG_ERROR_UNAUTHORIZED") {
		t.Fatalf("env viewer token refused: %s", rec.Body.String())
	}
}

// failingAdminStore fails the site-admin probe, the way a broken store would.
type failingAdminStore struct{ store.Backend }

func (failingAdminStore) AdminExists() (bool, error) { return false, errors.New("store unavailable") }

// TestGateFailsClosedOnStoreError pins fail-closed: when the gate cannot be
// read, an anonymous request is restricted (never Admin).
func TestGateFailsClosedOnStoreError(t *testing.T) {
	clearEnvTokens(t)
	st := newGateStore(t)
	engine := core.New(st, nil, nil)
	engine.SetProjectDir(t.TempDir())
	srv := New(engine)
	srv.SetAuthStore(failingAdminStore{st})
	h := srv.HTTPHandler()

	rec := rpc(h, toolCall("status", `{}`), "")
	requireAuthenticateHint(t, rec.Body.String())
	if rec := rpc(h, rpcToolsList, ""); rec.Code != http.StatusOK {
		t.Fatalf("fail-closed tools/list: %d %s", rec.Code, rec.Body.String())
	}
}

// TestStdioIsUngatedAdmin pins that the stdio session never sees the HTTP
// anonymous restriction: with a site admin in the store, a stdio-style session
// (no HTTP, no SetAuthStore) still runs gated tools as Admin.
func TestStdioIsUngatedAdmin(t *testing.T) {
	clearEnvTokens(t)
	st := newGateStore(t)
	if _, err := auth.CreateSiteAdmin(st, "stdio-admin", testAdminPassword); err != nil {
		t.Fatal(err)
	}
	engine := core.New(st, nil, nil)
	engine.SetProjectDir(t.TempDir())
	session := connectTo(t, New(engine))
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "status", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("stdio status with a site admin present: err=%v res=%+v", err, res)
	}
}
