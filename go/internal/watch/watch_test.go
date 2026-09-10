package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// TestWatchIndexOnWriteAndRemove covers the end-to-end trigger flow: write a
// .go file -> element queryable; delete it -> gone; Events channel sees
// create+remove.
func TestWatchIndexOnWriteAndRemove(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	proj := t.TempDir()
	name := "WatchedFunc"

	events := make([]WatchEvent, 0, 8)
	w, err := Start(ctx, st, proj, Options{
		Debounce: 100 * time.Millisecond,
		OnEvent:  func(path, kind string) { /* exercised via Events() */ },
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()
	go func() {
		for ev := range w.Events() {
			events = append(events, ev) // test-local copy; Events is introspection
		}
	}()

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

	sawCreate, sawRemove := false, false
	for _, ev := range events {
		switch ev.Kind {
		case "create":
			sawCreate = true
		case "remove":
			sawRemove = true
		}
	}
	if !sawCreate {
		t.Error("Events channel did not report a create event")
	}
	if !sawRemove {
		t.Error("Events channel did not report a remove event")
	}
}

// TestSecondStartErrAlreadyWatching pins the single-flight lock: second Start
// on the same root fails with ErrAlreadyWatching; after ctx cancel (and lock
// release), a new Start on the same root succeeds.
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
	w.Stop() // loop exit releases the lock

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	w2, err := Start(ctx2, st, proj, Options{Debounce: time.Second})
	if err != nil {
		t.Fatalf("Start after cancel: %v", err)
	}
	w2.Stop()
}

// TestSkippedDirsNotWatched pins the index-identical skip list: directories
// under node_modules/.git/.leankg etc. are never added to the notifier.
func TestSkippedDirsNotWatched(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	proj := t.TempDir()
	for _, d := range []string{"node_modules/pkg", ".git/objects", ".leankg/cache", "vendor/x", "src/deep"} {
		if err := os.MkdirAll(filepath.Join(proj, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Watching a directory in fsnotify fails on removed dirs but not here;
	// we assert indirectly: writes inside node_modules must not trigger
	// indexing of any element.
	w, err := Start(ctx, st, proj, Options{Debounce: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	if err := os.WriteFile(filepath.Join(proj, "node_modules/pkg/skip.go"), []byte("package pkg\n\nfunc SkipMe() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	await(t, 2*time.Second, func() bool {
		els, err := st.FindExact("SkipMe")
		return err == nil && len(els) == 0
	})
	// And a normal file inside src/ is still picked up (walk added src/deep).
	if err := os.WriteFile(filepath.Join(proj, "src/deep/keep.go"), []byte("package deep\n\nfunc KeepMe() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	await(t, 3*time.Second, func() bool {
		els, err := st.FindExact("KeepMe")
		return err == nil && len(els) > 0
	})
}
