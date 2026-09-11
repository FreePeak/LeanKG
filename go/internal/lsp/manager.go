package lsp

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/langs"
)

// defaultTTL is the idle lifetime used when NewManager is given a
// non-positive duration.
const defaultTTL = 60 * time.Second

// lspKey identifies one pooled server: a language at a workspace root.
type lspKey struct {
	lang langs.Language
	root string
}

// Manager keeps at most one live Client per (language, workspace root) pair
// and closes servers idle longer than its TTL. Operations are serialized on
// one mutex — including Start, so a slow handshake never double-spawns; the
// traffic here (one spawn per language per project) makes a finer lock
// pointless.
type Manager struct {
	ttl time.Duration

	mu       sync.Mutex
	clients  map[lspKey]*Client
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
		ttl:     ttl,
		clients: make(map[lspKey]*Client),
		done:    make(chan struct{}),
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictLocked()
	key := lspKey{lang, abs}
	if c := m.clients[key]; c != nil {
		return c, nil
	}
	c, err := Start(ctx, lang, spec, abs)
	if err != nil {
		return nil, err
	}
	m.clients[key] = c
	return c, nil
}

// Len returns the number of live pooled clients.
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.clients)
}

// Shutdown stops the reaper and closes every live client.
func (m *Manager) Shutdown() {
	m.stopOnce.Do(func() { close(m.done) })
	m.mu.Lock()
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
	defer m.mu.Unlock()
	m.evictLocked()
}

// evictLocked drops stale entries under the caller-held lock. Close runs
// with the lock held: it is bounded (2s shutdown grace + kill) and a reaper
// tick racing a Get on the same client is worse than a brief stall.
func (m *Manager) evictLocked() {
	for k, c := range m.clients {
		if c.IdleFor() > m.ttl {
			delete(m.clients, k)
			_ = c.Close()
		}
	}
}
