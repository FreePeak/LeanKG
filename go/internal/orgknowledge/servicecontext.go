// Service context: the Rust get_service_context MCP tool and
// /api/v2/service/context read (GraphEngine::get_service_context,
// src/graph/query.rs), which aggregates one service's graph neighborhood,
// operational profile and incident history for one environment.
package orgknowledge

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// ServiceContext is the aggregate (Rust graph::query::ServiceContext). JSON
// field names match the Rust serde output, which is what /api/v2/service/
// context returned verbatim.
type ServiceContext struct {
	Service         string   `json:"service"`
	Env             string   `json:"env"`
	Version         *string  `json:"version"`
	Team            *string  `json:"team"`
	OnCall          *string  `json:"on_call"`
	RepoURL         *string  `json:"repo_url"`
	Language        *string  `json:"language"`
	Calls           []string `json:"calls"`
	CalledBy        []string `json:"called_by"`
	Schemas         []string `json:"schemas"`
	OpenIncidents   int64    `json:"open_incidents"`
	RecentIncidents []string `json:"recent_incidents"`
	LastIncident    *string  `json:"last_incident"`
	KnownRisks      []string `json:"known_risks"`
}

// schemaTypeTerms are the element_type substrings the Rust read matched:
// (schema|protobuf|proto|openapi|json_schema|avro|sql_table|event|topic|config).
// Every alternative is a literal, so a substring test is equivalent to the
// Rust regex.
var schemaTypeTerms = []string{"schema", "protobuf", "proto", "openapi", "json_schema", "avro", "sql_table", "event", "topic", "config"}

// ServiceContext aggregates one (service, env) view.
//
//   - version/team/on_call/repo_url/language: from the element metadata and
//     the service_metadata profile (the Rust read preferred the element's
//     metadata.version and took the other four from service_metadata).
//   - calls/called_by: graph edges labeled "calls" or "service_calls" in
//     either direction.
//   - schemas: schema-shaped elements under ./<service>/ (Rust prefix).
//   - open_incidents: unresolved incidents (resolved_at NULL) for the service
//     in env, exact count.
//   - recent_incidents: the 3 newest incident titles.
//   - last_incident: "<occurred_at>: <title>" of the newest incident.
//   - known_risks: up to 5 "<prevention> (root: <root_cause>)" strings.
func (k *Knowledge) ServiceContext(service, env string) (ServiceContext, error) {
	if strings.TrimSpace(service) == "" {
		return ServiceContext{}, fmt.Errorf("service is required")
	}
	if env == "" {
		env = "local"
	}
	ctx := ServiceContext{Service: service, Env: env}

	// Element presence and metadata.version (Rust: code_elements row for
	// (qualified_name = service, env); Go: the live element plus, if one
	// exists, the env snapshot's metadata).
	if els, err := k.st.FindExact(service); err != nil {
		return ServiceContext{}, err
	} else if len(els) > 0 {
		ctx.Version = versionFromMetadata(els[0].Metadata)
	}
	if snap, ok, err := k.st.EnvSnapshotGet(env, service); err != nil {
		return ServiceContext{}, err
	} else if ok {
		ctx.Version = versionFromMetadata(snap.Metadata)
	}

	// Operational profile (Rust: get_service_metadata_fields).
	if m, ok, err := k.st.ServiceMetadataGet(service, env); err != nil {
		return ServiceContext{}, err
	} else if ok {
		ctx.Team = m.Team
		ctx.OnCall = m.OnCall
		ctx.RepoURL = m.RepoURL
		if m.Language != nil {
			ctx.Language = m.Language
		}
	}

	// Graph neighborhood: calls / service_calls edges in either direction.
	if rels, err := k.st.Outgoing(service); err != nil {
		return ServiceContext{}, err
	} else {
		ctx.Calls = []string{}
		for _, r := range rels {
			if r.RelType == "calls" || r.RelType == "service_calls" {
				ctx.Calls = append(ctx.Calls, r.Target)
			}
		}
	}
	if rels, err := k.st.Incoming(service); err != nil {
		return ServiceContext{}, err
	} else {
		ctx.CalledBy = []string{}
		for _, r := range rels {
			if r.RelType == "calls" || r.RelType == "service_calls" {
				ctx.CalledBy = append(ctx.CalledBy, r.Source)
			}
		}
	}

	// Schema-shaped elements under ./<service>/ (the Rust starts_with prefix;
	// no trailing slash, so "./api" also matches "./api-gateway" exactly as
	// the Rust predicate did).
	ctx.Schemas = []string{}
	if els, err := k.st.Elements(); err != nil {
		return ServiceContext{}, err
	} else {
		prefix := "./" + service
		for _, e := range els {
			if strings.HasPrefix(e.FilePath, prefix) &&
				matchesAnyTerm(e.ElementType, schemaTypeTerms) {
				ctx.Schemas = append(ctx.Schemas, e.Name)
			}
		}
	}

	// Incident history: every incident for the service in env, unbounded so
	// the open count is exact.
	incidents, err := k.st.IncidentsQuery(store.IncidentQuery{Service: service, Env: env, Limit: 0})
	if err != nil {
		return ServiceContext{}, err
	}
	for _, inc := range incidents {
		if inc.ResolvedAt == nil {
			ctx.OpenIncidents++
		}
	}
	if len(incidents) > 0 {
		// Newest first is the store's order; take(3) titles, and the first
		// row's occurred_at for last_incident.
		n := 3
		if len(incidents) < n {
			n = len(incidents)
		}
		for _, inc := range incidents[:n] {
			ctx.RecentIncidents = append(ctx.RecentIncidents, inc.Title)
		}
		last := fmt.Sprintf("%d: %s", incidents[0].OccurredAt, incidents[0].Title)
		ctx.LastIncident = &last
		for _, inc := range incidents {
			if inc.Prevention == nil || *inc.Prevention == "" {
				continue
			}
			if len(ctx.KnownRisks) == 5 {
				break
			}
			ctx.KnownRisks = append(ctx.KnownRisks, fmt.Sprintf("%s (root: %s)", *inc.Prevention, inc.RootCause))
		}
	}
	return ctx, nil
}

func versionFromMetadata(meta map[string]any) *string {
	if meta == nil {
		return nil
	}
	if v, ok := meta["version"].(string); ok && v != "" {
		return &v
	}
	return nil
}

// matchesAnyTerm is the substring variant of the Rust element-type regexps:
// the two patterns used alternation over plain terms with no anchors or
// metacharacters, so a case-sensitive substring test is equivalent.
func matchesAnyTerm(s string, terms []string) bool {
	for _, t := range terms {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

// ServiceContextJSON renders the aggregate as the Rust MCP tool's JSON object
// (same fields, same nulls) for callers that return map[string]any rather
// than the typed struct.
func (k *Knowledge) ServiceContextJSON(service, env string) (map[string]any, error) {
	ctx, err := k.ServiceContext(service, env)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(ctx)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
