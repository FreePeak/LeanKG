package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMultiProjectSchemaRequiresProject pins the contract that fixes the
// "leankg not working in xdev" failure.
//
// The self-host registers every repository under the scan root (286 of them
// on the machine this was measured on), and the MCP surface refuses a call
// that does not name a project — correctly, because an omitted project used
// to be answered from the serve process's cwd engine, so a question about
// another project got a confident answer built from the WRONG graph.
//
// That refusal used to reach the caller only as a RUNTIME error: the schema
// still said `project` was optional, so the model had already been told the
// argument was not needed. Measured live, an agent's very first `status`
// call on this server went out with no arguments and came back
// LEANKG_ERROR_MISSING_PARAM.
//
// The rule now lives in the schema — the one place the model reads before
// it calls.
func TestMultiProjectSchemaRequiresProject(t *testing.T) {
	_, session := newProjectServerForTest(t, WithProjectRequired(true))
	for _, tool := range listSchemas(t, session) {
		if !containsStr(tool.required, "project") {
			t.Errorf("tool %q on a multi-project server must REQUIRE project; required = %v",
				tool.name, tool.required)
		}
		if tool.projectDesc == "" {
			t.Errorf("tool %q must describe the project argument", tool.name)
		}
	}
}

// TestSingleProjectSchemaDoesNotRequireProject is the other half: a server
// that serves one project must keep the argument optional, or every existing
// client breaks on a surface that never needed it.
func TestSingleProjectSchemaDoesNotRequireProject(t *testing.T) {
	_, session := newProjectServerForTest(t)
	for _, tool := range listSchemas(t, session) {
		if containsStr(tool.required, "project") {
			t.Errorf("tool %q on a single-project server must not REQUIRE project; required = %v",
				tool.name, tool.required)
		}
	}
}

// TestMultiProjectCallWithoutProjectIsRefused keeps the backstop: the
// schema is what tells the model the rule, but the server still fails
// closed when a call arrives without one. A call that DOES name a project
// is routed by the router.
func TestMultiProjectCallWithoutProjectIsRefused(t *testing.T) {
	srv, _ := newProjectServerForTest(t, WithProjectRequired(true))
	srv.SetProjectRouter(fakeRouter{multi: true})

	if _, err := srv.engineFor(context.Background(), nil); err == nil {
		t.Fatal("a call with no project resolved an engine; under a router it must fail closed")
	} else if !strings.Contains(err.Error(), "project") {
		t.Fatalf("err = %v; it must name the missing project argument", err)
	}
}

// TestScopedServerKeepsProjectOptional covers the ?project= surface: the URL
// already answered which project the call is about, so the argument stays
// optional there — a client that sent ?project=… and then also had to
// repeat the project in the arguments was unusable (see
// TestScopedServerDoesNotReAskForProject).
func TestScopedServerKeepsProjectOptional(t *testing.T) {
	srv, session := newProjectServerForTest(t)
	srv.SetProjectRouter(fakeRouter{multi: true})
	srv.SetProjectScoped(true)
	for _, tool := range listSchemas(t, session) {
		if containsStr(tool.required, "project") {
			t.Errorf("tool %q on a scoped handler must not REQUIRE project; required = %v",
				tool.name, tool.required)
		}
	}
}

// toolSchema is one tool's advertised schema, reduced to what this test
// cares about.
type toolSchema struct {
	name        string
	required    string
	projectDesc string
}

func listSchemas(t *testing.T, session *mcp.ClientSession) []toolSchema {
	t.Helper()
	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var out []toolSchema
	for _, tool := range res.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		ts := toolSchema{name: tool.Name}
		for _, r := range schema.Required {
			ts.required += r + " "
		}
		ts.projectDesc = schema.Properties["project"].Description
		out = append(out, ts)
	}
	if len(out) == 0 {
		t.Fatal("no tools advertised")
	}
	return out
}

// newProjectServerForTest builds a Server over a throwaway store and
// connects a client session to it over the SDK's in-memory transport.
func newProjectServerForTest(t *testing.T, opts ...Option) (*Server, *mcp.ClientSession) {
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
	engine.SetProjectDir(dir)
	srv := New(engine, opts...)

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
	return srv, session
}
