// Package store is the SQLite storage layer of the Go engine: one store file
// per project at <project>/.leankg/leankg.db (same location the Rust engine
// used; SQLite users re-index because CozoDB owned the old file layout).
//
// Design (docs/go-rewrite-analysis.md §6.6):
//   - SQLite WAL mode gives real writer/reader separation: RW handles run with
//     WAL + busy_timeout; readers open mode=ro and never block the writer.
//   - Freshness is DB-resident: every write commit bumps write_watermark.seq.
//     There are no in-memory TTL caches — the per-process cache-race bug class
//     (Rust #350, C4 TOCTOU) is structurally gone.
//   - Vectors are float32 BLOBs with in-process cosine (documented ceiling;
//     upgrade path: sqlite-vec). ModelStamp pins every vector collection.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Mode controls how the store is opened.
type Mode int

const (
	// RW opens read-write with WAL enabled (writer role).
	RW Mode = iota
	// RO opens read-only (reader role); safe alongside a live writer.
	RO
)

// Store is a handle to one project's SQLite store.
type Store struct {
	db   *sql.DB
	mode Mode
	path string
}

// Open opens (creating if needed in RW mode) the store at path.
func Open(path string, mode Mode) (*Store, error) {
	if mode == RO {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("store: read-only open of missing store %s: %w", path, err)
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store: create .leankg dir: %w", err)
	}

	dsn := "file:" + path + "?_pragma=busy_timeout(10000)"
	if mode == RO {
		dsn += "&_pragma=query_only(ON)"
	} else {
		dsn += "&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// SQLite handles are cheap; serialize writes through one conn to keep
	// batch transactions deadlock-free under modernc's driver.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, mode: mode, path: path}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Path returns the store file path.
func (s *Store) Path() string { return s.path }

// Mode reports how the store was opened.
func (s *Store) Mode() Mode { return s.mode }

// String renders the mode for status output.
func (m Mode) String() string {
	if m == RO {
		return "read-only"
	}
	return "read-write"
}

// Migrate applies all pending migrations (RW only). Migrations are applied in
// order and recorded in schema_migrations; each runs inside one transaction.
func (s *Store) Migrate() error {
	if s.mode == RO {
		return fmt.Errorf("store: migrate requires RW mode")
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("store: ensure schema_migrations: %w", err)
	}
	for _, m := range migrations {
		var done int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&done); err != nil {
			return err
		}
		if done == 1 {
			continue
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.ddl); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: migration %03d %s: %w", m.version, m.name, err)
		}
		_, err = tx.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, m.version, m.name)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return fmt.Errorf("store: record migration %03d: %w", m.version, err)
		}
	}
	return s.backfillFTSTerms()
}

// ftsTermsVersion versions what ftsContent writes; bump it when that changes
// so existing stores rebuild their FTS rows once (a schema migration cannot:
// the identifier split is Go code, not SQL).
const ftsTermsVersion = "1"

// backfillFTSTerms rebuilds every elements_fts row with the current
// ftsContent once per ftsTermsVersion (RS-13: identifier-split names). Rows
// written after this point already carry the terms; an unchanged file is
// never re-extracted, so without this pass an existing store would keep the
// old rows until each file changed.
func (s *Store) backfillFTSTerms() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('kv','elements_fts','code_elements')`).Scan(&n); err != nil {
		return err
	}
	if n < 3 {
		return nil // a partial (synthetic upgrade-test) schema: nothing to rebuild
	}
	if v, ok, err := s.KVGet("fts", "terms_version"); err != nil {
		return err
	} else if ok && v == ftsTermsVersion {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(`SELECT id, name, qualified_name, COALESCE(content,'') FROM code_elements`)
	if err != nil {
		return fmt.Errorf("store: fts backfill read: %w", err)
	}
	type row struct {
		id int64
		e  Element
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.e.Name, &r.e.QualifiedName, &r.e.Content); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range all {
		if _, err := tx.Exec(`DELETE FROM elements_fts WHERE rowid = ?`, r.id); err != nil {
			return fmt.Errorf("store: fts backfill delete: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO elements_fts (rowid, name, qualified_name, content) VALUES (?, ?, ?, ?)`,
			r.id, r.e.Name, r.e.QualifiedName, ftsContent(r.e)); err != nil {
			return fmt.Errorf("store: fts backfill insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.KVSet("fts", "terms_version", ftsTermsVersion)
}

// MigrationStep is one embedded schema migration, identified by its
// ordinal version and name.
type MigrationStep struct {
	Version int
	Name    string
}

// Migrations returns the embedded migration list (version + name, oldest
// first) as a read-only view. Doctor --deep and other tooling compare it
// against the applied schema_migrations ledger for drift, without any
// write access to the store.
func Migrations() []MigrationStep {
	out := make([]MigrationStep, 0, len(migrations))
	for _, m := range migrations {
		out = append(out, MigrationStep{Version: m.version, Name: m.name})
	}
	return out
}

// AppliedMigrations returns the versions recorded in the store's
// schema_migrations ledger, ascending. It is the read side of Migrate for
// tooling that reports the applied/pending split (the `leankg migrate` verb);
// doctor --deep reads the same table through its own probes. A store that was
// never migrated fails with the driver's "no such table" error.
func AppliedMigrations(b Backend) ([]int, error) {
	const query = `SELECT version FROM schema_migrations ORDER BY version`
	switch st := b.(type) {
	case *Store:
		rows, err := st.db.Query(query)
		if err != nil {
			return nil, fmt.Errorf("store: applied migrations: %w", err)
		}
		defer rows.Close()
		return collectVersions(rows)
	case *PGStore:
		rows, err := st.pool.Query(pgCtx, query)
		if err != nil {
			return nil, fmt.Errorf("store: applied migrations: %w", err)
		}
		defer rows.Close()
		return collectVersions(rows)
	}
	return nil, fmt.Errorf("store: applied migrations: unknown backend %T", b)
}

// versionRows is the read side shared by database/sql and pgx rows (Close is
// excluded: its signature differs between the two).
type versionRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// collectVersions drains a version column; the caller owns closing rows.
func collectVersions(rows versionRows) ([]int, error) {
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: applied migrations: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: applied migrations: %w", err)
	}
	return out, nil
}
