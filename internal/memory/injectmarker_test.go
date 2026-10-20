package memory

import (
	"context"
	"strings"
	"testing"
)

// TestInjectedBlockNamesWhatItHid is the truncation-marker half of FR-SMA-06.
//
// Measured before this test: retain 30 rows, then inject, and the block
// carries 8 with NO indication the other 22 exist — an agent reading the
// block cannot tell it is seeing a prefix rather than the session's memory.
// Worse, FirstTurnMemoriesWithIDs reported `all=8`: a caller explicitly
// asking for the rows behind the block got the capped list, so "how many
// memories exist?" answered 8 when 30 do.
//
// A caller budget must be visible, not inferred: the block names what it
// dropped, and the API reports the true total alongside the injected prefix.
func TestInjectedBlockNamesWhatItHid(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	var turns []string
	for i := 0; i < 30; i++ {
		turns = append(turns, "row content that is long enough to matter to the budget")
	}
	if _, err := m.SessionRetainCtx(context.Background(), ScopeGlobal, dir, "probe", "sess", turns, 30, nil); err != nil {
		t.Fatal(err)
	}
	text, ids, total, err := m.FirstTurnMemoriesWithIDs(context.Background(), ScopeGlobal, dir, "probe", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 8 {
		t.Fatalf("injected %d rows, want the RecallLimit of 8", len(ids))
	}
	if total != 30 {
		t.Fatalf("total = %d, want the true total 30 — a caller asking how many memories exist must not be answered with the block's prefix", total)
	}
	if !strings.Contains(text, "hidden") {
		t.Errorf("the injected block hides 22 rows without naming them:\n%s", text)
	}
	if !strings.Contains(text, "22") {
		t.Errorf("the marker does not carry the count of hidden rows:\n%s", text)
	}
}
