package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain doubles as the fake llama-server sidecar used by the spawn
// tests: the test binary re-executes itself (through a stub script, the
// way a real operator would point LEANKG_EMBED_SIDECAR_CMD at something)
// with LEANKG_EMBED_TEST_SIDECAR=1 and serves /health + /v1/embeddings on
// the --port it is given.
func TestMain(m *testing.M) {
	if os.Getenv("LEANKG_EMBED_TEST_SIDECAR") == "1" {
		runFakeSidecarProcess()
		return
	}
	os.Exit(m.Run())
}

// runFakeSidecarProcess serves the llama-server wire surface: /health
// answers 200 once "ready" (after an optional startup delay) and
// /v1/embeddings returns deterministic vectors of the configured width.
func runFakeSidecarProcess() {
	port := 8080
	for i, a := range os.Args {
		if a == "--port" && i+1 < len(os.Args) {
			port, _ = strconv.Atoi(os.Args[i+1])
		}
	}
	if v := os.Getenv("LEANKG_EMBED_TEST_HEALTH_DELAY_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
	}
	// Spawn a child that outlives this process unless the WHOLE process
	// group is killed — the process-group shutdown contract.
	if p := os.Getenv("LEANKG_EMBED_TEST_CHILD_PID_FILE"); p != "" {
		child := exec.Command("sleep", "300")
		if err := child.Start(); err == nil {
			_ = os.WriteFile(p, []byte(strconv.Itoa(child.Process.Pid)), 0o644)
			go child.Wait()
		}
	}
	dims := 8
	if v := os.Getenv("LEANKG_EMBED_TEST_DIMS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dims = n
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var req embeddingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data := make([]struct {
			Embedding []float32 `json:"embedding"`
		}, len(req.Input))
		for i := range data {
			vec := make([]float32, dims)
			for j := range vec {
				vec[j] = float32(i+1) / float32(j+2)
			}
			data[i].Embedding = vec
		}
		out := map[string]any{"object": "list", "data": data}
		_ = json.NewEncoder(w).Encode(out)
	})
	if err := http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux); err != nil {
		fmt.Fprintln(os.Stderr, "fake sidecar:", err)
		os.Exit(1)
	}
}

// stubSidecarScript writes the shell stub the spawn tests point
// LEANKG_EMBED_SIDECAR_CMD at: it re-execs the test binary in fake-sidecar
// mode (env carried over), so no real llama.cpp is ever needed.
func stubSidecarScript(t *testing.T) string {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEANKG_EMBED_TEST_FAKE_BIN", bin)
	path := filepath.Join(t.TempDir(), "fake-llama-server.sh")
	script := "#!/bin/sh\nLEANKG_EMBED_TEST_SIDECAR=1 exec \"$LEANKG_EMBED_TEST_FAKE_BIN\" \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func waitPIDGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pid %d still alive after shutdown", pid)
}

func TestSidecarLifecycle(t *testing.T) {
	script := stubSidecarScript(t)
	t.Setenv("LEANKG_EMBED_TEST_DIMS", "8")
	t.Setenv("LEANKG_EMBED_TEST_HEALTH_DELAY_MS", "300") // poll loop must wait for readiness

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("LEANKG_EMBED_TEST_CHILD_PID_FILE", pidFile)

	port, err := freeTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc, err := StartSidecar(ctx, SidecarConfig{
		Command: script, Port: port,
		ReadyTimeout: 15 * time.Second, PollInterval: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StartSidecar: %v", err)
	}
	if want := fmt.Sprintf("http://127.0.0.1:%d/v1", port); sc.BaseURL() != want {
		t.Fatalf("BaseURL: got %q, want %q", sc.BaseURL(), want)
	}

	// The exposed base URL serves the OpenAI wire shape.
	p := OpenAICompatible(sc.BaseURL(), "", "fake", 8, "test")
	vecs, err := p.Embed(ctx, Document, []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Embed via sidecar: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 8 {
		t.Fatalf("Embed: got %d vectors of %d dims", len(vecs), len(vecs[0]))
	}

	sidecarPID := sc.cmd.Process.Pid
	childRaw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("fake sidecar did not record child pid: %v", err)
	}
	childPID, _ := strconv.Atoi(strings.TrimSpace(string(childRaw)))
	if childPID <= 0 || !pidAlive(childPID) {
		t.Fatalf("fake sidecar child pid %d not alive before shutdown (group setup broken)", childPID)
	}

	// Shutdown kills the whole process group: sidecar AND its child.
	if err := sc.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := sc.Shutdown(); err != nil { // idempotent
		t.Fatalf("second Shutdown: %v", err)
	}
	waitPIDGone(t, sidecarPID)
	waitPIDGone(t, childPID)
}

func TestSidecarShutdownOnContextCancel(t *testing.T) {
	script := stubSidecarScript(t)
	port, err := freeTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sc, err := StartSidecar(ctx, SidecarConfig{
		Command: script, Port: port,
		ReadyTimeout: 15 * time.Second, PollInterval: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StartSidecar: %v", err)
	}
	pid := sc.cmd.Process.Pid
	cancel() // must trigger the clean shutdown path
	waitPIDGone(t, pid)
}

func TestStartProviderSpawnsSidecar(t *testing.T) {
	script := stubSidecarScript(t)
	t.Setenv("LEANKG_EMBED_PROVIDER", "local")
	t.Setenv("LEANKG_EMBED_SIDECAR_CMD", script)
	t.Setenv("LEANKG_EMBED_TEST_DIMS", "8")
	port, err := freeTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEANKG_EMBED_SIDECAR_PORT", strconv.Itoa(port))
	t.Setenv("LEANKG_EMBED_DIMS", "8")

	p, release, err := StartProvider(context.Background())
	if err != nil {
		t.Fatalf("StartProvider: %v", err)
	}
	defer release()
	if p.Provider() != "local" {
		t.Fatalf("Provider: got %q, want local", p.Provider())
	}
	if p.ModelID() != "local" || p.Revision() != "local:local" || p.Dimensions() != 8 {
		t.Fatalf("stamp identity: got %s/%s/%d", p.ModelID(), p.Revision(), p.Dimensions())
	}
	// A non-default model/revision flows into the stamp (fresh sidecar on a
	// fresh port: the first one still holds its port).
	port2, err := freeTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEANKG_EMBED_SIDECAR_PORT", strconv.Itoa(port2))
	t.Setenv("LEANKG_EMBED_MODEL", "nomic-embed")
	t.Setenv("LEANKG_EMBED_REVISION", "gguf:abc123")
	p2, release2, err := StartProvider(context.Background())
	if err != nil {
		t.Fatalf("StartProvider (named model): %v", err)
	}
	defer release2()
	if p2.ModelID() != "nomic-embed" || p2.Revision() != "gguf:abc123" {
		t.Fatalf("named stamp: got %s/%s", p2.ModelID(), p2.Revision())
	}
}

func TestStartProviderAttachesToRunningSidecar(t *testing.T) {
	// Already-running OpenAI-compatible sidecar: attach, never spawn.
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.5,0.5]}]}`))
	}))
	defer srv.Close()

	t.Setenv("LEANKG_EMBED_PROVIDER", "local")
	t.Setenv("LEANKG_EMBED_BASE_URL", srv.URL+"/v1")
	t.Setenv("LEANKG_EMBED_API_KEY", "sk-test")
	t.Setenv("LEANKG_EMBED_DIMS", "2")
	// Poison the spawn path: if StartProvider tried to spawn this, it must fail loudly.
	t.Setenv("LEANKG_EMBED_SIDECAR_CMD", "leankg-no-such-sidecar-binary")

	p, release, err := StartProvider(context.Background())
	if err != nil {
		t.Fatalf("StartProvider attach: %v", err)
	}
	defer release()
	vecs, err := p.Embed(context.Background(), Query, []string{"q"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 2 {
		t.Fatalf("Embed: got %#v", vecs)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("attach mode dropped the API key: %q", gotAuth)
	}
}

func TestStartProviderSidecarAbsentFailsActionably(t *testing.T) {
	t.Setenv("LEANKG_EMBED_PROVIDER", "local")
	t.Setenv("LEANKG_EMBED_SIDECAR_CMD", "leankg-no-such-sidecar-binary")
	_, _, err := StartProvider(context.Background())
	if err == nil {
		t.Fatal("want hard error when the sidecar binary is absent")
	}
	for _, want := range []string{"not found on PATH", "LEANKG_EMBED_SIDECAR_CMD", "LEANKG_EMBED_BASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q misses %q", err, want)
		}
	}
}

func TestSidecarExitsDuringStartup(t *testing.T) {
	script := filepath.Join(t.TempDir(), "die.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho model load failed >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := StartSidecar(context.Background(), SidecarConfig{Command: script, Port: 18080})
	if err == nil {
		t.Fatal("want error when the sidecar exits during startup")
	}
	for _, want := range []string{"exited during startup", "exit status 3", "model load failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q misses %q", err, want)
		}
	}
}

func TestSidecarNotReadyWithinBudget(t *testing.T) {
	script := stubSidecarScript(t)
	t.Setenv("LEANKG_EMBED_TEST_HEALTH_DELAY_MS", "60000") // never ready inside the budget
	port, err := freeTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	_, err = StartSidecar(context.Background(), SidecarConfig{
		Command: script, Port: port,
		ReadyTimeout: 400 * time.Millisecond, PollInterval: 50 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "not ready within") {
		t.Fatalf("want not-ready error, got %v", err)
	}
	// The failed startup must not leave the process behind.
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, derr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if derr != nil {
			break
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("sidecar still listening after failed startup")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSidecarNotFoundOnPATH(t *testing.T) {
	_, err := StartSidecar(context.Background(), SidecarConfig{Command: "leankg-no-such-sidecar-binary"})
	if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Fatalf("want actionable not-found error, got %v", err)
	}
}

func TestSidecarConfigFromEnv(t *testing.T) {
	t.Setenv("LEANKG_EMBED_SIDECAR_CMD", "my-server")
	t.Setenv("LEANKG_EMBED_SIDECAR_ARGS", `-m "/models/with space.gguf" --ctx-size 2048`)
	t.Setenv("LEANKG_EMBED_SIDECAR_PORT", "9999")
	t.Setenv("LEANKG_EMBED_SIDECAR_READY_SECS", "5")
	cfg, err := SidecarConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Command != "my-server" || cfg.Port != 9999 || cfg.ReadyTimeout != 5*time.Second {
		t.Fatalf("cfg: %+v", cfg)
	}
	if len(cfg.Args) != 4 || cfg.Args[0] != "-m" || cfg.Args[1] != "/models/with space.gguf" ||
		cfg.Args[2] != "--ctx-size" || cfg.Args[3] != "2048" {
		t.Fatalf("args: %#v", cfg.Args)
	}
	t.Setenv("LEANKG_EMBED_SIDECAR_PORT", "nope")
	if _, err := SidecarConfigFromEnv(); err == nil {
		t.Fatal("want error for invalid port")
	}
	t.Setenv("LEANKG_EMBED_SIDECAR_PORT", "70000")
	if _, err := SidecarConfigFromEnv(); err == nil {
		t.Fatal("want error for out-of-range port")
	}
}

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  a  b ", []string{"a", "b"}},
		{`-m '/path/with space.gguf' -c 2048`, []string{"-m", "/path/with space.gguf", "-c", "2048"}},
		{`-m "/dq and 'sq' inside"`, []string{"-m", `/dq and 'sq' inside`}},
		{`a\ b`, []string{"a b"}},
	}
	for _, c := range cases {
		got, err := splitArgs(c.in)
		if err != nil {
			t.Fatalf("splitArgs(%q): %v", c.in, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("splitArgs(%q): got %#v, want %#v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("splitArgs(%q): got %#v, want %#v", c.in, got, c.want)
			}
		}
	}
	if _, err := splitArgs(`-m 'unterminated`); err == nil {
		t.Fatal("want error for unterminated quote")
	}
}

func TestPortFlagPresent(t *testing.T) {
	for _, args := range [][]string{{"--port", "9"}, {`--port=9`}, {"-m", "x", "--port=1"}} {
		if !portFlagPresent(args) {
			t.Fatalf("portFlagPresent(%#v) = false", args)
		}
	}
	if portFlagPresent([]string{"-m", "model.gguf", "--host", "0.0.0.0"}) {
		t.Fatal("--host must not count as a port flag")
	}
}

// freeTCPPort grabs an unused loopback port for test sidecars.
func freeTCPPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
