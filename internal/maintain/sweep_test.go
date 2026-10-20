package maintain

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// collectionFixture opens a migrated store holding TWO vector collections:
// "live" (the one the query path searches) and "old" (an abandoned one —
// the state every model swap used to leave behind).
func collectionFixture(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// A real element: a vector whose qualified name has no element is an
	// ORPHAN by the store's own definition, and the orphan sweep would
	// remove it before the stale-collection sweep ever saw it.
	if err := st.UpsertElements([]store.Element{{
		QualifiedName: "pkg.Fn", ElementType: "function", Name: "Fn",
		FilePath: "pkg.go", Language: "go", LineStart: 1, LineEnd: 3,
		Content: "func Fn() {}",
	}}); err != nil {
		t.Fatalf("upsert element: %v", err)
	}
	for _, model := range []string{"live", "old"} {
		if err := st.WriteStamp(store.ModelStamp{
			ModelID: model, Revision: "rev-" + model, Dimensions: 4,
			Distance: "cosine", Provider: "local",
		}); err != nil {
			t.Fatalf("write stamp %s: %v", model, err)
		}
		if err := st.UpsertVectors(model, []store.VectorRow{
			{QualifiedName: "pkg.Fn", Vec: []float32{1, 0, 0, 0}},
		}); err != nil {
			t.Fatalf("upsert vectors %s: %v", model, err)
		}
	}
	return st
}

// hasCollection reports whether the store still holds a collection under
// modelID (stamp + vectors).
func hasCollection(t *testing.T, st *store.Store, modelID string) bool {
	t.Helper()
	stamp, err := st.Stamp(modelID)
	if err != nil {
		t.Fatalf("stamp %s: %v", modelID, err)
	}
	if stamp == nil {
		return false
	}
	n, err := st.VectorCount(modelID)
	if err != nil {
		t.Fatalf("vector count %s: %v", modelID, err)
	}
	return n > 0
}

func TestPassSweepsStaleCollections(t *testing.T) {
	st := collectionFixture(t)
	res, err := Pass(st, Options{KeepModelIDs: []string{"live"}})
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if res.Sweep.StaleModels != 1 {
		t.Fatalf("sweep = %+v, want 1 stale collection dropped", res.Sweep)
	}
	if !hasCollection(t, st, "live") {
		t.Error("the live collection was dropped")
	}
	if hasCollection(t, st, "old") {
		t.Error("the stale collection survived the pass")
	}
}

// TestPassKeepsACollectionWhenTheKeepOneIsAbsent is the fleet regression:
// the first fleet run dropped a project's ONLY collection, because the store
// was embedded under "local" while the server's embedder said
// "embeddinggemma-2". A store whose keep collection is absent is BEHIND,
// not stale — its one collection must survive the pass.
func TestPassKeepsACollectionWhenTheKeepOneIsAbsent(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.UpsertElements([]store.Element{{
		QualifiedName: "pkg.Fn", ElementType: "function", Name: "Fn",
		FilePath: "pkg.go", Language: "go", LineStart: 1, LineEnd: 3,
		Content: "func Fn() {}",
	}}); err != nil {
		t.Fatalf("upsert element: %v", err)
	}
	if err := st.WriteStamp(store.ModelStamp{
		ModelID: "local", Revision: "rev-local", Dimensions: 4,
		Distance: "cosine", Provider: "local",
	}); err != nil {
		t.Fatalf("write stamp: %v", err)
	}
	if err := st.UpsertVectors("local", []store.VectorRow{
		{QualifiedName: "pkg.Fn", Vec: []float32{1, 0, 0, 0}},
	}); err != nil {
		t.Fatalf("upsert vectors: %v", err)
	}

	res, err := Pass(st, Options{KeepModelIDs: []string{"embeddinggemma-2"}})
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if !res.Sweep.Empty() {
		t.Fatalf("sweep = %+v, want an empty sweep", res.Sweep)
	}
	if !hasCollection(t, st, "local") {
		t.Fatal("the pass dropped a store's only collection because the keep collection was absent")
	}
}

func TestPassWithoutAKeepListLeavesEveryCollection(t *testing.T) {
	st := collectionFixture(t)
	res, err := Pass(st, Options{})
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if !res.Sweep.Empty() {
		t.Fatalf("sweep = %+v, want an empty sweep", res.Sweep)
	}
	for _, model := range []string{"live", "old"} {
		if !hasCollection(t, st, model) {
			t.Errorf("collection %s was dropped without a keep list", model)
		}
	}
}

// TestRunFleetSweepsEveryProject is the fleet contract: a serving process
// fronts many project stores, and EVERY one of them must be swept — before
// the fleet existed, only the default project's store was maintained.
func TestRunFleetSweepsEveryProject(t *testing.T) {
	first := collectionFixture(t)
	second := collectionFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	RunFleet(ctx, func() []Target {
		return []Target{
			{Store: first, Label: "first"},
			{Store: second, Label: "second"},
		}
	}, Options{KeepModelIDs: []string{"live"}}, 10*time.Millisecond, 0)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if !hasCollection(t, first, "old") && !hasCollection(t, second, "old") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fleet never swept every project")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, st := range []*store.Store{first, second} {
		if !hasCollection(t, st, "live") {
			t.Error("a live collection was dropped")
		}
	}
}

// TestRunFleetClosesWhatItOpened: a fleet target is opened for the pass and
// closed again (the serving process does not want 285 project stores
// resident), so the closer must run.
func TestRunFleetClosesWhatItOpened(t *testing.T) {
	st := collectionFixture(t)
	closed := make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	RunFleet(ctx, func() []Target {
		return []Target{{Store: st, Label: "one", Close: func() { closed <- struct{}{} }}}
	}, Options{}, 10*time.Millisecond, 0)

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the fleet never closed a target")
	}
}

// TestRunFleetSkipsANilStore: a project that could not be opened is a nil
// target, not a crash.
func TestRunFleetSkipsANilStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	RunFleet(ctx, func() []Target {
		return []Target{{Store: nil, Label: "missing"}}
	}, Options{}, 10*time.Millisecond, 0)
	time.Sleep(50 * time.Millisecond)
}
