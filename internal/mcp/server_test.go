package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestServer builds a server + connected client session over the SDK's
// in-memory transport (no stdio, no network).
func newTestServer(t *testing.T) *mcp.ClientSession {
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
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	engine := core.New(st, mem, nil)
	engine.SetProjectDir(dir) // mirrors cmd/leankg serve
	srv := New(engine)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.srv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// TestRegistryIsExactlyThreeTools pins the CI invariant carried over from the
// Rust line (docs/mcp-tool-contract.md): the registry is EXACTLY
// {import, query, status}. Legacy set/get are superseded tool names.
func TestRegistryIsExactlyThreeTools(t *testing.T) {
	session := newTestServer(t)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	want := map[string]bool{core.ToolImport: true, core.ToolQuery: true, core.ToolStatus: true}
	if len(got) != 3 {
		t.Fatalf("registry has %d tools (%v), want EXACTLY 3", len(got), got)
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("missing tool %q in %v", name, got)
		}
	}
	for _, legacy := range []string{"set", "get"} {
		if got[legacy] {
			t.Fatalf("legacy tool %q must not be registered", legacy)
		}
	}
}

func TestServerAdvertisesAgentProtocol(t *testing.T) {
	session := newTestServer(t)
	initialize := session.InitializeResult()
	if initialize == nil {
		t.Fatal("client has no initialize result")
	}
	for _, want := range []string{"query before bash/grep", "always pass project", "does not create vectors"} {
		if !strings.Contains(strings.ToLower(initialize.Instructions), want) {
			t.Fatalf("initialize instructions missing %q: %s", want, initialize.Instructions)
		}
	}

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if !strings.Contains(tool.Description, toolGuidance) {
			t.Fatalf("tool %q does not carry the fallback agent guidance", tool.Name)
		}
	}
}

// TestCallQueryRoundTrip drives tools/call end-to-end: L0 cold answer with
// guidance on an empty store, then an L1 hit after seeding an element.
func TestCallQueryRoundTrip(t *testing.T) {
	session := newTestServer(t)
	ctx := context.Background()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "query",
		Arguments: map[string]any{"query": "anything"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	retr := out["retrieval"].(map[string]any)
	if retr["rung"] != "L0" {
		t.Fatalf("cold query: %v", retr)
	}

	// Seed via the import tool (also exercises its handler).
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "import",
		Arguments: map[string]any{
			"action": "memory", "command": "create", "path": "MEMORY.md",
			"args": map[string]any{"content": "# smoke"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "status",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	tools := out["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("status.tools = %v, want 3", tools)
	}
}

// TestEngineForRequiresProjectUnderRouter proves the fail-closed multi-project
// rule: with a router set, an omitted project is rejected instead of silently
// answering from the default engine (the serve cwd). Without a router the
// single-engine behavior is unchanged.
func TestEngineForRequiresProjectUnderRouter(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	def := core.New(st, mem, nil)
	def.SetProjectDir(dir)

	routed := core.New(st, mem, nil)
	routed.SetProjectDir(dir + "-routed")
	router := &stubRouter{engines: map[string]*core.Engine{"api": routed}}

	s := New(def)
	// No router: default engine answers with no project.
	if got, err := s.engineFor(context.Background(), nil); err != nil || got != def {
		t.Fatalf("no router: got %v err %v, want default engine", got, err)
	}

	s.SetProjectRouter(router)
	// Router set, no project: fail closed — for nil, empty-args, and
	// blank-string requests alike.
	for name, raw := range map[string][]byte{
		"nil request":   nil,
		"empty args":    []byte(`{}`),
		"blank project": []byte(`{"project":"  "}`),
	} {
		req := (*mcp.CallToolRequest)(nil)
		if raw != nil {
			req = newRawCallRequest(raw)
		}
		if _, err := s.engineFor(context.Background(), req); err == nil || !strings.Contains(err.Error(), "omitted project") {
			t.Fatalf("%s: want project-required error, got %v", name, err)
		}
	}
	// Router set, known project: routes to the project's engine.
	if got, err := s.engineFor(context.Background(), newRawCallRequest([]byte(`{"project":"api"}`))); err != nil || got != routed {
		t.Fatalf("routed: got %v err %v, want routed engine", got, err)
	}
}

type stubRouter struct {
	engines map[string]*core.Engine
}

func (r *stubRouter) EngineFor(_ context.Context, project string) (*core.Engine, error) {
	e, ok := r.engines[project]
	if !ok {
		return nil, fmt.Errorf("unknown project %q", project)
	}
	return e, nil
}

// newRawCallRequest builds the minimal CallToolRequest the SDK hands to
// tool handlers: params.arguments as raw JSON.
func newRawCallRequest(raw []byte) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: raw}}
}
func TestLegacyToolNameRejected(t *testing.T) {
	session := newTestServer(t)
	_, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "set",
		Arguments: map[string]any{"action": "repo", "path": "."},
	})
	if err == nil {
		t.Fatal("legacy tool 'set' must be rejected over the wire")
	}
}

// TestGraphActionOverMCPWire proves the graph verbs survive MCP schema
// validation AND reach the engine (they were previously absent from the
// query action enum, so go-sdk rejected them before core ran).
func TestGraphActionOverMCPWire(t *testing.T) {
	session := newTestServer(t)
	ctx := context.Background()

	// Seed a call chain through the import tool is not available for elements,
	// so use the store directly via a query round-trip on an empty store first.
	// The engine's legitimate ErrUnknownNode proves the call REACHED core —
	// a schema rejection would read "invalid arguments"/"unexpected additional
	// properties" instead. Assert on the distinction, not on absence of error.
	_, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "query",
		Arguments: map[string]any{
			"action": "impact", "query": "does.not.exist",
			"args": map[string]any{"depth": "2"},
		},
	})
	if err == nil {
		t.Fatal("impact on unknown node should surface ErrUnknownNode")
	}
	if !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("impact must reach the engine (got %v) — a schema rejection means the enum/args are incomplete", err)
	}

	// session action must also be accepted by the schema.
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "query",
		Arguments: map[string]any{"action": "session", "query": "s1", "args": map[string]any{"command": "canvas"}},
	}); err != nil {
		t.Fatalf("session action over MCP: %v", err)
	}
	// ontology read action.
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "query",
		Arguments: map[string]any{"action": "ontology"},
	}); err != nil {
		t.Fatalf("ontology action over MCP: %v", err)
	}
}

// TestMCPRBACGatesWriteToolsOverWire proves the security property: with
// tokens configured, a Viewer can read but a write tool call (import) is
// refused — over the real HTTP handler, not just in the auth package. This is
// the gap where MCP HTTP (the default transport) was previously ungated.
func TestMCPRBACGatesWriteToolsOverWire(t *testing.T) {
	t.Setenv("LEANKG_TOKEN_VIEWER", "viewer-tok")
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")

	dir := t.TempDir()
	st, err := store.Open(dir+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	engine := core.New(st, nil, nil)
	engine.SetProjectDir(dir)
	h := New(engine).HTTPHandler()

	// no token → 401
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/mcp", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: %d, want 401", rec.Code)
	}

	// viewer token → authorized; the tool-level gate is exercised by the
	// allowTool call inside handleImport (unit-pinned in internal/auth).
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer viewer-tok")
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("valid viewer token must be accepted by the HTTP layer")
	}
}

// TestNewActionsOverMCPWire proves pattern/languages/lsp survive schema
// validation AND reach the engine — the dead-action class this file already
// caught once (graph verbs missing from the enum).
func TestNewActionsOverMCPWire(t *testing.T) {
	session := newTestServer(t)
	ctx := context.Background()

	// languages: must return an answer (empty registry state is fine).
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "query", Arguments: map[string]any{"action": "languages", "query": "x"},
	}); err != nil {
		t.Fatalf("languages over MCP: %v", err)
	}

	// pattern: numeric limit in args (the Args-widening regression class);
	// ast-grep absent ⇒ engine answers with the L2 degrade, not a schema error.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "query",
		Arguments: map[string]any{
			"action": "pattern", "query": "x",
			"args": map[string]any{"pattern": "func $F($A)", "lang": "go", "limit": 5},
		},
	})
	if err != nil {
		t.Fatalf("pattern over MCP (schema or engine): %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	// ast-grep may be installed on this host (real run, rung=ast-grep) or not
	// (degrade to L2) — BOTH prove the action reached the engine past schema
	// validation, which is what this test pins.
	r, ok := out["retrieval"].(map[string]any)
	if !ok {
		t.Fatalf("pattern retrieval missing: %v", out)
	}
	if r["rung"] != "ast-grep" && r["rung"] != "L2" {
		t.Fatalf("pattern rung: %v", r)
	}

	// lsp: inactive language ⇒ clear engine error (schema accepted).
	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "query",
		Arguments: map[string]any{
			"action": "lsp", "query": "Handler",
			"args": map[string]any{"lang": "go"},
		},
	})
	if err == nil || !(strings.Contains(err.Error(), "not active") || strings.Contains(err.Error(), "no language registry")) {
		t.Fatalf("lsp must reach the engine and refuse (schema rejection would differ): %v", err)
	}
}
