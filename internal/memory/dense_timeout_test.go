package memory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/embed"
)

// TestDenseArmReturnsByEmbedClientTimeout pins CX-34's deferred bound: with no
// memory-side deadline, the dense arm is still bounded by the embed client's
// request timeout (LEANKG_EMBED_REQUEST_TIMEOUT_SECS). A fake endpoint that
// never answers must make the recall return with the BM25 result instead of
// waiting for the endpoint.
func TestDenseArmReturnsByEmbedClientTimeout(t *testing.T) {
	var hits atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-r.Context().Done(): // the client gave up at its timeout
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	p, _, err := embed.Attach(embed.Config{Provider: "local", BaseURL: srv.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}

	m := openTest(t)
	if err := m.RetainRaw(context.Background(), "b", []Entry{
		{ID: "pg", Content: "We store embeddings in pgvector on Postgres."},
	}); err != nil {
		t.Fatal(err)
	}
	m.SetProvider(p, func() (float64, bool) { return 0, true })

	start := time.Now()
	_, err = m.Recall(context.Background(), "b", "which database holds the vectors", 3)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("recall failed instead of falling back to BM25: %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("the dense arm never reached the fake endpoint; the test proves nothing")
	}
	// The client timeout is 1 s. The bound is loose on purpose: the point is
	// that recall returns on the order of the timeout, not on the endpoint.
	if elapsed > 15*time.Second {
		t.Fatalf("recall took %s with a 1s embed timeout; the dense arm is not bounded by the client", elapsed)
	}
}
