package telemetry

import (
	"context"
	"testing"
	"time"
)

func TestRetentionSweepRemovesOnlyExpiredRows(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	now := t0.Add(60 * 24 * time.Hour)
	if err := st.InsertCalls(ctx, []CallEvent{
		{ID: "stale", TS: now.Add(-45 * 24 * time.Hour), Identity: Identity{ClientName: "pi", Cwd: "/w"}},
		{ID: "fresh", TS: now.Add(-day), Identity: Identity{ClientName: "pi", Cwd: "/w"}},
	}); err != nil {
		t.Fatal(err)
	}
	n, err := retentionSweepAt(ctx, st, 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("sweep removed %d, want 1", n)
	}
	if _, ok, _ := st.Call(ctx, "fresh"); !ok {
		t.Fatal("fresh call was swept")
	}
	if _, ok, _ := st.Call(ctx, "stale"); ok {
		t.Fatal("stale call survived")
	}
}

const day = 24 * time.Hour
