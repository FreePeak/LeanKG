package orgknowledge

import (
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// TestTeamMap mirrors the Rust get_team_map aggregation: group the
// environment's services by team, keep the first non-empty rotation, sort by
// team, and place team-less services under "(unassigned)".
func TestTeamMap(t *testing.T) {
	k, st := openKnowledge(t)
	seed := []store.ServiceMetadata{
		{ServiceName: "api", Env: "production", Team: strPtr("payments"), OnCall: strPtr("payments-oncall"), CreatedAt: 1, UpdatedAt: 1},
		{ServiceName: "billing", Env: "production", Team: strPtr("payments"), OnCall: nil, CreatedAt: 1, UpdatedAt: 1},
		{ServiceName: "ledger", Env: "production", Team: strPtr("payments"), OnCall: strPtr("ledger-oncall"), CreatedAt: 1, UpdatedAt: 1},
		{ServiceName: "search", Env: "production", Team: strPtr("discovery"), OnCall: nil, CreatedAt: 1, UpdatedAt: 1},
		{ServiceName: "legacy", Env: "production", Team: nil, OnCall: nil, CreatedAt: 1, UpdatedAt: 1},
		// Other environment must not leak in.
		{ServiceName: "api-staging", Env: "staging", Team: strPtr("staging-team"), CreatedAt: 1, UpdatedAt: 1},
	}
	for _, m := range seed {
		if err := st.ServiceMetadataUpsert(m); err != nil {
			t.Fatalf("seed %s: %v", m.ServiceName, err)
		}
	}

	got, err := k.TeamMap("production")
	if err != nil {
		t.Fatalf("TeamMap: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("teams = %+v, want 3", got)
	}
	// Sorted by team name, byte order: "(" sorts before letters, so the
	// placeholder team leads — the same order the Rust sort_by produced.
	if got[0].Team != unassignedTeam || got[1].Team != "discovery" || got[2].Team != "payments" {
		t.Fatalf("team order = %q,%q,%q; want %s,discovery,payments", got[0].Team, got[1].Team, got[2].Team, unassignedTeam)
	}
	if got[1].OnCall != noOnCall {
		t.Fatalf("discovery on_call = %q, want %q", got[1].OnCall, noOnCall)
	}
	payments := got[2]
	if payments.OnCall != "payments-oncall" {
		t.Fatalf("payments on_call = %q, want the first non-empty rotation", payments.OnCall)
	}
	if len(payments.Services) != 3 || payments.Services[0] != "api" || payments.Services[1] != "billing" || payments.Services[2] != "ledger" {
		t.Fatalf("payments services = %v, want [api billing ledger]", payments.Services)
	}
	if len(got[0].Services) != 1 || got[0].Services[0] != "legacy" {
		t.Fatalf("unassigned services = %v", got[0].Services)
	}

	if empty, err := k.TeamMap("nowhere"); err != nil || len(empty) != 0 {
		t.Fatalf("TeamMap(nowhere) = %+v, err %v", empty, err)
	}
}
