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

// Backend is the full storage contract of the Go engine: the lifecycle plus
// every role declared in roles.go. The SQLite implementation is *Store
// (internal/store); the PostgreSQL+pgvector implementation is *PGStore.
// Consumers depend on the narrowest role they use; Backend is for composition
// roots (cmd/*, core.New, OpenBackend) and for code that really needs all roles.
type Backend interface {
	// lifecycle
	Close() error
	Path() string // connection target (file path or DSN); for status display
	Migrate(ctx context.Context) error
	AppliedMigrations(ctx context.Context) ([]int, error)
	Engine() string
	Mode() Mode // how the store was opened (RW or RO)

	IndexWriter
	ElementReader
	GraphReader
	Searcher
	Vectors
	EmbedRuns
	Inventories
	Maintainer
	KV
	Audit
	Tokens
	Identity
	Knowledge
	Metrics
	Projects
	Summaries
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
func (s *Store) Elements(ctx context.Context) ([]Element, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+elementCols+` FROM code_elements ce ORDER BY ce.qualified_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanElements(rows)
}

// VectorCoverage reports (covered, orphans) for a model via anti-join.
func (s *Store) VectorCoverage(ctx context.Context, modelID string) (covered, orphans int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT
			COALESCE(SUM(CASE WHEN ce.qualified_name IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN ce.qualified_name IS NULL     THEN 1 ELSE 0 END), 0)
		FROM embedding_vectors v
		LEFT JOIN code_elements ce ON ce.qualified_name = v.qualified_name
		WHERE v.model_id = ?`, modelID).Scan(&covered, &orphans)
	return covered, orphans, err
}

// OpenOptions selects the storage for one project directory.
//
//   - Engine is EngineSQLite (also "") or EnginePostgres.
//   - PGURL is the PostgreSQL DSN; the postgres engine requires it.
//   - DBPath is the caller's explicit SQLite store file (serve's --db flag).
//     When it is empty the LEANKG_DB_PATH / leankg.yaml db.standalone_db_path
//     override is resolved here, so the configured store is what every caller
//     gets, and an empty path never lands on <project>/.leankg/leankg.db while
//     `serve` reads the configured file.
//   - Mode RO never creates the store: a missing SQLite file or PostgreSQL
//     schema is an error and nothing is written.
type OpenOptions struct {
	ProjectDir string
	Engine     string
	PGURL      string
	DBPath     string
	Mode       Mode
}

// OpenBackend opens the storage that opts selects. It is the one exported
// constructor of both engines.
func OpenBackend(ctx context.Context, opts OpenOptions) (Backend, error) {
	switch opts.Engine {
	case "", EngineSQLite:
		dbPath := opts.DBPath
		if dbPath == "" {
			dbPath = StandaloneDBPath(opts.ProjectDir)
		}
		if dbPath != "" {
			return openSQLite(ctx, dbPath, opts.Mode)
		}
		return openSQLite(ctx, opts.ProjectDir+"/.leankg/leankg.db", opts.Mode)
	case EnginePostgres:
		if opts.PGURL == "" {
			return nil, fmt.Errorf("store: postgres engine requires LEANKG_PG_URL")
		}
		return openPG(ctx, opts.PGURL, opts.ProjectDir, opts.Mode)
	default:
		return nil, fmt.Errorf("store: unknown engine %q (want sqlite|postgres)", opts.Engine)
	}
}

// Freshness labels (FR-ZCP-06): the vocabulary every transport uses to say how
// far behind the working tree the index is.
const (
	FreshnessFresh         = "fresh"
	FreshnessPossiblyStale = "possibly_stale"
	FreshnessCold          = "cold"
)

// Freshness derives the index-freshness label from two DB-resident facts: the
// write watermark seq and the seq the last inventory snapshot was computed at.
// An index with no elements is cold; anything whose watermark moved past the
// snapshot — or whose snapshot/watermark cannot be read — is only
// possibly_stale, because the label is a claim about being able to *prove*
// freshness, not proof that the tree changed.
//
// totalElements is a parameter, not a lookup: `status` has already counted it,
// so the shared helper must not add a second query per response.
func Freshness(ctx context.Context, st Backend, totalElements int) string {
	if totalElements == 0 {
		return FreshnessCold
	}
	inv, err := st.LoadInventory(ctx)
	if err != nil || inv == nil {
		return FreshnessPossiblyStale
	}
	seq, _, err := st.Watermark(ctx)
	if err != nil {
		return FreshnessPossiblyStale
	}
	if seq == inv.LastInventorySeq {
		return FreshnessFresh
	}
	return FreshnessPossiblyStale
}
