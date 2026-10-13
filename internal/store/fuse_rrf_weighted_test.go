package store

import (
	"math"
	"testing"
)

func TestFuseRRFWeightedDefaultsMatchFuseRRF(t *testing.T) {
	// The zero-value struct means every arm keeps weight 1.0, so
	// FuseRRFWeighted must be identical to FuseRRF — the weighted
	// variant is an extension, not a replacement.
	lists := []RankList{
		{Name: ArmVector, Keys: []string{"a", "b", "c"}},
		{Name: ArmTSVector, Keys: []string{"b", "a", "d"}},
	}
	weighted := FuseRRFWeighted(lists, FuseRRFWeights{})
	plain := FuseRRF(lists)
	if len(weighted) != len(plain) {
		t.Fatalf("len weighted = %d, plain = %d", len(weighted), len(plain))
	}
	for i := range plain {
		if weighted[i].Key != plain[i].Key {
			t.Fatalf("rank %d key = %q, want %q", i, weighted[i].Key, plain[i].Key)
		}
		if d := math.Abs(weighted[i].Score - plain[i].Score); d > 1e-9 {
			t.Fatalf("%s score weighted = %v, plain = %v", weighted[i].Key, weighted[i].Score, plain[i].Score)
		}
	}
}

func TestFuseRRFWeightedZeroWeightMeansDefault(t *testing.T) {
	// 0 is the zero-value that means "default weight 1.0", so a doc
	// ranked by the vector arm still scores its standard RRF share.
	// To drop an arm you omit its RankList — FuseRRFWeighted never
	// silently zeroes an arm behind the caller's back.
	lists := []RankList{
		{Name: ArmVector, Keys: []string{"a"}},
	}
	got := FuseRRFWeighted(lists, FuseRRFWeights{})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	want := 1 / float64(RRFK+1)
	if d := math.Abs(got[0].Score - want); d > 1e-9 {
		t.Fatalf("score = %v, want %v", got[0].Score, want)
	}
	if got[0].Ranks[ArmVector] != 1 {
		t.Fatalf("ranks = %v, want vector:1", got[0].Ranks)
	}
}

func TestFuseRRFWeightedUpweightsVectorArm(t *testing.T) {
	// Doubling the vector weight lifts a vector-rank-1 doc above a
	// doc both arms rank well, even when tsvector disagrees.
	lists := []RankList{
		{Name: ArmVector, Keys: []string{"low", "high"}},
		{Name: ArmTSVector, Keys: []string{"high", "low"}},
	}
	got := FuseRRFWeighted(lists, FuseRRFWeights{Vector: 2.0})
	if got[0].Key != "low" {
		t.Fatalf("top hit = %q, want low (2x vector weight lifts its rank-1 doc)", got[0].Key)
	}
	lows, highs := got[0].Score, 0.0
	for i := range got {
		if got[i].Key == "high" {
			highs = got[i].Score
		}
	}
	if !(lows > highs) {
		t.Fatalf("low score %v should exceed high %v", lows, highs)
	}
}

// TestFusionWeightChangesTheRanking pins the other half of the args.weight
// contract: a weight that is validated but not applied is the defect, not the
// fix. Measured live on the 298-project self-host 2026-10-11 before this
// change: weight=0.0 and weight=1.0 produced byte-identical rankings and
// retrieval.weight was absent from every answer.
//
// The fixture is built so every claim is OBSERVABLE, and building it took two
// corrections worth recording:
//
//  1. A mirrored list pair (vector: a,b / keyword: b,a) ties and proves
//     nothing — the first version of this test passed even with the
//     explicit-zero handling disabled, which is a test that guards nothing.
//  2. A SHARED key wins on both arms' scores summed, so `shared` correctly beat
//     a key that topped only one arm. That is RRF working, not a weight
//     failing; the expectation was wrong, not the code.
//
// DISJOINT arms make scaling and dropping both visible: the vector arm's keys
// must outrank the keyword arm's under a vector-heavy weight and sink under a
// keyword-heavy one.
func TestFusionWeightChangesTheRanking(t *testing.T) {
	lists := []RankList{
		{Name: ArmVector, Keys: []string{"alpha", "beta"}},
		{Name: ArmFTS5, Keys: []string{"kwonly", "kwtwo"}},
	}
	balanced := FuseRRF(lists)
	if len(balanced) != 4 {
		t.Fatalf("balanced fusion returned %d hits, want 4", len(balanced))
	}
	// Vector-heavy: both vector keys must lead.
	vectorHeavy := FuseRRFWeighted(lists, FuseRRFWeights{Vector: 3, Keyword: 1, Explicit: true})
	if got := keysOf(vectorHeavy); got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("vector-heavy order = %v, want the vector arm's keys first", got)
	}
	// Keyword-heavy: both keyword keys must lead.
	keywordHeavy := FuseRRFWeighted(lists, FuseRRFWeights{Vector: 1, Keyword: 3, Explicit: true})
	if got := keysOf(keywordHeavy); got[0] != "kwonly" || got[1] != "kwtwo" {
		t.Errorf("keyword-heavy order = %v, want the keyword arm's keys first", got)
	}
	// The documented "1,0" DROPS the keyword arm: its keys score 0 and sink
	// below every vector key, which is only visible because the arms are
	// disjoint.
	vectorOnly := keysOf(FuseRRFWeighted(lists, FuseRRFWeights{Vector: 1, Keyword: 0, Explicit: true}))
	if vectorOnly[0] != "alpha" || vectorOnly[1] != "beta" {
		t.Errorf("weight 1,0 order = %v, want the vector arm's keys first", vectorOnly)
	}
	if vectorOnly[2] != "kwonly" || vectorOnly[3] != "kwtwo" {
		t.Errorf("weight 1,0 did not sink the keyword arm: %v", vectorOnly)
	}
	// And the mirror: "0,1" drops the vector arm.
	keywordOnly := keysOf(FuseRRFWeighted(lists, FuseRRFWeights{Vector: 0, Keyword: 1, Explicit: true}))
	if keywordOnly[0] != "kwonly" || keywordOnly[1] != "kwtwo" {
		t.Errorf("weight 0,1 order = %v, want the keyword arm's keys first", keywordOnly)
	}
	if keywordOnly[2] != "alpha" || keywordOnly[3] != "beta" {
		t.Errorf("weight 0,1 did not sink the vector arm: %v", keywordOnly)
	}
	// A zero struct is NOT explicit: every arm stays at 1, which is the
	// unweighted default every existing caller gets.
	zero := keysOf(FuseRRFWeighted(lists, FuseRRFWeights{}))
	if len(zero) != len(balanced) {
		t.Fatalf("zero weight changed the count: %v vs %v", zero, keysOf(balanced))
	}
	for i := range zero {
		if zero[i] != balanced[i].Key {
			t.Fatalf("the zero weight changed the ranking: %v vs %v", zero, keysOf(balanced))
		}
	}
}

func keysOf(hits []FusedHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Key)
	}
	return out
}
