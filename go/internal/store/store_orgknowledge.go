// SQLite org/ops knowledge storage (Rust src/db/mod.rs + src/db/
// sqlite_backend.rs parity): incidents, knowledge entries, service metadata
// and environment snapshots. The PostgreSQL mirror is pg_orgknowledge.go;
// both share the entity types and filter builders in orgknowledge.go.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// incidentScan dest carries one row's raw columns; the two backends differ
// only in how they hand rows over.
type incidentScan struct {
	id, env, title, severity, rootCause, resolution, affected, tags, author string
	triggerPattern, prevention, linkedTicket                                sql.NullString
	occurredAt                                                              int64
	resolvedAt                                                              sql.NullInt64
}

func (r incidentScan) incident() Incident {
	return Incident{
		ID:               r.id,
		Env:              r.env,
		Title:            r.title,
		Severity:         r.severity,
		OccurredAt:       r.occurredAt,
		ResolvedAt:       nullInt64Ptr(r.resolvedAt),
		RootCause:        r.rootCause,
		Resolution:       r.resolution,
		AffectedServices: decodeStringList(r.affected),
		TriggerPattern:   nullStringPtr(r.triggerPattern),
		Prevention:       nullStringPtr(r.prevention),
		Tags:             decodeStringList(r.tags),
		Author:           r.author,
		LinkedTicket:     nullStringPtr(r.linkedTicket),
	}
}

func (r *incidentScan) dest() []any {
	return []any{
		&r.id, &r.env, &r.title, &r.severity, &r.occurredAt, &r.resolvedAt, &r.rootCause,
		&r.resolution, &r.affected, &r.triggerPattern, &r.prevention, &r.tags, &r.author, &r.linkedTicket,
	}
}

// nullString binds an optional text column: nil (or empty) becomes NULL, which
// the readers turn back into nil — the same answer the Rust Option<String>
// round-trip produced.
func nullString(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}

// nullInt64 binds an optional integer column.
func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// knowledgeScan carries one knowledge_entries row's raw columns.
type knowledgeScan struct {
	id, knowledgeType, title, content, tags, environment, author string
	elementQualified, userStoryID, featureID, branch             sql.NullString
	createdAt, updatedAt                                         int64
}

func (r knowledgeScan) entry() KnowledgeEntry {
	return KnowledgeEntry{
		ID:               r.id,
		KnowledgeType:    r.knowledgeType,
		Title:            r.title,
		Content:          r.content,
		ElementQualified: nullStringPtr(r.elementQualified),
		UserStoryID:      nullStringPtr(r.userStoryID),
		FeatureID:        nullStringPtr(r.featureID),
		Tags:             r.tags,
		Environment:      r.environment,
		Branch:           nullStringPtr(r.branch),
		Author:           r.author,
		CreatedAt:        r.createdAt,
		UpdatedAt:        r.updatedAt,
	}
}

func (r *knowledgeScan) dest() []any {
	return []any{
		&r.id, &r.knowledgeType, &r.title, &r.content, &r.elementQualified, &r.userStoryID,
		&r.featureID, &r.tags, &r.environment, &r.branch, &r.author, &r.createdAt, &r.updatedAt,
	}
}

// serviceMetaScan carries one service_metadata row's raw columns.
type serviceMetaScan struct {
	serviceName, env, tags, deployEnvs  string
	team, onCall, repoURL, language     sql.NullString
	healthEndpoint, version             sql.NullString
	sloP99Ms, lastIncident              sql.NullInt64
	incidentCount, createdAt, updatedAt int64
}

func (r serviceMetaScan) meta() ServiceMetadata {
	return ServiceMetadata{
		ServiceName:    r.serviceName,
		Env:            r.env,
		Team:           nullStringPtr(r.team),
		OnCall:         nullStringPtr(r.onCall),
		RepoURL:        nullStringPtr(r.repoURL),
		Language:       nullStringPtr(r.language),
		HealthEndpoint: nullStringPtr(r.healthEndpoint),
		SLOP99Ms:       nullInt64Ptr(r.sloP99Ms),
		IncidentCount:  r.incidentCount,
		LastIncident:   nullInt64Ptr(r.lastIncident),
		Tags:           r.tags,
		Version:        nullStringPtr(r.version),
		DeployEnvs:     r.deployEnvs,
		CreatedAt:      r.createdAt,
		UpdatedAt:      r.updatedAt,
	}
}

func (r *serviceMetaScan) dest() []any {
	return []any{
		&r.serviceName, &r.env, &r.team, &r.onCall, &r.repoURL, &r.language, &r.healthEndpoint,
		&r.sloP99Ms, &r.incidentCount, &r.lastIncident, &r.tags, &r.version, &r.deployEnvs,
		&r.createdAt, &r.updatedAt,
	}
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// IncidentUpsert inserts or replaces an incident keyed by ID (Rust
// create_incident / update_incident share the Cozo :put semantics). Severity
// and timestamp policy live in internal/orgknowledge (ValidateIncident).
func (s *Store) IncidentUpsert(inc Incident) error {
	if inc.ID == "" {
		return fmt.Errorf("store: incident upsert: id is required")
	}
	env := inc.Env
	if env == "" {
		env = "local"
	}
	_, err := s.db.Exec(`INSERT INTO incidents (`+incidentCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			env=excluded.env, title=excluded.title, severity=excluded.severity,
			occurred_at=excluded.occurred_at, resolved_at=excluded.resolved_at,
			root_cause=excluded.root_cause, resolution=excluded.resolution,
			affected_services=excluded.affected_services, trigger_pattern=excluded.trigger_pattern,
			prevention=excluded.prevention, tags=excluded.tags, author=excluded.author,
			linked_ticket=excluded.linked_ticket`,
		inc.ID, env, inc.Title, inc.Severity, inc.OccurredAt, nullInt64(inc.ResolvedAt),
		inc.RootCause, inc.Resolution, encodeStringList(inc.AffectedServices),
		nullString(inc.TriggerPattern), nullString(inc.Prevention), encodeStringList(inc.Tags),
		inc.Author, nullString(inc.LinkedTicket))
	if err != nil {
		return fmt.Errorf("store: incident upsert %s: %w", inc.ID, err)
	}
	return nil
}

// IncidentByID resolves one incident by id.
func (s *Store) IncidentByID(id string) (Incident, bool, error) {
	row := s.db.QueryRow(`SELECT `+incidentCols+` FROM incidents WHERE id = ?`, id)
	var r incidentScan
	if err := row.Scan(r.dest()...); err != nil {
		if isNoRows(err) {
			return Incident{}, false, nil
		}
		return Incident{}, false, fmt.Errorf("store: incident by id: %w", err)
	}
	return r.incident(), true, nil
}

// IncidentDelete removes an incident; an absent id is a no-op (Rust
// delete_incident's :delete semantics).
func (s *Store) IncidentDelete(id string) error {
	if _, err := s.db.Exec(`DELETE FROM incidents WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: incident delete %s: %w", id, err)
	}
	return nil
}

// IncidentsQuery lists incidents newest first. Deterministic ordering is a
// deliberate divergence: the Rust db::query_incidents Datalog body carried no
// :order, so its "first N" was arbitrary, while GraphEngine::query_incidents
// sorted by occurred_at descending. Sorting satisfies both callers.
func (s *Store) IncidentsQuery(q IncidentQuery) ([]Incident, error) {
	b := &whereBuilder{ph: func(int) string { return "?" }}
	where := incidentWhere(q, b)
	sqlText := `SELECT ` + incidentCols + ` FROM incidents WHERE ` + where + ` ORDER BY occurred_at DESC, id ASC` + limitSQL(q.Limit, b)
	rows, err := s.db.Query(sqlText, b.args...)
	if err != nil {
		return nil, fmt.Errorf("store: incidents query: %w", err)
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		var r incidentScan
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, fmt.Errorf("store: incidents query scan: %w", err)
		}
		out = append(out, r.incident())
	}
	return out, rows.Err()
}

// KnowledgeEntryUpsert inserts or replaces a knowledge entry keyed by ID.
func (s *Store) KnowledgeEntryUpsert(e KnowledgeEntry) error {
	if e.ID == "" {
		return fmt.Errorf("store: knowledge entry upsert: id is required")
	}
	env := e.Environment
	if env == "" {
		env = "local"
	}
	kt := e.KnowledgeType
	if kt == "" {
		kt = "general"
	}
	_, err := s.db.Exec(`INSERT INTO knowledge_entries (`+knowledgeCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			knowledge_type=excluded.knowledge_type, title=excluded.title, content=excluded.content,
			element_qualified=excluded.element_qualified, user_story_id=excluded.user_story_id,
			feature_id=excluded.feature_id, tags=excluded.tags, environment=excluded.environment,
			branch=excluded.branch, author=excluded.author, created_at=excluded.created_at,
			updated_at=excluded.updated_at`,
		e.ID, kt, e.Title, e.Content, nullString(e.ElementQualified), nullString(e.UserStoryID),
		nullString(e.FeatureID), e.Tags, env, nullString(e.Branch), e.Author, e.CreatedAt, e.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: knowledge entry upsert %s: %w", e.ID, err)
	}
	return nil
}

// KnowledgeEntryByID resolves one knowledge entry by id.
func (s *Store) KnowledgeEntryByID(id string) (KnowledgeEntry, bool, error) {
	row := s.db.QueryRow(`SELECT `+knowledgeCols+` FROM knowledge_entries WHERE id = ?`, id)
	var r knowledgeScan
	if err := row.Scan(r.dest()...); err != nil {
		if isNoRows(err) {
			return KnowledgeEntry{}, false, nil
		}
		return KnowledgeEntry{}, false, fmt.Errorf("store: knowledge entry by id: %w", err)
	}
	return r.entry(), true, nil
}

// KnowledgeEntryDelete removes a knowledge entry; an absent id is a no-op.
func (s *Store) KnowledgeEntryDelete(id string) error {
	if _, err := s.db.Exec(`DELETE FROM knowledge_entries WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: knowledge entry delete %s: %w", id, err)
	}
	return nil
}

// KnowledgeEntriesByElement lists the entries anchored to one element.
func (s *Store) KnowledgeEntriesByElement(qualifiedName string) ([]KnowledgeEntry, error) {
	return s.knowledgeRows(`SELECT `+knowledgeCols+` FROM knowledge_entries
		WHERE element_qualified = ? ORDER BY updated_at DESC, id ASC`, qualifiedName)
}

// KnowledgeEntriesByFeature lists the entries anchored to one feature.
func (s *Store) KnowledgeEntriesByFeature(featureID string) ([]KnowledgeEntry, error) {
	return s.knowledgeRows(`SELECT `+knowledgeCols+` FROM knowledge_entries
		WHERE feature_id = ? ORDER BY updated_at DESC, id ASC`, featureID)
}

// KnowledgeEntriesByEnvironment lists the newest entries in one environment.
func (s *Store) KnowledgeEntriesByEnvironment(environment string, limit int) ([]KnowledgeEntry, error) {
	b := &whereBuilder{ph: func(int) string { return "?" }}
	where := "environment = " + b.bind(environment)
	return s.knowledgeRows(`SELECT `+knowledgeCols+` FROM knowledge_entries WHERE `+where+
		` ORDER BY updated_at DESC, id ASC`+limitSQL(limit, b), b.args...)
}

// KnowledgeEntriesSearch substring-matches title or content (optionally
// narrowed by knowledge type and environment), newest first.
func (s *Store) KnowledgeEntriesSearch(query, knowledgeType, environment string, limit int) ([]KnowledgeEntry, error) {
	b := &whereBuilder{ph: func(int) string { return "?" }}
	where := searchWhere(query, knowledgeType, environment, b)
	clause := limitSQL(limit, b)
	return s.knowledgeRows(`SELECT `+knowledgeCols+` FROM knowledge_entries WHERE `+where+
		` ORDER BY updated_at DESC, id ASC`+clause, b.args...)
}

func (s *Store) knowledgeRows(query string, args ...any) ([]KnowledgeEntry, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: knowledge query: %w", err)
	}
	defer rows.Close()
	var out []KnowledgeEntry
	for rows.Next() {
		var r knowledgeScan
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, fmt.Errorf("store: knowledge query scan: %w", err)
		}
		out = append(out, r.entry())
	}
	return out, rows.Err()
}

// ServiceMetadataUpsert inserts or replaces one (service, env) profile. Unset
// optionals stay NULL; the Rust writer replaced them with "" and the reader
// filtered empties, which is the same observable answer.
func (s *Store) ServiceMetadataUpsert(m ServiceMetadata) error {
	if m.ServiceName == "" {
		return fmt.Errorf("store: service metadata upsert: service_name is required")
	}
	env := m.Env
	if env == "" {
		env = "local"
	}
	_, err := s.db.Exec(`INSERT INTO service_metadata (`+serviceMetaCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(service_name, env) DO UPDATE SET
			team=excluded.team, on_call=excluded.on_call, repo_url=excluded.repo_url,
			language=excluded.language, health_endpoint=excluded.health_endpoint,
			slo_p99_ms=excluded.slo_p99_ms, incident_count=excluded.incident_count,
			last_incident=excluded.last_incident, tags=excluded.tags, version=excluded.version,
			deploy_envs=excluded.deploy_envs, created_at=excluded.created_at, updated_at=excluded.updated_at`,
		m.ServiceName, env, nullString(m.Team), nullString(m.OnCall), nullString(m.RepoURL),
		nullString(m.Language), nullString(m.HealthEndpoint), nullInt64(m.SLOP99Ms),
		m.IncidentCount, nullInt64(m.LastIncident), m.Tags, nullString(m.Version), m.DeployEnvs,
		m.CreatedAt, m.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: service metadata upsert %s/%s: %w", m.ServiceName, m.Env, err)
	}
	return nil
}

// ServiceMetadataGet resolves one (service, env) profile.
func (s *Store) ServiceMetadataGet(service, env string) (ServiceMetadata, bool, error) {
	row := s.db.QueryRow(`SELECT `+serviceMetaCols+` FROM service_metadata
		WHERE service_name = ? AND env = ?`, service, env)
	var r serviceMetaScan
	if err := row.Scan(r.dest()...); err != nil {
		if isNoRows(err) {
			return ServiceMetadata{}, false, nil
		}
		return ServiceMetadata{}, false, fmt.Errorf("store: service metadata get: %w", err)
	}
	return r.meta(), true, nil
}

// EnvSnapshotsPut writes environment-scoped element snapshots in one
// transaction, replacing each (env, qualified_name) row.
func (s *Store) EnvSnapshotsPut(snaps []EnvSnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, snap := range snaps {
		if snap.Env == "" || snap.QualifiedName == "" {
			return fmt.Errorf("store: env snapshot: env and qualified_name are required")
		}
		meta := "{}"
		if len(snap.Metadata) > 0 {
			b, err := json.Marshal(snap.Metadata)
			if err != nil {
				return fmt.Errorf("store: env snapshot %s/%s metadata: %w", snap.Env, snap.QualifiedName, err)
			}
			meta = string(b)
		}
		if _, err := tx.Exec(`INSERT INTO env_snapshots (`+envSnapshotCols+`)
			VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(env, qualified_name) DO UPDATE SET
				element_type=excluded.element_type, name=excluded.name, file_path=excluded.file_path,
				metadata=excluded.metadata, captured_at=excluded.captured_at`,
			snap.Env, snap.QualifiedName, snap.ElementType, snap.Name, snap.FilePath, meta,
			snap.CapturedAt); err != nil {
			return fmt.Errorf("store: env snapshot upsert %s/%s: %w", snap.Env, snap.QualifiedName, err)
		}
	}
	return tx.Commit()
}

// EnvSnapshotGet resolves one environment-scoped element snapshot.
func (s *Store) EnvSnapshotGet(env, qualifiedName string) (EnvSnapshot, bool, error) {
	row := s.db.QueryRow(`SELECT `+envSnapshotCols+` FROM env_snapshots
		WHERE env = ? AND qualified_name = ?`, env, qualifiedName)
	var snap EnvSnapshot
	var meta string
	if err := row.Scan(&snap.Env, &snap.QualifiedName, &snap.ElementType, &snap.Name, &snap.FilePath,
		&meta, &snap.CapturedAt); err != nil {
		if isNoRows(err) {
			return EnvSnapshot{}, false, nil
		}
		return EnvSnapshot{}, false, fmt.Errorf("store: env snapshot get: %w", err)
	}
	if meta != "" && meta != "{}" {
		_ = json.Unmarshal([]byte(meta), &snap.Metadata)
	}
	return snap, true, nil
}

// EnvSnapshotConflictsWith lists conflicts_with relationships with the needle
// in either endpoint (case-insensitive substring, the Rust
// regex_matches(lowercase(endpoint), ".*needle.*") report).
func (s *Store) EnvSnapshotConflictsWith(needle string, limit int) ([]Relationship, error) {
	b := &whereBuilder{ph: func(int) string { return "?" }}
	where := "rel_type = 'conflicts_with' AND (LOWER(source_qualified) LIKE " + b.bind(likeContains(needle)) +
		" ESCAPE '\\' OR LOWER(target_qualified) LIKE " + b.bind(likeContains(needle)) + " ESCAPE '\\')"
	rows, err := s.db.Query(`SELECT source_qualified, target_qualified, rel_type, confidence, metadata `+
		`FROM relationships WHERE `+where+` ORDER BY source_qualified, target_qualified`+limitSQL(limit, b), b.args...)
	if err != nil {
		return nil, fmt.Errorf("store: conflicts_with query: %w", err)
	}
	return scanRelationships(rows)
}

// EnvSnapshotEnvVariants lists the qualified names snapshotted in more than
// one environment and containing needle (case-insensitive), ordered by
// qualified name with the environments ascending. limit <= 0 returns all.
func (s *Store) EnvSnapshotEnvVariants(needle string, limit int) ([]EnvVariant, error) {
	b := &whereBuilder{ph: func(int) string { return "?" }}
	where := "LOWER(qualified_name) LIKE " + b.bind(likeContains(needle)) + " ESCAPE '\\'"
	rows, err := s.db.Query(`SELECT qualified_name, env FROM env_snapshots WHERE `+where+
		` ORDER BY qualified_name ASC, env ASC`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("store: env variants query: %w", err)
	}
	defer rows.Close()
	variants, err := scanEnvVariants(rows)
	if err != nil {
		return nil, fmt.Errorf("store: env variants scan: %w", err)
	}
	if limit > 0 && len(variants) > limit {
		variants = variants[:limit]
	}
	return variants, nil
}

// ServiceMetadataAll lists every service profile in one environment, ordered
// by service name (Rust get_all_service_metadata, which took the Datalog
// order; ordering here is deterministic).
func (s *Store) ServiceMetadataAll(env string) ([]ServiceMetadata, error) {
	rows, err := s.db.Query(`SELECT `+serviceMetaCols+` FROM service_metadata
		WHERE env = ? ORDER BY service_name ASC`, env)
	if err != nil {
		return nil, fmt.Errorf("store: service metadata list: %w", err)
	}
	defer rows.Close()
	var out []ServiceMetadata
	for rows.Next() {
		var r serviceMetaScan
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, fmt.Errorf("store: service metadata list scan: %w", err)
		}
		out = append(out, r.meta())
	}
	return out, rows.Err()
}
