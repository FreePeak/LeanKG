package judge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
)

// Cached wraps a Judge with a content-addressed answer cache.
//
// Why this is correct rather than a shortcut: a judgment is a pure function of
// (state, questions) — Laya runs one forward pass over fixed weights, and
// repeated calls on identical input returned byte-identical answers in
// measurement. So caching cannot change a verdict; it only avoids recomputing
// one. That matters where the same step is scored repeatedly (the dsh-usage
// watcher re-scans its corpus every poll).
//
// Unavailable results (nil map) are never cached: a sidecar that is still
// loading must be retried, not remembered as broken.
//
// ponytail: single mutex + full-map clear at capacity, not an LRU. Judgment
// keys are few and hot; a real eviction policy earns its place only when a
// measured working set outgrows this. Upgrade path: swap the map for an LRU
// behind the same method.
type Cached struct {
	inner Judge
	max   int

	mu    sync.Mutex
	items map[string]map[string]Answer
}

// NewCached wraps inner with a cache of up to max distinct judgments
// (default 2048). A nil inner stays nil: no judge means no calls, and there is
// nothing to cache.
func NewCached(inner Judge, max int) *Cached {
	if inner == nil {
		return nil
	}
	if max <= 0 {
		max = 2048
	}
	return &Cached{inner: inner, max: max, items: make(map[string]map[string]Answer)}
}

// Ask answers from the cache when the exact state+questions were seen before,
// otherwise delegates and records the result.
func (c *Cached) Ask(ctx context.Context, state string, questions map[string]Question) (map[string]Answer, error) {
	key := cacheKey(state, questions)

	c.mu.Lock()
	if hit, ok := c.items[key]; ok {
		c.mu.Unlock()
		return hit, nil
	}
	c.mu.Unlock()

	answers, err := c.inner.Ask(ctx, state, questions)
	if err != nil || answers == nil {
		return answers, err // errors and unavailability are never cached
	}

	c.mu.Lock()
	if len(c.items) >= c.max {
		c.items = make(map[string]map[string]Answer)
	}
	c.items[key] = answers
	c.mu.Unlock()
	return answers, nil
}

// Len reports the number of cached judgments (diagnostics, tests).
func (c *Cached) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// cacheKey is a stable hash of the state and every question. Questions are
// serialised in sorted id order so map iteration order cannot produce two
// keys for the same judgment — the one way a naive cache here would silently
// double its misses.
func cacheKey(state string, questions map[string]Question) string {
	ids := make([]string, 0, len(questions))
	for id := range questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	ordered := make([]struct {
		ID string   `json:"id"`
		Q  Question `json:"q"`
	}, 0, len(ids))
	for _, id := range ids {
		ordered = append(ordered, struct {
			ID string   `json:"id"`
			Q  Question `json:"q"`
		}{id, questions[id]})
	}
	blob, _ := json.Marshal(struct {
		State     string `json:"state"`
		Questions any    `json:"questions"`
	}{state, ordered})
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}
