package memory

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/embed"
)

// TestDenseArmReturnsByEmbedClientTimeout pins that a hung endpoint cannot hold
// the dense arm: the embed client's request timeout
// (LEANKG_EMBED_REQUEST_TIMEOUT_SECS) cuts each request, and the dense recall
// deadline (denseRecallTimeout) bounds the whole recall. A fake endpoint that
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
	m.SetProvider(p, func(context.Context) (float64, bool) { return 0, true })

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

// deadlineProvider records the deadline of the context its Embed call got, and
// fails the call at once so the recall returns on the keyword arm.
type deadlineProvider struct {
	deadline time.Time
	bounded  bool
}

func (p *deadlineProvider) Identity() embed.Identity {
	return embed.Identity{Provider: "test", Model: "deadline-v1", Dims: 2}
}
func (p *deadlineProvider) Probe(context.Context) error { return nil }
func (p *deadlineProvider) Embed(ctx context.Context, _ embed.TextKind, _ []string) ([][]float32, error) {
	p.deadline, p.bounded = ctx.Deadline()
	return nil, errors.New("stub: no vectors")
}

// TestDenseRecallIsBoundedByOneDeadline pins the memory-side bound on the dense
// arm (issue #587): the whole dense recall runs under one 20 s deadline, so a
// hung query embedder cannot hold a recall past it. The check reads the
// deadline the embed call received, so the test takes no time.
func TestDenseRecallIsBoundedByOneDeadline(t *testing.T) {
	m := openTest(t)
	if err := m.RetainRaw(context.Background(), "b", []Entry{
		{ID: "pg", Content: "We store embeddings in pgvector on Postgres."},
	}); err != nil {
		t.Fatal(err)
	}
	p := &deadlineProvider{}
	m.SetProvider(p, func(context.Context) (float64, bool) { return 0, true })

	start := time.Now()
	if _, err := m.Recall(context.Background(), "b", "which database holds the vectors", 3); err != nil {
		t.Fatalf("recall failed instead of falling back to BM25: %v", err)
	}
	if !p.bounded {
		t.Fatal("the dense arm's embed call has no deadline; a hung embedder would hold the recall")
	}
	if got := p.deadline.Sub(start); got < 19*time.Second || got > 21*time.Second {
		t.Fatalf("dense recall deadline is %s after the recall started, want 20s", got)
	}
}
