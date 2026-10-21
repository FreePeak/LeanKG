package maintain

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// smallFreelistFixture opens a migrated store whose freelist holds real pages
// but far less than the 1 MiB floor this package used to carry: the elements
// are small and only a few of them are deleted, so the free space is tens of
// kilobytes, not megabytes.
//
// Measured on the live fleet: every store's freelist sat below that floor
// (the leankg store held 745,472 B against a 1,048,576 B floor), so the
// hourly pass reclaimed zero bytes everywhere while the machinery looked
// healthy.
func smallFreelistFixture(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for i := range 200 {
		p := "src/f" + itoa(i) + ".go"
		if err := st.UpsertElements([]store.Element{{
			QualifiedName: "pkg.Fn" + itoa(i),
			ElementType:   "function",
			Name:          "Fn" + itoa(i),
			FilePath:      p,
			Language:      "go",
			Content:       strings.Repeat("x", 800) + itoa(i),
		}}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	return st
}

// TestPassReclaimsAFreelistBelowTheOldOneMegabyteFloor is the regression for
// the floor that made the scheduled pass reclaim nothing: the reclaim
// machinery existed, and its threshold never fired.
func TestPassReclaimsAFreelistBelowTheOldOneMegabyteFloor(t *testing.T) {
	st := smallFreelistFixture(t)
	// The first pass performs the one-time header upgrade (auto_vacuum +
	// VACUUM), which is what makes a freelist reclaimable at all.
	if _, err := Pass(st, Options{}); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	// Re-create a freelist that is real but SMALLER than the old floor.
	for i := range 40 {
		if err := st.DeleteByFile("src/f" + itoa(i) + ".go"); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
	res, err := Pass(st, Options{})
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if res.FreeBefore <= 0 {
		t.Fatalf("the fixture left no freelist to reclaim: %+v", res)
	}
	if res.FreeBefore >= 1<<20 {
		t.Fatalf("the fixture's freelist is %d bytes, not below the old 1 MiB floor", res.FreeBefore)
	}
	if !res.Vacuumed {
		t.Fatalf("the pass skipped a %d-byte freelist: %+v", res.FreeBefore, res)
	}
	if res.FreeAfter >= res.FreeBefore {
		t.Errorf("the freelist did not drain: %d -> %d", res.FreeBefore, res.FreeAfter)
	}
}

// TestRunFleetPrunesTheOldMetricRows: context_metrics is append-only. The
// only retention sweep over it was reachable from a manual CLI verb, so the
// scheduled pass left it growing forever — measured live: 6,166 rows in the
// leankg store and one more every few minutes, with `is_deleted` never set
// to 1 anywhere in the repo.
func TestRunFleetPrunesTheOldMetricRows(t *testing.T) {
	st := collectionFixture(t)
	old := time.Now().Add(-60 * 24 * time.Hour).Unix()
	if err := st.RecordMetric(store.Metric{ToolName: "query", Timestamp: old, ProjectPath: "/p"}); err != nil {
		t.Fatalf("record the old metric: %v", err)
	}
	if err := st.RecordMetric(store.Metric{ToolName: "query", Timestamp: time.Now().Unix(), ProjectPath: "/p"}); err != nil {
		t.Fatalf("record the fresh metric: %v", err)
	}
	// A ten-year window counts every row, so the count says how many rows the
	// ledger still holds — not how many fall inside the retention window.
	const wideWindow = 3650
	before, err := st.MetricsSummary("", wideWindow)
	if err != nil {
		t.Fatalf("metrics summary before: %v", err)
	}
	if before.TotalInvocations != 2 {
		t.Fatalf("fixture holds %d metric rows, want 2", before.TotalInvocations)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	RunFleet(ctx, func() []Target { return []Target{{Store: st, Label: "one"}} }, Options{}, 10*time.Millisecond, 0)

	deadline := time.Now().Add(5 * time.Second)
	for {
		after, err := st.MetricsSummary("", wideWindow)
		if err != nil {
			t.Fatalf("metrics summary after: %v", err)
		}
		if after.TotalInvocations == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fleet never pruned the metric ledger: %d rows remain", after.TotalInvocations)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestRunFleetLogsWhatThePassDid: a pass that reclaims nothing, prunes
// nothing and compacts nothing used to leave no trace at all — RunFleet
// logged only on failure — which is why a sweep that did nothing ran for
// weeks without anyone noticing.
func TestRunFleetLogsWhatThePassDid(t *testing.T) {
	st := collectionFixture(t)
	var logs []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	RunFleet(ctx, func() []Target { return []Target{{Store: st, Label: "one"}} },
		Options{Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }},
		10*time.Millisecond, 0)

	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, l := range logs {
			if strings.Contains(l, "pass(es) over") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fleet never logged what the pass did: %v", logs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSweepDropsTheStaleCollectionWithTheKeepFunc: the writer passed NO keep
// list, so the stale-collection sweep returned early and an abandoned
// embedding collection was never pruned. The keep set is now derived per
// pass from the store's own newest embed run — the collection the next embed
// extends and the one the L3 rung queries.
func TestSweepDropsTheStaleCollectionWithTheKeepFunc(t *testing.T) {
	st := collectionFixture(t)
	// The live collection is the one the newest embed run names.
	runID, err := st.StartEmbedRun("live", "incremental", 1)
	if err != nil {
		t.Fatalf("start embed run: %v", err)
	}
	if err := st.FinishEmbedRun(runID, "completed", 1, 0, 0, 0, 0); err != nil {
		t.Fatalf("finish embed run: %v", err)
	}
	res := Sweep(st, Options{KeepModelIDsFunc: func(b store.Backend) []string {
		id, err := store.CurrentModelID(b)
		if err != nil || id == "" {
			return nil
		}
		return []string{id}
	}})
	if res.StaleModels != 1 {
		t.Fatalf("sweep = %+v, want 1 stale collection dropped", res)
	}
	if !hasCollection(t, st, "live") {
		t.Error("the live collection was dropped")
	}
	if hasCollection(t, st, "old") {
		t.Error("the stale collection survived the sweep")
	}
}

// TestCurrentModelIDNamesTheNewestRun: the keep set is derived from the run
// ledger, because a stamp carries no timestamp — two collections can both be
// stamped and nothing in emb_stamp says which one is live.
func TestCurrentModelIDNamesTheNewestRun(t *testing.T) {
	st := collectionFixture(t)
	if id, err := store.CurrentModelID(st); err != nil || id != "" {
		t.Fatalf("CurrentModelID on a store with no runs = %q, %v; want \"\"", id, err)
	}
	runID, err := st.StartEmbedRun("embeddinggemma-2", "incremental", 3)
	if err != nil {
		t.Fatalf("start embed run: %v", err)
	}
	if err := st.FinishEmbedRun(runID, "completed", 3, 0, 0, 0, 0); err != nil {
		t.Fatalf("finish embed run: %v", err)
	}
	id, err := store.CurrentModelID(st)
	if err != nil {
		t.Fatalf("CurrentModelID: %v", err)
	}
	if id != "embeddinggemma-2" {
		t.Fatalf("CurrentModelID = %q, want embeddinggemma-2", id)
	}
}
