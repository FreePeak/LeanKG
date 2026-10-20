package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestImportToolNamesWhereTheStoreWent pins the one thing an agent cannot
// discover about FR-P2: when the store is configured to live somewhere other
// than <project>/.leankg, every verb reads THAT store, and the agent's only
// way to know is the tool description.
//
// The defect: the description said "Use action=dir with path='.' to import the
// current directory as a scoped index target (FR-P2)" — which is true and
// says nothing about the store. An agent in standalone mode (LEANKG_DB_PATH or
// db.standalone_db_path set) that indexes, then sees `status` report a
// different element count than it wrote, has no way to learn from the schema
// that the path it controls is not the path the engine reads. Wave 1 fixed the
// engine side (one configured store, honoured by every verb); this fixes the
// side that tells the agent the rule exists.
func TestImportToolNamesWhereTheStoreWent(t *testing.T) {
	session := newTestServer(t)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var desc string
	for _, tl := range tools.Tools {
		if tl.Name == "import" {
			desc = tl.Description
		}
	}
	if desc == "" {
		t.Fatal("import tool not found")
	}
	for _, want := range []string{"LEANKG_DB_PATH", "standalone_db_path", ".leankg/leankg.db"} {
		if !strings.Contains(desc, want) {
			t.Errorf("import description must mention %q so an agent knows which store the engine reads; got:\n%s", want, desc)
		}
	}
}

// TestStatusAnswersCarryTheStorePath pins the other half: a status answer must
// say which store it read, so "I indexed 9272 elements but status says 0" is
// answerable without guessing.
func TestStatusAnswersCarryTheStorePath(t *testing.T) {
	session := newTestServer(t)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("status returned no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("status content is %T", res.Content[0])
	}
	var out struct {
		Store      string `json:"store"`
		ProjectDir string `json:"project_dir"`
	}
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("decode status: %v (%s)", err, text.Text)
	}
	if out.Store == "" {
		t.Errorf("status must name the store it read (an agent comparing counts needs to know which file); got keys: %s", text.Text)
	}
}
