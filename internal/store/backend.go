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
	Migrate() error
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
//
// dbPath is the caller's explicit SQLite store path (serve's --db flag). When
// it is empty the LEANKG_DB_PATH / leankg.yaml db.standalone_db_path override
// is resolved HERE, so the configured store is what every caller gets — a
// verb passing "" no longer silently lands on <project>/.leankg/leankg.db
// while `serve` reads the configured file.
func OpenBackend(ctx context.Context, projectDir, engine, pgURL, dbPath string, mode Mode) (Backend, error) {
	switch engine {
	case "", EngineSQLite:
		if dbPath == "" {
			dbPath = StandaloneDBPath(projectDir)
		}
		if dbPath != "" {
			return Open(dbPath, mode)
		}
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
func Freshness(st Backend, totalElements int) string {
	if totalElements == 0 {
		return FreshnessCold
	}
	inv, err := st.LoadInventory()
	if err != nil || inv == nil {
		return FreshnessPossiblyStale
	}
	seq, _, err := st.Watermark()
	if err != nil {
		return FreshnessPossiblyStale
	}
	if seq == inv.LastInventorySeq {
		return FreshnessFresh
	}
	return FreshnessPossiblyStale
}
