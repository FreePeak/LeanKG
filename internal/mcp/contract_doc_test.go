package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// renderContract renders docs/mcp-tool-contract.md from the LIVE tool registry
// (the same ListTools the client sees), so the document cannot drift from the
// surface it claims to describe.
//
// It replaces the Rust-era generator: that one read src/mcp/tools.rs, a file
// the Go rewrite deleted, and its output still advertised the 76-tool Rust
// registry (mcp_index, get_dependencies, ...) long after the engine served
// import/query/status. A doc generated from a file that no longer exists can
// only get staler.
func renderContract(t *testing.T) string {
	t.Helper()
	session := newTestServer(t)
	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var b strings.Builder
	b.WriteString("<!-- GENERATED-BY: go test ./internal/mcp -run TestToolContractDoc -->\n")
	b.WriteString("<!-- DO NOT EDIT BY HAND -->\n\n")
	b.WriteString("# MCP Tool Contract\n\n")
	fmt.Fprintf(&b, "Generated from the live tool registry (`leankgmcp.New(...).registerTools`, via `ListTools`). **%d tools.**\n", len(res.Tools))
	b.WriteString("To change the surface: edit the registry, run `go test ./internal/mcp -run TestToolContractDoc`, commit both.\n\n")
	b.WriteString("## Stability tiers\n\n")
	b.WriteString("- **stable** — input schema and output shape are contractual; breaking changes follow the deprecation policy below.\n")
	b.WriteString("- **beta** — may change or be removed in any minor release; feedback welcome.\n")
	b.WriteString("- New tools enter as **beta** and are promoted after one minor release without schema change.\n\n")
	b.WriteString("## Deprecation policy\n\n")
	b.WriteString("- Tool removal requires **2 minor releases** of deprecation notices (doc + tool description marked deprecated).\n")
	b.WriteString("- A breaking input-schema change to a stable tool requires a **minor version bump treated as major-equivalent**, plus a release notice.\n")
	b.WriteString("- Additive optional properties do not break the contract.\n\n")
	b.WriteString("## Deprecation history\n\n")
	b.WriteString("_None. The Go registry's tool count has only ever shrunk (76 Rust tools to 3), which is a rewrite, not a deprecation._\n\n")
	b.WriteString("## Tools\n\n")
	b.WriteString("| Tool | Description | Input properties |\n|---|---|---|\n")
	for _, tool := range res.Tools {
		props := tool.InputSchema
		var names []string
		if props != nil {
			if raw, ok := props.(map[string]any); ok {
				if p, ok := raw["properties"].(map[string]any); ok {
					for k := range p {
						names = append(names, k)
					}
				}
			}
		}
		desc := strings.ReplaceAll(tool.Description, "\n", " ")
		desc = strings.Join(strings.Fields(desc), " ")
		if len(desc) > 400 {
			desc = desc[:397] + "..."
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", tool.Name, desc, strings.Join(sortedStrings(names), ", "))
	}
	return b.String()
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// TestToolContractDoc is the drift guard. It fails when
// docs/mcp-tool-contract.md is not exactly what the live registry renders, so
// a tool added to the registry without the doc following it breaks the build
// instead of quietly lying. Re-render with the same test and commit both.
func TestToolContractDoc(t *testing.T) {
	want := renderContract(t)
	got, err := os.ReadFile("../../docs/mcp-tool-contract.md")
	if err != nil {
		t.Fatalf("read docs/mcp-tool-contract.md: %v", err)
	}
	if string(got) != want {
		t.Fatalf("docs/mcp-tool-contract.md is stale (%d bytes on disk, %d bytes live).\n"+
			"First divergence:\n%s\nRe-render with the same test and commit both.",
			len(got), len(want), firstDiff(string(got), want))
	}
}

// firstDiff reports the first differing line pair, which is the useful part of
// a 200-line document that no longer matches.
func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("line %d:\n  doc:  %q\n  live: %q", i+1, gl, wl)
		}
	}
	return "(no line difference — trailing content)"
}
