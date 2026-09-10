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
	return nil
}
