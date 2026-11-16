package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
)

// gateOff wraps a handler in the gate with no token configured, the local
// default: every caller resolves to Admin. Production always mounts REST under
// the gate; tests that exercise a route in isolation use this.
func gateOff(h http.Handler) http.Handler { return auth.MiddlewareWithStore(nil, h) }

// gatedREST builds the REST handler with the hindsight mount and a memory
// layer, the widest route set serve can mount, under the gate.
func gatedREST(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: dir + "/.leankg/leankg.db", Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, core.Options{Memory: mem, ProjectDir: dir})
	return auth.MiddlewareWithStore(st, Handler(eng, mem, WithHindsightCompat()))
}

// TestWriteRoutesRefuseViewer sends a Viewer to every route that mutates state.
// Each must answer 403, the role the route declares (CX-02, CX-49).
func TestWriteRoutesRefuseViewer(t *testing.T) {
	t.Setenv("LEANKG_TOKEN_VIEWER", "viewer-tok")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	h := gatedREST(t)
	writes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/import", "{}"},
		{http.MethodPost, "/api/v1/ontology/match", "{}"},
		{http.MethodPost, "/api/v1/memory/banks/b1/memories", `{"content":"x"}`},
		{http.MethodPost, "/api/v1/memory/session/retain", "{}"},
		{http.MethodPost, "/api/v2/graph/push", "{}"},
		{http.MethodPost, "/v1/default/banks/b1/memories", `{"items":[{"content":"x"}]}`},
		{http.MethodDelete, "/v1/default/banks/b1/documents/d1", ""},
	}
	for _, w := range writes {
		req := httptest.NewRequest(w.method, w.path, strings.NewReader(w.body))
		req.Header.Set("Authorization", "Bearer viewer-tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as viewer: %d, want 403", w.method, w.path, rec.Code)
		}
	}
}

// TestReadRoutesServeViewer sends a Viewer to routes that only read or
// ensure a bank. None may answer 401 or 403.
func TestReadRoutesServeViewer(t *testing.T) {
	t.Setenv("LEANKG_TOKEN_VIEWER", "viewer-tok")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	h := gatedREST(t)
	reads := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/status", ""},
		{http.MethodPost, "/api/v1/query", `{"query":"x"}`},
		{http.MethodPost, "/api/v1/memory/banks/b1/recall", `{"query":"x"}`},
		{http.MethodPost, "/v1/default/banks/b1/memories/recall", `{"query":"x"}`},
		{http.MethodPost, "/v1/default/banks/b1/reflect", `{"query":"x"}`},
		{http.MethodPut, "/v1/default/banks/b1", "{}"},
		{http.MethodGet, "/v1/default/banks/b1/memories", ""},
		{http.MethodGet, "/api/v2/graph", ""},
	}
	for _, rd := range reads {
		req := httptest.NewRequest(rd.method, rd.path, strings.NewReader(rd.body))
		req.Header.Set("Authorization", "Bearer viewer-tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("%s %s as viewer: %d, want a read answer", rd.method, rd.path, rec.Code)
		}
	}
}

// gatedHandler is Handler under the local-default gate (see gateOff).
func gatedHandler(engine *core.Engine, mem *memory.Memory, opts ...HandlerOption) http.Handler {
	return gateOff(Handler(engine, mem, opts...))
}
