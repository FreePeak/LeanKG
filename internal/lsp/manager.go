package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/FreePeak/LeanKG/internal/langs"
)

// defaultTTL is the idle lifetime used when NewManager is given a
// non-positive duration.
const defaultTTL = 60 * time.Second

// lspKey identifies one pooled server: a language at a workspace root.
type lspKey struct {
	lang langs.Language
	root string
}

// spawn is one in-flight start for a key. Callers that find it wait on done
// instead of starting a second server for the same key.
type spawn struct {
	done chan struct{}
	c    *Client
	err  error
}

// Manager keeps at most one live Client per (language, workspace root) pair
// and closes servers idle longer than its TTL. The mutex guards the maps only.
// A spawn (including its handshake) runs with the mutex released, so a slow
// handshake for one key never blocks another key, and a Close never runs under
// the lock.
type Manager struct {
	ttl time.Duration

	mu       sync.Mutex
	clients  map[lspKey]*Client
	starting map[lspKey]*spawn
	shut     bool
	done     chan struct{}
	stopOnce sync.Once
}

// NewManager returns a running manager. Clients idle beyond ttl are closed by
// a background reaper; ttl <= 0 selects defaultTTL. Callers MUST arrange for
// Shutdown to run before exit.
func NewManager(ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = defaultTTL
	}
	m := &Manager{
		ttl:      ttl,
		clients:  make(map[lspKey]*Client),
		starting: make(map[lspKey]*spawn),
		done:     make(chan struct{}),
	}
	interval := ttl / 4
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	} else if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-m.done:
				return
			case <-t.C:
				m.evict()
			}
		}
	}()
	return m
}

// Get returns a live client for (lang, rootDir), reusing a pooled one or
// starting a new server. rootDir is resolved to an absolute path for keying.
func (m *Manager) Get(ctx context.Context, lang langs.Language, spec *langs.LSPSpec, rootDir string) (*Client, error) {
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	return m.acquire(ctx, lspKey{lang, abs}, func(ctx context.Context) (*Client, error) {
		return Start(ctx, lang, spec, abs)
	})
}

// GetCommand is Get for a bridge-configured server: an explicit binary plus
// args (catalog/leankg.yaml servers carry flags such as --stdio) and optional
// initializationOptions. Pooling, idle eviction and shutdown semantics are
// identical.
func (m *Manager) GetCommand(ctx context.Context, lang langs.Language, bin string, args []string, rootDir string, initOpts any) (*Client, error) {
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	return m.acquire(ctx, lspKey{lang, abs}, func(ctx context.Context) (*Client, error) {
		return StartCommand(ctx, lang, bin, args, abs, initOpts)
	})
}

// acquire returns the pooled client for key, starting one with start when none
// is pooled. A concurrent caller for the same key waits for the spawn already
// in flight, or gives up when its own ctx ends.
func (m *Manager) acquire(ctx context.Context, key lspKey, start func(context.Context) (*Client, error)) (*Client, error) {
	m.mu.Lock()
	if m.shut {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: manager is shut down", ErrNoServer)
	}
	stale := m.evictLocked()
	if c := m.clients[key]; c != nil {
		m.mu.Unlock()
		closeAll(stale)
		return c, nil
	}
	if sp := m.starting[key]; sp != nil {
		m.mu.Unlock()
		closeAll(stale)
		select {
		case <-sp.done:
			return sp.c, sp.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	sp := &spawn{done: make(chan struct{})}
	m.starting[key] = sp
	m.mu.Unlock()
	closeAll(stale)

	c, err := start(ctx)

	m.mu.Lock()
	delete(m.starting, key)
	pooled := err == nil && !m.shut
	if pooled {
		m.clients[key] = c
	}
	m.mu.Unlock()
	if err == nil && !pooled {
		// Shutdown ran while the spawn was in flight; nothing may outlive it.
		_ = c.Close()
		c, err = nil, fmt.Errorf("%w: manager is shut down", ErrNoServer)
	}
	sp.c, sp.err = c, err
	close(sp.done)
	return c, err
}

// Evict closes and drops the pooled client for (lang, rootDir) so the next
// Get spawns a fresh server. The Rust bridge did the same after a failed
// request (entry = None) so one dead server never poisons the pool.
func (m *Manager) Evict(lang langs.Language, rootDir string) {
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return
	}
	m.mu.Lock()
	c := m.clients[lspKey{lang, abs}]
	delete(m.clients, lspKey{lang, abs})
	m.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// Len returns the number of live pooled clients.
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.clients)
}

// Shutdown stops the reaper and closes every live client. A spawn still in
// flight closes its own client when it lands, and later Gets are refused.
func (m *Manager) Shutdown() {
	m.stopOnce.Do(func() { close(m.done) })
	m.mu.Lock()
	m.shut = true
	clients := m.clients
	m.clients = make(map[lspKey]*Client)
	m.mu.Unlock()
	for _, c := range clients {
		_ = c.Close()
	}
}

// evict closes clients idle beyond the TTL.
func (m *Manager) evict() {
	m.mu.Lock()
	stale := m.evictLocked()
	m.mu.Unlock()
	closeAll(stale)
}

// evictLocked drops the clients idle beyond the TTL and returns them. The
// caller closes them after releasing the lock: Close is bounded but blocks on
// a graceful shutdown round trip.
func (m *Manager) evictLocked() []*Client {
	var stale []*Client
	for k, c := range m.clients {
		if c.IdleFor() > m.ttl {
			delete(m.clients, k)
			stale = append(stale, c)
		}
	}
	return stale
}

func closeAll(cs []*Client) {
	for _, c := range cs {
		_ = c.Close()
	}
}
