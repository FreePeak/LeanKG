package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/ccmemory"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/store"
)

// agentRecallDashboard serves the dashboard /api/ under the gate with the central
// handle wired, as serve wires it, and returns the store that mints tokens.
func agentRecallDashboard(t *testing.T) (http.Handler, store.Backend) {
	t.Helper()
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
	t.Setenv("LEANKG_TOKEN_VIEWER", "")
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(dir, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, core.Options{ProjectDir: dir, AgentMemory: ccmemory.NewHandle(t.TempDir(), nil)})
	return auth.MiddlewareWithStore(st, APIHandler(eng, nil)), st
}

// TestAgentRecallRefusedToLimitedCallersOverDashboard sends a viewer whose token
// is limited to a project, and one whose token carries an organization, to the
// dashboard query route. Both must get 403 with no bank or project name.
func TestAgentRecallRefusedToLimitedCallersOverDashboard(t *testing.T) {
	h, st := agentRecallDashboard(t)
	const body = `{"query":"tabs","params":{"action":"memory","command":"agent_recall","cwd":"/work/repo-beta","scope":"all"}}`
	limited := map[string]auth.MintRequest{
		"project grant": {Role: "viewer", Scopes: []string{"project:repo-alpha"}},
		"org scope":     {Role: "viewer", OrgID: "org-placeholder"},
	}
	for name, mint := range limited {
		t.Run(name, func(t *testing.T) {
			mint.Name = t.Name()
			secret, _, err := auth.Mint(context.Background(), st, mint)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+secret)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body.String())
			}
			for _, leak := range []string{"repo-alpha", "repo-beta", "cc-"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Errorf("refusal leaks %q: %s", leak, rec.Body.String())
				}
			}
		})
	}
}
