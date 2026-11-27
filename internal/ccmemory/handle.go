package ccmemory

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/FreePeak/LeanKG/internal/memory"
)

// ErrNoCentralRoot means the central memory root has no index yet, so no sync
// has run. Answering a read never creates the root; the caller runs the sync.
var ErrNoCentralRoot = errors.New("ccmemory: no central memory root yet; run `leankg memory sync` once")

// errHandleClosed is returned by Get after Close.
var errHandleClosed = errors.New("ccmemory: central memory handle is closed")

// errCentralUnavailable is what a caller gets when the central index exists but
// cannot be opened. The cause names local paths, so it is logged, never returned.
var errCentralUnavailable = errors.New("ccmemory: central memory could not be opened (run `leankg memory sync`)")

// Handle is a lazily opened handle on the central memory root,
// memory.GlobalRoot(home). It is safe for concurrent use.
type Handle struct {
	home   string
	mu     sync.Mutex
	mem    *memory.Memory
	owned  bool // mem was opened by this handle, so Close closes it
	closed bool
}

// NewHandle returns a handle on the central root under home. open, when
// non-nil, is an already-open central memory (the serve --memory-global
// handle); Get returns it and Close leaves it open, since its owner closes it.
// Otherwise the first Get opens the root, and only once its index exists.
func NewHandle(home string, open *memory.Memory) *Handle {
	return &Handle{home: home, mem: open}
}

// Get returns the central memory, opening it on the first call that finds the
// index. A failed or absent open is not cached, so a sync that runs later is
// picked up without a restart.
func (h *Handle) Get(ctx context.Context) (*memory.Memory, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errHandleClosed
	}
	if h.mem != nil {
		return h.mem, nil
	}
	index := filepath.Join(memory.GlobalRoot(h.home), "index.db")
	if _, err := os.Stat(index); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNoCentralRoot
		}
		return nil, h.unavailable(err)
	}
	// memory.Open(ctx, dir, false) opens <dir>/.leankg/memory, which is the
	// central root when dir is home.
	mem, err := memory.Open(ctx, h.home, false)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, h.unavailable(err)
	}
	h.mem, h.owned = mem, true
	return mem, nil
}

// unavailable logs the cause with the home rewritten to ~ (the plan's privacy
// rule for local paths) and returns the fixed refusal.
func (h *Handle) unavailable(cause error) error {
	log.Printf("ccmemory: central memory unavailable: %s", strings.ReplaceAll(cause.Error(), h.home, "~"))
	return errCentralUnavailable
}

// Close closes the memory this handle opened. An injected memory is left open.
// Close is idempotent; Get after Close fails.
func (h *Handle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	if !h.owned {
		return nil
	}
	return h.mem.Close()
}
