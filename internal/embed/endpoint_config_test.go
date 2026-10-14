package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// llamaNativeArrayBody is the top-level array llama-server's native /embeddings
// route answers when LEANKG_EMBED_BASE_URL is the bare root (no /v1 prefix).
const llamaNativeArrayBody = `[{"index":0,"embedding":[[0.1,0.2]]}]`

// newEndpointFake serves /props like a llama-server and answers every POST
// /embeddings with reply. The returned counter is the number of embeddings
// requests the run made.
func newEndpointFake(t *testing.T, reply func(w http.ResponseWriter, r *http.Request, n int32)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/props":
			_, _ = w.Write([]byte(`{"model_path":"/cache/embeddinggemma-2.gguf","default_generation_settings":{"n_ctx":2048}}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/embeddings"):
			reply(w, r, n.Add(1))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// seedForty seeds 40 elements in one file: more than one provider batch
// (batchSize is 32).
func seedForty(t *testing.T, st *store.Store) {
	t.Helper()
	els := make([]store.Element, 40)
	for i := range els {
		els[i] = el(fmt.Sprintf("a::f%02d", i), fmt.Sprintf("func f%02d() {}", i))
	}
	seed(t, st, els...)
}

// TestRunStopsOnUndecodableResponse: a bare llama-server root answers 200 with a
// top-level array. That is a configuration error, not a batch to retry: the run
// stops after the first request, names the /v1 fix and writes nothing.
func TestRunStopsOnUndecodableResponse(t *testing.T) {
	srv, calls := newEndpointFake(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		_, _ = w.Write([]byte(llamaNativeArrayBody))
	})
	setGemmaEnv(t, srv.URL, "")
	p, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st := testStore(t)
	seedForty(t, st)

	_, err = Run(context.Background(), st, p, "incremental")
	if !errors.Is(err, ErrEndpointConfig) {
		t.Fatalf("Run err = %v, want ErrEndpointConfig", err)
	}
	if !strings.Contains(err.Error(), "/v1") {
		t.Fatalf("Run err must name the /v1 root fix: %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("embeddings requests = %d, want 1 (no retry or shrink)", n)
	}
	if v := vectorCount(t, st, "embeddinggemma-2"); v != 0 {
		t.Fatalf("vectors written = %d, want 0", v)
	}
}

// TestRunStopsOn404: a route that does not exist at the configured URL is a
// configuration error too, and the message names the URL that was called.
func TestRunStopsOn404(t *testing.T) {
	srv, calls := newEndpointFake(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		http.Error(w, "404 page not found", http.StatusNotFound)
	})
	setGemmaEnv(t, srv.URL, "")
	p, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st := testStore(t)
	seedForty(t, st)

	_, err = Run(context.Background(), st, p, "incremental")
	if !errors.Is(err, ErrEndpointConfig) {
		t.Fatalf("Run err = %v, want ErrEndpointConfig", err)
	}
	if !strings.Contains(err.Error(), srv.URL+"/embeddings") {
		t.Fatalf("Run err must name the URL called %s/embeddings: %v", srv.URL, err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("embeddings requests = %d, want 1", n)
	}
	if v := vectorCount(t, st, "embeddinggemma-2"); v != 0 {
		t.Fatalf("vectors written = %d, want 0", v)
	}
}

// TestRunStillShrinksOnTransientError: a 500 on the first batch is a transient
// provider failure. The per-item retry still runs and every element embeds.
func TestRunStillShrinksOnTransientError(t *testing.T) {
	srv, _ := newEndpointFake(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		if n == 1 {
			http.Error(w, "model overloaded", http.StatusInternalServerError)
			return
		}
		var body embeddingsRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		vec := make([]float32, 768)
		for i := range vec {
			vec[i] = 1
		}
		data := make([]map[string]any, len(body.Input))
		for i := range data {
			data[i] = map[string]any{"embedding": vec}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	setGemmaEnv(t, srv.URL+"/v1", "")
	p, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st := testStore(t)
	seedForty(t, st)

	rep, err := Run(context.Background(), st, p, "incremental")
	if err != nil {
		t.Fatalf("Run after one transient 500: %v", err)
	}
	if rep.Embedded != 40 || rep.Failed != 0 {
		t.Fatalf("report embedded=%d failed=%d, want 40/0", rep.Embedded, rep.Failed)
	}
	if v := vectorCount(t, st, "embeddinggemma-2"); v != 40 {
		t.Fatalf("vectors = %d, want 40", v)
	}
}

// TestRunStopsOn401NamesKeyWithoutValue: a refused bearer token is a
// configuration error that names LEANKG_EMBED_API_KEY and never echoes the key.
func TestRunStopsOn401NamesKeyWithoutValue(t *testing.T) {
	const secret = "sk-test-secret-value-123"
	srv, calls := newEndpointFake(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		http.Error(w, "invalid key "+secret, http.StatusUnauthorized)
	})
	setGemmaEnv(t, srv.URL+"/v1", "")
	t.Setenv("LEANKG_EMBED_API_KEY", secret)
	p, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st := testStore(t)
	seedForty(t, st)

	_, err = Run(context.Background(), st, p, "incremental")
	if !errors.Is(err, ErrEndpointConfig) {
		t.Fatalf("Run err = %v, want ErrEndpointConfig", err)
	}
	if !strings.Contains(err.Error(), "LEANKG_EMBED_API_KEY") {
		t.Fatalf("Run err must name LEANKG_EMBED_API_KEY: %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Run err leaks the API key value: %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("embeddings requests = %d, want 1", n)
	}
}
