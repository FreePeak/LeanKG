// Incidents: the Rust `leankg incident add|list|show` verbs, the
// query_incidents MCP tool, /api/v2/incidents and db::validate_incident.
package orgknowledge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// ValidateIncident is the port of db::validate_incident. Message text matches
// the Rust errors so scripts and docs that quote them keep working.
func ValidateIncident(inc store.Incident) error {
	if strings.TrimSpace(inc.Title) == "" {
		return errors.New("Incident title is required")
	}
	switch inc.Severity {
	case "P0", "P1", "P2", "P3":
	default:
		return fmt.Errorf("Invalid severity '%s': must be P0, P1, P2, or P3", inc.Severity)
	}
	if len(inc.AffectedServices) == 0 {
		return errors.New("At least one affected service is required")
	}
	if strings.TrimSpace(inc.RootCause) == "" {
		return errors.New("Root cause is required")
	}
	if strings.TrimSpace(inc.Resolution) == "" {
		return errors.New("Resolution is required")
	}
	if inc.OccurredAt <= 0 {
		return errors.New("occurred_at timestamp is required")
	}
	if strings.TrimSpace(inc.Author) == "" {
		return errors.New("Author is required")
	}
	if inc.LinkedTicket != nil {
		if strings.TrimSpace(*inc.LinkedTicket) == "" {
			return errors.New("linked_ticket must not be empty if provided")
		}
		if !strings.Contains(*inc.LinkedTicket, "-") && !strings.HasPrefix(*inc.LinkedTicket, "#") {
			return errors.New("linked_ticket should include a project prefix (e.g., TICKET-123)")
		}
	}
	if inc.ResolvedAt != nil {
		if *inc.ResolvedAt <= 0 {
			return errors.New("resolved_at must be a positive timestamp")
		}
		if *inc.ResolvedAt < inc.OccurredAt {
			return errors.New("resolved_at must be >= occurred_at")
		}
	}
	return nil
}

// SplitServices parses the CLI's comma-separated --affected value. Empty
// entries are dropped: the Rust .split(',') kept them, which let "--affected
// ”" pass validation as a single blank service name.
func SplitServices(affected string) []string {
	var out []string
	for _, s := range strings.Split(affected, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// CreateIncident validates and stores an incident, assigning an INC-<uuid> id
// when the caller left it empty (Rust CLI did the same before insert).
func (k *Knowledge) CreateIncident(inc store.Incident) (store.Incident, error) {
	if inc.ID == "" {
		inc.ID = NewID("INC-")
	}
	if inc.Env == "" {
		inc.Env = "local"
	}
	if err := ValidateIncident(inc); err != nil {
		return store.Incident{}, err
	}
	if err := k.st.IncidentUpsert(inc); err != nil {
		return store.Incident{}, err
	}
	return inc, nil
}

// GetIncident resolves one incident by id (Rust db::get_incident / `incident
// show`).
func (k *Knowledge) GetIncident(id string) (store.Incident, bool, error) {
	return k.st.IncidentByID(id)
}

// UpdateIncident re-validates and replaces an incident (Rust update_incident
// was a validated upsert).
func (k *Knowledge) UpdateIncident(inc store.Incident) (store.Incident, error) {
	if inc.ID == "" {
		return store.Incident{}, errors.New("incident id is required")
	}
	return k.CreateIncident(inc)
}

// DeleteIncident removes an incident (Rust db::delete_incident).
func (k *Knowledge) DeleteIncident(id string) error { return k.st.IncidentDelete(id) }

// QueryIncidents lists incidents newest first with the Rust
// db::query_incidents filters: service is a case-insensitive substring of the
// affected-services list, pattern of title or root_cause, env an exact match
// ("" = every environment). limit <= 0 returns every match — the surfaces pass
// their own default (CLI 10, MCP 5, REST 10).
func (k *Knowledge) QueryIncidents(service, pattern, env string, limit int) ([]store.Incident, error) {
	return k.st.IncidentsQuery(store.IncidentQuery{Service: service, Pattern: pattern, Env: env, Limit: limit})
}

// IncidentsForService is the Rust query_incidents_for_service shortcut
// (service + env, no pattern).
func (k *Knowledge) IncidentsForService(service, env string, limit int) ([]store.Incident, error) {
	return k.st.IncidentsQuery(store.IncidentQuery{Service: service, Env: env, Limit: limit})
}
