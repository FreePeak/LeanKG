package store

import (
	"context"
	"time"
)

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

// IndexWriter persists the code graph of one indexed file set. A cancelled ctx
// rolls back the batch in progress; the batches that already committed stay.
type IndexWriter interface {
	UpsertElements(ctx context.Context, els []Element) error
	UpsertRelationships(ctx context.Context, rels []Relationship) error
	UpsertFiles(ctx context.Context, files []FileRecord) error
	Files(ctx context.Context) ([]FileRecord, error)
	DeleteByFile(ctx context.Context, path string) error
	// ReplaceFileRelationships replaces the edges of relTypes whose SOURCE element
	// belongs to file path with rels, in one transaction. Elements, other edge
	// types, the file record and vectors are untouched. When the stored set already
	// equals rels (source, target, rel_type, confidence) it writes nothing and does
	// not bump the watermark. Caller contract: every rel must be sourced in path and
	// have a type in relTypes.
	ReplaceFileRelationships(ctx context.Context, path string, relTypes []string, rels []Relationship) error
	DeleteFileRecord(ctx context.Context, path string) error
}

// ElementReader reads indexed elements and their counts.
type ElementReader interface {
	FindExact(ctx context.Context, name string) ([]Element, error)
	ElementCount(ctx context.Context) (int, error)
	RelationshipCount(ctx context.Context) (int, error)
	ElementsByType(ctx context.Context) (map[string]int, error)
	FileCount(ctx context.Context) (int, error)
	// Elements enumerates every indexed element ordered by qualified_name
	// (embed pipeline scan + parity fixtures).
	Elements(ctx context.Context) ([]Element, error)
}

// GraphReader reads relationship edges (graph traversal seeds; pure-Go BFS
// lives in internal/graph).
type GraphReader interface {
	Outgoing(ctx context.Context, source string) ([]Relationship, error)
	Incoming(ctx context.Context, target string) ([]Relationship, error)
	RelationshipsAll(ctx context.Context, limit int) ([]Relationship, error)
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

// VectorQuery is one vector search: the K nearest vectors of ModelID among the
// elements Filter admits. The filter applies before K, so a filter that
// excludes most of the collection still returns K hits when K admitted rows
// exist.
type VectorQuery struct {
	ModelID string
	Vec     []float32
	K       int
	Filter  ElementFilter
}

// HybridQuery is one fused search. Text feeds the keyword and name arms and
// Vec the vector arm; an empty one skips its arm. Limit <= 0 means 50. Weights
// scales the fusion: the zero value weighs every arm at 1, and
// DefaultFuseWeights is the store default. Filter applies to every arm before
// that arm's LIMIT, so no arm spends its window on excluded rows.
type HybridQuery struct {
	ModelID string
	Text    string
	Vec     []float32
	Limit   int
	Filter  ElementFilter
	Weights FuseRRFWeights
}

// Searcher ranks elements by keyword and by vector similarity.
type Searcher interface {
	// FindFuzzy is the keyword arm. It returns the hits and the arm that served
	// them (ArmFTS5 on SQLite; ArmTSVector, ArmTrigram or ArmILIKE on PostgreSQL).
	FindFuzzy(ctx context.Context, query string, limit int) ([]FuzzyMatch, Arm, error)
	// SearchVectors is the vector arm (see VectorQuery).
	SearchVectors(ctx context.Context, q VectorQuery) ([]VectorSearchHit, error)
	// Hybrid fuses the vector, keyword and name arms of q by reciprocal rank
	// fusion. The returned Arm is the keyword arm that contributed: ArmFTS5 on
	// SQLite; on PostgreSQL the first of ArmTSVector, ArmTrigram and ArmILIKE
	// that contributed. It is empty when no keyword arm did. A cancelled ctx
	// is an error, never a vector-only answer.
	Hybrid(ctx context.Context, q HybridQuery) ([]HybridHit, Arm, error)
}

// Vectors stores embedding rows, their per-model stamps and their state.
type Vectors interface {
	WriteStamp(ctx context.Context, st ModelStamp) error
	Stamp(ctx context.Context, modelID string) (*ModelStamp, error)
	Stamps(ctx context.Context) ([]ModelStamp, error)
	EmbeddingStateMap(ctx context.Context, modelID string) (map[string]string, error)
	SetEmbeddingStates(ctx context.Context, modelID string, states map[string]string) error
	UpsertVectors(ctx context.Context, modelID string, rows []VectorRow) error
	// ReplaceFileVectors atomically replaces one file's embedding rows for a
	// model: delete + insert + state in one transaction (issue #279 per-file
	// atomic replace). rows must carry the file's complete vector set.
	ReplaceFileVectors(ctx context.Context, modelID string, rows []VectorRow, states map[string]string) error
	ClearVectors(ctx context.Context, modelID string) error
	VectorCount(ctx context.Context, modelID string) (int, error)
	// VectorCoverage reports (covered, orphans): vector rows whose QN still
	// has a live element vs. rows whose QN disappeared.
	VectorCoverage(ctx context.Context, modelID string) (covered, orphans int, err error)
}

// EmbedRuns is the embed-pipeline run ledger (status reads it; no in-process
// coupling).
type EmbedRuns interface {
	StartEmbedRun(ctx context.Context, modelID, mode string, dirty int) (int64, error)
	FinishEmbedRun(ctx context.Context, id int64, status string, embedded, skipped, failed, truncations, orphans int) error
	LastEmbedRunAny(ctx context.Context) (*EmbedRun, error)
}

// Inventories is the inventory snapshot and the freshness watermark. It is
// named Inventories because Inventory is the snapshot struct in this package.
type Inventories interface {
	// freshness watermark (DB-resident; readers never bump)
	Watermark(ctx context.Context) (seq, at int64, err error)
	BumpWatermark(ctx context.Context) error
	SaveInventory(ctx context.Context, inv Inventory) error
	LoadInventory(ctx context.Context) (*Inventory, error)
}

// Maintainer owns space accounting, orphan sweeps and the maintenance
// operations (the reclaim half of DeleteByFile/gc: those return rows to the
// freelist, these return the freelist to the filesystem). Every write here
// refuses a read-only handle with ErrReadOnly.
type Maintainer interface {
	// Space is a no-write probe, safe on a read-only handle. Postgres
	// implements it with pg_database_size.
	Space(ctx context.Context) (SpaceReport, error)
	// Maintain runs one engine-neutral maintenance op (see MaintainKind).
	// Each engine runs the kinds it supports and reports the rest through
	// MaintainResult.Skipped, so the maintenance loop is engine-agnostic.
	Maintain(ctx context.Context, op MaintainOp) (MaintainResult, error)
	// PruneEmbedRuns trims the embed-run ledger: it closes out the runs
	// still marked "running" past the cutoff (a run that was killed
	// mid-flight otherwise claims to be running forever) and deletes the
	// finished runs older than it. The newest row is never deleted — it
	// names the store's current collection (CurrentModelID).
	PruneEmbedRuns(ctx context.Context, cutoff time.Time) (EmbedRunPrune, error)
	// DeleteOrphanRelationships purges edges whose source or target is neither
	// a live element nor a file in code_files. A file is an endpoint of its own
	// edges (a Kotlin WorkManager request names its worker from the file, which
	// has no element), so those edges live while the file does and go when it
	// is deleted. It runs after every index pass that changed a file (edges into
	// a deleted file, and edges to a target no extractor made an element or file
	// for), as well as in maintain and `leankg gc`. Returns the number of edges
	// removed.
	DeleteOrphanRelationships(ctx context.Context) (int, error)
	// DeleteOrphanVectors purges embedding_vectors + embedding_state rows
	// whose qualified_name no longer belongs to any element, across every
	// model collection (a dangling QN dangles in all of them). This is the
	// residue `leankg-embed` reports as Orphans: DeleteByFile owns elements
	// and edges, so until #411 nothing owned vector rows, and every deleted
	// file left its vectors behind forever. Returns the number of vector
	// rows removed.
	DeleteOrphanVectors(ctx context.Context) (int, error)
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
	DeleteStaleStamps(ctx context.Context, keep []string) (StaleStampSweep, error)
}

// KV is the generic namespaced key/value store (ontology catalogs, doctor
// state; not a document store).
type KV interface {
	KVSet(ctx context.Context, namespace, key, value string) error
	KVGet(ctx context.Context, namespace, key string) (string, bool, error)
}

// Audit is the hash-chained audit ledger (FR parity with the Rust audit/mod.rs).
type Audit interface {
	AppendAudit(ctx context.Context, e *AuditEntry) error
	AuditTail(ctx context.Context, limit int) ([]AuditEntry, error)
	VerifyAuditChain(ctx context.Context) (ok bool, brokenAt int64, err error)
}

// Tokens is the DB-backed bearer-token store (Rust auth/tokens.rs parity).
// TokenUpsert and TokenFind take the PLAINTEXT secret and store/match
// only its SHA-256 hash; returned Tokens carry the at-rest hash.
// TokenFind rejects revoked and expired rows with ErrTokenRevoked /
// ErrTokenExpired; TokenRevoke is the soft (revoked_at) counterpart of
// the hard TokenDelete; TokenTouch records a successful authentication's
// last_used_at, coalesced to one write per TokenTouchWindowSecs.
type Tokens interface {
	TokenUpsert(ctx context.Context, tok Token) error
	TokenList(ctx context.Context) ([]Token, error)
	TokenDelete(ctx context.Context, id string) error
	TokenRevoke(ctx context.Context, id string, at int64) error
	TokenTouch(ctx context.Context, id string, at int64) error
	TokenFind(ctx context.Context, secret string) (Token, bool, error)
}

// Identity is enterprise auth (Rust auth/accounts.rs + 004_auth.sql parity):
// accounts, orgs, org memberships and resource ownership. Role POLICY lives in
// internal/auth (hierarchy, bootstrap orgs, ownership precedence); these are
// typed table accesses. Upserts replace the whole row on the primary key, and
// membership writes are keyed on the pair, so a re-add re-roles instead of
// duplicating.
type Identity interface {
	AccountUpsert(ctx context.Context, a Account) error
	AccountByEmail(ctx context.Context, email string) (Account, bool, error)
	// AccountByID reports the account with the given id. A token minted for an
	// account resolves through it, so a disabled account stops its tokens.
	AccountByID(ctx context.Context, id string) (Account, bool, error)
	// AdminExists reports whether any account carries the site-admin marker.
	AdminExists(ctx context.Context) (bool, error)
	OrgUpsert(ctx context.Context, o Org) error
	OrgByID(ctx context.Context, id string) (Org, bool, error)
	OrgsByOwner(ctx context.Context, ownerAccountID string) ([]Org, error)
	OrgMembershipUpsert(ctx context.Context, m OrgMember) error
	OrgMemberOf(ctx context.Context, orgID, accountID string) (OrgMember, bool, error)
	OrgMembers(ctx context.Context, orgID string) ([]OrgMember, error)
	ResourceClaim(ctx context.Context, resourceType, resourceID, ownerAccountID, orgID string) error
	IsResourceOwner(ctx context.Context, resourceType, resourceID, accountID string) (bool, error)
}

// Knowledge is the org/ops knowledge store (Rust db::mod.rs incidents /
// knowledge_entries / service_metadata plus the env-scoped code_elements reads
// behind find_env_conflicts). Typed table access only; validation and the
// aggregation policy live in internal/orgknowledge. See orgknowledge.go for the
// entity types and the env_snapshots ceiling note.
type Knowledge interface {
	IncidentUpsert(ctx context.Context, inc Incident) error
	IncidentByID(ctx context.Context, id string) (Incident, bool, error)
	IncidentDelete(ctx context.Context, id string) error
	IncidentsQuery(ctx context.Context, q IncidentQuery) ([]Incident, error)
	KnowledgeEntryUpsert(ctx context.Context, e KnowledgeEntry) error
	KnowledgeEntryByID(ctx context.Context, id string) (KnowledgeEntry, bool, error)
	KnowledgeEntryDelete(ctx context.Context, id string) error
	KnowledgeEntriesByElement(ctx context.Context, qualifiedName string) ([]KnowledgeEntry, error)
	KnowledgeEntriesByFeature(ctx context.Context, featureID string) ([]KnowledgeEntry, error)
	KnowledgeEntriesByEnvironment(ctx context.Context, environment string, limit int) ([]KnowledgeEntry, error)
	KnowledgeEntriesSearch(ctx context.Context, query, knowledgeType, environment string, limit int) ([]KnowledgeEntry, error)
	// ServiceMetadataUpsert writes one service profile (the write side of
	// ServiceMetadataGet; kept in the role for the org-knowledge seeding path).
	ServiceMetadataUpsert(ctx context.Context, m ServiceMetadata) error
	ServiceMetadataGet(ctx context.Context, service, env string) (ServiceMetadata, bool, error)
	// ServiceMetadataAll lists the profiles in one environment, ordered by
	// service name (the Rust get_all_service_metadata read behind the team
	// map).
	ServiceMetadataAll(ctx context.Context, env string) ([]ServiceMetadata, error)
	EnvSnapshotsPut(ctx context.Context, snaps []EnvSnapshot) error
	EnvSnapshotGet(ctx context.Context, env, qualifiedName string) (EnvSnapshot, bool, error)
	// EnvSnapshotConflictsWith lists conflicts_with relationships whose
	// source or target matches `needle` case-insensitively (substring, like
	// the Rust regex_matches(lowercase(...), ".*needle.*") report).
	EnvSnapshotConflictsWith(ctx context.Context, needle string, limit int) ([]Relationship, error)
	// EnvSnapshotEnvVariants lists qualified names having snapshots in more
	// than one environment, case-insensitively containing `needle`.
	EnvSnapshotEnvVariants(ctx context.Context, needle string, limit int) ([]EnvVariant, error)
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
	RecordMetric(ctx context.Context, m Metric) error
	MetricsSummary(ctx context.Context, tool string, retentionDays int) (MetricSummary, error)
	UsageAggregates(ctx context.Context, sinceCutoff int64) (UsageAggregates, error)
	CleanupMetrics(ctx context.Context, retentionDays int) (int64, error)
	ResetMetrics(ctx context.Context) (int64, error)
}

// Projects is the portfolio registry (issue #376, migration 013): the
// server-side list of registered projects, keyed on the canonical project dir.
// Separate from internal/registry's JSON file — that one is the CLI's per-user
// list of named repos. ProjectUpsert keeps the first registered_at;
// ProjectForget is the inverse and reports whether a row went.
type Projects interface {
	ProjectUpsert(ctx context.Context, p ProjectRecord) error
	ProjectGet(ctx context.Context, dir string) (ProjectRecord, bool, error)
	ProjectList(ctx context.Context) ([]ProjectRecord, error)
	ProjectForget(ctx context.Context, dir string) (bool, error)
}

// Summaries is the file-summary checkpoint of the LLM-meaning pipeline (issue
// #297, migration 014): SummaryUpsert replaces the row for a path (the row IS
// the resume state); SummaryGet reports it.
type Summaries interface {
	SummaryUpsert(ctx context.Context, sum FileSummary) error
	SummaryGet(ctx context.Context, path string) (FileSummary, bool, error)
}
