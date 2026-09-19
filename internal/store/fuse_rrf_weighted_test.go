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
