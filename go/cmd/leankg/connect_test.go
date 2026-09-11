package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testExe = "/usr/local/bin/leankg"

const testURL = "http://localhost:9699"

// readJSON decodes a config file for assertions.
func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return root
}

// writeSeed writes raw text content (creating parent dirs).
func writeSeed(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeSeedJSON writes a pre-existing client config.
func writeSeedJSON(t *testing.T, path string, root map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeSeed(t, path, string(data)+"\n")
}

// TestWriteClientStdioShapes pins the exact JSON shape (and container key)
// of a fresh stdio config for every JSON client. The claude-code and
// cursor/gemini entries carry NO "type" key — enforced by the full-map
// comparison.
func TestWriteClientStdioShapes(t *testing.T) {
	stdio := map[string]any{"command": testExe, "args": []any{"serve", "--stdio"}}
	cases := []struct {
		client string
		rel    string
		want   map[string]any
	}{
		{ClientClaudeCode, ".claude.json", map[string]any{
			"mcpServers": map[string]any{"leankg": stdio},
		}},
		{ClientCursor, filepath.Join(".cursor", "mcp.json"), map[string]any{
			"mcpServers": map[string]any{"leankg": stdio},
		}},
		{ClientGemini, filepath.Join(".gemini", "settings.json"), map[string]any{
			"mcpServers": map[string]any{"leankg": stdio},
		}},
		{ClientOpencode, filepath.Join(".config", "opencode", "opencode.json"), map[string]any{
			"mcp": map[string]any{"leankg": map[string]any{
				"type": "local", "command": []any{testExe, "serve", "--stdio"}, "enabled": true,
			}},
		}},
		{ClientOmp, filepath.Join(".omp", "agent", "mcp.json"), map[string]any{
			"mcpServers": map[string]any{"leankg": map[string]any{
				"type": "stdio", "command": testExe, "args": []any{"serve", "--stdio"}, "enabled": true,
			}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.client, func(t *testing.T) {
			home := t.TempDir()
			if err := WriteClient(home, tc.client, Config{Mode: "stdio", Exe: testExe}); err != nil {
				t.Fatal(err)
			}
			got := readJSON(t, filepath.Join(home, tc.rel))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

// TestWriteClientHTTPShapes pins the remote-entry shapes: bare URL, no
// command/args anywhere.
func TestWriteClientHTTPShapes(t *testing.T) {
	cases := []struct {
		client string
		rel    string
		want   map[string]any
	}{
		{ClientClaudeCode, ".claude.json", map[string]any{
			"mcpServers": map[string]any{"leankg": map[string]any{"type": "http", "url": testURL}},
		}},
		{ClientCursor, filepath.Join(".cursor", "mcp.json"), map[string]any{
			"mcpServers": map[string]any{"leankg": map[string]any{"type": "http", "url": testURL}},
		}},
		{ClientGemini, filepath.Join(".gemini", "settings.json"), map[string]any{
			"mcpServers": map[string]any{"leankg": map[string]any{"type": "http", "url": testURL}},
		}},
		{ClientOpencode, filepath.Join(".config", "opencode", "opencode.json"), map[string]any{
			"mcp": map[string]any{"leankg": map[string]any{"type": "remote", "url": testURL, "enabled": true}},
		}},
		{ClientOmp, filepath.Join(".omp", "agent", "mcp.json"), map[string]any{
			"mcpServers": map[string]any{"leankg": map[string]any{"type": "http", "url": testURL, "enabled": true}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.client, func(t *testing.T) {
			home := t.TempDir()
			if err := WriteClient(home, tc.client, Config{Mode: "http", URL: testURL}); err != nil {
				t.Fatal(err)
			}
			got := readJSON(t, filepath.Join(home, tc.rel))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

// TestWriteClientCodexStdio pins the generated TOML file byte-for-byte.
func TestWriteClientCodexStdio(t *testing.T) {
	home := t.TempDir()
	if err := WriteClient(home, ClientCodex, Config{Mode: "stdio", Exe: testExe}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "[mcp_servers.leankg]\ncommand = [\"/usr/local/bin/leankg\", \"serve\", \"--stdio\"]\n"
	if string(data) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", data, want)
	}
}

// TestWriteClientCodexHTTP pins the remote TOML shape.
func TestWriteClientCodexHTTP(t *testing.T) {
	home := t.TempDir()
	if err := WriteClient(home, ClientCodex, Config{Mode: "http", URL: testURL}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "[mcp_servers.leankg]\nurl = \"http://localhost:9699\"\n"
	if string(data) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", data, want)
	}
}

// TestWriteClientCodexAppendsSection seeds a TOML without the leankg table
// and pins the appended layout.
func TestWriteClientCodexAppendsSection(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	writeSeed(t, path, "model = \"o3\"\n")
	if err := WriteClient(home, ClientCodex, Config{Mode: "stdio", Exe: testExe}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "model = \"o3\"\n\n[mcp_servers.leankg]\ncommand = [\"/usr/local/bin/leankg\", \"serve\", \"--stdio\"]\n"
	if string(data) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", data, want)
	}
}

// TestWriteClientCodexReplacesSectionPreservingSiblings swaps a stale leankg
// table in place while comments, keys, and the sibling table survive.
func TestWriteClientCodexReplacesSectionPreservingSiblings(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	writeSeed(t, path, "# codex config\nmodel = \"o3\"\n\n[mcp_servers.leankg]\ncommand = [\"stale\"]\n\n[other]\nx = 1\n")
	if err := WriteClient(home, ClientCodex, Config{Mode: "stdio", Exe: testExe}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# codex config\nmodel = \"o3\"\n\n[mcp_servers.leankg]\ncommand = [\"/usr/local/bin/leankg\", \"serve\", \"--stdio\"]\n\n[other]\nx = 1\n"
	if string(data) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", data, want)
	}
}

// TestWriteClientProjectEscapeHatch: an explicit Project is the only source
// of a --project flag, and relative paths are absolutized against the cwd.
func TestWriteClientProjectEscapeHatch(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	home := t.TempDir()
	project := filepath.Join(tmp, "rel", "proj")

	if err := WriteClient(home, ClientClaudeCode, Config{Mode: "stdio", Exe: testExe, Project: "rel/proj"}); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, filepath.Join(home, ".claude.json"))
	args := got["mcpServers"].(map[string]any)["leankg"].(map[string]any)["args"].([]any)
	if want := []any{"serve", "--stdio", "--project", project}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}

	if err := WriteClient(home, ClientCodex, Config{Mode: "stdio", Exe: testExe, Project: "rel/proj"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	wantLine := "command = [\"/usr/local/bin/leankg\", \"serve\", \"--stdio\", \"--project\", \"" + project + "\"]\n"
	if !strings.Contains(string(data), wantLine) {
		t.Fatalf("TOML missing %q:\n%s", wantLine, data)
	}
}

// TestWriteClientPreservesSiblings: unrelated servers and unknown fields
// survive the merge.
func TestWriteClientPreservesSiblings(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".cursor", "mcp.json")
	writeSeedJSON(t, path, map[string]any{
		"mcpServers": map[string]any{"other": map[string]any{"command": "foo", "args": []any{"-x"}}},
		"theme":      "dark",
		"nested":     map[string]any{"nums": []any{1.0, 2.0}},
	})

	if err := WriteClient(home, ClientCursor, Config{Mode: "stdio", Exe: testExe}); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, path)
	want := map[string]any{
		"mcpServers": map[string]any{
			"other":  map[string]any{"command": "foo", "args": []any{"-x"}},
			"leankg": map[string]any{"command": testExe, "args": []any{"serve", "--stdio"}},
		},
		"theme":  "dark",
		"nested": map[string]any{"nums": []any{1.0, 2.0}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

// TestWriteClientIdempotent: re-running writes byte-identical configs.
func TestWriteClientIdempotent(t *testing.T) {
	for _, client := range Clients() {
		t.Run(client, func(t *testing.T) {
			home := t.TempDir()
			path, err := clientConfigPath(home, client)
			if err != nil {
				t.Fatal(err)
			}
			cfg := Config{Mode: "stdio", Exe: testExe}
			if err := WriteClient(home, client, cfg); err != nil {
				t.Fatal(err)
			}
			first, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteClient(home, client, cfg); err != nil {
				t.Fatal(err)
			}
			second, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(first) != string(second) {
				t.Fatalf("re-run changed bytes:\nfirst:\n%s\nsecond:\n%s", first, second)
			}
		})
	}
}

// TestWriteClientInvalidJSONRefused: a broken user config is an error, never
// clobbered.
func TestWriteClientInvalidJSONRefused(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".cursor", "mcp.json")
	writeSeed(t, path, "{not json")
	if err := WriteClient(home, ClientCursor, Config{Mode: "stdio", Exe: testExe}); err == nil {
		t.Fatal("expected error for invalid JSON config")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{not json" {
		t.Fatalf("config was clobbered: %q", data)
	}
}

// TestWriteClientUnknownClientNamesValidSet: the error lists every valid
// target.
func TestWriteClientUnknownClientNamesValidSet(t *testing.T) {
	err := WriteClient(t.TempDir(), "windsurf", Config{Mode: "stdio", Exe: testExe})
	if err == nil {
		t.Fatal("expected error for unknown client")
	}
	for _, c := range Clients() {
		if !strings.Contains(err.Error(), c) {
			t.Fatalf("error %q does not mention valid client %q", err, c)
		}
	}
}

// TestWriteClientConfigValidation: http needs a URL; mode must be known.
func TestWriteClientConfigValidation(t *testing.T) {
	home := t.TempDir()
	if err := WriteClient(home, ClientOmp, Config{Mode: "http"}); err == nil || !strings.Contains(err.Error(), "URL") {
		t.Fatalf("http without URL: err = %v", err)
	}
	if err := WriteClient(home, ClientOmp, Config{Mode: "grpc"}); err == nil || !strings.Contains(err.Error(), "stdio") {
		t.Fatalf("unknown mode: err = %v", err)
	}
}

// TestRegisterCWDCreatesHook pins the fresh-settings.json shape.
func TestRegisterCWDCreatesHook(t *testing.T) {
	home := t.TempDir()
	if err := RegisterCWD(home, ClientClaudeCode, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	want := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"matcher": "*",
					"hooks": []any{
						map[string]any{"type": "command", "command": "leankg index $CLAUDE_PROJECT_DIR"},
					},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

// TestRegisterCWDMergesExistingHooks: existing settings, hooks, and sibling
// hook events survive; our hook is appended to SessionStart.
func TestRegisterCWDMergesExistingHooks(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "settings.json")
	writeSeed(t, path, `{"model":"opus","hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"echo hi"}]}],"Stop":[{"hooks":[]}]}}`)
	if err := RegisterCWD(home, ClientClaudeCode, ""); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, path)
	if got["model"] != "opus" {
		t.Fatalf("model setting lost: %#v", got)
	}
	hooks := got["hooks"].(map[string]any)
	if _, ok := hooks["Stop"]; !ok {
		t.Fatalf("Stop hooks lost: %#v", hooks)
	}
	ss := hooks["SessionStart"].([]any)
	if len(ss) != 2 {
		t.Fatalf("SessionStart entries = %d, want 2: %#v", len(ss), ss)
	}
}

// TestRegisterCWDIdempotent: the identical command already present is a
// no-op.
func TestRegisterCWDIdempotent(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "settings.json")
	if err := RegisterCWD(home, ClientClaudeCode, ""); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterCWD(home, ClientClaudeCode, ""); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("duplicate hook appended:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestRegisterCWDUnsupportedClient: only claude-code has a hook mechanism.
func TestRegisterCWDUnsupportedClient(t *testing.T) {
	for _, c := range []string{ClientCursor, ClientCodex, ClientGemini, ClientOpencode, ClientOmp} {
		if err := RegisterCWD(t.TempDir(), c, ""); !errors.Is(err, ErrUnsupportedClient) {
			t.Fatalf("%s: err = %v, want ErrUnsupportedClient", c, err)
		}
	}
}

// TestClients pins the supported set and order.
func TestClients(t *testing.T) {
	want := []string{"claude-code", "cursor", "codex", "gemini", "opencode", "omp"}
	if got := Clients(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Clients() = %v, want %v", got, want)
	}
}

// TestWriteClientCodexEmptyExeFallsBack pins the PATH fallback parity with
// jsonEntry: an empty Exe must produce "leankg", never an empty array slot.
func TestWriteClientCodexEmptyExeFallsBack(t *testing.T) {
	home := t.TempDir()
	if err := WriteClient(home, ClientCodex, Config{Mode: "stdio", Exe: ""}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	want := "[mcp_servers.leankg]\ncommand = [\"leankg\", \"serve\", \"--stdio\"]\n"
	if got != want {
		t.Fatalf("codex section:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
