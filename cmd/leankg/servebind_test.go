package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/servebind"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestServeBindRefusesExposedREST pins the RS-03 exposure rule end to end:
// `leankg serve --rest <non-loopback>` must be REFUSED when no auth token
// is configured, and must come up when one is.
//
// The refusal is the whole point: with the gate off every caller resolves
// to Admin, so a wildcard bind hands the project graph, the file reader and
// the memory write path to the LAN. The pre-fix code only logged a warning
// and bound anyway — measured 2026-10-21, a LAN POST to the REST listener
// answered {"written":1}.
//
// The refusal is checked on the PROCESS, not on a helper: a helper test
// would pass while the serve path still bound the listener, which is
// exactly the bug this closes.
func TestServeBindRefusesExposedREST(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the leankg binary")
	}
	bin := buildLeanKG(t)
	proj := seedProject(t)
	if out, err := exec.Command(bin, "index", proj, "--auto").CombinedOutput(); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	// A free port on every interface: the shape the live server was measured
	// on (`leankg serve --rest :9700`).
	port := freePortStr(t)
	addr := ":" + port

	// 1. No token -> refused, with an actionable message.
	out, err := runServeExpectExit(t, bin, proj, addr, nil)
	if err == nil {
		t.Fatalf("serve --rest %s with no token exited 0; want a refusal\n%s", addr, out)
	}
	if !strings.Contains(out, "refusing to serve REST") {
		t.Fatalf("serve --rest %s: no refusal message in output:\n%s", addr, out)
	}
	if !strings.Contains(out, "leankg auth token create") || !strings.Contains(out, "LEANKG_TOKEN_ADMIN") {
		t.Fatalf("serve --rest %s: refusal does not name the fix:\n%s", addr, out)
	}
	if !strings.Contains(out, "127.0.0.1:"+port) {
		t.Fatalf("serve --rest %s: refusal does not name the loopback address:\n%s", addr, out)
	}

	// 2. A token in the environment -> the same bind is accepted and the
	// listener comes up.
	out, err = runServeExpectExit(t, bin, proj, addr, []string{"LEANKG_TOKEN_ADMIN=s3cret"})
	if err != nil {
		t.Fatalf("serve --rest %s with LEANKG_TOKEN_ADMIN: %v\n%s", addr, err, out)
	}
	if strings.Contains(out, "refusing to serve REST") {
		t.Fatalf("serve --rest %s with a token was refused:\n%s", addr, out)
	}
	if !strings.Contains(out, "leankg serve (REST) on "+addr) {
		t.Fatalf("serve --rest %s with a token: listener did not come up:\n%s", addr, out)
	}
}

// TestServeBindAcceptsLoopbackREST is the other half: the loopback REST
// listener the omp memory backend depends on (http://127.0.0.1:9700) must
// keep working with no token at all. A refusal here would break the
// hindsight-compat client.
func TestServeBindAcceptsLoopbackREST(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the leankg binary")
	}
	bin := buildLeanKG(t)
	proj := seedProject(t)
	if out, err := exec.Command(bin, "index", proj, "--auto").CombinedOutput(); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	addr := "127.0.0.1:" + freePortStr(t)
	out, err := runServeExpectExit(t, bin, proj, addr, nil)
	if err != nil {
		t.Fatalf("serve --rest %s (loopback, no token): %v\n%s", addr, err, out)
	}
	if strings.Contains(out, "refusing to serve") {
		t.Fatalf("serve --rest %s (loopback, no token) was refused:\n%s", addr, out)
	}
	if !strings.Contains(out, "leankg serve (REST) on "+addr) {
		t.Fatalf("serve --rest %s (loopback, no token): listener did not come up:\n%s", addr, out)
	}
}

// TestServeBindRefusesExposedRPCAndMCP pins the same rule for the two
// listeners that had no exposure check at all: the ConnectRPC listener and
// the MCP streamable-HTTP listener. The MCP one is the surface that indexes
// and imports, so it is the widest door of the three.
func TestServeBindRefusesExposedRPCAndMCP(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the leankg binary")
	}
	bin := buildLeanKG(t)
	proj := seedProject(t)
	if out, err := exec.Command(bin, "index", proj, "--auto").CombinedOutput(); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	for _, c := range []struct {
		flag string
		want string
	}{
		{"--rpc", "refusing to serve ConnectRPC"},
		{"--http", "refusing to serve MCP"},
	} {
		addr := ":" + freePortStr(t)
		cmd := exec.Command(bin, "serve", "--read-only", c.flag, addr, "--project", proj)
		cmd.Env = tokenFreeEnv()
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("serve %s %s with no token exited 0; want a refusal\n%s", c.flag, addr, out)
		}
		if !strings.Contains(string(out), c.want) {
			t.Fatalf("serve %s %s with no token: want %q in output:\n%s", c.flag, addr, c.want, out)
		}
	}
}

// TestServeBindDefaultIsLoopback pins the shipped default: with no address
// flag the MCP listener binds 127.0.0.1, never every interface.
func TestServeBindDefaultIsLoopback(t *testing.T) {
	if defaultHTTPAddr != "127.0.0.1:9699" {
		t.Fatalf("default MCP address %q is not loopback", defaultHTTPAddr)
	}
}

// runServeExpectExit runs `leankg serve --rest addr` and returns its
// combined output plus the exit error.
//
// The serve command runs until it is signalled, so the process is stopped
// once the listener has come up. A refusal exits on its own; an accepted
// bind is stopped after a short settle.
func runServeExpectExit(t *testing.T, bin, proj, addr string, env []string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, "serve", "--read-only", "--rest", addr, "--project", proj)
	cmd.Env = append(tokenFreeEnv(), env...)
	out := &strings.Builder{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("serve: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(3 * time.Second):
		// The listener is up (or the server is settling); stop it.
		_ = cmd.Process.Kill()
		<-done
		return out.String(), nil
	}
}

// tokenFreeEnv is the child environment with every LEANKG_TOKEN_* variable
// removed, so a token on the developer's machine cannot make the
// "no token" case pass.
func tokenFreeEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "LEANKG_TOKEN_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// freePortStr returns a port that was free a moment ago, as a string.
func freePortStr(t *testing.T) string {
	t.Helper()
	return strconv.Itoa(freePort(t))
}

// TestServeBindAcceptsWildcardWithSiteAdmin pins that a site-admin account counts
// as "auth configured". serve feeds servebind the same probe this test runs
// (auth.GateEnabled over the store), so a wildcard REST bind is refused while
// nothing gates the callers and allowed once the site admin exists.
func TestServeBindAcceptsWildcardWithSiteAdmin(t *testing.T) {
	t.Setenv("LEANKG_TOKEN_ADMIN", "")
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
	t.Setenv("LEANKG_TOKEN_VIEWER", "")
	st, err := openFixtureStore(t.TempDir()+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	check := func() error {
		t.Helper()
		gateOn, err := auth.GateEnabled(context.Background(), st)
		if err != nil {
			t.Fatal(err)
		}
		return servebind.Check(servebind.KindREST, ":9700", servebind.Options{GateOn: gateOn})
	}
	if err := check(); err == nil {
		t.Fatal("a wildcard bind with no token and no site admin must be refused")
	}
	if _, err := auth.CreateSiteAdmin(context.Background(), st, "ops", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if err := check(); err != nil {
		t.Fatalf("a wildcard bind with a site admin must be allowed: %v", err)
	}
}

// TestServeUIFileRouteExposure pins D1: the --ui dashboard's GET /api/file
// reads any file under the project root. With no token configured the route
// is served only on a loopback --ui bind; a wildcard bind answers 403
// (PERMISSION_DENIED) instead of handing the file reader to the LAN. With a
// viewer token configured, the wildcard bind keeps requiring it.
//
// The route is built inside serve, so the check runs against the real process.
func TestServeUIFileRouteExposure(t *testing.T) {
	bin := buildLeanKG(t)
	proj := seedProject(t)
	if out, err := exec.Command(bin, "index", proj, "--auto").CombinedOutput(); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	for _, c := range []struct {
		name string
		host string
		env  []string
		auth string
		want int
	}{
		{"wildcard, no token: refused", "0.0.0.0", nil, "", http.StatusForbidden},
		{"loopback, no token: open", "127.0.0.1", nil, "", http.StatusOK},
		{"wildcard, viewer token, no bearer: 401", "0.0.0.0", []string{"LEANKG_TOKEN_VIEWER=s3cret"}, "", http.StatusUnauthorized},
		{"wildcard, viewer token, bearer: open", "0.0.0.0", []string{"LEANKG_TOKEN_VIEWER=s3cret"}, "Bearer s3cret", http.StatusOK},
	} {
		t.Run(c.name, func(t *testing.T) {
			port := freePortStr(t)
			cmd := exec.Command(bin, "serve", "--read-only", "--ui", c.host+":"+port, "--http", "127.0.0.1:0", "--project", proj)
			cmd.Env = append(tokenFreeEnv(), c.env...)
			logs := &lockedBuffer{}
			cmd.Stdout, cmd.Stderr = logs, logs
			if err := cmd.Start(); err != nil {
				t.Fatalf("serve: %v", err)
			}
			defer func() {
				_ = cmd.Process.Kill()
				_, _ = cmd.Process.Wait()
			}()

			base := "http://127.0.0.1:" + port
			for deadline := time.Now().Add(20 * time.Second); ; {
				if resp, err := http.Get(base + "/health"); err == nil {
					resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("dashboard did not come up:\n%s", logs.String())
				}
				time.Sleep(100 * time.Millisecond)
			}

			req, err := http.NewRequest(http.MethodGet, base+"/api/file?path=demo.go", nil)
			if err != nil {
				t.Fatal(err)
			}
			if c.auth != "" {
				req.Header.Set("Authorization", c.auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET /api/file: %v\n%s", err, logs.String())
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != c.want {
				t.Fatalf("GET /api/file on --ui %s: %d, want %d; body=%s", c.host+":"+port, resp.StatusCode, c.want, body)
			}
			if c.want == http.StatusForbidden && !strings.Contains(string(body), "LEANKG_ERROR_PERMISSION_DENIED") {
				t.Errorf("403 body lacks the PERMISSION_DENIED code: %s", body)
			}
		})
	}
}
