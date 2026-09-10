package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func openStore(t *testing.T) store.Backend {
	t.Helper()
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), dir, store.EngineSQLite, "", store.RW)
	if err != nil {
		t.Fatalf("OpenBackend: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func await(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

type eventLog struct {
	mu  sync.Mutex
	ks  map[string]bool
	ch  <-chan WatchEvent
	end chan struct{}
}

func newEventLog(w *Watcher) *eventLog {
	el := &eventLog{ks: map[string]bool{}, ch: w.Events(), end: make(chan struct{})}
	go func() {
		defer close(el.end)
		for ev := range el.ch {
			el.mu.Lock()
			el.ks[ev.Kind] = true
			el.mu.Unlock()
		}
	}()
	return el
}

func (el *eventLog) saw(kind string) bool {
	el.mu.Lock()
	defer el.mu.Unlock()
	return el.ks[kind]
}

// TestWatchIndexOnWriteAndRemove covers the end-to-end trigger flow: write a
// .go file -> element queryable; delete it -> gone; Events channel saw
// create+remove.
func TestWatchIndexOnWriteAndRemove(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	proj := t.TempDir()
	name := "WatchedFunc"

	w, err := Start(ctx, st, proj, Options{Debounce: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()
	el := newEventLog(w)

	file := filepath.Join(proj, "sample.go")
	if err := os.WriteFile(file, []byte("package sample\n\nfunc "+name+"() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	await(t, 3*time.Second, func() bool {
		els, err := st.FindExact(name)
		return err == nil && len(els) > 0
	})

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	await(t, 3*time.Second, func() bool {
		els, err := st.FindExact(name)
		return err == nil && len(els) == 0
	})

	await(t, time.Second, func() bool { return el.saw("create") && el.saw("remove") })
}

// TestSecondStartErrAlreadyWatching pins the single-flight lock: a second
// Start on the same root fails with ErrAlreadyWatching; after ctx cancel
// (loop exit releases the lock), a new Start on the same root succeeds.
func TestSecondStartErrAlreadyWatching(t *testing.T) {
	st := openStore(t)
	proj := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	w, err := Start(ctx, st, proj, Options{Debounce: time.Second})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer w.Stop()

	_, err = Start(context.Background(), st, proj, Options{Debounce: time.Second})
	if !errors.Is(err, ErrAlreadyWatching) {
		t.Fatalf("second Start: want ErrAlreadyWatching, got %v", err)
	}

	cancel()
	<-w.Done() // loop exit => stopOnce => flock released

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	w2, err := Start(ctx2, st, proj, Options{Debounce: time.Second})
	if err != nil {
		t.Fatalf("Start after cancel: %v", err)
	}
	w2.Stop()
}

// TestSkipDirsNotWatched pins the index-identical skip list at the WATCHER
// level: node_modules contents never produce events (IndexDir would also
// skip them — here the whole subtree is outside the notifier), while
// normal nested dirs are watched.
func TestSkipDirsNotWatched(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	proj := t.TempDir()
	for _, d := range []string{"node_modules/pkg", "src/deep"} {
		if err := os.MkdirAll(filepath.Join(proj, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w, err := Start(ctx, st, proj, Options{Debounce: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	// Inside node_modules: no event must ever surface, so no re-index runs.
	nm := filepath.Join(proj, "node_modules/pkg/skip.go")
	if err := os.WriteFile(nm, []byte("package pkg\n\nfunc SkipMe() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Inside a watched dir: the write triggers the flow.
	if err := os.WriteFile(filepath.Join(proj, "src/deep/keep.go"), []byte("package deep\n\nfunc KeepMe() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	await(t, 3*time.Second, func() bool {
		els, err := st.FindExact("KeepMe")
		return err == nil && len(els) > 0
	})
	// Give any (wrong) node_modules-triggered flush a chance to land, then
	// assert the skip actually held.
	time.Sleep(300 * time.Millisecond)
	if els, _ := st.FindExact("SkipMe"); len(els) > 0 {
		t.Error("node_modules file was indexed: skip list failed at watcher level")
	}
}

// TestEventsChannelDropOnOverflow pins the never-blocks contract: with no
// reader, 64+ events must not stall the loop.
func TestEventsChannelDropOnOverflow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	proj := t.TempDir()
	w, err := Start(ctx, st, proj, Options{Debounce: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	for i := 0; i < 128; i++ {
		if err := os.WriteFile(filepath.Join(proj, "many.go"),
			[]byte("package p\n\nfunc Many() int { return "+string(rune('0'+i%10))+" }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// If the loop blocked on a full channel, this re-index would never run.
	await(t, 3*time.Second, func() bool {
		els, err := st.FindExact("Many")
		return err == nil && len(els) > 0
	})
}
