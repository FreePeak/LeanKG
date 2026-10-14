package embed

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// wideDims is a width no catalog row serves, so the test model is uncatalogued
// and LEANKG_EMBED_DIMS sets it. At this width a 32-vector reply is over the
// 1 MiB read cap while a single-vector reply is far under it.
const wideDims = 3072

// wideVector is a deterministic vector whose float32 JSON form is long.
func wideVector(seed int) []float32 {
	v := make([]float32, wideDims)
	for i := range v {
		v[i] = float32(math.Sin(float64(seed*wideDims + i + 1)))
	}
	return v
}

// wideReply is a valid OpenAI embeddings reply for n inputs.
func wideReply(n int) []byte {
	data := make([]map[string]any, n)
	for i := range data {
		data[i] = map[string]any{"embedding": wideVector(i)}
	}
	b, _ := json.Marshal(map[string]any{"data": data})
	return b
}

// setWideEnv points the openai provider at baseURL with an uncatalogued model
// whose width is wideDims.
func setWideEnv(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv("LEANKG_EMBED_PROVIDER", "openai")
	t.Setenv("LEANKG_EMBED_BASE_URL", baseURL)
	t.Setenv("LEANKG_EMBED_MODEL", "wide-test-embed")
	t.Setenv("LEANKG_EMBED_DIMS", strconv.Itoa(wideDims))
	t.Setenv("LEANKG_EMBED_REVISION", "")
	t.Setenv("LEANKG_EMBED_API_KEY", "")
}

// TestOversizedValidResponseIsNotConfigError: a valid reply larger than the
// 1 MiB read cap is a batch failure, not a configuration error. The run shrinks
// and recovers, so all 40 vectors are written.
func TestOversizedValidResponseIsNotConfigError(t *testing.T) {
	if n := len(wideReply(batchSize)); n <= 1<<20 {
		t.Fatalf("precondition: a %d-input reply is %d bytes, want over 1 MiB", batchSize, n)
	}
	if n := len(wideReply(1)); n >= 1<<20 {
		t.Fatalf("precondition: a 1-input reply is %d bytes, want under 1 MiB", n)
	}
	srv, _ := newEndpointFake(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		var body embeddingsRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		_, _ = w.Write(wideReply(len(body.Input)))
	})
	setWideEnv(t, srv.URL+"/v1")
	p, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st := testStore(t)
	seedForty(t, st)

	rep, err := Run(context.Background(), st, p, "incremental")
	if err != nil {
		t.Fatalf("Run: %v (an oversized valid reply must shrink, not abort)", err)
	}
	if rep.Embedded != 40 || rep.Failed != 0 {
		t.Fatalf("report embedded=%d failed=%d, want 40/0", rep.Embedded, rep.Failed)
	}
	if v := vectorCount(t, st, "wide-test-embed"); v != 40 {
		t.Fatalf("vectors = %d, want 40", v)
	}

	// The 32-input batch is the request that overflows the cap: its error must
	// be an ordinary batch error that names the cap, never ErrEndpointConfig.
	texts := make([]string, batchSize)
	for i := range texts {
		texts[i] = "x"
	}
	_, err = p.Embed(context.Background(), Document, texts)
	if err == nil {
		t.Fatal("32-input batch: want the read-cap error, got nil")
	}
	if errors.Is(err, ErrEndpointConfig) {
		t.Fatalf("read-cap overflow classified as ErrEndpointConfig: %v", err)
	}
	if !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("read-cap error must name the 1 MiB cap: %v", err)
	}
}

// TestNon200ErrorRedactsBearerToken: a non-200 reply that echoes the bearer
// token must not leak it into the error text, which the run logs.
func TestNon200ErrorRedactsBearerToken(t *testing.T) {
	const key = "test-key-value-123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "received authorization="+r.Header.Get("Authorization"), http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	p := OpenAICompatible(srv.URL+"/v1", key, "wide-test-embed", wideDims, "")
	_, err := p.Embed(context.Background(), Document, []string{"x"})
	if err == nil {
		t.Fatal("Embed on HTTP 500: want an error, got nil")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error leaks the bearer token: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error must show the redaction marker: %v", err)
	}
}
