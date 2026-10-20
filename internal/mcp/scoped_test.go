package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestScopedServerDoesNotReAskForProject pins the multi-project HTTP wiring.
//
// routeByProject selects the project from ?project=<dir|name> and hands the
// request to a Server built for THAT project — but the routed Server was
// built with SetProjectRouter(router), so engineFor asked for args["project"]
// a second time and failed closed even though routing had already answered
// the question.
//
// Measured live on the 298-project self-host before this fix:
//
//	POST /mcp?project=/…/leankg  {tools/call query}
//	  → LEANKG_ERROR_MISSING_PARAM: this server serves multiple projects …
//
// while the SAME url routed correctly on the REST listener (whose per-project
// handler does not re-ask). The practical effect: every MCP client config
// written with a ?project= suffix — which is what the multi-project docs tell
// clients to use — was unusable, and a client that cannot inject a per-call
// project argument (opencode) had no working form at all.
func TestScopedServerDoesNotReAskForProject(t *testing.T) {
	s := newServerForTest(t)
	s.SetProjectRouter(fakeRouter{multi: true})
	s.SetProjectScoped(true)

	// No project argument at all: the handler is already scoped, so it answers.
	if _, err := s.engineFor(context.Background(), nil); err != nil {
		t.Fatalf("a scoped handler re-asked for the project: %v", err)
	}
	// An explicit project argument still wins over the scoped default.
	req := &mcp.CallToolRequest{}
	req.Params = &mcp.CallToolParamsRaw{Name: "query"}
	req.Params.Arguments = []byte(`{"project":"be-zlx"}`)
	if _, err := s.engineFor(context.Background(), req); err == nil || !containsStr(err.Error(), "fakeRouter") {
		t.Fatalf("an explicit project must still be routed; err = %v", err)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
