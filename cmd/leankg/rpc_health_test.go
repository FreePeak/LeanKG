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

// TestRPCListenerServesHealth guards FR-SELF-01's "a /health on every
// listener" AC for the ConnectRPC address, the one mount that shipped
// without it. The MCP, REST and UI listeners each expose a JSON
// {"ok":true}; --rpc answered 404 on every path but the service route,
// so an orchestrator health check against it saw a dead server that was
// in fact serving.
func TestRPCListenerServesHealth(t *testing.T) {
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

	cmd := exec.Command(bin, "serve", "--read-only", "--rpc", addr, "--project", proj)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	var code int
	var ctype, body string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			ctype = resp.Header.Get("Content-Type")
			bodyBytes, _ := io.ReadAll(resp.Body)
			body = string(bodyBytes)
			code = resp.StatusCode
			_ = resp.Body.Close()
			if code == http.StatusOK && strings.Contains(ctype, "application/json") {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code != http.StatusOK || !strings.Contains(ctype, "application/json") {
		t.Fatalf("GET /health on the ConnectRPC listener: %d %s (want 200 application/json)", code, ctype)
	}
	if !strings.Contains(body, `"ok"`) {
		t.Fatalf("GET /health on the ConnectRPC listener: body %q has no ok field", body)
	}
}

// TestRPCListenerServesService pins the other half: adding /health must
// not displace the ConnectRPC service route, which is the reason the
// listener exists.
func TestRPCListenerServesService(t *testing.T) {
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

	cmd := exec.Command(bin, "serve", "--read-only", "--rpc", addr, "--project", proj)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// The ConnectRPC service path; GET is not a valid method for it, so a
	// mounted route answers 405 while an unmounted one answers 404.
	statusURL := fmt.Sprintf("http://%s/leankg.v1.LeanKG/Status", addr)
	var code int
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		resp, err := http.Get(statusURL)
		if err == nil {
			code = resp.StatusCode
			_ = resp.Body.Close()
			if code == http.StatusMethodNotAllowed {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("GET %s: %d (want 405 — the service route must stay mounted)", statusURL, code)
	}
}
