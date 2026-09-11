package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildLeanKG compiles the real CLI into a temp dir. Both guards below need
// the actual binary: the whole point is to execute what clients and hooks
// execute, which string-equality tests cannot check.
func buildLeanKG(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the leankg binary")
	}
	bin := filepath.Join(t.TempDir(), "leankg")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/leankg")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// seedProject builds a one-file Go repo for a spawn or hook to target.
func seedProject(t *testing.T) string {
	t.Helper()
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "demo.go"),
		[]byte("package demo\n\nfunc Handle(id string) string { return id }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj
}

// TestStdioSpawnServesMCP is the end-to-end guard for the client wiring: the
// argv `connect`/`install` write into client configs must actually start an MCP
// server. It builds the real binary and drives the emitted spawn
// (`stdioArgs("")`) over stdio, asserting the handshake and the 3-tool
// registry. The wiring used to emit `leankg mcp-stdio` — a subcommand this
// binary does not have — so every wired client spawned a process that died
// immediately, and the pinned strings in connect_test.go said nothing.
func TestStdioSpawnServesMCP(t *testing.T) {
	bin := buildLeanKG(t)
	proj := seedProject(t)

	cmd := exec.Command(bin, stdioArgs("")...)
	cmd.Dir = proj // projectless contract: the server resolves cwd
	cmd.Stderr = nil
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn %v: %v", cmd.Args, err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}()

	write := func(line string) {
		t.Helper()
		if _, err := stdin.Write([]byte(line + "\n")); err != nil {
			t.Fatalf("stdin write: %v", err)
		}
	}
	// Newline-delimited JSON-RPC over stdio. tools/list is rejected outright
	// unless initialize is answered first, so the order carries meaning.
	write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"wiring-test","version":"0"}}}`)
	write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	write(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)

	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	sawServerInfo, sawTools := false, false
	timeout := time.After(60 * time.Second) // generous: covers a cold `go build`
	for !sawServerInfo || !sawTools {
		select {
		case <-timeout:
			t.Fatalf("spawn never completed the MCP handshake (serverInfo=%v tools=%v)", sawServerInfo, sawTools)
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("spawn closed stdout before the handshake completed (serverInfo=%v tools=%v)", sawServerInfo, sawTools)
			}
			var resp struct {
				ID     int `json:"id"`
				Result struct {
					ServerInfo struct {
						Name string `json:"name"`
					} `json:"serverInfo"`
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
				} `json:"result"`
			}
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				continue // notifications are not responses
			}
			switch resp.ID {
			case 1:
				if resp.Result.ServerInfo.Name != "leankg" {
					t.Fatalf("serverInfo.name = %q, want leankg", resp.Result.ServerInfo.Name)
				}
				sawServerInfo = true
			case 2:
				names := map[string]bool{}
				for _, tool := range resp.Result.Tools {
					names[tool.Name] = true
				}
				for _, want := range []string{"import", "query", "status"} {
					if !names[want] {
						t.Fatalf("tools/list missing %q: got %v", want, names)
					}
				}
				sawTools = true
			}
		}
	}
}

// TestHookCommandRuns guards the other wiring artifact: the SessionStart hook
// string RegisterCWD writes into ~/.claude/settings.json must run. It used to
// be `leankg add <dir>`, a Rust-era verb with no Go case, so Claude Code would
// fail the hook every session. The assertion is behavioral: run the expanded
// hook and require the store it promises to create.
func TestHookCommandRuns(t *testing.T) {
	bin := buildLeanKG(t) // named "leankg", so the hook's argv[0] resolves here
	proj := seedProject(t)

	expanded := strings.ReplaceAll(hookCommand, "$CLAUDE_PROJECT_DIR", proj)
	args := strings.Fields(expanded)
	if len(args) < 2 {
		t.Fatalf("hookCommand %q: no argv to run", hookCommand)
	}
	// exec.Command resolves argv[0] against the PARENT's PATH (where a stale
	// developer `leankg` may live), so pin it to the fresh build.
	args[0] = bin
	if args[1] != "index" {
		t.Fatalf("hook %q: expected `leankg index <dir>`, got argv %v", hookCommand, args[1:])
	}
	if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("hook %q: %v\n%s", expanded, err, out)
	}
	if _, err := os.Stat(filepath.Join(proj, ".leankg", "leankg.db")); err != nil {
		t.Fatalf("hook ran but created no store under %s/.leankg: %v", proj, err)
	}
}
