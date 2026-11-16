package core

import (
	"context"
	"sync"
	"time"

	"github.com/FreePeak/LeanKG/internal/embed"
)

// embedHealthTTL bounds how often status probes the query embedder.
const embedHealthTTL = 30 * time.Second

// embedProbeTimeout caps one probe even when the caller has no deadline; the
// caller's ctx can end it sooner.
const embedProbeTimeout = 3 * time.Second

// embedHealth caches one reachability probe of the query embedder, so status
// can report a provider outage without an embedding call per status request.
type embedHealth struct {
	mu  sync.Mutex
	at  time.Time
	ok  bool
	err string
}

// check returns the cached result, re-probing after embedHealthTTL. The probe
// runs outside the mutex, so concurrent status calls do not queue behind a
// slow provider. A probe ended by the caller's ctx is not cached: it says
// nothing about the provider.
func (h *embedHealth) check(ctx context.Context, em embed.Provider) (bool, string) {
	h.mu.Lock()
	if !h.at.IsZero() && time.Since(h.at) < embedHealthTTL {
		ok, msg := h.ok, h.err
		h.mu.Unlock()
		return ok, msg
	}
	h.mu.Unlock()

	pctx, cancel := context.WithTimeout(ctx, embedProbeTimeout)
	defer cancel()
	_, err := embed.EmbedQuery(pctx, em, "health check")
	if ctx.Err() != nil {
		return false, ctx.Err().Error()
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.at, h.ok, h.err = time.Now(), err == nil, ""
	if err != nil {
		h.err = err.Error()
	}
	return h.ok, h.err
}
