package store

import "time"

// The store contract is one interface per role (plan CX-14, section 3.1).
// Backend embeds every role. Consumers should depend on the narrowest role they
// use (plan section 3.3); the ones that still take Backend migrate in the
// CX-19..CX-26 items. The signatures below are the current ones: ctx and the
// other contract changes land with the item that names their role.
//
// Both engines must satisfy Backend; the assertions below fail the build when
// one drifts.
var (
	_ Backend = (*Store)(nil)
	_ Backend = (*PGStore)(nil)
)

// IndexWriter persists the code graph of one indexed file set.
type IndexWriter interface {
	UpsertElements(els []Element) error
	UpsertRelationships(rels []Relationship) error
	UpsertFiles(files []FileRecord) error
	Files() ([]FileRecord, error)
	DeleteByFile(path string) error
	DeleteFileRecord(path string) error
}

// ElementReader reads indexed elements and their counts.
type ElementReader interface {
	FindExact(name string) ([]Element, error)
	ElementCount() (int, error)
	RelationshipCount() (int, error)
	ElementsByType() (map[string]int, error)
	FileCount() (int, error)
	// Elements enumerates every indexed element ordered by qualified_name
	// (embed pipeline scan + parity fixtures).
	Elements() ([]Element, error)
}

// GraphReader reads relationship edges (graph traversal seeds; pure-Go BFS
// lives in internal/graph).
type GraphReader interface {
	Outgoing(source string) ([]Relationship, error)
	Incoming(target string) ([]Relationship, error)
	RelationshipsAll(limit int) ([]Relationship, error)
}

// Arm names one ranking source: a keyword or vector arm that a rung reports
// (FindFuzzy) or that a fusion labels (RankList, HybridHit.Ranks). ArmFTS5 is
// the SQLite keyword arm; ArmTSVector, ArmTrigram and ArmILIKE are the
// PostgreSQL keyword arms, in the order the degrade chain tries them.
type Arm string

const (
	ArmVector   Arm = "vector"
	ArmName     Arm = "name"
	ArmFTS5     Arm = "fts5"
	ArmTSVector Arm = "tsvector"
	ArmTrigram  Arm = "trigram"
	ArmILIKE    Arm = "ilike"
)

// Searcher ranks elements by keyword and by vector similarity.
type Searcher interface {
	// FindFuzzy is the keyword arm. It returns the hits and the arm that served
	// them (ArmFTS5 on SQLite; ArmTSVector, ArmTrigram or ArmILIKE on PostgreSQL).
	FindFuzzy(query string, limit int) ([]FuzzyMatch, Arm, error)
	SearchVectors(modelID string, q []float32, k int) ([]VectorSearchHit, error)
	// SearchVectorsScoped is SearchVectors restricted to the elements keep
	// accepts; a nil keep is exactly SearchVectors. A caller that wants to rank
	// production code without test fixtures and archived prose otherwise ranks
	// over the same collection, and the filter costs one predicate per row on a
	// scan both backends already do.
	SearchVectorsScoped(modelID string, q []float32, k int, keep func(Element) bool) ([]VectorSearchHit, error)
}

// Vectors stores embedding rows, their per-model stamps and their state.
type Vectors interface {
	WriteStamp(st ModelStamp) error
	Stamp(modelID string) (*ModelStamp, error)
	Stamps() ([]ModelStamp, error)
	EmbeddingStateMap(modelID string) (map[string]string, error)
	SetEmbeddingStates(modelID string, states map[string]string) error
	UpsertVectors(modelID string, rows []VectorRow) error
	// ReplaceFileVectors atomically replaces one file's embedding rows for a
	// model: delete + insert + state in one transaction (issue #279 per-file
	// atomic replace). rows must carry the file's complete vector set.
	ReplaceFileVectors(modelID string, rows []VectorRow, states map[string]string) error
	ClearVectors(modelID string) error
	VectorCount(modelID string) (int, error)
	// VectorCoverage reports (covered, orphans): vector rows whose QN still
	// has a live element vs. rows whose QN disappeared.
	VectorCoverage(modelID string) (covered, orphans int, err error)
}

// EmbedRuns is the embed-pipeline run ledger (status reads it; no in-process
// coupling).
type EmbedRuns interface {
	StartEmbedRun(modelID, mode string, dirty int) (int64, error)
	FinishEmbedRun(id int64, status string, embedded, skipped, failed, truncations, orphans int) error
	LastEmbedRunAny() (*EmbedRun, error)
}

// Inventories is the inventory snapshot and the freshness watermark. It is
// named Inventories because Inventory is the snapshot struct in this package.
type Inventories interface {
	// freshness watermark (DB-resident; readers never bump)
	Watermark() (seq, at int64, err error)
	SaveInventory(inv Inventory) error
	LoadInventory() (*Inventory, error)
}

// Maintainer owns space accounting, orphan sweeps and the maintenance
// operations (the reclaim half of DeleteByFile/gc: those return rows to the
// freelist, these return the freelist to the filesystem).
type Maintainer interface {
	// Space is a no-write probe, safe on a read-only handle; Checkpoint,
	// IncrementalVacuum and Vacuum are writes and refuse a read-only store.
	// Postgres implements Space with pg_database_size and the rest as
	// autovacuum-driven no-ops — the maintenance loop is engine-agnostic.
	Space() (SpaceReport, error)
	Checkpoint() error
	IncrementalVacuum(maxPages int) error
	Vacuum() error
	EnsureIncrementalVacuum() error
	// CompactFTS compacts the keyword index: SQLite merges the FTS5
	// segments and rebuilds the index from the content table; PostgreSQL
	// has no segment list to merge (its keyword rung is a GIN-indexed
	// tsvector column) and the implementation there is a no-op. It is a
	// write — a read-only handle refuses it — and it rewrites the whole
	// index, so the scheduler runs it on a slow cadence, not every pass.
	CompactFTS() error
	// PruneEmbedRuns trims the embed-run ledger: it closes out the runs
	// still marked "running" past the cutoff (a run that was killed
	// mid-flight otherwise claims to be running forever) and deletes the
	// finished runs older than it. The newest row is never deleted — it
	// names the store's current collection (CurrentModelID).
	PruneEmbedRuns(cutoff time.Time) (EmbedRunPrune, error)
	// DeleteOrphanRelationships purges edges whose source or target element
	// no longer exists (leankg gc; the repair `doctor --deep` names for the
	// documented reconcile ceiling — edges into a deleted file survive until
	// their source file is re-indexed, which incremental index never
	// revisits). Returns the number of edges removed.
	DeleteOrphanRelationships() (int, error)
	// DeleteOrphanVectors purges embedding_vectors + embedding_state rows
	// whose qualified_name no longer belongs to any element, across every
	// model collection (a dangling QN dangles in all of them). This is the
	// residue `leankg-embed` reports as Orphans: DeleteByFile owns elements
	// and edges, so until #411 nothing owned vector rows, and every deleted
	// file left its vectors behind forever. Returns the number of vector
	// rows removed.
	DeleteOrphanVectors() (int, error)
	// DeleteStaleStamps drops every vector collection that is not in keep:
	// its vectors, its embedding state and its stamp row. A collection is
	// stale when nothing queries it any more — the L3 rung selects the
	// collection BY the embedder's model id (core.rungSemantic), so a
	// collection under any other id is never searched and never rebuilt
	// (an embedder writes only its own id). Every model swap used to
	// double the vector footprint because nothing ever dropped the old
	// collection; that is the gap the scheduled maintenance pass closes.
	//
	// An empty keep is a NO-OP: a caller that does not know which
	// collection is live (a server with no embedder wired) must not be able
	// to delete the live one. Returns what the sweep removed.
	DeleteStaleStamps(keep []string) (StaleStampSweep, error)
}

// KV is the generic namespaced key/value store (ontology catalogs, doctor
// state; not a document store).
type KV interface {
	KVSet(namespace, key, value string) error
	KVGet(namespace, key string) (string, bool, error)
}

// Audit is the hash-chained audit ledger (FR parity with the Rust audit/mod.rs).
type Audit interface {
	AuditTail(limit int) ([]AuditEntry, error)
	VerifyAuditChain() (ok bool, brokenAt int64, err error)
}

// Tokens is the DB-backed bearer-token store (Rust auth/tokens.rs parity).
// TokenUpsert and TokenFind take the PLAINTEXT secret and store/match
// only its SHA-256 hash; returned Tokens carry the at-rest hash.
// TokenFind rejects revoked and expired rows with ErrTokenRevoked /
// ErrTokenExpired; TokenRevoke is the soft (revoked_at) counterpart of
// the hard TokenDelete; TokenTouch records a successful authentication's
// last_used_at, coalesced to one write per TokenTouchWindowSecs.
type Tokens interface {
	TokenUpsert(tok Token) error
	TokenList() ([]Token, error)
	TokenDelete(id string) error
	TokenRevoke(id string, at int64) error
	TokenTouch(id string, at int64) error
	TokenFind(secret string) (Token, bool, error)
}

// Identity is enterprise auth (Rust auth/accounts.rs + 004_auth.sql parity):
// accounts, orgs, org memberships and resource ownership. Role POLICY lives in
// internal/auth (hierarchy, bootstrap orgs, ownership precedence); these are
// typed table accesses. Upserts replace the whole row on the primary key, and
// membership writes are keyed on the pair, so a re-add re-roles instead of
// duplicating.
type Identity interface {
	AccountUpsert(a Account) error
	AccountByEmail(email string) (Account, bool, error)
	// AdminExists reports whether any account carries the site-admin marker.
	AdminExists() (bool, error)
	OrgUpsert(o Org) error
	OrgByID(id string) (Org, bool, error)
	OrgsByOwner(ownerAccountID string) ([]Org, error)
	OrgMembershipUpsert(m OrgMember) error
	OrgMemberOf(orgID, accountID string) (OrgMember, bool, error)
	OrgMembers(orgID string) ([]OrgMember, error)
	ResourceClaim(resourceType, resourceID, ownerAccountID, orgID string) error
	IsResourceOwner(resourceType, resourceID, accountID string) (bool, error)
}

// Knowledge is the org/ops knowledge store (Rust db::mod.rs incidents /
// knowledge_entries / service_metadata plus the env-scoped code_elements reads
// behind find_env_conflicts). Typed table access only; validation and the
// aggregation policy live in internal/orgknowledge. See orgknowledge.go for the
// entity types and the env_snapshots ceiling note.
type Knowledge interface {
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
}

// Metrics is the context-metrics ledger (Rust db::record_metric /
// get_metrics_summary / cleanup_old_metrics / reset_metrics over the
// context_metrics table, plus the H10/FR-PLG-8 usage buckets). See
// store_metrics.go for the fold rules, the Rust provenance and the documented
// ceilings.
// RecordMetric no-ops on a read-only backend (Rust's is_read_only guard);
// MetricsSummary windows by retentionDays and takes an optional tool filter
// ("" = every tool); UsageAggregates takes an epoch-second cutoff where 0
// means all time; CleanupMetrics/ResetMetrics return the number of rows
// removed.
type Metrics interface {
	RecordMetric(m Metric) error
	MetricsSummary(tool string, retentionDays int) (MetricSummary, error)
	UsageAggregates(sinceCutoff int64) (UsageAggregates, error)
	CleanupMetrics(retentionDays int) (int64, error)
	ResetMetrics() (int64, error)
}

// Projects is the portfolio registry (issue #376, migration 013): the
// server-side list of registered projects, keyed on the canonical project dir.
// Separate from internal/registry's JSON file — that one is the CLI's per-user
// list of named repos. ProjectUpsert keeps the first registered_at;
// ProjectForget is the inverse and reports whether a row went.
type Projects interface {
	ProjectUpsert(p ProjectRecord) error
	ProjectGet(dir string) (ProjectRecord, bool, error)
	ProjectList() ([]ProjectRecord, error)
	ProjectForget(dir string) (bool, error)
}

// Summaries is the file-summary checkpoint of the LLM-meaning pipeline (issue
// #297, migration 014): SummaryUpsert replaces the row for a path (the row IS
// the resume state); SummaryGet reports it.
type Summaries interface {
	SummaryUpsert(sum FileSummary) error
	SummaryGet(path string) (FileSummary, bool, error)
}
