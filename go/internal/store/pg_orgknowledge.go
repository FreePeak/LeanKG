// PostgreSQL org/ops knowledge storage (mirror of store_orgknowledge.go over
// pgx). Semantics are identical; only the placeholders ($n), BIGINT columns
// and the COALESCE/empty-as-unset convention differ — the same convention the
// rest of store_pg.go uses, chosen over sql.Null* so no scanning path depends
// on database/sql compatibility shims. Empty string and 0 therefore mean
// "unset", exactly as the Rust Option round-trip reported them.
package store

import (
	"encoding/json"
	"fmt"
)

// pgIncidentRow is one incidents row with empty-as-unset columns.
type pgIncidentRow struct {
	id, env, title, severity, rootCause, resolution, affected, tags, author string
	triggerPattern, prevention, linkedTicket                                string
	occurredAt, resolvedAt                                                  int64
}

func (r pgIncidentRow) incident() Incident {
	return Incident{
		ID:               r.id,
		Env:              r.env,
		Title:            r.title,
		Severity:         r.severity,
		OccurredAt:       r.occurredAt,
		ResolvedAt:       int64PtrIfSet(r.resolvedAt),
		RootCause:        r.rootCause,
		Resolution:       r.resolution,
		AffectedServices: decodeStringList(r.affected),
		TriggerPattern:   stringPtrIfSet(r.triggerPattern),
		Prevention:       stringPtrIfSet(r.prevention),
		Tags:             decodeStringList(r.tags),
		Author:           r.author,
		LinkedTicket:     stringPtrIfSet(r.linkedTicket),
	}
}

func stringPtrIfSet(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func int64PtrIfSet(n int64) *int64 {
	if n == 0 {
		return nil
	}
	return &n
}

// pgKnowledgeRow is one knowledge_entries row with empty-as-unset columns.
type pgKnowledgeRow struct {
	id, knowledgeType, title, content, tags, environment, author string
	elementQualified, userStoryID, featureID, branch             string
	createdAt, updatedAt                                         int64
}

func (r pgKnowledgeRow) entry() KnowledgeEntry {
	return KnowledgeEntry{
		ID:               r.id,
		KnowledgeType:    r.knowledgeType,
		Title:            r.title,
		Content:          r.content,
		ElementQualified: stringPtrIfSet(r.elementQualified),
		UserStoryID:      stringPtrIfSet(r.userStoryID),
		FeatureID:        stringPtrIfSet(r.featureID),
		Tags:             r.tags,
		Environment:      r.environment,
		Branch:           stringPtrIfSet(r.branch),
		Author:           r.author,
		CreatedAt:        r.createdAt,
		UpdatedAt:        r.updatedAt,
	}
}

// pgServiceMetaRow is one service_metadata row with empty-as-unset columns.
type pgServiceMetaRow struct {
	serviceName, env, tags, deployEnvs  string
	team, onCall, repoURL, language     string
	healthEndpoint, version             string
	sloP99Ms, lastIncident              int64
	incidentCount, createdAt, updatedAt int64
}

func (r pgServiceMetaRow) meta() ServiceMetadata {
	return ServiceMetadata{
		ServiceName:    r.serviceName,
		Env:            r.env,
		Team:           stringPtrIfSet(r.team),
		OnCall:         stringPtrIfSet(r.onCall),
		RepoURL:        stringPtrIfSet(r.repoURL),
		Language:       stringPtrIfSet(r.language),
		HealthEndpoint: stringPtrIfSet(r.healthEndpoint),
		SLOP99Ms:       int64PtrIfSet(r.sloP99Ms),
		IncidentCount:  r.incidentCount,
		LastIncident:   int64PtrIfSet(r.lastIncident),
		Tags:           r.tags,
		Version:        stringPtrIfSet(r.version),
		DeployEnvs:     r.deployEnvs,
		CreatedAt:      r.createdAt,
		UpdatedAt:      r.updatedAt,
	}
}

// pgIncidentCols coalesces the optional columns for the empty-as-unset read.
const pgIncidentCols = `id, env, title, severity, occurred_at, COALESCE(resolved_at, 0), ` +
	`root_cause, resolution, affected_services, COALESCE(trigger_pattern, ''), ` +
	`COALESCE(prevention, ''), tags, author, COALESCE(linked_ticket, '')`

const pgKnowledgeCols = `id, knowledge_type, title, content, COALESCE(element_qualified, ''), ` +
	`COALESCE(user_story_id, ''), COALESCE(feature_id, ''), tags, environment, COALESCE(branch, ''), ` +
	`author, created_at, updated_at`

const pgServiceMetaCols = `service_name, env, COALESCE(team, ''), COALESCE(on_call, ''), ` +
	`COALESCE(repo_url, ''), COALESCE(language, ''), COALESCE(health_endpoint, ''), ` +
	`COALESCE(slo_p99_ms, 0), incident_count, COALESCE(last_incident, 0), tags, ` +
	`COALESCE(version, ''), deploy_envs, created_at, updated_at`

func (r *pgIncidentRow) dest() []any {
	return []any{
		&r.id, &r.env, &r.title, &r.severity, &r.occurredAt, &r.resolvedAt, &r.rootCause,
		&r.resolution, &r.affected, &r.triggerPattern, &r.prevention, &r.tags, &r.author, &r.linkedTicket,
	}
}

func (r *pgKnowledgeRow) dest() []any {
	return []any{
		&r.id, &r.knowledgeType, &r.title, &r.content, &r.elementQualified, &r.userStoryID,
		&r.featureID, &r.tags, &r.environment, &r.branch, &r.author, &r.createdAt, &r.updatedAt,
	}
}

func (r *pgServiceMetaRow) dest() []any {
	return []any{
		&r.serviceName, &r.env, &r.team, &r.onCall, &r.repoURL, &r.language, &r.healthEndpoint,
		&r.sloP99Ms, &r.incidentCount, &r.lastIncident, &r.tags, &r.version, &r.deployEnvs,
		&r.createdAt, &r.updatedAt,
	}
}

// IncidentUpsert inserts or replaces an incident keyed by ID.
func (s *PGStore) IncidentUpsert(inc Incident) error {
	if inc.ID == "" {
		return fmt.Errorf("store: incident upsert: id is required")
	}
	env := inc.Env
	if env == "" {
		env = "local"
	}
	_, err := s.pool.Exec(pgCtx, `INSERT INTO incidents (`+incidentCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (id) DO UPDATE SET
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
func (s *PGStore) IncidentByID(id string) (Incident, bool, error) {
	var r pgIncidentRow
	err := s.pool.QueryRow(pgCtx, `SELECT `+pgIncidentCols+` FROM incidents WHERE id = $1`, id).Scan(r.dest()...)
	if err != nil {
		if isNoRows(err) {
			return Incident{}, false, nil
		}
		return Incident{}, false, fmt.Errorf("store: incident by id: %w", err)
	}
	return r.incident(), true, nil
}

// IncidentDelete removes an incident; an absent id is a no-op.
func (s *PGStore) IncidentDelete(id string) error {
	if _, err := s.pool.Exec(pgCtx, `DELETE FROM incidents WHERE id = $1`, id); err != nil {
		return fmt.Errorf("store: incident delete %s: %w", id, err)
	}
	return nil
}

// IncidentsQuery lists incidents newest first (see the SQLite method for the
// ordering note).
func (s *PGStore) IncidentsQuery(q IncidentQuery) ([]Incident, error) {
	b := &whereBuilder{ph: func(n int) string { return fmt.Sprintf("$%d", n) }}
	where := incidentWhere(q, b)
	clause := limitSQL(q.Limit, b)
	rows, err := s.pool.Query(pgCtx, `SELECT `+pgIncidentCols+` FROM incidents WHERE `+where+
		` ORDER BY occurred_at DESC, id ASC`+clause, b.args...)
	if err != nil {
		return nil, fmt.Errorf("store: incidents query: %w", err)
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		var r pgIncidentRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, fmt.Errorf("store: incidents query scan: %w", err)
		}
		out = append(out, r.incident())
	}
	return out, rows.Err()
}

// KnowledgeEntryUpsert inserts or replaces a knowledge entry keyed by ID.
func (s *PGStore) KnowledgeEntryUpsert(e KnowledgeEntry) error {
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
	_, err := s.pool.Exec(pgCtx, `INSERT INTO knowledge_entries (`+knowledgeCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (id) DO UPDATE SET
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
func (s *PGStore) KnowledgeEntryByID(id string) (KnowledgeEntry, bool, error) {
	var r pgKnowledgeRow
	err := s.pool.QueryRow(pgCtx, `SELECT `+pgKnowledgeCols+` FROM knowledge_entries WHERE id = $1`, id).Scan(r.dest()...)
	if err != nil {
		if isNoRows(err) {
			return KnowledgeEntry{}, false, nil
		}
		return KnowledgeEntry{}, false, fmt.Errorf("store: knowledge entry by id: %w", err)
	}
	return r.entry(), true, nil
}

// KnowledgeEntryDelete removes a knowledge entry; an absent id is a no-op.
func (s *PGStore) KnowledgeEntryDelete(id string) error {
	if _, err := s.pool.Exec(pgCtx, `DELETE FROM knowledge_entries WHERE id = $1`, id); err != nil {
		return fmt.Errorf("store: knowledge entry delete %s: %w", id, err)
	}
	return nil
}

// KnowledgeEntriesByElement lists the entries anchored to one element.
func (s *PGStore) KnowledgeEntriesByElement(qualifiedName string) ([]KnowledgeEntry, error) {
	return s.pgKnowledgeRows(`SELECT `+pgKnowledgeCols+` FROM knowledge_entries
		WHERE element_qualified = $1 ORDER BY updated_at DESC, id ASC`, qualifiedName)
}

// KnowledgeEntriesByFeature lists the entries anchored to one feature.
func (s *PGStore) KnowledgeEntriesByFeature(featureID string) ([]KnowledgeEntry, error) {
	return s.pgKnowledgeRows(`SELECT `+pgKnowledgeCols+` FROM knowledge_entries
		WHERE feature_id = $1 ORDER BY updated_at DESC, id ASC`, featureID)
}

// KnowledgeEntriesByEnvironment lists the newest entries in one environment.
func (s *PGStore) KnowledgeEntriesByEnvironment(environment string, limit int) ([]KnowledgeEntry, error) {
	b := &whereBuilder{ph: func(n int) string { return fmt.Sprintf("$%d", n) }}
	where := "environment = " + b.bind(environment)
	return s.pgKnowledgeRows(`SELECT `+pgKnowledgeCols+` FROM knowledge_entries WHERE `+where+
		` ORDER BY updated_at DESC, id ASC`+limitSQL(limit, b), b.args...)
}

// KnowledgeEntriesSearch substring-matches title or content (optionally
// narrowed by knowledge type and environment), newest first.
func (s *PGStore) KnowledgeEntriesSearch(query, knowledgeType, environment string, limit int) ([]KnowledgeEntry, error) {
	b := &whereBuilder{ph: func(n int) string { return fmt.Sprintf("$%d", n) }}
	where := searchWhere(query, knowledgeType, environment, b)
	clause := limitSQL(limit, b)
	return s.pgKnowledgeRows(`SELECT `+pgKnowledgeCols+` FROM knowledge_entries WHERE `+where+
		` ORDER BY updated_at DESC, id ASC`+clause, b.args...)
}

func (s *PGStore) pgKnowledgeRows(query string, args ...any) ([]KnowledgeEntry, error) {
	rows, err := s.pool.Query(pgCtx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: knowledge query: %w", err)
	}
	defer rows.Close()
	var out []KnowledgeEntry
	for rows.Next() {
		var r pgKnowledgeRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, fmt.Errorf("store: knowledge query scan: %w", err)
		}
		out = append(out, r.entry())
	}
	return out, rows.Err()
}

// ServiceMetadataUpsert inserts or replaces one (service, env) profile.
func (s *PGStore) ServiceMetadataUpsert(m ServiceMetadata) error {
	if m.ServiceName == "" {
		return fmt.Errorf("store: service metadata upsert: service_name is required")
	}
	env := m.Env
	if env == "" {
		env = "local"
	}
	_, err := s.pool.Exec(pgCtx, `INSERT INTO service_metadata (`+serviceMetaCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (service_name, env) DO UPDATE SET
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
func (s *PGStore) ServiceMetadataGet(service, env string) (ServiceMetadata, bool, error) {
	var r pgServiceMetaRow
	err := s.pool.QueryRow(pgCtx, `SELECT `+pgServiceMetaCols+` FROM service_metadata
		WHERE service_name = $1 AND env = $2`, service, env).Scan(r.dest()...)
	if err != nil {
		if isNoRows(err) {
			return ServiceMetadata{}, false, nil
		}
		return ServiceMetadata{}, false, fmt.Errorf("store: service metadata get: %w", err)
	}
	return r.meta(), true, nil
}

// EnvSnapshotsPut writes environment-scoped element snapshots in one
// transaction, replacing each (env, qualified_name) row.
func (s *PGStore) EnvSnapshotsPut(snaps []EnvSnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(pgCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(pgCtx) }()
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
		if _, err := tx.Exec(pgCtx, `INSERT INTO env_snapshots (`+envSnapshotCols+`)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (env, qualified_name) DO UPDATE SET
				element_type=excluded.element_type, name=excluded.name, file_path=excluded.file_path,
				metadata=excluded.metadata, captured_at=excluded.captured_at`,
			snap.Env, snap.QualifiedName, snap.ElementType, snap.Name, snap.FilePath, meta,
			snap.CapturedAt); err != nil {
			return fmt.Errorf("store: env snapshot upsert %s/%s: %w", snap.Env, snap.QualifiedName, err)
		}
	}
	return tx.Commit(pgCtx)
}

// EnvSnapshotGet resolves one environment-scoped element snapshot.
func (s *PGStore) EnvSnapshotGet(env, qualifiedName string) (EnvSnapshot, bool, error) {
	var snap EnvSnapshot
	var meta string
	err := s.pool.QueryRow(pgCtx, `SELECT `+envSnapshotCols+` FROM env_snapshots
		WHERE env = $1 AND qualified_name = $2`, env, qualifiedName).Scan(
		&snap.Env, &snap.QualifiedName, &snap.ElementType, &snap.Name, &snap.FilePath, &meta, &snap.CapturedAt)
	if err != nil {
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
// in either endpoint (mirror of the SQLite method).
func (s *PGStore) EnvSnapshotConflictsWith(needle string, limit int) ([]Relationship, error) {
	b := &whereBuilder{ph: func(n int) string { return fmt.Sprintf("$%d", n) }}
	where := "rel_type = 'conflicts_with' AND (LOWER(source_qualified) LIKE " + b.bind(likeContains(needle)) +
		" ESCAPE '\\' OR LOWER(target_qualified) LIKE " + b.bind(likeContains(needle)) + " ESCAPE '\\')"
	rows, err := s.pool.Query(pgCtx, `SELECT source_qualified, target_qualified, rel_type, confidence, metadata `+
		`FROM relationships WHERE `+where+` ORDER BY source_qualified, target_qualified`+limitSQL(limit, b), b.args...)
	if err != nil {
		return nil, fmt.Errorf("store: conflicts_with query: %w", err)
	}
	defer rows.Close()
	var out []Relationship
	for rows.Next() {
		var r Relationship
		var meta string
		if err := rows.Scan(&r.Source, &r.Target, &r.RelType, &r.Confidence, &meta); err != nil {
			return nil, fmt.Errorf("store: conflicts_with scan: %w", err)
		}
		if meta != "" && meta != "{}" {
			_ = json.Unmarshal([]byte(meta), &r.Metadata)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EnvSnapshotEnvVariants lists the qualified names snapshotted in more than
// one environment and containing needle (mirror of the SQLite method).
func (s *PGStore) EnvSnapshotEnvVariants(needle string, limit int) ([]EnvVariant, error) {
	b := &whereBuilder{ph: func(n int) string { return fmt.Sprintf("$%d", n) }}
	where := "LOWER(qualified_name) LIKE " + b.bind(likeContains(needle)) + " ESCAPE '\\'"
	rows, err := s.pool.Query(pgCtx, `SELECT qualified_name, env FROM env_snapshots WHERE `+where+
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
// by service name (mirror of the SQLite method).
func (s *PGStore) ServiceMetadataAll(env string) ([]ServiceMetadata, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT `+pgServiceMetaCols+` FROM service_metadata
		WHERE env = $1 ORDER BY service_name ASC`, env)
	if err != nil {
		return nil, fmt.Errorf("store: service metadata list: %w", err)
	}
	defer rows.Close()
	var out []ServiceMetadata
	for rows.Next() {
		var r pgServiceMetaRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, fmt.Errorf("store: service metadata list scan: %w", err)
		}
		out = append(out, r.meta())
	}
	return out, rows.Err()
}
