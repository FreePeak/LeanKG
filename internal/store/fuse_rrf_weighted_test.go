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

// TestDefaultFusionRanksVectorAboveLexicalOnlyRows pins the default weight
// (vector:keyword = 3:1, applied when the caller names no weight). Under plain
// 1:1 RRF, the keyword-only row x (rank 1 of its arm, 1/61) beats vector rank 3
// (1/63); under the default, vector rank 3 scores 3/63 and wins. Lexical-only
// rows are down-weighted, never dropped.
func TestDefaultFusionRanksVectorAboveLexicalOnlyRows(t *testing.T) {
	vector := RankList{Name: ArmVector, Keys: []string{"v1", "v2", "v3", "v4", "v5"}}
	lexical := RankList{Name: ArmFTS5, Keys: []string{"x", "y"}}
	got := FuseRRFWeighted([]RankList{vector, lexical}, DefaultFuseWeights())
	pos := positionOf(got)
	for _, k := range []string{"x", "y"} {
		if _, ok := pos[k]; !ok {
			t.Fatalf("lexical-only row %q was dropped: %v", k, keysOf(got))
		}
	}
	if pos["v3"] > pos["x"] {
		t.Errorf("default fusion order = %v: vector rank 3 (v3) must rank ahead of lexical-only x", keysOf(got))
	}
	// A row both arms rank first still leads.
	both := FuseRRFWeighted([]RankList{vector, {Name: ArmFTS5, Keys: []string{"v1", "x", "y"}}}, DefaultFuseWeights())
	if keysOf(both)[0] != "v1" {
		t.Errorf("row in both arms should lead: %v", keysOf(both))
	}
}

// TestExplicitEqualWeightsKeepPlainRRFOrder pins the other side of the default:
// a caller who names "1,1" gets exactly the plain-RRF order and scores, so the
// new default does not leak into explicit weights.
func TestExplicitEqualWeightsKeepPlainRRFOrder(t *testing.T) {
	lists := []RankList{
		{Name: ArmVector, Keys: []string{"v1", "v2", "v3", "v4", "v5"}},
		{Name: ArmFTS5, Keys: []string{"v1", "x", "y"}},
	}
	plain := FuseRRF(lists)
	got := FuseRRFWeighted(lists, FuseRRFWeights{Vector: 1, Keyword: 1, Explicit: true})
	if len(got) != len(plain) {
		t.Fatalf("len = %d, want %d", len(got), len(plain))
	}
	for i := range plain {
		if got[i].Key != plain[i].Key {
			t.Fatalf("rank %d = %q, want %q (explicit 1,1 must equal plain RRF)", i+1, got[i].Key, plain[i].Key)
		}
		if d := math.Abs(got[i].Score - plain[i].Score); d > 1e-9 {
			t.Fatalf("%s score = %v, want %v", got[i].Key, got[i].Score, plain[i].Score)
		}
	}
}

// TestSQLiteDefaultEntryAppliesDefaultWeights is the SQLite caller's half of
// the one-home contract: the plain HybridSearch, with no weight, must score its
// vector arm at DefaultFuseWeights().Vector. A single vector hit at rank 1 with
// no lexical match scores weight/(RRFK+1): 3/61 under the default, 1/61 under
// plain RRF. The PostgreSQL caller reads the same function (its half is in the
// PG-gated TestPGHybridSearchFusesThreeArms, which needs the pgvector fixture).
func TestSQLiteDefaultEntryAppliesDefaultWeights(t *testing.T) {
	st := openTestStore(t)
	if err := upsertVectorsFor(t, st, "m", Element{QualifiedName: "pkg/a.go::Alpha", Name: "Alpha", FilePath: "pkg/a.go"}); err != nil {
		t.Fatal(err)
	}
	hits, method, err := st.HybridSearch("m", "zzqnomatch", []float32{1, 0, 0, 0}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if method != "rrf("+ArmVector+")" {
		t.Fatalf("method = %q, want the vector arm alone", method)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	want := DefaultFuseWeights().Vector / float64(RRFK+1)
	if d := math.Abs(hits[0].Score - want); d > 1e-9 {
		t.Fatalf("vector-only score = %v, want %v (default vector weight %v)", hits[0].Score, want, DefaultFuseWeights().Vector)
	}
}

// TestDefaultFuseWeightsIsOneSharedValue pins the single named default that
// both engines read (T3). Vector 3, keyword 1, not explicit: an unset arm stays
// at 1, so the name and trigram arms keep their weight of 1.
func TestDefaultFuseWeightsIsOneSharedValue(t *testing.T) {
	want := FuseRRFWeights{Vector: 3, Keyword: 1}
	if got := DefaultFuseWeights(); got != want {
		t.Fatalf("DefaultFuseWeights() = %+v, want %+v", got, want)
	}
	for _, arm := range []string{ArmTSVector, ArmTrigram, ArmILIKE, ArmName} {
		if w := DefaultFuseWeights().armWeight(arm); w != 1 {
			t.Errorf("arm %s weight = %v, want 1 (lexical arms stay at 1)", arm, w)
		}
	}
}

func positionOf(hits []FusedHit) map[string]int {
	out := make(map[string]int, len(hits))
	for i, h := range hits {
		out[h.Key] = i
	}
	return out
}

// TestExplicitKeywordWeightReachesEveryPGLexicalArm pins that an explicit
// "<vector>,<keyword>" pair governs the PostgreSQL keyword arms (tsvector,
// trigram, ilike) as well as the SQLite FTS5 arm. Under "1,0" every lexical row
// must stay in the fused list with score 0 and sort after the vector rows.
func TestExplicitKeywordWeightReachesEveryPGLexicalArm(t *testing.T) {
	lists := []RankList{
		{Name: ArmVector, Keys: []string{"v1", "v2"}},
		{Name: ArmTSVector, Keys: []string{"t1"}},
		{Name: ArmTrigram, Keys: []string{"g1"}},
		{Name: ArmILIKE, Keys: []string{"i1"}},
	}
	got := FuseRRFWeighted(lists, FuseRRFWeights{Vector: 1, Keyword: 0, Explicit: true})
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5 (zero-weighted rows stay in the list): %v", len(got), keysOf(got))
	}
	if got[0].Key != "v1" || got[1].Key != "v2" {
		t.Errorf("order = %v, want the vector rows first", keysOf(got))
	}
	for _, h := range got[2:] {
		if h.Score != 0 {
			t.Errorf("lexical row %q score = %v under 1,0, want 0", h.Key, h.Score)
		}
	}
}

// TestExplicitZeroVectorWeightZeroesTheVectorRow is the mirror of the test
// above: "0,1" leaves the keyword rows scoring and the vector-only row at 0.
func TestExplicitZeroVectorWeightZeroesTheVectorRow(t *testing.T) {
	lists := []RankList{
		{Name: ArmVector, Keys: []string{"v1"}},
		{Name: ArmTSVector, Keys: []string{"t1"}},
	}
	got := FuseRRFWeighted(lists, FuseRRFWeights{Vector: 0, Keyword: 1, Explicit: true})
	if len(got) != 2 || got[0].Key != "t1" {
		t.Fatalf("order = %v, want the tsvector row first", keysOf(got))
	}
	if got[1].Key != "v1" || got[1].Score != 0 {
		t.Errorf("vector-only row = %q score %v, want v1 score 0", got[1].Key, got[1].Score)
	}
}
