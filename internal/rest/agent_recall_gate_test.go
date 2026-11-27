package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/ccmemory"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/store"
)

// agentRecallBody asks for the central memory of every repository. The handle
// sits on an empty home, so a caller the gate lets through reaches the banks
// check and gets a sync message, never a permission refusal.
const agentRecallBody = `{"action":"memory","query":"tabs","args":{"command":"agent_recall","cwd":"/work/repo-beta","scope":"all"}}`

// agentRecallREST serves the REST query route under the gate with the central
// handle wired, as serve wires it, and returns the store that mints tokens.
func agentRecallREST(t *testing.T) (http.Handler, store.Backend) {
	t.Helper()
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
	t.Setenv("LEANKG_TOKEN_VIEWER", "")
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: dir + "/.leankg/leankg.db", Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, core.Options{ProjectDir: dir, AgentMemory: ccmemory.NewHandle(t.TempDir(), nil)})
	return auth.MiddlewareWithStore(st, Handler(eng, nil)), st
}

// TestAgentRecallRefusedToLimitedCallersOverREST sends a viewer whose token is
// limited to a project, and a viewer whose token carries an organization, to
// POST /api/v1/query. Both must get 403 with no bank or project name.
func TestAgentRecallRefusedToLimitedCallersOverREST(t *testing.T) {
	h, st := agentRecallREST(t)
	limited := map[string]auth.MintRequest{
		"project grant": {Role: "viewer", Scopes: []string{"project:repo-alpha"}},
		"org scope":     {Role: "viewer", OrgID: "org-placeholder"},
	}
	for name, req := range limited {
		t.Run(name, func(t *testing.T) {
			req.Name = t.Name()
			secret, _, err := auth.Mint(context.Background(), st, req)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/v1/query", strings.NewReader(agentRecallBody))
			r.Header.Set("Authorization", "Bearer "+secret)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "LEANKG_ERROR_PERMISSION_DENIED") {
				t.Fatalf("body does not carry the permission code: %s", rec.Body.String())
			}
			for _, leak := range []string{"repo-alpha", "repo-beta", "cc-"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Errorf("refusal leaks %q: %s", leak, rec.Body.String())
				}
			}
		})
	}
}
