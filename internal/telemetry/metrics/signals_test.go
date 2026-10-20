package metrics

import (
	"testing"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

func lkCall(outcome string) telemetry.CallEvent {
	c := mkCall("c1", "claude-code:a", 0, "query", "search", outcome, 5)
	return c
}

func matched() *sessionlink.ToolCall {
	return &sessionlink.ToolCall{ID: "tu1", Norm: "leankg.query", IsLeanKG: true, Args: `{"query":"auth login handler"}`}
}

func TestSignalsUsedHitsFromFollowUpAndLaterText(t *testing.T) {
	c := lkCall(telemetry.OutcomeOK)
	c.HitFiles = "internal/a.go\ninternal/b.go"
	w := sessionlink.Window{
		Matched:   matched(),
		FollowUps: []sessionlink.ToolCall{{Norm: "read", Target: "internal/a.go"}},
		After: []sessionlink.Turn{
			{Role: "assistant", Text: "The handler lives in internal/b.go."},
			{Role: "user", Text: "next prompt"},
			{Role: "assistant", Text: "Later, internal/c.go changed."},
		},
	}
	s := SignalsFor(c, w)
	if s.HitsReturned != 2 || s.HitsUsed != 2 {
		t.Fatalf("used=%d returned=%d, want 2 of 2 (a via read, b via text)", s.HitsUsed, s.HitsReturned)
	}
	if s.UsedPrecision != 1 {
		t.Fatalf("precision=%v want 1", s.UsedPrecision)
	}
}

func TestSignalsPartialUseAndTextAfterPromptIgnored(t *testing.T) {
	c := lkCall(telemetry.OutcomeOK)
	c.HitFiles = "internal/a.go\ninternal/b.go"
	w := sessionlink.Window{
		Matched: matched(),
		After: []sessionlink.Turn{
			{Role: "user", Text: "next prompt"},
			{Role: "assistant", Text: "internal/b.go is relevant"},
		},
	}
	s := SignalsFor(c, w)
	if s.HitsUsed != 0 || s.UsedPrecision != 0 {
		t.Fatalf("used=%d precision=%v: text after the next prompt must not count", s.HitsUsed, s.UsedPrecision)
	}
}

func TestSignalsBasenameMatchCountsAsUse(t *testing.T) {
	c := lkCall(telemetry.OutcomeOK)
	c.HitFiles = "pkg/store/sqlite/store.go\nother.go"
	w := sessionlink.Window{Matched: matched(), FollowUps: []sessionlink.ToolCall{{Norm: "edit", Target: "/repo/pkg/store/sqlite/store.go"}}}
	s := SignalsFor(c, w)
	if s.HitsUsed != 1 || s.HitsReturned != 2 {
		t.Fatalf("used=%d returned=%d want 1 of 2", s.HitsUsed, s.HitsReturned)
	}
}

func TestSignalsFallbackOutsideHitsWithinK(t *testing.T) {
	c := lkCall(telemetry.OutcomeOK)
	c.HitFiles = "internal/a.go"
	w := sessionlink.Window{
		Matched: matched(),
		FollowUps: []sessionlink.ToolCall{
			{Norm: "read", Target: "internal/a.go"},
			{Norm: "grep", Target: "internal/other.go"},
			{Norm: "glob", Target: "internal/a.go"},
		},
	}
	s := SignalsFor(c, w)
	if !s.Fallback {
		t.Fatal("grep outside the hits within K must set fallback")
	}
	if len(s.FallbackTools) != 1 || s.FallbackTools[0] != "grep" {
		t.Fatalf("fallback tools=%v want [grep]", s.FallbackTools)
	}
}

func TestSignalsNoFallbackInsideHitsOrBeyondK(t *testing.T) {
	c := lkCall(telemetry.OutcomeOK)
	c.HitFiles = "internal/a.go"
	inside := sessionlink.Window{Matched: matched(), FollowUps: []sessionlink.ToolCall{{Norm: "grep", Target: "internal/a.go"}}}
	if s := SignalsFor(c, inside); s.Fallback || s.FallbackTools == nil {
		t.Fatalf("discovery inside hits must not be fallback: %+v", s)
	}

	beyond := sessionlink.Window{Matched: matched()}
	for i := 0; i < 5; i++ {
		beyond.FollowUps = append(beyond.FollowUps, sessionlink.ToolCall{Norm: "edit", Target: "internal/a.go"})
	}
	beyond.FollowUps = append(beyond.FollowUps, sessionlink.ToolCall{Norm: "grep", Target: "x.go"})
	if s := SignalsFor(c, beyond); s.Fallback {
		t.Fatal("the sixth follow-up is outside K=5 and must not count")
	}
}

func TestSignalsRequeryByTermOverlap(t *testing.T) {
	c := lkCall(telemetry.OutcomeOK)
	similar := sessionlink.Window{Matched: matched(), FollowUps: []sessionlink.ToolCall{
		{Norm: "leankg.query", IsLeanKG: true, Args: `{"query":"login handler session"}`},
	}}
	if s := SignalsFor(c, similar); !s.Requery {
		t.Fatal("two of three terms shared must count as a re-query")
	}
	unrelated := sessionlink.Window{Matched: matched(), FollowUps: []sessionlink.ToolCall{
		{Norm: "leankg.query", IsLeanKG: true, Args: `{"query":"payment refund"}`},
	}}
	if s := SignalsFor(c, unrelated); s.Requery {
		t.Fatal("no shared terms must not count as a re-query")
	}
	notLeanKG := sessionlink.Window{Matched: matched(), FollowUps: []sessionlink.ToolCall{
		{Norm: "grep", Args: `{"query":"login handler"}`},
	}}
	if s := SignalsFor(c, notLeanKG); s.Requery {
		t.Fatal("only leankg.* follow-ups are re-queries")
	}
}

func TestSignalsErrorRecoveryFollowedGuidance(t *testing.T) {
	c := lkCall(telemetry.OutcomeZeroHit)
	c.OutcomeReason = "no hits: retry with action=fuzzy"
	w := sessionlink.Window{Matched: matched(), FollowUps: []sessionlink.ToolCall{
		{Norm: "leankg.query", IsLeanKG: true, Args: `{"query":"auth","action":"fuzzy"}`},
	}}
	s := SignalsFor(c, w)
	if s.FollowedGuide == nil || !*s.FollowedGuide {
		t.Fatalf("next call used the suggested action: followed_guidance must be true, got %v", s.FollowedGuide)
	}
	if s.AbandonedAfter {
		t.Fatal("a further leankg call means not abandoned")
	}
}

func TestSignalsErrorRecoveryAbandoned(t *testing.T) {
	c := lkCall("error:ERR_DB")
	c.OutcomeReason = "retry with action=fuzzy"
	w := sessionlink.Window{Matched: matched(), FollowUps: []sessionlink.ToolCall{
		{Norm: "grep", Target: "x.go"},
	}}
	s := SignalsFor(c, w)
	if s.FollowedGuide == nil || *s.FollowedGuide {
		t.Fatalf("no leankg call after the error: followed_guidance must be false, got %v", s.FollowedGuide)
	}
	if !s.AbandonedAfter {
		t.Fatal("no further leankg call before the next prompt must be abandoned")
	}
}

func TestSignalsOKCallHasNoRecoveryFields(t *testing.T) {
	c := lkCall(telemetry.OutcomeOK)
	s := SignalsFor(c, sessionlink.Window{Matched: matched()})
	if s.FollowedGuide != nil || s.AbandonedAfter {
		t.Fatalf("ok call: followed=%v abandoned=%v, want nil/false", s.FollowedGuide, s.AbandonedAfter)
	}
}

func TestSignalsHitsFromCountWhenNoFiles(t *testing.T) {
	c := lkCall(telemetry.OutcomeOK)
	c.Hits = 4
	s := SignalsFor(c, sessionlink.Window{Matched: matched()})
	if s.HitsReturned != 4 || s.HitsUsed != 0 || s.UsedPrecision != 0 {
		t.Fatalf("returned=%d used=%d precision=%v want 4/0/0", s.HitsReturned, s.HitsUsed, s.UsedPrecision)
	}
}

func TestSignalsEmptyWindowAndNilMatched(t *testing.T) {
	s := SignalsFor(telemetry.CallEvent{}, sessionlink.Window{})
	if s.FallbackTools == nil {
		t.Fatal("fallback_tools must be non-nil")
	}
	if s.Fallback || s.Requery || s.HitsUsed != 0 || s.HitsReturned != 0 {
		t.Fatalf("empty window must yield zero signals: %+v", s)
	}
}
