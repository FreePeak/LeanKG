package maintain

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// A cancelled ctx stops a pass before any sweep runs: the stale collection
// that a live pass would drop is still there afterwards.
func TestPassStopsOnCancelledCtxBeforeSweeping(t *testing.T) {
	st := collectionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := Pass(ctx, st, Options{KeepModelIDs: []string{"live"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !res.Sweep.Empty() {
		t.Fatalf("sweep ran under a cancelled ctx: %+v", res.Sweep)
	}
	if res.Checkpointed || res.Vacuumed {
		t.Fatalf("checkpoint or reclaim ran under a cancelled ctx: %+v", res)
	}
	if !hasCollection(t, st, "old") {
		t.Fatal("the stale collection was swept under a cancelled ctx")
	}
}

// Sweep checks ctx between its steps: a cancelled ctx removes nothing.
func TestSweepStopsOnCancelledCtx(t *testing.T) {
	st := collectionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := Sweep(ctx, st, Options{KeepModelIDs: []string{"live"}})
	if !res.Empty() {
		t.Fatalf("Sweep removed rows under a cancelled ctx: %+v", res)
	}
	if !hasCollection(t, st, "old") {
		t.Fatal("the stale collection was swept under a cancelled ctx")
	}
}

// RunFleet returns a channel closed only after its goroutine has exited. A
// cancelled ctx stops the loop, and the close proves the join: nothing is left
// running behind it.
func TestRunFleetIsJoinedOnCancel(t *testing.T) {
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	done := RunFleet(ctx, func() []Target { return nil }, Options{}, time.Millisecond, 0)

	time.Sleep(20 * time.Millisecond) // let a few ticks run
	select {
	case <-done:
		t.Fatal("RunFleet's done channel closed before its ctx was cancelled")
	default:
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunFleet did not stop within 5s of cancel")
	}

	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("goroutines after cancel = %d, before = %d: the fleet loop leaked", n, before)
	}
}

// A disabled loop (interval <= 0) hands back a channel that is already closed,
// so a caller that joins on it never blocks.
func TestRunFleetDisabledIsAlreadyJoined(t *testing.T) {
	done := RunFleet(context.Background(), func() []Target { return nil }, Options{}, 0, 0)
	select {
	case <-done:
	default:
		t.Fatal("a disabled RunFleet returned an open done channel")
	}
}

// cancelAtCheckpoint cancels the pass the moment its checkpoint step runs and
// fails that step with the cancel: the shape of a SIGINT that lands mid-pass.
type cancelAtCheckpoint struct {
	Store
	cancel context.CancelFunc
}

func (s cancelAtCheckpoint) Maintain(ctx context.Context, op store.MaintainOp) (store.MaintainResult, error) {
	if op.Kind == store.MaintainCheckpoint {
		s.cancel()
		return store.MaintainResult{}, ctx.Err()
	}
	return s.Store.Maintain(ctx, op)
}

// A pass cut short by a cancel must report it: a checkpoint or space probe
// that failed under a cancelled ctx is not a successful pass.
func TestPassReturnsCancelWhenCheckpointIsCut(t *testing.T) {
	st := collectionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := Pass(ctx, cancelAtCheckpoint{Store: st, cancel: cancel}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pass cut at checkpoint: err = %v, want context.Canceled", err)
	}
}
