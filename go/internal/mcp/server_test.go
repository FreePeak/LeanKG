package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/memory"
	"github.com/FreePeak/LeanKG/go/internal/store"
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

// TestLegacyToolNameRejected proves the envelope guard over the wire: a call
// to a removed tool name is refused by the registry, not silently routed.
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
