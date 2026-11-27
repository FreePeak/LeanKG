package rpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/ccmemory"
	"github.com/FreePeak/LeanKG/internal/core"
	leankgv1 "github.com/FreePeak/LeanKG/internal/rpc/leankg/v1"
	"github.com/FreePeak/LeanKG/internal/rpc/leankg/v1/leankgv1connect"
	"github.com/FreePeak/LeanKG/internal/store"
)

// agentRecallService serves ConnectRPC under the gate with the central handle
// wired, as serve wires it, and returns a client plus the store that mints
// tokens. The handle sits on an empty home, so a caller the engine lets through
// reaches the banks check instead of a permission refusal.
func agentRecallService(t *testing.T) (leankgv1connect.LeanKGClient, store.Backend) {
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
	path, handler := leankgv1connect.NewLeanKGHandler(NewLeanKGService(eng))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(auth.MiddlewareWithStore(st, mux))
	t.Cleanup(srv.Close)
	return leankgv1connect.NewLeanKGClient(srv.Client(), srv.URL), st
}

// TestAgentRecallRefusedToLimitedCallersOverConnectRPC sends a viewer whose token
// is limited to a project, and one whose token carries an organization, to Query.
// Both must get CodePermissionDenied with no bank or project name.
func TestAgentRecallRefusedToLimitedCallersOverConnectRPC(t *testing.T) {
	c, st := agentRecallService(t)
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
			req := connect.NewRequest(&leankgv1.QueryRequest{
				Action: "memory",
				Query:  "tabs",
				Args:   map[string]string{"command": "agent_recall", "cwd": "/work/repo-beta", "scope": "all"},
			})
			req.Header().Set("Authorization", "Bearer "+secret)
			_, err = c.Query(context.Background(), req)
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("code = %v (err %v), want %v", connect.CodeOf(err), err, connect.CodePermissionDenied)
			}
			for _, leak := range []string{"repo-alpha", "repo-beta", "cc-"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("refusal leaks %q: %v", leak, err)
				}
			}
		})
	}
}
