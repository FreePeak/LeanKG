package maintain

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestPassReportsGrowthHonestly pins that a pass which makes the store BIGGER
// says so, instead of reporting a negative amount as "reclaimed".
//
// The one-time auto_vacuum header upgrade rewrites the store, and on a small
// store the rewrite lands 4096 bytes LARGER (the header change plus the
// incremental-vacuum bookkeeping). The signed delta Before-After is therefore
// negative on that first pass — and the CLI printed it verbatim:
//
//	$ leankg vacuum --project .
//	vacuum: sqlite reclaimed -4096 bytes (331776 -> 335872, freelist 0 -> 0, wal 0)
//
// "reclaimed -4096 bytes" reads as a bug in the tool, and it hides the fact
// that the pass did exactly what it was asked to do: switch the store into a
// mode where it can return space at all. The internal log line already
// suppressed the negative (it is guarded by `> 0`); the CLI verb did not.
//
// The fix belongs in the shared place: Pass reports what happened, and the
// verb prints that.
func TestPassReportsGrowthHonestly(t *testing.T) {
	st := bloatFixture(t)
	res, err := Pass(st, Options{})
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if res.Reclaimed < 0 {
		t.Errorf("Reclaimed = %d; a negative amount must not be reported as reclaimed", res.Reclaimed)
	}
	if res.Grew > 0 {
		// The store grew: the pass must say so, and must not also claim it
		// reclaimed something.
		if res.Reclaimed != 0 {
			t.Errorf("Reclaimed = %d with Grew = %d; a pass cannot both reclaim and grow", res.Reclaimed, res.Grew)
		}
		if res.After <= res.Before {
			t.Errorf("Grew = %d but the store shrank: %d -> %d", res.Grew, res.Before, res.After)
		}
	}
	if res.Reclaimed > 0 && res.After >= res.Before {
		t.Errorf("Reclaimed = %d but the store did not shrink: %d -> %d", res.Reclaimed, res.Before, res.After)
	}
}

// TestPassReclaimSummaryNamesTheOutcome pins the sentence the CLI prints: it
// must name what happened, and never say "reclaimed" about a negative amount.
func TestPassReclaimSummaryNamesTheOutcome(t *testing.T) {
	cases := []struct {
		name       string
		res        Result
		wantSubstr string
		notSubstr  string
	}{
		{
			name:       "shrank",
			res:        Result{Before: 1000, After: 400},
			wantSubstr: "reclaimed 600 bytes",
			notSubstr:  "grew",
		},
		{
			name:       "grew",
			res:        Result{Before: 400, After: 1000},
			wantSubstr: "grew 600 bytes",
			notSubstr:  "reclaimed",
		},
		{
			name:       "unchanged",
			res:        Result{Before: 400, After: 400},
			wantSubstr: "reclaimed 0 bytes",
			notSubstr:  "grew",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.res.ReclaimSummary()
			if !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("ReclaimSummary() = %q, want it to contain %q", got, tc.wantSubstr)
			}
			if tc.notSubstr != "" && strings.Contains(got, tc.notSubstr) {
				t.Errorf("ReclaimSummary() = %q, must not contain %q", got, tc.notSubstr)
			}
		})
	}
}

// TestPassOnAFreshStoreGrows is the reproduction: a store that has never been
// auto-vacuumed is upgraded on the first pass, and that upgrade can make the
// file bigger. The pass must not report a negative reclaim.
func TestPassOnAFreshStoreGrows(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	res, err := Pass(st, Options{})
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if res.Reclaimed < 0 {
		t.Errorf("Reclaimed = %d on a first pass over a fresh store; the header upgrade grows the file, and that must be reported as growth", res.Reclaimed)
	}
	if res.Grew > 0 && !strings.Contains(res.ReclaimSummary(), "grew") {
		t.Errorf("ReclaimSummary() = %q, want it to name the growth", res.ReclaimSummary())
	}
}
