package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestSchemaPropertiesAreConsumed pins RS-06: every property a tool's
// advertised inputSchema names must be a JSON field of the request struct the
// handler decodes into (or `project`, which the router reads). A property the
// struct lacks is silently dropped by json.Unmarshal — `new_path` broke
// `memory rename` that way, after `content` (#443) broke memory writes.
func TestSchemaPropertiesAreConsumed(t *testing.T) {
	session := newTestServer(t)
	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	structs := map[string]reflect.Type{
		"import": reflect.TypeOf(core.ImportRequest{}),
		"query":  reflect.TypeOf(core.QueryRequest{}),
	}
	for _, tool := range res.Tools {
		typ, ok := structs[tool.Name]
		if !ok {
			continue
		}
		fields := map[string]bool{"project": true}
		for i := range typ.NumField() {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			fields[name] = true
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		for prop := range schema.Properties {
			if !fields[prop] {
				t.Errorf("tool %q advertises %q but %s has no such JSON field — the value is dropped", tool.Name, prop, typ.Name())
			}
		}
	}
}
