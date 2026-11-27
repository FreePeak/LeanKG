package ccmemory

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/memory"
)

func TestHandleAbsentRootReturnsErrNoCentralRootAndCreatesNothing(t *testing.T) {
	home := t.TempDir()
	h := NewHandle(home, nil)
	t.Cleanup(func() { _ = h.Close() })

	mem, err := h.Get(context.Background())
	if !errors.Is(err, ErrNoCentralRoot) {
		t.Fatalf("Get on an unsynced home: err = %v, want ErrNoCentralRoot", err)
	}
	if mem != nil {
		t.Fatalf("Get on an unsynced home: mem = %p, want nil", mem)
	}
	if _, err := os.Stat(filepath.Join(home, ".leankg")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("answering a read created the central tree: stat .leankg err = %v, want not exist", err)
	}
}

func TestHandleOpensOnceTheIndexExists(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	h := NewHandle(home, nil)
	t.Cleanup(func() { _ = h.Close() })

	if _, err := h.Get(ctx); !errors.Is(err, ErrNoCentralRoot) {
		t.Fatalf("first Get before sync: err = %v, want ErrNoCentralRoot", err)
	}

	// A sync creates the central index; the same handle must pick it up
	// without a restart.
	seed, err := memory.Open(ctx, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	first, err := h.Get(ctx)
	if err != nil {
		t.Fatalf("Get after the index exists: %v", err)
	}
	if got, want := first.Root(), memory.GlobalRoot(home); got != want {
		t.Fatalf("root: got %q, want %q", got, want)
	}
	second, err := h.Get(ctx)
	if err != nil || second != first {
		t.Fatalf("second Get: mem %p err %v, want the handle opened by the first Get (%p)", second, err, first)
	}
}

func TestHandleConcurrentGetOpensOnce(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	seed, err := memory.Open(ctx, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	h := NewHandle(home, nil)
	t.Cleanup(func() { _ = h.Close() })

	const n = 16
	mems := make(chan *memory.Memory, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			m, err := h.Get(ctx)
			if err != nil {
				errs <- err
				return
			}
			mems <- m
		}()
	}
	var first *memory.Memory
	for i := 0; i < n; i++ {
		select {
		case err := <-errs:
			t.Fatalf("concurrent Get: %v", err)
		case m := <-mems:
			if first == nil {
				first = m
			} else if m != first {
				t.Fatalf("concurrent Get returned two handles: %p and %p", first, m)
			}
		}
	}
}

func TestHandleInjectedMemoryIsReturnedAndNotClosed(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	mem, err := memory.Open(ctx, home, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mem.Close() })

	h := NewHandle(home, mem)
	got, err := h.Get(ctx)
	if err != nil || got != mem {
		t.Fatalf("Get with an injected memory: mem %p err %v, want the injected %p", got, err, mem)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := mem.Banks(); err != nil {
		t.Fatalf("Close closed the injected memory, which the handle does not own: %v", err)
	}
}

// A failed open or stat must not hand the caller the local home path: the error
// reaches every transport that answers agent_recall. The two branches are the
// stat of the index (the root cannot be entered) and the open (the index is
// present but the root cannot take its topics directory). Both need a non-root
// user, because root ignores directory modes.
func TestHandleFailureNamesNoLocalPath(t *testing.T) {
	cases := map[string]fs.FileMode{
		"stat fails": 0o000,
		"open fails": 0o555,
	}
	for name, mode := range cases {
		t.Run(name, func(t *testing.T) {
			if os.Geteuid() == 0 {
				t.Skip("root ignores directory modes")
			}
			home := t.TempDir()
			root := memory.GlobalRoot(home)
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "index.db"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(root, mode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

			_, err := NewHandle(home, nil).Get(context.Background())
			if err == nil {
				t.Fatal("Get on an unreadable root: err = nil, want a refusal")
			}
			if errors.Is(err, ErrNoCentralRoot) {
				t.Fatalf("Get on an unreadable root answered as an absent root: %v", err)
			}
			for _, leak := range []string{home, root} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error leaks local path %q: %v", leak, err)
				}
			}
		})
	}
}
