package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVersionMatchesReleaseStamp keeps the MCP serverInfo honest: clients log
// this string, so it must track the one version source in the tree
// (cmd/leankg/VERSION, which the release workflow reads).
func TestVersionMatchesReleaseStamp(t *testing.T) {
	stamp, err := os.ReadFile(filepath.Join("..", "..", "cmd", "leankg", "VERSION"))
	if err != nil {
		t.Fatalf("read release stamp: %v", err)
	}
	if want := strings.TrimSpace(string(stamp)); version != want {
		t.Errorf("mcp version = %q, want %q (cmd/leankg/VERSION)", version, want)
	}
}
