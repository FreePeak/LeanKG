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

// providerVectorizer adapts a real embed.Provider to the dense arm's Vectorizer
// seam, the way core does in production, without a noise floor.
type providerVectorizer struct{ p embed.Provider }

func (v providerVectorizer) Key() string            { return "hung-endpoint" }
func (v providerVectorizer) Floor() (float64, bool) { return 0, true }
func (v providerVectorizer) EmbedQuery(ctx context.Context, t string) ([]float32, error) {
	vecs, err := v.p.Embed(ctx, embed.Query, []string{t})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}
func (v providerVectorizer) EmbedDocuments(ctx context.Context, ts []string) ([][]float32, error) {
	return v.p.Embed(ctx, embed.Document, ts)
}

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

	t.Setenv("LEANKG_EMBED_PROVIDER", "local")
	t.Setenv("LEANKG_EMBED_BASE_URL", srv.URL)
	t.Setenv("LEANKG_EMBED_REQUEST_TIMEOUT_SECS", "1")
	p, err := embed.FromEnv()
	if err != nil {
		t.Fatal(err)
	}

	m := openTest(t)
	if err := m.RetainRaw(context.Background(), "b", []Entry{
		{ID: "pg", Content: "We store embeddings in pgvector on Postgres."},
	}); err != nil {
		t.Fatal(err)
	}
	m.SetVectorizer(providerVectorizer{p: p})

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
