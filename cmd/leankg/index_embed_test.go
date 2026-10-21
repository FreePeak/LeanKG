package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// embedServer is an httptest OpenAI-compatible /v1/embeddings endpoint that
// answers every text with a dims-wide vector and counts the requests it sees.
// propsJSON512 is a sidecar /props answer for a 512-token model (text budget
// 960 runes).
const propsJSON512 = `{"model_path":"/models/bge-small-en-v1.5-f16.gguf","default_generation_settings":{"n_ctx":512}}`

func embedServer(t *testing.T, dims int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" {
			// A llama.cpp sidecar reports its context here; the local write cap needs it.
			_, _ = w.Write([]byte(propsJSON512))
			return
		}
		hits.Add(1)
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, len(body.Input))
		for i := range data {
			vec := make([]float32, dims)
			for j := range vec {
				vec[j] = 0.1
			}
			data[i] = map[string]any{"embedding": vec}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// vectorCount opens dir's store and counts the vectors written for its embedding
// stamp; a store that was never embedded has no stamp and no vectors.
func vectorCount(t *testing.T, dir string) int {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	stamps, err := st.Stamps()
	if err != nil {
		t.Fatalf("stamps: %v", err)
	}
	if len(stamps) == 0 {
		return 0
	}
	if len(stamps) != 1 {
		t.Fatalf("want one embedding stamp, got %d", len(stamps))
	}
	n, err := st.VectorCount(stamps[0].ModelID)
	if err != nil {
		t.Fatalf("vector count: %v", err)
	}
	return n
}

// TestIndexEmbedsAfterIndex: a manual `leankg index` runs the incremental embed
// after it indexes, reports the outcome on one line, and leaves vectors behind.
func TestIndexEmbedsAfterIndex(t *testing.T) {
	hookEnv(t, "10")
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", dir)
	if code != 0 {
		t.Fatalf("index exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "\nembedded ") {
		t.Fatalf("stdout missing the embedded line:\n%s", stdout)
	}
	if n := vectorCount(t, dir); n == 0 {
		t.Fatal("store has no vectors after index")
	}
}

// TestIndexHookNeverCallsRemoteProvider: the SessionStart hook never calls a
// remote embedding endpoint, even with one configured. It exits 0, says why it
// skipped the embed, and the endpoint sees no request.
func TestIndexHookNeverCallsRemoteProvider(t *testing.T) {
	hookEnv(t, "10")
	srv, hits := embedServer(t, 4)
	t.Setenv("LEANKG_EMBED_PROVIDER", "openai")
	t.Setenv("LEANKG_EMBED_MODEL", "m")
	t.Setenv("LEANKG_EMBED_BASE_URL", srv.URL)
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", "--hook", dir)
	if code != 0 {
		t.Fatalf("hook index exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	want := "embed skipped: hook mode only attaches to a running local provider"
	if !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q:\n%s", want, stdout)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("hook reached the remote endpoint %d time(s)", n)
	}
}

// TestIndexHookNeverSpawnsSidecar: the local provider with no base URL means
// spawning llama-server. The hook must not spawn it: the sidecar command is a
// script that leaves a marker file, and the marker must stay absent.
func TestIndexHookNeverSpawnsSidecar(t *testing.T) {
	hookEnv(t, "10")
	marker := filepath.Join(t.TempDir(), "spawned")
	script := filepath.Join(t.TempDir(), "sidecar.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEANKG_EMBED_PROVIDER", "local")
	t.Setenv("LEANKG_EMBED_BASE_URL", "")
	t.Setenv("LEANKG_EMBED_SIDECAR_CMD", script)
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", "--hook", dir)
	if code != 0 {
		t.Fatalf("hook index exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "embed skipped: hook mode only attaches to a running local provider") {
		t.Fatalf("stdout missing the hook skip line:\n%s", stdout)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("hook spawned the sidecar (marker stat err: %v)", err)
	}
}

// TestIndexHookRejectsNonLoopbackBaseURL: the hook attaches only to a loopback
// endpoint. A remote base URL is refused before any connection is attempted.
func TestIndexHookRejectsNonLoopbackBaseURL(t *testing.T) {
	hookEnv(t, "10")
	t.Setenv("LEANKG_EMBED_PROVIDER", "local")
	t.Setenv("LEANKG_EMBED_BASE_URL", "http://embed.example.invalid:9")
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", "--hook", dir)
	if code != 0 {
		t.Fatalf("hook index exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "embed skipped: hook mode only attaches to a running local provider") {
		t.Fatalf("stdout missing the hook skip line:\n%s", stdout)
	}
}

// TestIndexHookAttachesToRunningLocalProvider: a local provider already running
// on loopback is attached to, not spawned. The hook embeds through it and the
// store gets vectors.
func TestIndexHookAttachesToRunningLocalProvider(t *testing.T) {
	hookEnv(t, "10")
	srv, hits := embedServer(t, 4)
	t.Setenv("LEANKG_EMBED_PROVIDER", "local")
	t.Setenv("LEANKG_EMBED_BASE_URL", srv.URL+"/v1")
	t.Setenv("LEANKG_EMBED_DIMS", "4")
	// The fixture serves 4 dimensions and names no model: the legacy local
	// identity. An operator's catalogued model would carry its own widths, and
	// a width the model does not serve is refused before a vector is written.
	t.Setenv("LEANKG_EMBED_MODEL", "")
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", "--hook", dir)
	if code != 0 {
		t.Fatalf("hook index exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "\nembedded ") {
		t.Fatalf("stdout missing the embedded line:\n%s", stdout)
	}
	if hits.Load() == 0 {
		t.Fatal("hook never reached the attached provider")
	}
	if n := vectorCount(t, dir); n == 0 {
		t.Fatal("store has no vectors after the hook embed")
	}
}

// TestIndexHookSkipsEmbedOverBacklogLimit: the hook indexes, then skips the embed
// when more elements need vectors than LEANKG_HOOK_MAX_EMBED allows. The
// three-function fixture needs 3 vectors against a limit of 1: the skip line
// names the manual command, the exit is 0, and the store gets no vectors.
func TestIndexHookSkipsEmbedOverBacklogLimit(t *testing.T) {
	hookEnv(t, "10")
	t.Setenv("LEANKG_HOOK_MAX_EMBED", "1")
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", "--hook", dir)
	if code != 0 {
		t.Fatalf("hook index exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	want := "embed skipped: 3 elements need vectors, over LEANKG_HOOK_MAX_EMBED=1; run it manually: leankg index " + dir
	if !strings.Contains(stdout, "\n"+want+"\n") {
		t.Fatalf("stdout missing the backlog skip line %q:\n%s", want, stdout)
	}
	if strings.Contains(stdout, "\nembedded ") {
		t.Fatalf("hook over the backlog limit must not embed:\n%s", stdout)
	}
	if n := vectorCount(t, dir); n != 0 {
		t.Fatalf("hook over the backlog limit wrote %d vectors", n)
	}
}

// TestIndexHookEmbedsAtBacklogLimit: the limit is inclusive. A backlog equal to
// LEANKG_HOOK_MAX_EMBED (3 elements, limit 3) embeds as today.
func TestIndexHookEmbedsAtBacklogLimit(t *testing.T) {
	hookEnv(t, "10")
	t.Setenv("LEANKG_HOOK_MAX_EMBED", "3")
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", "--hook", dir)
	if code != 0 {
		t.Fatalf("hook index exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "\nembedded 3 elements (0 failed)") {
		t.Fatalf("hook at the backlog limit must embed:\n%s", stdout)
	}
	if n := vectorCount(t, dir); n != 3 {
		t.Fatalf("store has %d vectors, want 3", n)
	}
}

// TestIndexHookInvalidEmbedLimitUsesDefault: a LEANKG_HOOK_MAX_EMBED that is not
// a positive integer means the default (500). "0" must not read as a limit of
// zero, which would skip the 3-element backlog.
func TestIndexHookInvalidEmbedLimitUsesDefault(t *testing.T) {
	hookEnv(t, "10")
	t.Setenv("LEANKG_HOOK_MAX_EMBED", "0")
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", "--hook", dir)
	if code != 0 {
		t.Fatalf("hook index exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "\nembedded 3 elements (0 failed)") {
		t.Fatalf("LEANKG_HOOK_MAX_EMBED=0 must fall back to the default and embed:\n%s", stdout)
	}
}

// TestIndexManualIgnoresHookEmbedLimit: LEANKG_HOOK_MAX_EMBED gates the hook
// only. A manual index of the same over-limit fixture embeds all 3 elements.
func TestIndexManualIgnoresHookEmbedLimit(t *testing.T) {
	hookEnv(t, "10")
	t.Setenv("LEANKG_HOOK_MAX_EMBED", "1")
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", dir)
	if code != 0 {
		t.Fatalf("manual index exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "\nembedded 3 elements (0 failed)") {
		t.Fatalf("manual index must ignore LEANKG_HOOK_MAX_EMBED and embed:\n%s", stdout)
	}
	if n := vectorCount(t, dir); n != 3 {
		t.Fatalf("store has %d vectors, want 3", n)
	}
}

// TestIndexEmbedFailureIsSoft: a failed embed never fails the index. A manual
// run against a provider that answers 500 still indexes, reports the failure on
// an `embed skipped:` line, and exits 0.
func TestIndexEmbedFailureIsSoft(t *testing.T) {
	hookEnv(t, "10")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model overloaded", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LEANKG_EMBED_PROVIDER", "openai")
	t.Setenv("LEANKG_EMBED_MODEL", "m")
	t.Setenv("LEANKG_EMBED_BASE_URL", srv.URL)
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", dir)
	if code != 0 {
		t.Fatalf("index with a failing embed must exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "files=3") {
		t.Fatalf("index did not run (files=3 missing):\n%s", stdout)
	}
	if !strings.Contains(stdout, "embed skipped:") {
		t.Fatalf("stdout missing the embed skipped line:\n%s", stdout)
	}
}

// TestIndexNoEmbedSkipsEmbedding: --no-embed indexes and stops there, so there
// is no embed line and no vector is written.
func TestIndexNoEmbedSkipsEmbedding(t *testing.T) {
	hookEnv(t, "10")
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", "--no-embed", dir)
	if code != 0 {
		t.Fatalf("index --no-embed exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "files=3") {
		t.Fatalf("--no-embed must still index (files=3):\n%s", stdout)
	}
	if strings.Contains(stdout, "embedded ") || strings.Contains(stdout, "embed skipped") {
		t.Fatalf("--no-embed must print no embed line:\n%s", stdout)
	}
	if n := vectorCount(t, dir); n != 0 {
		t.Fatalf("--no-embed wrote %d vectors", n)
	}
}

// TestIndexEndpointConfigFailsFast: a manual index whose LEANKG_EMBED_BASE_URL is
// the bare llama-server root answers 200 with a top-level array. The run stops at
// the first embeddings request, prints exactly one `embed skipped:` line that
// names the /v1 root, and exits 0.
func TestIndexEndpointConfigFailsFast(t *testing.T) {
	hookEnv(t, "10")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" {
			_, _ = w.Write([]byte(propsJSON512))
			return
		}
		if r.URL.Path != "/embeddings" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		_, _ = w.Write([]byte(`[{"index":0,"embedding":[[0.1,0.2]]}]`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LEANKG_EMBED_PROVIDER", "local")
	t.Setenv("LEANKG_EMBED_MODEL", "embeddinggemma-2")
	t.Setenv("LEANKG_EMBED_BASE_URL", srv.URL)
	dir := t.TempDir()
	writeThreeGoFiles(t, dir)

	stdout, stderr, code := runCLIIn(t, dir, "index", dir)
	if code != 0 {
		t.Fatalf("index with an endpoint configuration error must exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var skipped []string
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "embed skipped:") {
			skipped = append(skipped, line)
		}
	}
	if len(skipped) != 1 {
		t.Fatalf("want exactly one embed skipped line, got %d:\n%s", len(skipped), stdout)
	}
	if !strings.Contains(skipped[0], "/v1") {
		t.Fatalf("embed skipped line must name the /v1 root fix: %s", skipped[0])
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("embeddings requests = %d, want 1", n)
	}
}
