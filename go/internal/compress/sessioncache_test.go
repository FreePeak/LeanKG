package compress

import (
	"testing"
	"time"
)

func TestSessionCacheStoreAndHit(t *testing.T) {
	cache := NewSessionCache()

	entry1, hit1, old1, hasOld1 := cache.Store("dummy.rs", "pub fn main() {}")
	if hit1 || hasOld1 || old1 != "" {
		t.Fatalf("first store: hit=%v hasOld=%v old=%q, want false/false/empty", hit1, hasOld1, old1)
	}
	if entry1.ReadCount != 1 || entry1.LineCount != 1 {
		t.Errorf("entry1 = %+v, want read_count 1 and 1 line", entry1)
	}

	entry2, hit2, _, hasOld2 := cache.Store("dummy.rs", "pub fn main() {}")
	if !hit2 || hasOld2 {
		t.Fatalf("second store: hit=%v hasOld=%v, want true/false", hit2, hasOld2)
	}
	if entry2.ReadCount != 2 {
		t.Errorf("read_count = %d, want 2", entry2.ReadCount)
	}

	_, hit3, old3, hasOld3 := cache.Store("dummy.rs", "pub fn diff() {}")
	if hit3 || !hasOld3 {
		t.Fatalf("changed store: hit=%v hasOld=%v, want false/true", hit3, hasOld3)
	}
	if old3 != "pub fn main() {}" {
		t.Errorf("old content = %q, want the previous contents", old3)
	}
}

func TestSessionCacheInvalidation(t *testing.T) {
	cache := NewSessionCache()
	cache.Store("target.rs", "data")
	if _, ok := cache.Get("target.rs"); !ok {
		t.Fatal("entry should be present after Store")
	}
	cache.Invalidate("target.rs")
	if _, ok := cache.Get("target.rs"); ok {
		t.Fatal("entry should be gone after Invalidate")
	}
}

func TestSessionCacheFileRefs(t *testing.T) {
	cache := NewSessionCache()
	first := cache.GetFileRef("a.rs")
	again := cache.GetFileRef("a.rs")
	second := cache.GetFileRef("b.rs")
	if first != "_F1_" || again != "_F1_" || second != "_F2_" {
		t.Errorf("file refs = %q %q %q, want _F1_ _F1_ _F2_", first, again, second)
	}
	cache.Invalidate("a.rs")
	if got := cache.GetFileRef("a.rs"); got != "_F3_" {
		t.Errorf("ref after invalidation = %q, want _F3_", got)
	}
}

func TestSessionCacheRecordCacheHit(t *testing.T) {
	cache := NewSessionCache()
	cache.Store("hit.rs", "content")
	entry, ok := cache.RecordCacheHit("hit.rs")
	if !ok || entry.ReadCount != 2 {
		t.Errorf("RecordCacheHit = %+v, %v; want read_count 2", entry, ok)
	}
	if _, ok := cache.RecordCacheHit("missing.rs"); ok {
		t.Error("RecordCacheHit on a missing path should report false")
	}
}

func TestSessionCacheEviction(t *testing.T) {
	cache := NewSessionCache()
	cache.Store("file1", "a b c d e f g h i j k l m n o p q r s t u v w x y z")
	cache.Store("file2", "hello world")

	if cache.TotalCachedTokens() == 0 {
		t.Fatal("expected cached tokens before eviction")
	}
	cache.EvictIfNeeded(500_001)
	if cache.TotalCachedTokens() != 0 {
		t.Errorf("eviction should drain entries, %d tokens remain", cache.TotalCachedTokens())
	}
	if _, ok := cache.Get("file1"); ok {
		t.Error("file1 should have been evicted")
	}
}

func TestSessionCacheEvictionRespectsEnvBudget(t *testing.T) {
	t.Setenv("LEANKG_CACHE_MAX_TOKENS", "10")
	cache := NewSessionCache()
	// ~15 tokens then ~15 tokens: the second store must evict the first.
	cache.Store("first.go", "some content that costs a few tokens")
	cache.Store("second.go", "other content that costs a few tokens")

	if _, ok := cache.Get("first.go"); ok {
		t.Error("first.go should be evicted once the budget is exceeded")
	}
	if _, ok := cache.Get("second.go"); !ok {
		t.Error("second.go should be present")
	}
}

func TestCacheEntryEvictionScoreRanksRecencyAndFrequency(t *testing.T) {
	now := time.Now()
	recent := CacheEntry{OriginalTokens: 100, ReadCount: 5, LastAccess: now}
	stale := CacheEntry{OriginalTokens: 100, ReadCount: 5, LastAccess: now.Add(-time.Hour)}
	if recent.EvictionScore(now) <= stale.EvictionScore(now) {
		t.Error("recently accessed entries must score higher than stale ones")
	}
	frequent := CacheEntry{OriginalTokens: 100, ReadCount: 50, LastAccess: now}
	rare := CacheEntry{OriginalTokens: 100, ReadCount: 1, LastAccess: now}
	if frequent.EvictionScore(now) <= rare.EvictionScore(now) {
		t.Error("frequently read entries must score higher than rarely read ones")
	}
	big := CacheEntry{OriginalTokens: 10000, ReadCount: 1, LastAccess: now}
	small := CacheEntry{OriginalTokens: 10, ReadCount: 1, LastAccess: now}
	if big.EvictionScore(now) <= small.EvictionScore(now) {
		t.Error("larger entries must score higher than smaller ones")
	}
}
