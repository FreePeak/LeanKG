package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/embed"
)

// stubIdentity gives a test embedder an identity without an endpoint.
type stubIdentity struct{}

func (stubIdentity) Identity() embed.Identity {
	return embed.Identity{Provider: "test", Model: "test-embed", Dims: 1}
}
func (stubIdentity) Probe(context.Context) error { return nil }

// blockingEmbedder never answers until the probe ctx ends.
type blockingEmbedder struct{ stubIdentity }

func (blockingEmbedder) Embed(ctx context.Context, _ embed.TextKind, _ []string) ([][]float32, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// gatedEmbedder signals when a probe starts and answers once released.
type gatedEmbedder struct {
	stubIdentity
	started chan struct{}
	release chan struct{}
}

func (g gatedEmbedder) Embed(ctx context.Context, _ embed.TextKind, _ []string) ([][]float32, error) {
	g.started <- struct{}{}
	select {
	case <-g.release:
		return [][]float32{{1}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// okEmbedder answers at once.
type okEmbedder struct{ stubIdentity }

func (okEmbedder) Embed(context.Context, embed.TextKind, []string) ([][]float32, error) {
	return [][]float32{{1}}, nil
}

// TestEmbedProbeBoundedByCallerCtx: the probe ends when the caller's deadline
// does, not after its own 3 s.
func TestEmbedProbeBoundedByCallerCtx(t *testing.T) {
	var h embedHealth
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	ok, msg := h.check(ctx, blockingEmbedder{})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("probe took %v under a 50ms caller deadline", elapsed)
	}
	if ok || !strings.Contains(msg, "deadline exceeded") {
		t.Fatalf("check = (%v, %q), want unreachable with a deadline error", ok, msg)
	}
}

// TestEmbedProbeCancelledIsNotCached: a probe cut short by the caller is not
// a provider outage, so the next status re-probes instead of reading 30 s of
// cached failure.
func TestEmbedProbeCancelledIsNotCached(t *testing.T) {
	var h embedHealth
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.check(ctx, blockingEmbedder{})

	if ok, msg := h.check(context.Background(), okEmbedder{}); !ok {
		t.Fatalf("second check = (false, %q), want reachable: the cancelled probe was cached", msg)
	}
}

// TestEmbedProbeDoesNotHoldLock: a probe in flight must not block other
// callers on the mutex.
func TestEmbedProbeDoesNotHoldLock(t *testing.T) {
	var h embedHealth
	g := gatedEmbedder{started: make(chan struct{}, 1), release: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		h.check(context.Background(), g)
		close(done)
	}()
	<-g.started

	locked := make(chan struct{})
	go func() {
		h.mu.Lock()
		h.mu.Unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("the probe holds the embedHealth mutex while it runs")
	}
	close(g.release)
	<-done
}

// TestStatusFlagsDeterministicProvider keeps the hash-derived provider visible
// in status: it is selectable only by name (D10), so the status payload must
// say it is not a semantic embedder.
func TestStatusFlagsDeterministicProvider(t *testing.T) {
	st := seededStore(t)
	e := New(st, Options{Embedder: embed.Deterministic(8)})
	out, err := e.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	qe, ok := out["query_embedder"].(map[string]any)
	if !ok {
		t.Fatalf("query_embedder missing from status: %v", out)
	}
	if qe["provider"] != "deterministic" || qe["deterministic"] != true {
		t.Fatalf("query_embedder = %v, want provider deterministic and deterministic=true", qe)
	}
}
