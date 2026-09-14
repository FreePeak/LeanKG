package compress

import (
	"hash/fnv"
	"math"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

// cacheEntry is one cached file (Rust session_cache::CacheEntry).
type CacheEntry struct {
	Content        string
	Hash           string
	LineCount      int
	OriginalTokens int
	ReadCount      uint32
	Path           string
	LastAccess     time.Time
}

// EvictionScore is a Boltzmann-inspired value score; higher = keep longer
// (Rust CacheEntry::eviction_score).
func (e CacheEntry) EvictionScore(now time.Time) float64 {
	elapsed := now.Sub(e.LastAccess).Seconds()
	recency := 1.0 / (1.0 + math.Sqrt(elapsed))
	frequency := math.Log(float64(e.ReadCount) + 1.0)
	sizeValue := math.Log(float64(e.OriginalTokens) + 1.0)
	return recency*0.4 + frequency*0.3 + sizeValue*0.3
}

// SessionCache caches file contents for the diff/cache-hit modes. It is the
// Go port of Arc<RwLock<SessionCache>>: the mutex lives inside (Rust
// session_cache::SessionCache).
type SessionCache struct {
	mu       sync.Mutex
	entries  map[string]CacheEntry
	fileRefs map[string]string
	nextRef  int
}

// NewSessionCache builds an empty cache.
func NewSessionCache() *SessionCache {
	return &SessionCache{
		entries:  map[string]CacheEntry{},
		fileRefs: map[string]string{},
		nextRef:  1,
	}
}

// cacheMaxTokens is the byte/token budget (Rust max_cache_tokens; env
// LEANKG_CACHE_MAX_TOKENS, default 500000).
func cacheMaxTokens() int {
	if v := os.Getenv("LEANKG_CACHE_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 500000
}

// computeHash fingerprints content for change detection. The Rust reference
// used SipHash via DefaultHasher; the hash value is never observable, so
// FNV-1a covers the equality contract.
func computeHash(content string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(content))
	return strconv.FormatUint(h.Sum64(), 16)
}

// GetFileRef returns the stable short reference ("_F1_", "_F2_", ...) for a
// path.
func (c *SessionCache) GetFileRef(path string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getFileRefLocked(path)
}

func (c *SessionCache) getFileRefLocked(path string) string {
	if r, ok := c.fileRefs[path]; ok {
		return r
	}
	r := "_F" + strconv.Itoa(c.nextRef) + "_"
	c.nextRef++
	c.fileRefs[path] = r
	return r
}

// Get returns the cached entry for a path, if present.
func (c *SessionCache) Get(path string) (CacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[path]
	return e, ok
}

// Invalidate drops a path from the cache.
func (c *SessionCache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, path)
	delete(c.fileRefs, path)
}

// RecordCacheHit bumps the read count and recency of a cached entry.
func (c *SessionCache) RecordCacheHit(path string) (CacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[path]
	if !ok {
		return CacheEntry{}, false
	}
	entry.ReadCount++
	entry.LastAccess = time.Now()
	c.entries[path] = entry
	return entry, true
}

// Store inserts or updates a file. It returns the entry, whether it was an
// unchanged-content hit, and the previous content when it changed (Rust
// SessionCache::store).
func (c *SessionCache) Store(path, content string) (entry CacheEntry, hit bool, oldContent string, hasOld bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hash := computeHash(content)
	lineCount := len(splitLines(content))
	originalTokens := EstimateTokens(content)
	now := time.Now()

	if existing, ok := c.entries[path]; ok {
		existing.LastAccess = now
		if existing.Hash == hash {
			existing.ReadCount++
			c.entries[path] = existing
			return existing, true, "", false
		}
		old := existing.Content
		existing.Content = content
		existing.Hash = hash
		existing.LineCount = lineCount
		existing.OriginalTokens = originalTokens
		existing.ReadCount++
		c.entries[path] = existing
		return existing, false, old, true
	}

	c.evictIfNeededLocked(originalTokens, now)
	c.getFileRefLocked(path)

	entry = CacheEntry{
		Content:        content,
		Hash:           hash,
		LineCount:      lineCount,
		OriginalTokens: originalTokens,
		ReadCount:      1,
		Path:           path,
		LastAccess:     now,
	}
	c.entries[path] = entry
	return entry, false, "", false
}

// TotalCachedTokens sums the token cost of all cached entries.
func (c *SessionCache) TotalCachedTokens() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalCachedTokensLocked()
}

func (c *SessionCache) totalCachedTokensLocked() int {
	total := 0
	for _, e := range c.entries {
		total += e.OriginalTokens
	}
	return total
}

// EvictIfNeeded drops lowest-value entries until incoming tokens fit the
// budget (Rust SessionCache::evict_if_needed).
func (c *SessionCache) EvictIfNeeded(incomingTokens int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictIfNeededLocked(incomingTokens, time.Now())
}

func (c *SessionCache) evictIfNeededLocked(incomingTokens int, now time.Time) {
	maxTokens := cacheMaxTokens()
	current := c.totalCachedTokensLocked()
	if current+incomingTokens <= maxTokens {
		return
	}

	type scored struct {
		path  string
		score float64
	}
	scores := make([]scored, 0, len(c.entries))
	for path, entry := range c.entries {
		scores = append(scores, scored{path, entry.EvictionScore(now)})
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score < scores[j].score })

	freed := 0
	target := current + incomingTokens - maxTokens
	if target < 0 {
		target = 0
	}
	for _, s := range scores {
		if freed >= target {
			break
		}
		if entry, ok := c.entries[s.path]; ok {
			delete(c.entries, s.path)
			delete(c.fileRefs, s.path)
			freed += entry.OriginalTokens
		}
	}
}
