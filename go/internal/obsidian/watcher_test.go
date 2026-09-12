package obsidian

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// TestWatcherDebounceThrottle pins the Rust throttle semantics with a
// deterministic clock: the event loop reads the clock once at start and once
// per syncable event, so event times are exact and the test needs no sleeps.
func TestWatcherDebounceThrottle(t *testing.T) {
	st := openStore(t)
	vault := filepath.Join(t.TempDir(), "vault")
	e := New(vault, st)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	const debounce = 100 * time.Millisecond
	base := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)

	var mu sync.Mutex
	clockCalls := 0
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		clockCalls++
		return base.Add(time.Duration(clockCalls) * 60 * time.Millisecond)
	}

	events := make(chan rawEvent, 8)
	var synced int32
	var paths []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.debounceLoop(ctx, events, debounce, clock, WatchOptions{
			OnEvent: func(p string) { paths = append(paths, filepath.Base(p)) },
			OnSync:  func(PullResult, error) { atomic.AddInt32(&synced, 1) },
		})
	}()

	// chmod is not a sync trigger and must not consume a clock read.
	events <- rawEvent{path: "chmod.md", op: fsnotify.Chmod}
	events <- rawEvent{path: "a.md", op: fsnotify.Write}  // +120ms: inside the window, coalesced
	events <- rawEvent{path: "b.md", op: fsnotify.Write}  // +180ms: 120ms since last, applied
	events <- rawEvent{path: "c.md", op: fsnotify.Write}  // +240ms: 60ms, coalesced
	events <- rawEvent{path: "d.md", op: fsnotify.Create} // +300ms: 120ms, applied
	close(events)
	<-done

	if got := atomic.LoadInt32(&synced); got != 2 {
		t.Fatalf("applied syncs = %d, want 2", got)
	}
	if got := strings.Join(paths, ","); got != "b.md,d.md" {
		t.Fatalf("applied event paths = %q, want %q", got, "b.md,d.md")
	}
	mu.Lock()
	calls := clockCalls
	mu.Unlock()
	if calls != 5 {
		t.Fatalf("clock reads = %d, want 5 (loop start plus one per syncable event)", calls)
	}
}

// TestWatchPullsOnVaultEdit drives the real fsnotify path end to end: a note
// written after the watcher settles is pulled into the store, and the watcher
// stops on context cancellation.
func TestWatchPullsOnVaultEdit(t *testing.T) {
	st := openStore(t)
	vault := filepath.Join(t.TempDir(), "vault")
	e := New(vault, st)
	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	type syncEvent struct {
		res PullResult
		err error
	}
	got := make(chan syncEvent, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchErr := make(chan error, 1)
	go func() {
		watchErr <- e.Watch(ctx, WatchOptions{
			Debounce: 50 * time.Millisecond,
			OnSync:   func(res PullResult, err error) { got <- syncEvent{res, err} },
		})
	}()

	// Settle past the watcher's first debounce window, then edit the vault.
	time.Sleep(150 * time.Millisecond)
	writeNote(t, vault, "watched.md", "# Watched\n")

	select {
	case ev := <-got:
		if ev.err != nil {
			t.Fatalf("watcher sync: %v", ev.err)
		}
		if ev.res.Notes != 1 {
			t.Fatalf("watcher synced %d notes, want 1 (README is not a note)", ev.res.Notes)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watcher did not sync within 10s")
	}

	cancel()
	select {
	case err := <-watchErr:
		if err != nil {
			t.Fatalf("Watch: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Watch did not stop after cancellation")
	}

	els, err := st.FindExact("note/watched")
	if err != nil {
		t.Fatalf("FindExact: %v", err)
	}
	if len(els) != 1 {
		t.Fatalf("watched note missing from the store (%d matches)", len(els))
	}
}
