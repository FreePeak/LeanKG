package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestInstallStdioAllClients: install --target X writes the same projectless
// stdio entry shape connect produces (command = current exe + ["serve", "--stdio"],
// NO --project flag) for every JSON client.
func TestInstallStdioAllClients(t *testing.T) {
	for _, client := range Clients() {
		if client == ClientCodex { // covered by TOML-specific tests below
			continue
		}
		t.Run(client, func(t *testing.T) {
			home := t.TempDir()
			if err := Install(home, client, InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			path, err := clientConfigPath(home, client)
			if err != nil {
				t.Fatal(err)
			}
			root := readJSON(t, path)
			container := root[jsonContainer(client)].(map[string]any)
			entry, ok := container["leankg"].(map[string]any)
			if !ok {
				t.Fatalf("no leankg entry under %q: %#v", jsonContainer(client), root)
			}
			// Entry carries the real executable of this test binary —
			// CurrentCommand() resolves os.Executable() under go test.
			exe := CurrentCommand()
			switch client {
			case ClientOpencode:
				want := []any{exe, "serve", "--stdio"}
				if !reflect.DeepEqual(entry["command"], want) {
					t.Fatalf("command = %#v, want %#v", entry["command"], want)
				}
			case ClientOmp:
				if entry["command"] != exe {
					t.Fatalf("command = %v, want %v", entry["command"], exe)
				}
				if want := []any{"serve", "--stdio"}; !reflect.DeepEqual(entry["args"], want) {
					t.Fatalf("args = %v, want %v", entry["args"], want)
				}
			default:
				if entry["command"] != exe {
					t.Fatalf("command = %v, want %v", entry["command"], exe)
				}
				if want := []any{"serve", "--stdio"}; !reflect.DeepEqual(entry["args"], want) {
					t.Fatalf("args = %v, want %v", entry["args"], want)
				}
			}
		})
	}
}

// TestInstallHTTPWritesURLEntry: install --http URL keeps the bare-URL
// contract.
func TestInstallHTTPWritesURLEntry(t *testing.T) {
	home := t.TempDir()
	if err := Install(home, ClientOmp, InstallOptions{HTTP: true, URL: testURL}); err != nil {
		t.Fatal(err)
	}
	root := readJSON(t, filepath.Join(home, ".omp", "agent", "mcp.json"))
	entry := root["mcpServers"].(map[string]any)["leankg"].(map[string]any)
	if entry["url"] != testURL || entry["type"] != "http" || entry["enabled"] != true {
		t.Fatalf("entry = %#v", entry)
	}
	if _, has := entry["command"]; has {
		t.Fatalf("http entry must not carry a command: %#v", entry)
	}
}

// TestInstallCodexStdio: the generated TOML uses the current executable as
// the single command array.
func TestInstallCodexStdio(t *testing.T) {
	home := t.TempDir()
	if err := Install(home, ClientCodex, InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "[mcp_servers.leankg]\ncommand = [" + quoteTOML(CurrentCommand()) + ", \"serve\", \"--stdio\"]\n"
	if string(data) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", data, want)
	}
}

// TestInstallProjectEscapeHatch: opts.Project emits --project into the
// stdio entry (relative path absolutized).
func TestInstallProjectEscapeHatch(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	home := t.TempDir()
	if err := Install(home, ClientClaudeCode, InstallOptions{Project: "rel/proj"}); err != nil {
		t.Fatal(err)
	}
	root := readJSON(t, filepath.Join(home, ".claude.json"))
	args := root["mcpServers"].(map[string]any)["leankg"].(map[string]any)["args"].([]any)
	want := []any{"serve", "--stdio", "--project", filepath.Join(tmp, "rel", "proj")}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

// TestInstallRegisterCWD chains the SessionStart hook write.
func TestInstallRegisterCWD(t *testing.T) {
	home := t.TempDir()
	if err := Install(home, ClientClaudeCode, InstallOptions{RegisterCWD: true}); err != nil {
		t.Fatal(err)
	}
	// MCP config written...
	root := readJSON(t, filepath.Join(home, ".claude.json"))
	if _, ok := root["mcpServers"].(map[string]any)["leankg"]; !ok {
		t.Fatalf("no leankg mcpServers entry: %#v", root)
	}
	// ...and the SessionStart hook too.
	settings := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	ss := settings["hooks"].(map[string]any)["SessionStart"].([]any)
	found := false
	for _, item := range ss {
		obj := item.(map[string]any)
		for _, h := range obj["hooks"].([]any) {
			if h.(map[string]any)["command"] == "leankg index $CLAUDE_PROJECT_DIR" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("SessionStart hook missing: %#v", settings)
	}
}

// TestInstallRegisterCWDUnsupported: --register-cwd on a client without a
// hook mechanism surfaces ErrUnsupportedClient after the config write.
func TestInstallRegisterCWDUnsupported(t *testing.T) {
	err := Install(t.TempDir(), ClientCursor, InstallOptions{RegisterCWD: true})
	if err == nil || !strings.Contains(err.Error(), ErrUnsupportedClient.Error()) {
		t.Fatalf("err = %v, want ErrUnsupportedClient", err)
	}
}

// TestInstallUnknownTargetNamesValidSet: the rejection error lists every
// valid target.
func TestInstallUnknownTargetNamesValidSet(t *testing.T) {
	err := Install(t.TempDir(), "windsurf", InstallOptions{})
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
	for _, c := range Clients() {
		if !strings.Contains(err.Error(), c) {
			t.Fatalf("error %q does not mention valid target %q", err, c)
		}
	}
}

// TestInstallHTTPWithoutURL is a configuration error.
func TestInstallHTTPWithoutURL(t *testing.T) {
	err := Install(t.TempDir(), ClientOmp, InstallOptions{HTTP: true})
	if err == nil || !strings.Contains(err.Error(), "URL") {
		t.Fatalf("err = %v, want URL-required error", err)
	}
}

// TestInstallIdempotent end-to-end: full install re-run is byte-identical
// (MCP config AND hook settings).
func TestInstallIdempotent(t *testing.T) {
	home := t.TempDir()
	run := func() (string, string) {
		t.Helper()
		if err := Install(home, ClientClaudeCode, InstallOptions{RegisterCWD: true}); err != nil {
			t.Fatal(err)
		}
		mcp, err := os.ReadFile(filepath.Join(home, ".claude.json"))
		if err != nil {
			t.Fatal(err)
		}
		settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(mcp), string(settings)
	}
	mcp1, settings1 := run()
	mcp2, settings2 := run()
	if mcp1 != mcp2 || settings1 != settings2 {
		t.Fatalf("re-run changed bytes\nmcp:\n%s\nsettings:\n%s", mcp2, settings2)
	}
}

// TestInstallPreservesExistingConfig proves the read-modify-write path on a
// real home dir (no clobber of sibling entries or settings).
func TestInstallPreservesExistingConfig(t *testing.T) {
	home := t.TempDir()
	mcpPath := filepath.Join(home, ".cursor", "mcp.json")
	writeSeedJSON(t, mcpPath, map[string]any{
		"mcpServers": map[string]any{"other": map[string]any{"command": "foo", "args": []any{"-x"}}},
	})

	if err := Install(home, ClientCursor, InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	root := readJSON(t, mcpPath)
	servers := root["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Fatalf("sibling server lost: %#v", servers)
	}
	if _, ok := servers["leankg"]; !ok {
		t.Fatalf("leankg entry missing: %#v", servers)
	}
}

// quoteTOML is strconv.Quote for readability of expected TOML strings.
func quoteTOML(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
