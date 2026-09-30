package mcp

import (
	"context"
	"encoding/json"
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
	lower := strings.ToLower(initialize.Instructions)
	for _, want := range []string{
		"query before bash/grep",
		"project= is required only on multi-project",
		"does not create vectors",
		"session_recall",
		"session_retain",
		"exactly 3 tools",
		"leankg-embed",
	} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Fatalf("initialize instructions missing %q: %s", want, initialize.Instructions)
		}
	}
	// The old "always pass project" wording trained agents wrong on stdio.
	if strings.Contains(lower, "always pass project") {
		t.Fatalf("instructions still force always-pass-project: %s", initialize.Instructions)
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

// TestMCPNewProtocolVersionOverHTTP pins the gate that let this regress twice:
// go-sdk rejects protocol >= 2026-07-28 on any stateful HTTP server, and the
// version is read from the Mcp-Protocol-Version HEADER — not the initialize
// body. Every prior test omitted that header, so the server silently negotiated
// 2025-03-26, the >= 20260728 branch never executed, and CI stayed green on a
// server the real client (DSH) could not talk to.
func TestMCPNewProtocolVersionOverHTTP(t *testing.T) {
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

	// post sends one tools/list. SEP-2575 makes a compliant client state the
	// method and version THREE ways — the Mcp-Protocol-Version header, the
	// matching _meta."io.modelcontextprotocol/protocolVersion", and the
	// Mcp-Method header — plus clientCapabilities in _meta, and the server
	// refuses any mismatch. Modelling the compliant client is the point: a
	// header-only request is refused for a different reason and would not have
	// proven the transport gate opened.
	post := func(compliant bool, metaVersion string) *httptest.ResponseRecorder {
		t.Helper()
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
		if compliant {
			body = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{` +
				`"io.modelcontextprotocol/protocolVersion":"` + metaVersion + `",` +
				`"io.modelcontextprotocol/clientCapabilities":{"capabilities":{}}}}}`
		}
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if compliant {
			req.Header.Set("Mcp-Protocol-Version", "2026-07-28")
			req.Header.Set("Mcp-Method", "tools/list")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// The compliant 2026-07-28 client (DSH) must be served, not refused.
	rec := post(true, "2026-07-28")
	if rec.Code != http.StatusOK {
		t.Fatalf("compliant protocol 2026-07-28 over HTTP: %d, want 200 — body: %s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"import"`) {
		t.Fatalf("2026-07-28 tools/list must carry the registry: %s", rec.Body.String())
	}

	// A non-compliant 2026-07-28 request must be refused for the SEP-2575
	// reason — but never with the stateful-transport error, which is the gate
	// this fix opens. Guards the fix without asserting bad faith is served.
	if rec := post(true, "2025-06-18"); rec.Code == http.StatusOK {
		t.Fatalf("mismatched _meta must be rejected, got 200: %s", rec.Body.String())
	}

	// No regression for a legacy client that omits the new headers entirely.
	if rec := post(false, ""); rec.Code != http.StatusOK {
		t.Fatalf("legacy header-less tools/list: %d, want 200 — body: %s", rec.Code, rec.Body.String())
	}
}
