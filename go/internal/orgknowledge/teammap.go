// Team map: the Rust get_team_map MCP tool (US-V2-12 / FR-V2-12,
// src/graph/query.rs) — one entry per team with its on-call rotation and the
// services it owns, aggregated from the service_metadata rows of one
// environment.
package orgknowledge

import "sort"

// TeamMapEntry is one aggregated team row (Rust
// graph::query::TeamMapEntry).
type TeamMapEntry struct {
	Team     string   `json:"team"`
	OnCall   string   `json:"on_call"`
	Services []string `json:"services"`
}

// unassignedTeam and noOnCall are the Rust placeholder labels for services
// without a team or rotation.
const (
	unassignedTeam = "(unassigned)"
	noOnCall       = "(none)"
)

// TeamMap aggregates the service_metadata rows of one environment per team.
// Services without a team land under "(unassigned)"; the first non-empty
// on-call label of a team becomes its rotation; service names keep the
// store's ascending order (the Rust pushed them in scan order and the
// underlying Datalog read had none, so this is the deterministic choice).
func (k *Knowledge) TeamMap(env string) ([]TeamMapEntry, error) {
	if env == "" {
		env = "local"
	}
	rows, err := k.st.ServiceMetadataAll(env)
	if err != nil {
		return nil, err
	}
	byTeam := map[string]*TeamMapEntry{}
	var order []string
	for _, svc := range rows {
		team, onCall := unassignedTeam, noOnCall
		if svc.Team != nil {
			team = *svc.Team
		}
		if svc.OnCall != nil {
			onCall = *svc.OnCall
		}
		entry, ok := byTeam[team]
		if !ok {
			entry = &TeamMapEntry{Team: team, OnCall: onCall}
			byTeam[team] = entry
			order = append(order, team)
		}
		if entry.OnCall == noOnCall && onCall != noOnCall {
			entry.OnCall = onCall
		}
		entry.Services = append(entry.Services, svc.ServiceName)
	}
	sort.Strings(order)
	out := make([]TeamMapEntry, 0, len(order))
	for _, team := range order {
		out = append(out, *byTeam[team])
	}
	return out, nil
}
