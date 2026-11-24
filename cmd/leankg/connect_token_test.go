package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeToken is a sentinel that must never reach a client config. The env var
// is set to it in each test so a leak would be a literal match.
const fakeToken = "lkg_secret_test"

// tokenEnvCase is one client's remote entry with --token-env set: the file
// that holds it and the reference string the writer must emit.
type tokenEnvCase struct {
	client string
	rel    string
	ref    string
}

var tokenEnvCases = []tokenEnvCase{
	{ClientClaudeCode, ".claude.json", "Bearer ${LEANKG_TOKEN}"},
	{ClientCursor, filepath.Join(".cursor", "mcp.json"), "Bearer ${env:LEANKG_TOKEN}"},
	{ClientOpencode, filepath.Join(".config", "opencode", "opencode.json"), "Bearer {env:LEANKG_TOKEN}"},
	{ClientCodex, filepath.Join(".codex", "config.toml"), `bearer_token_env_var = "LEANKG_TOKEN"`},
}

// tokenEnvRefused lists the clients with no verified env-reference header
// syntax; --token-env must be refused for them.
var tokenEnvRefused = []string{ClientGemini, ClientOmp, ClientXdev}

func tokenEnvConfig(url string) Config {
	return Config{Mode: "http", URL: url, TokenEnv: "LEANKG_TOKEN"}
}

// TestWriteClientTokenEnvReference: the entry carries the client's native
// env reference and the token value never appears in the file.
func TestWriteClientTokenEnvReference(t *testing.T) {
	t.Setenv("LEANKG_TOKEN", fakeToken)
	for _, tc := range tokenEnvCases {
		t.Run(tc.client, func(t *testing.T) {
			home := t.TempDir()
			if err := WriteClient(home, tc.client, tokenEnvConfig(testURL)); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(home, tc.rel))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tc.ref) {
				t.Fatalf("missing reference %q in:\n%s", tc.ref, data)
			}
			if strings.Contains(string(data), fakeToken) {
				t.Fatalf("token value written to client config:\n%s", data)
			}
		})
	}
}

// TestWriteClientTokenEnvJSONShape pins the JSON header object exactly.
func TestWriteClientTokenEnvJSONShape(t *testing.T) {
	cases := []struct {
		client, rel, header string
	}{
		{ClientClaudeCode, ".claude.json", "Bearer ${LEANKG_TOKEN}"},
		{ClientCursor, filepath.Join(".cursor", "mcp.json"), "Bearer ${env:LEANKG_TOKEN}"},
		{ClientOpencode, filepath.Join(".config", "opencode", "opencode.json"), "Bearer {env:LEANKG_TOKEN}"},
	}
	for _, tc := range cases {
		t.Run(tc.client, func(t *testing.T) {
			home := t.TempDir()
			if err := WriteClient(home, tc.client, tokenEnvConfig(testURL)); err != nil {
				t.Fatal(err)
			}
			root := readJSON(t, filepath.Join(home, tc.rel))
			entry := root[jsonContainer(tc.client)].(map[string]any)[serverKey].(map[string]any)
			headers, ok := entry["headers"].(map[string]any)
			if !ok {
				t.Fatalf("no headers object in entry: %#v", entry)
			}
			if got := headers["Authorization"]; got != tc.header {
				t.Fatalf("Authorization: got %v, want %q", got, tc.header)
			}
		})
	}
}

// TestWriteClientTokenEnvDefaultUnchanged: without --token-env no client
// writes a headers object or a bearer_token_env_var key.
func TestWriteClientTokenEnvDefaultUnchanged(t *testing.T) {
	for _, client := range Clients() {
		t.Run(client, func(t *testing.T) {
			home := t.TempDir()
			if err := WriteClient(home, client, Config{Mode: "http", URL: testURL}); err != nil {
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
			if strings.Contains(string(data), "Authorization") || strings.Contains(string(data), "bearer_token_env_var") {
				t.Fatalf("default output carries auth wiring:\n%s", data)
			}
		})
	}
}

// TestWriteClientTokenEnvRefused: clients without a verified syntax refuse the
// flag and write nothing.
func TestWriteClientTokenEnvRefused(t *testing.T) {
	for _, client := range tokenEnvRefused {
		t.Run(client, func(t *testing.T) {
			home := t.TempDir()
			err := WriteClient(home, client, tokenEnvConfig(testURL))
			if err == nil {
				t.Fatal("want refusal, got nil error")
			}
			if !strings.Contains(err.Error(), "--token-env") {
				t.Fatalf("error does not name the flag: %v", err)
			}
			path, _ := clientConfigPath(home, client)
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatalf("refused write still created %s", path)
			}
		})
	}
}

// TestWriteClientTokenEnvInvalidName: only letters, digits and underscore.
func TestWriteClientTokenEnvInvalidName(t *testing.T) {
	for _, name := range []string{"BAD-NAME", "A B", "LEANKG_TOKEN}", "X$Y", "TOKEN;rm"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			cfg := tokenEnvConfig(testURL)
			cfg.TokenEnv = name
			if err := WriteClient(home, ClientClaudeCode, cfg); err == nil {
				t.Fatalf("name %q accepted", name)
			}
			if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
				t.Fatalf("invalid name still wrote a config")
			}
		})
	}
}

// TestWriteClientTokenEnvStdioRefused: the flag is for remote entries only.
func TestWriteClientTokenEnvStdioRefused(t *testing.T) {
	home := t.TempDir()
	cfg := Config{Mode: "stdio", Exe: testExe, TokenEnv: "LEANKG_TOKEN"}
	if err := WriteClient(home, ClientClaudeCode, cfg); err == nil {
		t.Fatal("stdio entry accepted --token-env")
	}
}

// TestWriteClientTokenEnvIdempotentAndPreserves: a second run is byte-identical
// and sibling servers in the same file survive.
func TestWriteClientTokenEnvIdempotentAndPreserves(t *testing.T) {
	t.Setenv("LEANKG_TOKEN", fakeToken)
	for _, tc := range tokenEnvCases {
		t.Run(tc.client, func(t *testing.T) {
			home := t.TempDir()
			path, err := clientConfigPath(home, tc.client)
			if err != nil {
				t.Fatal(err)
			}
			seedOtherServer(t, home, tc.client, path)
			cfg := tokenEnvConfig(testURL)
			if err := WriteClient(home, tc.client, cfg); err != nil {
				t.Fatal(err)
			}
			first, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteClient(home, tc.client, cfg); err != nil {
				t.Fatal(err)
			}
			second, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(first) != string(second) {
				t.Fatalf("re-run changed bytes:\nfirst:\n%s\nsecond:\n%s", first, second)
			}
			if !strings.Contains(string(second), "other-server") {
				t.Fatalf("sibling server dropped:\n%s", second)
			}
		})
	}
}

// seedOtherServer writes a config holding one unrelated server in the shape
// the client's writer merges into.
func seedOtherServer(t *testing.T, home, client, path string) {
	t.Helper()
	if client == ClientCodex {
		writeSeed(t, path, "[mcp_servers.other-server]\nurl = \"http://other.example\"\n")
		return
	}
	root := map[string]any{
		jsonContainer(client): map[string]any{
			"other-server": map[string]any{"type": "http", "url": "http://other.example"},
		},
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeSeed(t, path, string(data)+"\n")
}
