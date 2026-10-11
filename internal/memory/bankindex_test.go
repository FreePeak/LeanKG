package memory

import (
	"testing"
	"time"
)

// TestRecallTieBreaksRecency is K7 "recall ranking": two rows with identical
// content carry identical bm25, and the old one ranked FIRST, because the only
// tie-break was bank order. A memory injected into this turn's prompt should
// prefer the newer row when relevance is equal — a stale row outranking a fresh
// one is the recall defect that makes agents repeat superseded advice.
func TestRecallTieBreaksRecency(t *testing.T) {
	m := openTest(t)
	oldTS := time.Now().Add(-90 * 24 * time.Hour).Unix()
	newTS := time.Now().Unix()
	if err := m.RetainRaw("probe", []Entry{{ID: "old-1", Content: "alpha probe row", Timestamp: oldTS}}); err != nil {
		t.Fatal(err)
	}
	if err := m.RetainRaw("probe", []Entry{{ID: "new-1", Content: "alpha probe row", Timestamp: newTS}}); err != nil {
		t.Fatal(err)
	}
	rows, err := m.RecallBanks([]string{"probe"}, "alpha probe", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].ID != "new-1" {
		t.Fatalf("ranked order = [%s %s]; equal-relevance rows must prefer the newer one, so new-1 first",
			rows[0].ID, rows[1].ID)
	}
}

// TestRecallFusionTieBreaksRecency pins the same rule on the fused path: two
// rows at the SAME rank in both arms get the same RRF score, and the newer one
// must come first. A stale row that outranks a fresh one just because the two
// arms agreed on it equally is the recall defect this closes.
func TestRecallFusionTieBreaksRecency(t *testing.T) {
	older := Entry{ID: "old-f", Content: "fused probe", Timestamp: time.Now().Add(-48 * time.Hour).Unix()}
	newer := Entry{ID: "new-f", Content: "fused probe", Timestamp: time.Now().Unix()}
	// Each arm ranks the two rows in a different order, so each row takes one
	// rank-0 slot and one rank-1 slot and the fusion scores come out equal —
	// indistinguishable to RRF except by age, which is what the tie-break for.
	got := fuseRRF([]Entry{older, newer}, []Entry{newer, older})
	if len(got) != 2 {
		t.Fatalf("fused rows = %d, want 2", len(got))
	}
	if got[0].Score != got[1].Score {
		t.Fatalf("scores differ (%.6f vs %.6f): this is a relevance difference, not a tie", got[0].Score, got[1].Score)
	}
	if got[0].ID != "new-f" {
		t.Fatalf("fused order = [%s %s]; equal fusion scores must prefer the newer row", got[0].ID, got[1].ID)
	}
}
