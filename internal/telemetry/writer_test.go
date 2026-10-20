package telemetry

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// reopenRO reads the ledger through a fresh handle, since the recorder closes st.
func reopenRO(t *testing.T, st *SQLiteStore) *SQLiteStore {
	t.Helper()
	ro, err := OpenSQLite(st.Path(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	return ro
}

func TestRecorderOffIsNoopAndWritesNothing(t *testing.T) {
	st := openTestLedger(t)
	rec := NewRecorder(st, Off, 1024)
	if rec.Level() != Off {
		t.Fatalf("Level = %q", rec.Level())
	}
	for i := 0; i < 10; i++ {
		rec.RecordCall(CallEvent{Tool: "query"})
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	stats, _ := reopenRO(t, st).Stats(context.Background())
	if stats.Calls != 0 {
		t.Fatalf("Off recorder wrote %d calls", stats.Calls)
	}
}

func TestRecorderAssignsIDAndTSAndBlanksBodiesBelowBodiesLevel(t *testing.T) {
	st := openTestLedger(t)
	rec := NewRecorder(st, Metadata, 1024)
	rec.RecordCall(CallEvent{Tool: "query", ArgsRedacted: "args", BodyRedacted: "body", Identity: Identity{ClientName: "pi", Cwd: "/w"}})
	rec.RecordMemory(MemoryEvent{Verb: "recall", Error: "boom"})
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	ro := reopenRO(t, st)
	calls, err := ro.Calls(context.Background(), CallFilter{})
	if err != nil || len(calls) != 1 {
		t.Fatalf("calls = %d err=%v", len(calls), err)
	}
	c := calls[0]
	if c.ID == "" || c.TS.IsZero() {
		t.Fatalf("ID or TS not assigned: %+v", c)
	}
	if c.ArgsRedacted != "" || c.BodyRedacted != "" {
		t.Fatalf("bodies stored below Bodies level: %+v", c)
	}
	if c.SessionID == "" {
		t.Fatal("session not assigned")
	}
	mems, _ := ro.MemoryEvents(context.Background(), CallFilter{})
	if len(mems) != 1 || mems[0].ID == "" || mems[0].TS.IsZero() {
		t.Fatalf("memory event = %+v", mems)
	}
}

func TestRecorderBodiesRedactsAndCaps(t *testing.T) {
	st := openTestLedger(t)
	rec := NewRecorder(st, Bodies, 64)
	secret := "password=hunter2supersecret"
	rec.RecordCall(CallEvent{
		Tool:         "query",
		ArgsRedacted: secret,
		BodyRedacted: strings.Repeat("y", 500) + " Bearer abcdef0123456789XYZ",
	})
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	calls, _ := reopenRO(t, st).Calls(context.Background(), CallFilter{})
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	c := calls[0]
	if strings.Contains(c.ArgsRedacted, "hunter2supersecret") {
		t.Fatalf("secret stored: %q", c.ArgsRedacted)
	}
	if strings.Contains(c.BodyRedacted, "abcdef0123456789XYZ") {
		t.Fatalf("bearer token stored: %q", c.BodyRedacted)
	}
	if len(c.BodyRedacted) > 64+len("...[truncated]") {
		t.Fatalf("body not capped: %d bytes", len(c.BodyRedacted))
	}
}

// A store whose inserts block until released, so the queue fills.
type blockingStore struct {
	Store   // nil; only the methods below are used
	release chan struct{}
	mu      sync.Mutex
	stored  int
	dropped int64
}

func (b *blockingStore) InsertCalls(_ context.Context, calls []CallEvent) error {
	<-b.release
	b.mu.Lock()
	b.stored += len(calls)
	b.mu.Unlock()
	return nil
}

func (b *blockingStore) AddDropped(_ context.Context, n int64) error {
	b.mu.Lock()
	b.dropped += n
	b.mu.Unlock()
	return nil
}

func (b *blockingStore) Close() error { return nil }

func TestRecorderDropsWhenFullAndCountsDrops(t *testing.T) {
	st := &blockingStore{release: make(chan struct{})}
	rec := NewRecorder(st, Metadata, 1024)
	const sent = 2000
	done := make(chan struct{})
	go func() {
		for i := 0; i < sent; i++ {
			rec.RecordCall(CallEvent{Tool: "query"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RecordCall blocked on a full queue")
	}
	close(st.release)
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.dropped == 0 {
		t.Fatal("no drops counted with a 1024-slot queue and 2000 events")
	}
	if int64(st.stored)+st.dropped != sent {
		t.Fatalf("stored %d + dropped %d != sent %d", st.stored, st.dropped, sent)
	}
}

func TestRecorderNeverPanicsAfterClose(t *testing.T) {
	st := openTestLedger(t)
	rec := NewRecorder(st, Metadata, 16)
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	rec.RecordCall(CallEvent{Tool: "query"})
	rec.RecordMemory(MemoryEvent{Verb: "recall"})
}

// TestRecorderReloadRaisesLevelLive is the SIGHUP-consent half of the live
// reload plan: an operator who grants consent mid-session must not need a
// server restart for capture to start. Before this the level was fixed at
// construction, and an Off recorder had no writer goroutine to raise.
func TestRecorderReloadRaisesLevelLive(t *testing.T) {
	st := openTestLedger(t)
	rec := NewRecorder(st, Off, 1024).(*recorder)
	if rec.Level() != Off {
		t.Fatalf("Level = %q, want Off before the grant", rec.Level())
	}
	// Consent is granted: re-read the config and apply it to the live recorder.
	if err := rec.SetLevel(Metadata); err != nil {
		t.Fatalf("SetLevel(Metadata): %v", err)
	}
	if rec.Level() != Metadata {
		t.Fatalf("Level = %q after SetLevel, want metadata", rec.Level())
	}
	rec.RecordCall(CallEvent{Tool: "query", Transport: TransportHTTP})
	// Give the writer goroutine its beat, then read through a fresh handle.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats, _ := reopenRO(t, st).Stats(context.Background())
		if stats.Calls >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stats, _ := reopenRO(t, st).Stats(context.Background())
	if stats.Calls != 1 {
		t.Fatalf("calls after the grant = %d, want 1 — a raised level must capture", stats.Calls)
	}
}

// TestRecorderReloadLoweringTurnsCaptureOff covers the direction that matters
// for the operator's "Off means off" expectation: dropping consent stops
// capture without a restart, and in-flight events are not resurrected.
func TestRecorderReloadLoweringTurnsCaptureOff(t *testing.T) {
	st := openTestLedger(t)
	rec := NewRecorder(st, Metadata, 1024).(*recorder)
	rec.RecordCall(CallEvent{Tool: "query", Transport: TransportHTTP})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats, _ := reopenRO(t, st).Stats(context.Background())
		if stats.Calls >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := rec.SetLevel(Off); err != nil {
		t.Fatalf("SetLevel(Off): %v", err)
	}
	if rec.Level() != Off {
		t.Fatalf("Level = %q after SetLevel(Off)", rec.Level())
	}
	rec.RecordCall(CallEvent{Tool: "status", Transport: TransportHTTP})
	time.Sleep(150 * time.Millisecond)
	stats, _ := reopenRO(t, st).Stats(context.Background())
	if stats.Calls != 1 {
		t.Fatalf("calls = %d, want 1 — events after Off must not be captured", stats.Calls)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
}
