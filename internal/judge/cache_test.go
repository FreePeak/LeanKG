package judge

import (
	"context"
	"errors"
	"testing"
)

// countingJudge records how many times the inner judge was actually asked.
type countingJudge struct {
	calls int
	reply map[string]Answer
	err   error
	nil   bool
}

func (c *countingJudge) Ask(_ context.Context, _ string, _ map[string]Question) (map[string]Answer, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	if c.nil {
		return nil, nil
	}
	return c.reply, nil
}

func noulQ() map[string]Question {
	return map[string]Question{"is_defect": {Type: Noul, Instructions: "Is it a defect?"}}
}

func TestCacheServesRepeatWithoutCallingInner(t *testing.T) {
	inner := &countingJudge{reply: map[string]Answer{"is_defect": {Value: "0.9", Confidence: 0.9}}}
	c := NewCached(inner, 8)

	first, err := c.Ask(context.Background(), "state", noulQ())
	if err != nil || first["is_defect"].Value != "0.9" {
		t.Fatalf("first = (%v, %v)", first, err)
	}
	second, err := c.Ask(context.Background(), "state", noulQ())
	if err != nil || second["is_defect"].Value != "0.9" {
		t.Fatalf("second = (%v, %v)", second, err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner called %d times, want 1 (second must be a cache hit)", inner.calls)
	}
	if c.Len() != 1 {
		t.Fatalf("cache len = %d, want 1", c.Len())
	}
}

// TestCacheKeyIsOrderIndependent pins the one way this cache could silently
// miss: Go map iteration order must not leak into the key.
func TestCacheKeyIsOrderIndependent(t *testing.T) {
	a := map[string]Question{
		"alpha": {Type: Noul, Instructions: "A?"},
		"beta":  {Type: Noul, Instructions: "B?"},
		"gamma": {Type: Noul, Instructions: "C?"},
	}
	b := map[string]Question{
		"gamma": {Type: Noul, Instructions: "C?"},
		"alpha": {Type: Noul, Instructions: "A?"},
		"beta":  {Type: Noul, Instructions: "B?"},
	}
	if cacheKey("s", a) != cacheKey("s", b) {
		t.Fatal("same questions in different map order produced different keys")
	}
	if cacheKey("s", a) == cacheKey("other", a) {
		t.Fatal("different state produced the same key")
	}
}

func TestCacheDoesNotRememberUnavailable(t *testing.T) {
	inner := &countingJudge{nil: true}
	c := NewCached(inner, 8)
	if _, err := c.Ask(context.Background(), "s", noulQ()); err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Ask(context.Background(), "s", noulQ()); err != nil {
		t.Fatalf("err = %v", err)
	}
	if inner.calls != 2 {
		t.Fatalf("inner called %d times, want 2 — an unavailable judge must be retried, not cached", inner.calls)
	}
	if c.Len() != 0 {
		t.Fatalf("cache len = %d, want 0", c.Len())
	}
}

func TestCacheDoesNotRememberErrors(t *testing.T) {
	inner := &countingJudge{err: errors.New("bad question")}
	c := NewCached(inner, 8)
	_, _ = c.Ask(context.Background(), "s", noulQ())
	_, _ = c.Ask(context.Background(), "s", noulQ())
	if inner.calls != 2 || c.Len() != 0 {
		t.Fatalf("calls = %d len = %d, want 2 and 0", inner.calls, c.Len())
	}
}

func TestCacheNilInnerStaysNil(t *testing.T) {
	if NewCached(nil, 8) != nil {
		t.Fatal("nil judge should stay nil — no judge means no calls")
	}
}

func TestCacheClearsAtCapacity(t *testing.T) {
	inner := &countingJudge{reply: map[string]Answer{"is_defect": {Value: "0.5"}}}
	c := NewCached(inner, 2)
	for _, s := range []string{"a", "b", "c"} {
		if _, err := c.Ask(context.Background(), s, noulQ()); err != nil {
			t.Fatal(err)
		}
	}
	if c.Len() > 2 {
		t.Fatalf("cache len = %d, want <= 2 after capacity clear", c.Len())
	}
}

// TestCachedJudgeAnswersMatchUncached is the property that makes caching safe:
// wrapping changes latency, never the verdict.
func TestCachedJudgeAnswersMatchUncached(t *testing.T) {
	reply := map[string]Answer{
		"is_defect": {Value: "0.91", Confidence: 0.91, Distribution: map[string]float64{"true": 0.91}},
	}
	plain := &countingJudge{reply: reply}
	cached := NewCached(&countingJudge{reply: reply}, 8)

	want, _ := plain.Ask(context.Background(), "s", noulQ())
	for i := 0; i < 3; i++ {
		got, err := cached.Ask(context.Background(), "s", noulQ())
		if err != nil {
			t.Fatal(err)
		}
		if got["is_defect"].Value != want["is_defect"].Value || got["is_defect"].Confidence != want["is_defect"].Confidence {
			t.Fatalf("cached verdict differs: %+v vs %+v", got, want)
		}
	}
}
