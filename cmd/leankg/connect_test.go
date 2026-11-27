package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
// TestWriteClientXdevYAML pins the xdev target (FR-ZCP-14 K9): xdev reads a
// YAML mcp.yml, not JSON, so the writer must produce the shape its loader
// parses and must preserve every sibling server. Unknown targets must list
// xdev, so an operator who types the wrong one is told about it.
func TestWriteClientXdevYAML(t *testing.T) {
	home := t.TempDir()
	seed := strings.Join([]string{
		"# MCP servers for xdev",
		"servers:",
		"    atlassian:",
		"        url: http://127.0.0.1:8081/mcp",
		"        autoStart:",
		"            command: /usr/local/bin/mcp-atlassian",
		"",
	}, "\n")
	path := filepath.Join(home, ".xdev", "agent", "mcp.yml")
	writeSeed(t, path, seed)

	if err := WriteClient(home, ClientXdev, Config{Mode: "stdio", Exe: testExe}); err != nil {
		t.Fatalf("WriteClient xdev: %v", err)
	}
	var root struct {
		Servers map[string]map[string]any `yaml:"servers"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("written mcp.yml is not valid YAML: %v\n%s", err, data)
	}
	if _, ok := root.Servers["atlassian"]; !ok {
		t.Fatal("sibling server atlassian was dropped")
	}
	entry, ok := root.Servers["leankg"]
	if !ok {
		t.Fatalf("leankg entry missing; servers = %v", root.Servers)
	}
	if entry["type"] != "stdio" || entry["command"] != testExe {
		t.Fatalf("stdio entry = %v, want type=stdio command=%s", entry, testExe)
	}
	args, _ := entry["args"].([]any)
	if len(args) < 3 || args[0] != "serve" || args[1] != "--stdio" || args[2] != "--memory" {
		t.Fatalf("args = %v, want serve --stdio --memory", args)
	}
}

// TestWriteClientXdevHTTPShape: the HTTP entry carries xdev's url key and no
// command, so the streamable HTTP client is wired.
func TestWriteClientXdevHTTPShape(t *testing.T) {
	home := t.TempDir()
	if err := WriteClient(home, ClientXdev, Config{Mode: "http", URL: testURL}); err != nil {
		t.Fatalf("WriteClient xdev http: %v", err)
	}
	var root struct {
		Servers map[string]map[string]any `yaml:"servers"`
	}
	data, err := os.ReadFile(filepath.Join(home, ".xdev", "agent", "mcp.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("not valid YAML: %v", err)
	}
	entry := root.Servers["leankg"]
	if entry == nil || entry["url"] != testURL {
		t.Fatalf("http entry = %v, want url=%s", entry, testURL)
	}
	if _, ok := entry["command"]; ok {
		t.Fatal("http entry must not carry a stdio command")
	}
}

// TestXdevAutoStartSchema pins the autoStart block to xdev's AutoStartConfig
// field set. #467 shipped five knobs xdev does not define (startupWaitSecs,
// logFile, restartOnExit, restartBackoffSec, maxRestarts): the loader ignores
// unknown keys, so the file read as configured while the restart supervision
// it promised did not exist. The set below is checked against the consumer's
// struct tags. pidFile is in the set because xdev declares it; note that xdev
// does not read it yet, so a file with or without it behaves identically today
// — this guard is about not inventing keys, not about a live behaviour.
func TestXdevAutoStartSchema(t *testing.T) {
	want := map[string]bool{
		"command": true, "args": true, "cwd": true, "env": true,
		"healthUrl": true, "healthTimeoutSec": true, "pidFile": true,
	}
	got := map[string]bool{}
	for _, cfg := range []Config{
		{Mode: "stdio", Exe: testExe},
		{Mode: "http", URL: testURL},
	} {
		entry, _ := xdevEntry(cfg)["autoStart"].(map[string]any)
		if entry == nil {
			t.Fatalf("%s entry has no autoStart", cfg.Mode)
		}
		if len(entry) != len(want) {
			t.Fatalf("%s autoStart has %d keys %v, want exactly %d: %v", cfg.Mode, len(entry), entry, len(want), want)
		}
		for k := range entry {
			got[k] = true
		}
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("autoStart is missing %q", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Fatalf("autoStart carries %q, which xdev's AutoStartConfig does not define", k)
		}
	}
}

// TestWriteClientXdevAutoStartsLeankg: xdev's autoStart block is what makes a
// missing server self-heal, so the written stdio entry must carry one with the
// health probe and the sidecar port that keeps startup alive.
func TestWriteClientXdevAutoStart(t *testing.T) {
	home := t.TempDir()
	if err := WriteClient(home, ClientXdev, Config{Mode: "http", URL: testURL, Project: home}); err != nil {
		t.Fatalf("WriteClient xdev: %v", err)
	}
	var root struct {
		Servers map[string]struct {
			AutoStart map[string]any `yaml:"autoStart"`
		} `yaml:"servers"`
	}
	data, err := os.ReadFile(filepath.Join(home, ".xdev", "agent", "mcp.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("not valid YAML: %v", err)
	}
	auto := root.Servers["leankg"].AutoStart
	if auto == nil {
		t.Fatal("http entry has no autoStart block; a stopped server would not self-heal")
	}
	for _, k := range []string{"command", "healthUrl", "healthTimeoutSec"} {
		if _, ok := auto[k]; !ok {
			t.Fatalf("autoStart is missing %q: %v", k, auto)
		}
	}
}

func TestWriteClientStdioShapes(t *testing.T) {
	stdio := map[string]any{"command": testExe, "args": []any{"serve", "--stdio", "--memory"}}
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
				"type": "local", "command": []any{testExe, "serve", "--stdio", "--memory"}, "enabled": true,
			}},
		}},
		{ClientOmp, filepath.Join(".omp", "agent", "mcp.json"), map[string]any{
			"mcpServers": map[string]any{"leankg": map[string]any{
				"type": "stdio", "command": testExe, "args": []any{"serve", "--stdio", "--memory"}, "enabled": true,
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

// TestWriteClientHTTPURLHasNoQuery pins the FR-ZCP-04 URL contract at the
// writer, not just in the prose: a remote entry carries the bare URL and no
// `?project=`. Stdio resolves the project from its cwd (FR-ZCP-01 clause 1),
// so the suffix is unnecessary there; HTTP callers pass the project as a tool
// argument (roots/list was dropped 2026-11-18). A hardcoded
// absolute path is a stale machine moment waiting to happen — measured
// 2026-10-11 on this machine: three configs carrying
// `?project=/workspace-freepeak` (a Docker mount that exists nowhere on the
// host) made every initialize fail with `unknown project`, so the clients
// that read them dropped leankg from their tool list entirely.
func TestWriteClientHTTPURLHasNoQuery(t *testing.T) {
	for _, client := range Clients() {
		t.Run(client, func(t *testing.T) {
			home := t.TempDir()
			// A caller-supplied query is the shape that must not be
			// forwarded: connect --http --url "...?project=..." is the
			// exact invocation that produced the broken files above.
			u := testURL + "?project=/some/other/machine"
			if err := WriteClient(home, client, Config{Mode: "http", URL: u}); err != nil {
				t.Fatal(err)
			}
			path, err := clientConfigPath(home, client)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "?") {
				t.Fatalf("%s: emitted URL carries a query string, want the bare %q:\n%s",
					client, testURL, data)
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
	want := "[mcp_servers.leankg]\ncommand = [\"/usr/local/bin/leankg\", \"serve\", \"--stdio\", \"--memory\"]\n"
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
	want := "model = \"o3\"\n\n[mcp_servers.leankg]\ncommand = [\"/usr/local/bin/leankg\", \"serve\", \"--stdio\", \"--memory\"]\n"
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
	want := "# codex config\nmodel = \"o3\"\n\n[mcp_servers.leankg]\ncommand = [\"/usr/local/bin/leankg\", \"serve\", \"--stdio\", \"--memory\"]\n\n[other]\nx = 1\n"
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
	if want := []any{"serve", "--stdio", "--memory", "--project", project}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}

	if err := WriteClient(home, ClientCodex, Config{Mode: "stdio", Exe: testExe, Project: "rel/proj"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	wantLine := "command = [\"/usr/local/bin/leankg\", \"serve\", \"--stdio\", \"--memory\", \"--project\", \"" + project + "\"]\n"
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
			"leankg": map[string]any{"command": testExe, "args": []any{"serve", "--stdio", "--memory"}},
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
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
	if err := RegisterCWD(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	want := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"matcher": "startup|resume",
					"hooks": []any{
						map[string]any{"type": "command", "command": testExe + ` index --hook "$CLAUDE_PROJECT_DIR"`, "async": true},
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
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
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
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
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

// TestRegisterCWDReplacesLegacyGeneratedEntry: a previously generated entry
// (the pre-async `index "$CLAUDE_PROJECT_DIR"` form) is replaced in place by
// the current entry; unrelated entries keep their position and content.
func TestRegisterCWDReplacesLegacyGeneratedEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
	path := filepath.Join(home, ".claude", "settings.json")
	writeSeed(t, path, `{"hooks":{"SessionStart":[`+
		`{"matcher":"startup","hooks":[{"type":"command","command":"echo hi"}]},`+
		`{"matcher":"*","hooks":[{"type":"command","command":"/old/bin/leankg index \"$CLAUDE_PROJECT_DIR\""}]}`+
		`]}}`)
	if err := RegisterCWD(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	ss := readJSON(t, path)["hooks"].(map[string]any)["SessionStart"].([]any)
	want := []any{
		map[string]any{"matcher": "startup", "hooks": []any{map[string]any{"type": "command", "command": "echo hi"}}},
		hookEntryForTest(testExe),
	}
	if !reflect.DeepEqual(ss, want) {
		t.Fatalf("got  %#v\nwant %#v", ss, want)
	}
}

// TestRegisterCWDRemovesLegacyWhenCurrentPresent: the current entry is already
// there but a legacy generated entry sits beside it. Left alone the hook would
// run twice, so the legacy entry goes and the current one stays the only copy.
func TestRegisterCWDRemovesLegacyWhenCurrentPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
	path := filepath.Join(home, ".claude", "settings.json")
	writeSeed(t, path, `{"hooks":{"SessionStart":[`+
		`{"matcher":"startup","hooks":[{"type":"command","command":"echo hi"}]},`+
		`{"matcher":"startup|resume","hooks":[{"type":"command","command":"`+testExe+` index --hook \"$CLAUDE_PROJECT_DIR\"","async":true}]},`+
		`{"matcher":"*","hooks":[{"type":"command","command":"/old/bin/leankg index \"$CLAUDE_PROJECT_DIR\""}]}`+
		`]}}`)
	if err := RegisterCWD(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	ss := readJSON(t, path)["hooks"].(map[string]any)["SessionStart"].([]any)
	want := []any{
		map[string]any{"matcher": "startup", "hooks": []any{map[string]any{"type": "command", "command": "echo hi"}}},
		hookEntryForTest(testExe),
	}
	if !reflect.DeepEqual(ss, want) {
		t.Fatalf("got  %#v\nwant %#v", ss, want)
	}
}

// TestRegisterCWDKeepsCustomizedEntry: a leankg command with extra tokens is
// the user's own wiring, not a generated entry, so it survives untouched and
// the generated entry is appended after it.
func TestRegisterCWDKeepsCustomizedEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
	path := filepath.Join(home, ".claude", "settings.json")
	customised := `{"matcher":"*","hooks":[{"type":"command","command":"\"$HOME/.claude/hooks/freepeak-only\" env X=1 /path/leankg index --hook \"$CLAUDE_PROJECT_DIR\""}]}`
	writeSeed(t, path, `{"hooks":{"SessionStart":[`+customised+`]}}`)
	if err := RegisterCWD(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	ss := readJSON(t, path)["hooks"].(map[string]any)["SessionStart"].([]any)
	var kept any
	if err := json.Unmarshal([]byte(customised), &kept); err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 || !reflect.DeepEqual(ss[0], kept) {
		t.Fatalf("customised entry not preserved: %#v", ss)
	}
	gen := ss[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if gen["command"] != testExe+` index --hook "$CLAUDE_PROJECT_DIR"` {
		t.Fatalf("generated entry not appended: %#v", ss[1])
	}
}

// TestRegisterCWDKeepsForeignHooksOfGeneratedEntry: a legacy leankg hook that
// shares an entry with another hook loses only its own hook; the foreign one
// stays in place and the generated entry follows it.
func TestRegisterCWDKeepsForeignHooksOfGeneratedEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
	path := filepath.Join(home, ".claude", "settings.json")
	writeSeed(t, path, `{"hooks":{"SessionStart":[{"matcher":"*","hooks":[`+
		`{"type":"command","command":"/old/bin/leankg index \"$CLAUDE_PROJECT_DIR\""},`+
		`{"type":"command","command":"echo keep"}]}]}}`)
	if err := RegisterCWD(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	ss := readJSON(t, path)["hooks"].(map[string]any)["SessionStart"].([]any)
	want := []any{
		map[string]any{"matcher": "*", "hooks": []any{map[string]any{"type": "command", "command": "echo keep"}}},
		hookEntryForTest(testExe),
	}
	if !reflect.DeepEqual(ss, want) {
		t.Fatalf("got  %#v\nwant %#v", ss, want)
	}
}

// hookEntryForTest spells out the generated entry for exe, independent of
// hookEntry, so the assertions pin the wire shape.
func hookEntryForTest(exe string) map[string]any {
	return map[string]any{
		"matcher": "startup|resume",
		"hooks": []any{
			map[string]any{"type": "command", "command": exe + ` index --hook "$CLAUDE_PROJECT_DIR"`, "async": true},
		},
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
	want := []string{"claude-code", "cursor", "codex", "gemini", "opencode", "omp", "xdev"}
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
	want := "[mcp_servers.leankg]\ncommand = [\"leankg\", \"serve\", \"--stdio\", \"--memory\"]\n"
	if got != want {
		t.Fatalf("codex section:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRegisterMemoryCreatesHook pins the fresh-settings.json shape: one
// synchronous command hook with timeout 10 and no matcher key.
func TestRegisterMemoryCreatesHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
	if err := RegisterMemory(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	want := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": testExe + " memory hook", "timeout": float64(10)},
					},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

// TestRegisterMemoryPreservesSettingsAndIndexHook: unrelated keys, other hook
// events, and the index hook already present all survive; the memory entry is
// appended beside the index entry.
func TestRegisterMemoryPreservesSettingsAndIndexHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
	path := filepath.Join(home, ".claude", "settings.json")
	writeSeed(t, path, `{"model":"opus","hooks":{"Stop":[{"hooks":[]}]}}`)
	if err := RegisterCWD(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	if err := RegisterMemory(home, ClientClaudeCode, testExe); err != nil {
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
		t.Fatalf("SessionStart entries = %d, want 2 (index + memory): %#v", len(ss), ss)
	}
	index := ss[0].(map[string]any)
	if index["matcher"] != "startup|resume" {
		t.Fatalf("index entry changed: %#v", index)
	}
	memory := ss[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if memory["command"] != testExe+" memory hook" {
		t.Fatalf("memory command = %v", memory["command"])
	}
}

// TestRegisterMemoryIdempotent: a second run leaves the file byte-identical.
func TestRegisterMemoryIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
	path := filepath.Join(home, ".claude", "settings.json")
	if err := RegisterMemory(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterMemory(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("second run changed bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestRegisterMemoryKeepsForeignSessionStartEntries: a SessionStart entry
// with a matcher and a foreign command is left as it is.
func TestRegisterMemoryKeepsForeignSessionStartEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
	path := filepath.Join(home, ".claude", "settings.json")
	writeSeed(t, path, `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"echo hi"}]}]}}`)
	if err := RegisterMemory(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	ss := readJSON(t, path)["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(ss) != 2 {
		t.Fatalf("SessionStart entries = %d, want 2: %#v", len(ss), ss)
	}
	foreign := ss[0].(map[string]any)
	if foreign["matcher"] != "startup" {
		t.Fatalf("foreign entry changed: %#v", foreign)
	}
}

// TestRegisterMemoryReplacesStaleBinaryPath: an earlier memory entry under a
// different binary path is recognised by its ` memory hook` suffix and
// replaced in place, so exactly one memory entry remains.
func TestRegisterMemoryReplacesStaleBinaryPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "") // settings live under home, whatever the caller exports
	path := filepath.Join(home, ".claude", "settings.json")
	writeSeed(t, path, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/old/leankg memory hook","timeout":10}]}]}}`)
	if err := RegisterMemory(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	ss := readJSON(t, path)["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(ss) != 1 {
		t.Fatalf("SessionStart entries = %d, want 1: %#v", len(ss), ss)
	}
	hook := ss[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if hook["command"] != testExe+" memory hook" {
		t.Fatalf("command = %v, want %s", hook["command"], testExe+" memory hook")
	}
}

// TestRegisterMemoryUnsupportedClient: only claude-code has a hook mechanism.
func TestRegisterMemoryUnsupportedClient(t *testing.T) {
	for _, c := range []string{ClientCursor, ClientCodex, ClientGemini, ClientOpencode, ClientOmp} {
		if err := RegisterMemory(t.TempDir(), c, ""); !errors.Is(err, ErrUnsupportedClient) {
			t.Fatalf("%s: err = %v, want ErrUnsupportedClient", c, err)
		}
	}
}

// TestRegisterHooksHonorClaudeConfigDir: Claude Code reads its settings from
// $CLAUDE_CONFIG_DIR, so both hooks land in that directory and not under
// <home>/.claude, where Claude Code would never look.
func TestRegisterHooksHonorClaudeConfigDir(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "claude-config")
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	if err := RegisterCWD(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	if err := RegisterMemory(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	ss := readJSON(t, filepath.Join(cfg, "settings.json"))["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(ss) != 2 {
		t.Fatalf("SessionStart entries under CLAUDE_CONFIG_DIR = %d, want 2 (index + memory): %#v", len(ss), ss)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("settings were written under <home>/.claude (stat err = %v)", err)
	}
}

// TestRegisterMemoryKeepsForeignToolWithMemoryHookSuffix: a user's own tool that
// ends in " memory hook" is not a leankg hook. It survives the add, and it also
// survives the replacement of a stale leankg entry beside it.
func TestRegisterMemoryKeepsForeignToolWithMemoryHookSuffix(t *testing.T) {
	cases := map[string]string{
		"add": `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/opt/acme/tool memory hook"}]}]}}`,
		"stale replacement": `{"hooks":{"SessionStart":[` +
			`{"hooks":[{"type":"command","command":"/opt/acme/tool memory hook"}]},` +
			`{"hooks":[{"type":"command","command":"/old/leankg memory hook","timeout":10}]}]}}`,
	}
	for name, seed := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			path := filepath.Join(home, ".claude", "settings.json")
			writeSeed(t, path, seed)
			if err := RegisterMemory(home, ClientClaudeCode, testExe); err != nil {
				t.Fatal(err)
			}
			ss := readJSON(t, path)["hooks"].(map[string]any)["SessionStart"].([]any)
			foreign := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/opt/acme/tool memory hook"}}}
			if len(ss) != 2 || !reflect.DeepEqual(ss[0], foreign) {
				t.Fatalf("foreign tool not kept first: %#v", ss)
			}
			ours := ss[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)
			if ours["command"] != testExe+" memory hook" {
				t.Fatalf("leankg memory command = %v", ours["command"])
			}
		})
	}
}

// TestRegisterCWDKeepsForeignToolWithIndexHookSuffix: the same rule for the index
// hook. A foreign ` index --hook "$CLAUDE_PROJECT_DIR"` command is not replaced.
func TestRegisterCWDKeepsForeignToolWithIndexHookSuffix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	path := filepath.Join(home, ".claude", "settings.json")
	foreign := `{"matcher":"*","hooks":[{"type":"command","command":"/opt/acme/tool index --hook \"$CLAUDE_PROJECT_DIR\""}]}`
	writeSeed(t, path, `{"hooks":{"SessionStart":[`+foreign+`]}}`)
	if err := RegisterCWD(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	ss := readJSON(t, path)["hooks"].(map[string]any)["SessionStart"].([]any)
	var kept any
	if err := json.Unmarshal([]byte(foreign), &kept); err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 || !reflect.DeepEqual(ss[0], kept) {
		t.Fatalf("foreign index tool not kept: %#v", ss)
	}
}

// TestRegisterMemoryKeepsSettingsFileMode: a rewritten settings file keeps the
// mode it had, so a 0600 file that may hold env values stays private. A new
// file keeps the 0644 default.
func TestRegisterMemoryKeepsSettingsFileMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	path := filepath.Join(home, ".claude", "settings.json")
	writeSeed(t, path, `{"env":{"TOKEN_NAME":"placeholder"}}`)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RegisterMemory(home, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %v, want 0600 kept", fi.Mode().Perm())
	}

	fresh := t.TempDir()
	if err := RegisterMemory(fresh, ClientClaudeCode, testExe); err != nil {
		t.Fatal(err)
	}
	fi, err = os.Stat(filepath.Join(fresh, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("new settings mode = %v, want 0644", fi.Mode().Perm())
	}
}

// TestRegisterRejectsNonObjectSettings: settings that are valid JSON but not an
// object are a clear error, and the file is left byte-identical.
func TestRegisterRejectsNonObjectSettings(t *testing.T) {
	cases := map[string]string{"null": "null\n", "array": "[]\n", "string": "\"text\"\n", "number": "42\n"}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			path := filepath.Join(home, ".claude", "settings.json")
			writeSeed(t, path, content)
			err := RegisterMemory(home, ClientClaudeCode, testExe)
			if err == nil || !strings.Contains(err.Error(), "settings file is not a JSON object") {
				t.Fatalf("err = %v, want the settings file is not a JSON object error", err)
			}
			if got, _ := os.ReadFile(path); string(got) != content {
				t.Fatalf("settings changed to %q, want %q unchanged", got, content)
			}
		})
	}
}
