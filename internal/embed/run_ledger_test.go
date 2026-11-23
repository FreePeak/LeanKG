package embed

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// faultStore wraps a Store and, at the named call, requests a stop (cancel) and
// returns the context's error, the way a store does when ctx ends mid-call. When
// err is set it returns err at that call instead, without a stop request.
type faultStore struct {
	Store
	at     string
	cancel context.CancelFunc
	err    error
}

func (s faultStore) hit(ctx context.Context, name string) error {
	if s.at != name {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.err != nil {
		return s.err
	}
	return ctx.Err()
}

func (s faultStore) Stamp(ctx context.Context, modelID string) (*store.ModelStamp, error) {
	if err := s.hit(ctx, "Stamp"); err != nil {
		return nil, err
	}
	return s.Store.Stamp(ctx, modelID)
}

func (s faultStore) ClearVectors(ctx context.Context, modelID string) error {
	if err := s.hit(ctx, "ClearVectors"); err != nil {
		return err
	}
	return s.Store.ClearVectors(ctx, modelID)
}

func (s faultStore) EmbeddingStateMap(ctx context.Context, modelID string) (map[string]string, error) {
	if err := s.hit(ctx, "EmbeddingStateMap"); err != nil {
		return nil, err
	}
	return s.Store.EmbeddingStateMap(ctx, modelID)
}

func (s faultStore) DeleteOrphanVectors(ctx context.Context) (int, error) {
	if err := s.hit(ctx, "DeleteOrphanVectors"); err != nil {
		return 0, err
	}
	return s.Store.DeleteOrphanVectors(ctx)
}

func (s faultStore) VectorCoverage(ctx context.Context, modelID string) (int, int, error) {
	if err := s.hit(ctx, "VectorCoverage"); err != nil {
		return 0, 0, err
	}
	return s.Store.VectorCoverage(ctx, modelID)
}

// stopProbe is a provider whose Probe is the stop point: it requests the stop
// and returns the context's error.
type stopProbe struct {
	Provider
	cancel context.CancelFunc
}

func (p stopProbe) Probe(ctx context.Context) error {
	p.cancel()
	return ctx.Err()
}

// lastRunID is the id of the newest ledger row, 0 when there is none.
func lastRunID(t *testing.T, st store.Backend) int64 {
	t.Helper()
	run, err := st.LastEmbedRunAny(context.Background())
	if err != nil {
		t.Fatalf("LastEmbedRunAny: %v", err)
	}
	if run == nil {
		return 0
	}
	return run.ID
}

// assertNewRun fails unless the run ledger gained a row after beforeID and its
// status is want.
func assertNewRun(t *testing.T, st store.Backend, beforeID int64, want string) {
	t.Helper()
	run, err := st.LastEmbedRunAny(context.Background())
	if err != nil {
		t.Fatalf("LastEmbedRunAny: %v", err)
	}
	if run == nil || run.ID <= beforeID {
		t.Fatalf("no ledger row recorded for the run (newest id %v, before %d)", run, beforeID)
	}
	if run.Status != want {
		t.Fatalf("ledger status = %q, want %q", run.Status, want)
	}
}

// A run stopped on any exit path, before or after it starts its ledger row,
// leaves a row with status cancelled and returns the cancellation.
func TestRunStopRecordsCancelledOnEveryExit(t *testing.T) {
	cases := []struct {
		name string
		prep func(t *testing.T, st store.Backend)
		run  func(ctx context.Context, cancel context.CancelFunc, st store.Backend) (Report, error)
	}{
		{
			name: "probe",
			run: func(ctx context.Context, cancel context.CancelFunc, st store.Backend) (Report, error) {
				return Run(ctx, st, stopProbe{Provider: Deterministic(8), cancel: cancel}, "incremental")
			},
		},
		{
			name: "stamp",
			run: func(ctx context.Context, cancel context.CancelFunc, st store.Backend) (Report, error) {
				return Run(ctx, faultStore{Store: st, at: "Stamp", cancel: cancel}, Deterministic(8), "incremental")
			},
		},
		{
			name: "clear vectors",
			prep: func(t *testing.T, st store.Backend) {
				mustRun(t, st, Deterministic(8), "full")
			},
			run: func(ctx context.Context, cancel context.CancelFunc, st store.Backend) (Report, error) {
				// Same model id, drifted revision: a full run clears the collection.
				drift := overrideProvider{inner: Deterministic(8), revision: "test:deterministic-v2", dims: 8}
				return Run(ctx, faultStore{Store: st, at: "ClearVectors", cancel: cancel}, drift, "full")
			},
		},
		{
			name: "state map",
			run: func(ctx context.Context, cancel context.CancelFunc, st store.Backend) (Report, error) {
				return Run(ctx, faultStore{Store: st, at: "EmbeddingStateMap", cancel: cancel}, Deterministic(8), "incremental")
			},
		},
		{
			name: "delete orphans",
			run: func(ctx context.Context, cancel context.CancelFunc, st store.Backend) (Report, error) {
				return Run(ctx, faultStore{Store: st, at: "DeleteOrphanVectors", cancel: cancel}, Deterministic(8), "incremental")
			},
		},
		{
			name: "vector coverage",
			run: func(ctx context.Context, cancel context.CancelFunc, st store.Backend) (Report, error) {
				return Run(ctx, faultStore{Store: st, at: "VectorCoverage", cancel: cancel}, Deterministic(8), "incremental")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := testStore(t)
			seed(t, st, el("a::f1", "func f1() {}"))
			if tc.prep != nil {
				tc.prep(t, st)
			}
			before := lastRunID(t, st)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := tc.run(ctx, cancel, st)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Run error: got %v, want context.Canceled", err)
			}
			assertNewRun(t, st, before, "cancelled")
		})
	}
}

// Only a real failure is recorded as failed: a store error that is not a
// cancellation keeps its failed status on the orphan-reclaim and coverage exits.
func TestRunStoreFailureRecordsFailed(t *testing.T) {
	for _, at := range []string{"DeleteOrphanVectors", "VectorCoverage"} {
		t.Run(at, func(t *testing.T) {
			st := testStore(t)
			seed(t, st, el("a::f1", "func f1() {}"))
			before := lastRunID(t, st)
			_, err := Run(context.Background(), faultStore{Store: st, at: at, err: errors.New("disk full")}, Deterministic(8), "incremental")
			if err == nil || errors.Is(err, context.Canceled) {
				t.Fatalf("Run error: got %v, want the store failure", err)
			}
			assertNewRun(t, st, before, "failed")
		})
	}
}

// cancelOnRead requests the stop when the import starts reading its input.
type cancelOnRead struct {
	r      io.Reader
	cancel context.CancelFunc
	once   sync.Once
}

func (c *cancelOnRead) Read(p []byte) (int, error) {
	c.once.Do(c.cancel)
	return c.r.Read(p)
}

// An import stopped mid-stream records cancelled, not failed, and an import
// stopped before its ledger row starts still leaves one.
func TestImportNDJSONStopRecordsCancelled(t *testing.T) {
	cfg := Config{Provider: "openai", Model: "m", Dims: 8, Revision: "rev"}
	line := `{"qualified_name":"a::f1","vec":[0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8]}` + "\n"

	t.Run("mid stream", func(t *testing.T) {
		st := testStore(t)
		seed(t, st, el("a::f1", "func f1() {}"))
		before := lastRunID(t, st)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := &cancelOnRead{r: strings.NewReader(line), cancel: cancel}
		_, err := ImportNDJSON(ctx, st, cfg, "m", "rev", "cosine", 8, r)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ImportNDJSON error: got %v, want context.Canceled", err)
		}
		assertNewRun(t, st, before, "cancelled")
	})

	t.Run("before the run starts", func(t *testing.T) {
		st := testStore(t)
		seed(t, st, el("a::f1", "func f1() {}"))
		before := lastRunID(t, st)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err := ImportNDJSON(ctx, faultStore{Store: st, at: "EmbeddingStateMap", cancel: cancel}, cfg, "m", "rev", "cosine", 8, strings.NewReader(line))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ImportNDJSON error: got %v, want context.Canceled", err)
		}
		assertNewRun(t, st, before, "cancelled")
	})
}

// A stop that coincides with a post-start store error is recorded cancelled:
// the store error is the failure, but the stop was requested, so the ledger
// keeps the same status as the end-of-run cancellation (issue #587).
func TestRunStopCoincidingWithStoreErrorRecordsCancelled(t *testing.T) {
	for _, at := range []string{"DeleteOrphanVectors", "VectorCoverage"} {
		t.Run(at, func(t *testing.T) {
			st := testStore(t)
			seed(t, st, el("a::f1", "func f1() {}"))
			before := lastRunID(t, st)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := Run(ctx, faultStore{Store: st, at: at, cancel: cancel, err: errors.New("disk full")}, Deterministic(8), "incremental")
			if err == nil {
				t.Fatal("Run error: got nil, want the store failure")
			}
			assertNewRun(t, st, before, "cancelled")
		})
	}
}
