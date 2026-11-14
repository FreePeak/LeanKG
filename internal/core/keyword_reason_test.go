package core

import (
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// The L2 reason string is part of the query response contract. Each keyword
// arm must keep the wording the engines reported before FindFuzzy returned
// the arm itself (golden per engine: FTS5 for SQLite, tsvector/trigram/ILIKE
// for PostgreSQL).
func TestKeywordReasonPerArm(t *testing.T) {
	cases := map[store.Arm]string{
		store.ArmFTS5:     "FTS5 keyword match",
		store.ArmTSVector: "tsvector keyword match",
		store.ArmTrigram:  "trigram keyword match (tsvector degraded)",
		store.ArmILIKE:    "substring keyword match (tsvector+trigram degraded)",
	}
	for arm, want := range cases {
		if got := keywordReason(arm); got != want {
			t.Errorf("keywordReason(%q) = %q; want %q", arm, got, want)
		}
	}
}
