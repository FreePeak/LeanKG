package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/ccmemory"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// placeholderRepoDir is a repository the central banks hold memory for. It
// is not registered with the test server, so the server never routes to it.
const placeholderRepoDir = "/work/example-repo-a"

// seedCentralBanks writes one Claude Code memory for placeholderRepoDir under
// the home's Claude projects root, then syncs it into the central root under
// home, the way `leankg memory sync` does.
func seedCentralBanks(t *testing.T, home string) {
	t.Helper()
	ctx := context.Background()
	projects := claudecode.ProjectsRoot(home)
	memDir := filepath.Join(projects, claudecode.Slug(placeholderRepoDir), "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: Release freeze\ndescription: Freeze deploys during audits.\nmetadata:\n  type: project\n---\nNo deploys during audits.\n"
	if err := os.WriteFile(filepath.Join(memDir, "freeze.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(ctx, home, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mem.Close() })
	if _, err := ccmemory.Sync(ctx, mem, ccmemory.SyncOptions{Projects: projects, CentralRoot: memory.GlobalRoot(home)}); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

// newAgentRecallServer serves a multi-project surface (the router refuses
// every selector, so an unregistered project is refused) whose default engine
// holds the central banks under home through a lazy handle.
func newAgentRecallServer(t *testing.T, home string) *Server {
	t.Helper()
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: dir + "/.leankg/leankg.db", Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	agent := ccmemory.NewHandle(home, nil)
	t.Cleanup(func() { agent.Close() })
	engine := core.New(st, core.Options{ProjectDir: dir, AgentMemory: agent})
	srv := New(engine)
	srv.SetProjectRouter(fakeRouter{multi: true})
	return srv
}

// callAgentQuery runs one query call through handleQuery with ctx as the
// resolved caller, the way the HTTP layer hands a caller to a tool. It returns
// the text answer or the error.
func callAgentQuery(ctx context.Context, srv *Server, args string) (string, error) {
	req := &mcp.CallToolRequest{}
	req.Params = &mcp.CallToolParamsRaw{Name: core.ToolQuery, Arguments: json.RawMessage(args)}
	res, err := srv.handleQuery(ctx, req)
	if err != nil {
		return "", err
	}
	return res.Content[0].(*mcp.TextContent).Text, nil
}

// agentRecallSetup gives one test a home with seeded central banks and a
// multi-project server over them.
func agentRecallSetup(t *testing.T) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	seedCentralBanks(t, home)
	return newAgentRecallServer(t, home)
}

// TestAgentRecallAnswersUnregisteredProjectFromCentralBanks is the routing
// exemption: agent_recall names a project the server does not serve and is
// still answered from the central banks, while every other memory command with
// that same project is refused.
func TestAgentRecallAnswersUnregisteredProjectFromCentralBanks(t *testing.T) {
	srv := agentRecallSetup(t)
	ctx := context.Background()

	out, err := callAgentQuery(ctx, srv, `{"action":"memory","query":"deploys","project":"`+placeholderRepoDir+`","args":{"command":"agent_recall","scope":"all"}}`)
	if err != nil {
		t.Fatalf("agent_recall with an unregistered project was refused: %v", err)
	}
	if !strings.Contains(out, "Release freeze") {
		t.Fatalf("agent_recall did not answer from the central banks: %s", out)
	}

	for _, args := range []string{
		`{"action":"exact","query":"deploys","project":"` + placeholderRepoDir + `"}`,
		`{"action":"memory","query":"deploys","project":"` + placeholderRepoDir + `","args":{"command":"memories"}}`,
	} {
		_, err := callAgentQuery(ctx, srv, args)
		if err == nil || !strings.Contains(err.Error(), "fakeRouter: consulted") {
			t.Fatalf("a non-recall call with an unregistered project must still be routed and refused; args %s, err = %v", args, err)
		}
	}
}

// TestAgentRecallProjectArgumentIsCallerIdentity pins the identity rule: the
// project argument picks the caller's repository bank (the engine resolves it
// with ProjectKey), it never routes the call.
func TestAgentRecallProjectArgumentIsCallerIdentity(t *testing.T) {
	srv := agentRecallSetup(t)
	out, err := callAgentQuery(context.Background(), srv, `{"action":"memory","query":"deploys","project":"`+placeholderRepoDir+`","args":{"command":"agent_recall","scope":"project"}}`)
	if err != nil {
		t.Fatalf("agent_recall: %v", err)
	}
	var got struct {
		Project  string `json:"project"`
		Memories []struct {
			Name string `json:"name"`
		} `json:"memories"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("answer is not JSON: %v: %s", err, out)
	}
	if want := claudecode.Slug(placeholderRepoDir); got.Project != want {
		t.Fatalf("project = %q, want the caller bank %q", got.Project, want)
	}
	if len(got.Memories) == 0 {
		t.Fatalf("project scope returned no rows for the caller's bank: %s", out)
	}
}

// TestAgentRecallRefusesProjectRestrictedCaller is the fail-closed rule: the
// central banks span every repository, so a caller whose token is limited to
// some projects, or to an organization's projects, is refused, and the refusal
// names no bank and no project.
func TestAgentRecallRefusesProjectRestrictedCaller(t *testing.T) {
	srv := agentRecallSetup(t)
	args := `{"action":"memory","query":"deploys","args":{"command":"agent_recall","scope":"all"}}`

	restricted := map[string]caller{
		"project grant": {ac: auth.AuthContext{Role: auth.Viewer, Via: auth.ViaToken, Scopes: []string{"project:example-repo-a"}}},
		"org scope": {
			ac:      auth.AuthContext{Role: auth.Viewer, Via: auth.ViaToken, OrgID: "org-placeholder"},
			visible: func(string) bool { return false },
		},
	}
	for name, c := range restricted {
		t.Run(name, func(t *testing.T) {
			ctx := withCaller(context.Background(), c)
			_, err := callAgentQuery(ctx, srv, args)
			if err == nil {
				t.Fatal("a project-restricted caller was answered from the central banks")
			}
			var e *errs.Error
			if !errors.As(err, &e) || e.Entry.Code != errs.PermissionDenied.Code {
				t.Fatalf("err = %v; want %s", err, errs.PermissionDenied.Code)
			}
			for _, leak := range []string{"example-repo-a", "cc-", "Release freeze"} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("refusal leaks %q: %v", leak, err)
				}
			}
		})
	}
}

// TestAgentRecallLeavesFourTools keeps the registry at exactly four tools:
// agent_recall is a command inside query, not a tool of its own.
func TestAgentRecallLeavesFourTools(t *testing.T) {
	_, session := newProjectServerForTest(t)
	if got := len(listSchemas(t, session)); got != 4 {
		t.Fatalf("tools/list reports %d tools, want 4", got)
	}
}

// TestAgentRecallIsAdvertisedInQuerySchema pins the schema text: the query tool
// and its args name the command and its arguments where the other memory
// commands are named.
func TestAgentRecallIsAdvertisedInQuerySchema(t *testing.T) {
	_, session := newProjectServerForTest(t)
	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != core.ToolQuery {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(tool.Description, "agent_recall") {
			t.Errorf("query description does not name agent_recall: %s", tool.Description)
		}
		var schema struct {
			Properties struct {
				Args struct {
					Description string `json:"description"`
				} `json:"args"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"agent_recall", "cwd", "project|global|all", "types"} {
			if !strings.Contains(schema.Properties.Args.Description, want) {
				t.Errorf("query args description missing %q: %s", want, schema.Properties.Args.Description)
			}
		}
		return
	}
	t.Fatal("query tool is not advertised")
}

// TestSearchWithAgentRecallCommandIsNotRoutedAsRecall pins that only
// action=memory is agent_recall: a search that carries args.command=agent_recall
// is routed like any other search. For a limited caller it must not meet the
// central-memory refusal, and for any caller it must not be answered from the
// central banks.
func TestSearchWithAgentRecallCommandIsNotRoutedAsRecall(t *testing.T) {
	srv := agentRecallSetup(t)
	args := `{"action":"search","query":"deploys","args":{"command":"agent_recall","scope":"all"}}`

	out, err := callAgentQuery(context.Background(), srv, args)
	if err == nil && strings.Contains(out, "Release freeze") {
		t.Fatalf("action=search with command=agent_recall was answered from the central banks: %s", out)
	}

	limited := withCaller(context.Background(), caller{ac: auth.AuthContext{Role: auth.Viewer, Via: auth.ViaToken, Scopes: []string{"project:example-repo-a"}}})
	_, err = callAgentQuery(limited, srv, args)
	if err != nil && strings.Contains(err.Error(), "spans every repository") {
		t.Fatalf("action=search met the agent_recall refusal: %v", err)
	}
}
