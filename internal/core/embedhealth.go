package core

import (
	"context"
	"sync"
	"time"
)

// embedHealthTTL bounds how often status probes the query embedder.
const embedHealthTTL = 30 * time.Second

// embedHealth caches one reachability probe of the query embedder, so status
// can report a provider outage without an embedding call per status request.
type embedHealth struct {
	mu  sync.Mutex
	at  time.Time
	ok  bool
	err string
}

// check returns the cached result, re-probing after embedHealthTTL.
func (h *embedHealth) check(em QueryEmbedder) (bool, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.at.IsZero() && time.Since(h.at) < embedHealthTTL {
		return h.ok, h.err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := em.EmbedQuery(ctx, "health check")
	h.at, h.ok, h.err = time.Now(), err == nil, ""
	if err != nil {
		h.err = err.Error()
	}
	return h.ok, h.err
}
