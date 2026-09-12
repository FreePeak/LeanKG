package store

import (
	"context"
	"fmt"
)

// Engine names the storage backends.
const (
	EngineSQLite   = "sqlite"
	EnginePostgres = "postgres"
)

// Backend is the full storage contract of the Go engine. The SQLite
// implementation is *Store (internal/store); the PostgreSQL+pgvector
// implementation is *PGStore. Consumers (core, index, embed, rest, cmd)
// depend on this interface, never on a concrete type — that is what makes
// the engine dual-backend.
type Backend interface {
	// lifecycle
	Close() error
	Path() string // connection target (file path or DSN); for status display
	Migrate() error
	Engine() string

	// index layer
	UpsertElements(els []Element) error
	UpsertRelationships(rels []Relationship) error
	UpsertFiles(files []FileRecord) error
	Files() ([]FileRecord, error)
	DeleteByFile(path string) error
	DeleteFileRecord(path string) error
	// relationship reads (graph traversal seeds; pure-Go BFS lives in internal/graph)
	Outgoing(source string) ([]Relationship, error)
	Incoming(target string) ([]Relationship, error)
	RelationshipsAll(limit int) ([]Relationship, error)

	// generic namespaced kv (ontology catalogs, doctor state; not a document store)
	KVSet(namespace, key, value string) error
	KVGet(namespace, key string) (string, bool, error)

	FindExact(name string) ([]Element, error)
	FindFuzzy(query string, limit int) ([]FuzzyMatch, error)
	ElementCount() (int, error)
	RelationshipCount() (int, error)
	ElementsByType() (map[string]int, error)
	FileCount() (int, error)
	// Elements enumerates every indexed element ordered by qualified_name
	// (embed pipeline scan + parity fixtures).
	Elements() ([]Element, error)

	// embedding layer
	WriteStamp(st ModelStamp) error
	Stamp(modelID string) (*ModelStamp, error)
	Stamps() ([]ModelStamp, error)
	EmbeddingStateMap(modelID string) (map[string]string, error)
	SetEmbeddingStates(modelID string, states map[string]string) error
	UpsertVectors(modelID string, rows []VectorRow) error
	ClearVectors(modelID string) error
	VectorCount(modelID string) (int, error)
	// VectorCoverage reports (covered, orphans): vector rows whose QN still
	// has a live element vs. rows whose QN disappeared.
	VectorCoverage(modelID string) (covered, orphans int, err error)
	SearchVectors(modelID string, q []float32, k int) ([]VectorSearchHit, error)

	// freshness watermark (DB-resident; readers never bump)
	Watermark() (seq, at int64, err error)
	BumpWatermark() error

	// inventory
	SaveInventory(inv Inventory) error
	LoadInventory() (*Inventory, error)

	// embed-pipeline run bookkeeping (status reads this; no in-process coupling)
	StartEmbedRun(modelID, mode string, dirty int) (int64, error)
	FinishEmbedRun(id int64, status string, embedded, skipped, failed, truncations, orphans int) error
	LastEmbedRunAny() (*EmbedRun, error)
	LastEmbedRun(modelID string) (*EmbedRun, error)

	// audit ledger (hash-chained; FR parity with the Rust audit/mod.rs)
	AppendAudit(e *AuditEntry) error
	AuditTail(limit int) ([]AuditEntry, error)
	VerifyAuditChain() (ok bool, brokenAt int64, err error)

	// auth tokens (DB-backed bearer tokens; Rust auth/tokens.rs parity).
	// TokenUpsert and TokenFind take the PLAINTEXT secret and store/match
	// only its SHA-256 hash; returned Tokens carry the at-rest hash.
	// TokenFind rejects revoked and expired rows with ErrTokenRevoked /
	// ErrTokenExpired; TokenRevoke is the soft (revoked_at) counterpart of
	// the hard TokenDelete; TokenTouch records a successful authentication's
	// last_used_at, coalesced to one write per TokenTouchWindowSecs.
	TokenUpsert(tok Token) error
	TokenList() ([]Token, error)
	TokenDelete(id string) error
	TokenRevoke(id string, at int64) error
	TokenTouch(id string, at int64) error
	TokenFind(secret string) (Token, bool, error)

	// enterprise auth (Rust auth/accounts.rs + 004_auth.sql parity):
	// accounts, orgs, org memberships, team members and resource ownership.
	// Role POLICY lives in internal/auth (hierarchy, bootstrap orgs, ownership
	// precedence); these are typed table accesses. Upserts replace the whole
	// row on the primary key, and membership writes are keyed on the pair, so
	// a re-add re-roles instead of duplicating.
	AccountUpsert(a Account) error
	AccountByEmail(email string) (Account, bool, error)
	OrgUpsert(o Org) error
	OrgByID(id string) (Org, bool, error)
	OrgsByOwner(ownerAccountID string) ([]Org, error)
	OrgMembershipUpsert(m OrgMember) error
	OrgMemberOf(orgID, accountID string) (OrgMember, bool, error)
	OrgMembers(orgID string) ([]OrgMember, error)
	TeamMemberUpsert(m TeamMember) error
	TeamMemberOf(teamID, accountID string) (TeamMember, bool, error)
	TeamMembers(teamID string) ([]TeamMember, error)
	ResourceClaim(resourceType, resourceID, ownerAccountID, orgID string) error
	IsResourceOwner(resourceType, resourceID, accountID string) (bool, error)

	// org/ops knowledge (Rust db::mod.rs incidents / knowledge_entries /
	// service_metadata plus the env-scoped code_elements reads behind
	// find_env_conflicts). Typed table access only; validation and the
	// aggregation policy live in internal/orgknowledge. See orgknowledge.go
	// for the entity types and the env_snapshots ceiling note.
	IncidentUpsert(inc Incident) error
	IncidentByID(id string) (Incident, bool, error)
	IncidentDelete(id string) error
	IncidentsQuery(q IncidentQuery) ([]Incident, error)
	KnowledgeEntryUpsert(e KnowledgeEntry) error
	KnowledgeEntryByID(id string) (KnowledgeEntry, bool, error)
	KnowledgeEntryDelete(id string) error
	KnowledgeEntriesByElement(qualifiedName string) ([]KnowledgeEntry, error)
	KnowledgeEntriesByFeature(featureID string) ([]KnowledgeEntry, error)
	KnowledgeEntriesByEnvironment(environment string, limit int) ([]KnowledgeEntry, error)
	KnowledgeEntriesSearch(query, knowledgeType, environment string, limit int) ([]KnowledgeEntry, error)
	ServiceMetadataUpsert(m ServiceMetadata) error
	ServiceMetadataGet(service, env string) (ServiceMetadata, bool, error)
	// ServiceMetadataAll lists the profiles in one environment, ordered by
	// service name (the Rust get_all_service_metadata read behind the team
	// map).
	ServiceMetadataAll(env string) ([]ServiceMetadata, error)
	EnvSnapshotsPut(snaps []EnvSnapshot) error
	EnvSnapshotGet(env, qualifiedName string) (EnvSnapshot, bool, error)
	// EnvSnapshotConflictsWith lists conflicts_with relationships whose
	// source or target matches `needle` case-insensitively (substring, like
	// the Rust regex_matches(lowercase(...), ".*needle.*") report).
	EnvSnapshotConflictsWith(needle string, limit int) ([]Relationship, error)
	// EnvSnapshotEnvVariants lists qualified names having snapshots in more
	// than one environment, case-insensitively containing `needle`.
	EnvSnapshotEnvVariants(needle string, limit int) ([]EnvVariant, error)

	// context-metrics ledger (Rust db::record_metric / get_metrics_summary /
	// cleanup_old_metrics / reset_metrics over the context_metrics table, plus
	// the H10/FR-PLG-8 usage buckets). See store_metrics.go for the fold rules,
	// the Rust provenance and the documented ceilings.
	// RecordMetric no-ops on a read-only backend (Rust's is_read_only guard);
	// MetricsSummary windows by retentionDays and takes an optional tool filter
	// ("" = every tool); UsageAggregates takes an epoch-second cutoff where 0
	// means all time; CleanupMetrics/ResetMetrics return the number of rows
	// removed.
	RecordMetric(m Metric) error
	MetricsSummary(tool string, retentionDays int) (MetricSummary, error)
	UsageAggregates(sinceCutoff int64) (UsageAggregates, error)
	CleanupMetrics(retentionDays int) (int64, error)
	ResetMetrics() (int64, error)
}

// AuditEntry is one hash-chained audit record. Hash = sha256(prev_hash |
// at | actor | action | target | details_json); the chain makes any
// tampering detectable via VerifyAuditChain.
type AuditEntry struct {
	Seq      int64          `json:"seq"`
	At       int64          `json:"at"`
	Actor    string         `json:"actor"`
	Action   string         `json:"action"`
	Target   string         `json:"target"`
	Details  map[string]any `json:"details,omitempty"`
	PrevHash string         `json:"prev_hash"`
	Hash     string         `json:"hash"`
}

// Elements enumerates every indexed element (ordered by qualified_name).
func (s *Store) Elements() ([]Element, error) {
	rows, err := s.db.Query(`SELECT ` + elementCols + ` FROM code_elements ce ORDER BY ce.qualified_name`)
	if err != nil {
		return nil, err
	}
	return scanElements(rows)
}

// VectorCoverage reports (covered, orphans) for a model via anti-join.
func (s *Store) VectorCoverage(modelID string) (covered, orphans int, err error) {
	err = s.db.QueryRow(`SELECT
			COALESCE(SUM(CASE WHEN ce.qualified_name IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN ce.qualified_name IS NULL     THEN 1 ELSE 0 END), 0)
		FROM embedding_vectors v
		LEFT JOIN code_elements ce ON ce.qualified_name = v.qualified_name
		WHERE v.model_id = ?`, modelID).Scan(&covered, &orphans)
	return covered, orphans, err
}

// OpenBackend opens the engine's storage for a project directory.
// engine is EngineSQLite (default) or EnginePostgres (dsn via env
// LEANKG_PG_URL or the pgURL argument when non-empty).
func OpenBackend(ctx context.Context, projectDir, engine, pgURL string, mode Mode) (Backend, error) {
	switch engine {
	case "", EngineSQLite:
		return Open(projectDir+"/.leankg/leankg.db", mode)
	case EnginePostgres:
		if pgURL == "" {
			return nil, fmt.Errorf("store: postgres engine requires LEANKG_PG_URL")
		}
		return OpenPG(ctx, pgURL, projectDir, mode) // implemented in store_pg.go (W4)
	default:
		return nil, fmt.Errorf("store: unknown engine %q (want sqlite|postgres)", engine)
	}
}
