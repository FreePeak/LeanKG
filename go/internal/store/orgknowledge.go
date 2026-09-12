// Org/ops knowledge entities (Rust src/db/models.rs parity): incidents,
// knowledge entries (team notes / annotations / PRD rows), service metadata
// and environment snapshots.
//
// Field names and JSON tags mirror the Rust serde layout so the REST/MCP
// payloads keep the same shape (notably the optional strings, which stay
// JSON null instead of disappearing).
//
// CEILING: the Rust engine kept one code_elements row per (qualified_name,
// env); the Go engine keys code_elements on qualified_name alone and indexes
// a single environment, so the env-scoped rows that find_env_conflicts
// compares are carried here as EnvSnapshot records instead. See
// schema.go migration 010 for the full note.
package store

import (
	"encoding/json"
	"strings"
)

// Incident is one ops incident record (Rust db::models::Incident).
type Incident struct {
	ID               string   `json:"id"`
	Env              string   `json:"env"`
	Title            string   `json:"title"`
	Severity         string   `json:"severity"`
	OccurredAt       int64    `json:"occurred_at"`
	ResolvedAt       *int64   `json:"resolved_at"`
	RootCause        string   `json:"root_cause"`
	Resolution       string   `json:"resolution"`
	AffectedServices []string `json:"affected_services"`
	TriggerPattern   *string  `json:"trigger_pattern"`
	Prevention       *string  `json:"prevention"`
	Tags             []string `json:"tags"`
	Author           string   `json:"author"`
	LinkedTicket     *string  `json:"linked_ticket"`
}

// IncidentQuery filters IncidentsQuery. The zero value of each string means
// "no filter"; Limit <= 0 falls back to 50. Service and Pattern are
// case-insensitive substring matches, the SQL equivalent of the Rust
// regex .*needle.* semantics (Service over the affected-services list,
// Pattern over title or root_cause).
type IncidentQuery struct {
	Service string
	Pattern string
	Env     string
	Limit   int
}

// KnowledgeEntry is one knowledge row (Rust db::models::KnowledgeEntry): team
// notes (tags "note"), risky-pattern annotations (tags "pattern,risk"), PRD
// mappings and any other annotation surfaced by the knowledge tools.
type KnowledgeEntry struct {
	ID               string  `json:"id"`
	KnowledgeType    string  `json:"knowledge_type"`
	Title            string  `json:"title"`
	Content          string  `json:"content"`
	ElementQualified *string `json:"element_qualified"`
	UserStoryID      *string `json:"user_story_id"`
	FeatureID        *string `json:"feature_id"`
	Tags             string  `json:"tags"`
	Environment      string  `json:"environment"`
	Branch           *string `json:"branch"`
	Author           string  `json:"author"`
	CreatedAt        int64   `json:"created_at"`
	UpdatedAt        int64   `json:"updated_at"`
}

// ServiceMetadata is one service's operational profile for an environment
// (Rust db::models::ServiceMetadata); the service-context read takes team,
// on-call, repo URL and language from it.
type ServiceMetadata struct {
	ServiceName    string  `json:"service_name"`
	Env            string  `json:"env"`
	Team           *string `json:"team"`
	OnCall         *string `json:"on_call"`
	RepoURL        *string `json:"repo_url"`
	Language       *string `json:"language"`
	HealthEndpoint *string `json:"health_endpoint"`
	SLOP99Ms       *int64  `json:"slo_p99_ms"`
	IncidentCount  int64   `json:"incident_count"`
	LastIncident   *int64  `json:"last_incident"`
	Tags           string  `json:"tags"`
	Version        *string `json:"version"`
	DeployEnvs     string  `json:"deploy_envs"`
	CreatedAt      int64   `json:"created_at"`
	UpdatedAt      int64   `json:"updated_at"`
}

// EnvSnapshot is one environment-scoped element record: the Go stand-in for a
// Rust code_elements row carrying an env (see the package comment).
type EnvSnapshot struct {
	Env           string         `json:"env"`
	QualifiedName string         `json:"qualified_name"`
	ElementType   string         `json:"element_type"`
	Name          string         `json:"name"`
	FilePath      string         `json:"file_path"`
	Metadata      map[string]any `json:"metadata"`
	CapturedAt    int64          `json:"captured_at"`
}

// EnvVariant is one qualified name that exists in more than one environment,
// together with the environments it was seen in (the Rust
// show_env_conflicts cross-environment grouping).
type EnvVariant struct {
	QualifiedName string   `json:"qualified_name"`
	Envs          []string `json:"envs"`
}

const (
	incidentCols = `id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, ` +
		`affected_services, trigger_pattern, prevention, tags, author, linked_ticket`

	knowledgeCols = `id, knowledge_type, title, content, element_qualified, user_story_id, ` +
		`feature_id, tags, environment, branch, author, created_at, updated_at`

	serviceMetaCols = `service_name, env, team, on_call, repo_url, language, health_endpoint, ` +
		`slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at`

	envSnapshotCols = `env, qualified_name, element_type, name, file_path, metadata, captured_at`
)

// whereBuilder accumulates bound arguments and renders placeholders through a
// backend-supplied function, so one filter SQL text works for SQLite (?) and
// PostgreSQL ($n).
type whereBuilder struct {
	ph   func(n int) string
	args []any
}

// bind appends v and returns its placeholder.
func (w *whereBuilder) bind(v any) string {
	w.args = append(w.args, v)
	return w.ph(len(w.args))
}

// incidentWhere renders the Rust query_incidents filters.
func incidentWhere(q IncidentQuery, b *whereBuilder) string {
	var conds []string
	if q.Env != "" {
		conds = append(conds, "env = "+b.bind(q.Env))
	}
	if q.Service != "" {
		conds = append(conds, "LOWER(affected_services) LIKE "+b.bind(likeContains(q.Service))+" ESCAPE '\\'")
	}
	if q.Pattern != "" {
		// Two placeholders, two bound arguments: SQLite has no positional
		// reuse, and PostgreSQL is happy with the same value bound twice.
		t, r := b.bind(likeContains(q.Pattern)), b.bind(likeContains(q.Pattern))
		conds = append(conds, "(LOWER(title) LIKE "+t+" ESCAPE '\\' OR LOWER(root_cause) LIKE "+r+" ESCAPE '\\')")
	}
	if len(conds) == 0 {
		return "1 = 1"
	}
	return strings.Join(conds, " AND ")
}

// searchWhere renders the Rust search_knowledge_entries filters (substring
// over title or content, then optional exact type/environment).
func searchWhere(query, knowledgeType, environment string, b *whereBuilder) string {
	t, c := b.bind(likeContains(query)), b.bind(likeContains(query))
	conds := []string{"(LOWER(title) LIKE " + t + " ESCAPE '\\' OR LOWER(content) LIKE " + c + " ESCAPE '\\')"}
	if knowledgeType != "" {
		conds = append(conds, "knowledge_type = "+b.bind(knowledgeType))
	}
	if environment != "" {
		conds = append(conds, "environment = "+b.bind(environment))
	}
	return strings.Join(conds, " AND ")
}

// likeContains renders the SQL equivalent of the Rust .*needle.* regex: a
// wildcard-wrapped, lowercased literal substring with LIKE metacharacters
// escaped (callers emit the matching ESCAPE clause).
func likeContains(s string) string {
	var sb strings.Builder
	sb.WriteByte('%')
	for _, r := range strings.ToLower(s) {
		switch r {
		case '%', '_', '\\':
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	sb.WriteByte('%')
	return sb.String()
}

// limitSQL renders an optional LIMIT clause. limit <= 0 means "unbounded"
// rather than a hidden default: the service-context aggregate needs an exact
// open-incident count (the Rust read had no :limit there), so the surfaces
// pass their own default (CLI 10, MCP 5, REST 10) instead of the store
// guessing. The Rust sqlite clamp of 1..100 is deliberately dropped: the
// PostgreSQL path took the value as given, and the clamp silently rewrote
// limit 0 to 1.
func limitSQL(limit int, b *whereBuilder) string {
	if limit <= 0 {
		return ""
	}
	return " LIMIT " + b.bind(limit)
}

// encodeStringList renders a JSON array column; nil becomes "[]" (the Rust
// Cozo blobs held the same encoding).
func encodeStringList(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// decodeStringList parses a JSON array column; malformed input is an empty
// list, matching the Rust serde_json::from_str(...).unwrap_or_default().
func decodeStringList(raw string) []string {
	if raw == "" || raw == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// scanEnvVariants folds (qualified_name, env) rows — ordered by qualified
// name — into the variants that appear in more than one environment. It
// takes the shared row interface so SQLite and pgx rows use one path.
func scanEnvVariants(rows versionRows) ([]EnvVariant, error) {
	var grouped []EnvVariant
	cur := -1
	for rows.Next() {
		var qn, env string
		if err := rows.Scan(&qn, &env); err != nil {
			return nil, err
		}
		if cur < 0 || grouped[cur].QualifiedName != qn {
			grouped = append(grouped, EnvVariant{QualifiedName: qn, Envs: []string{env}})
			cur = len(grouped) - 1
			continue
		}
		if envs := grouped[cur].Envs; envs[len(envs)-1] != env {
			grouped[cur].Envs = append(envs, env)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]EnvVariant, 0, len(grouped))
	for _, v := range grouped {
		if len(v.Envs) > 1 {
			out = append(out, v)
		}
	}
	return out, nil
}
