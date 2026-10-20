package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRESTListenerServesAutoConfig guards a dropped return value:
// cmd/leankg/main.go called restauto.RegisterAutoConfig(h, engine) for its
// side effect, but the function returns the mux it wrapped, so the endpoint
// it mounted lived in a mux nobody served. Both listeners that register it
// answered 404 for POST /api/v1/mcp/auto-config while the log line and the
// package claimed otherwise — the auto-index/auto-embed handshake a fresh
// install relies on was dead.
func TestRESTListenerServesAutoConfig(t *testing.T) {
	bin := buildLeanKG(t)
	proj := seedProject(t)
	if out, err := exec.Command(bin, "index", proj, "--auto").CombinedOutput(); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "serve", "--read-only", "--rest", addr, "--project", proj)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	url := fmt.Sprintf("http://%s/api/v1/mcp/auto-config", addr)
	var code int
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Post(url, "application/json", strings.NewReader(
			`{"index_enabled":false,"embed_enabled":false}`))
		if err == nil {
			code = resp.StatusCode
			_ = resp.Body.Close()
			// 200 = mounted; 404 = the mux that mounted it was discarded.
			if code == http.StatusOK {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if code != http.StatusOK {
		t.Fatalf("POST %s: %d (want 200 — the auto-config endpoint must be mounted)", url, code)
	}
}
