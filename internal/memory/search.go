package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// openFTS opens (creating if needed) the FTS5 side index at path and applies
// its schema. It mirrors the store's DSN style: busy_timeout plus WAL, one
// connection so writes serialize.
func openFTS(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("memory: create root: %w", err)
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("memory: open FTS index %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	// modernc.org/sqlite builds FTS5 in; these statements fail as one
	// ErrNoRows-shaped nothing if not, which the round-trip test catches.
	for _, stmt := range []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS memory_index USING fts5(path, body, tokenize='unicode61')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("memory: init FTS schema: %w", err)
		}
	}
	return db, nil
}

// reindex replaces a file's rows in the FTS index. One row per non-empty
// line, path-keyed; called after every successful write.
func (m *Memory) reindex(key string) error {
	if err := m.deleteRows(key); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(m.real, filepath.FromSlash(key)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	tx, err := m.fts.Begin()
	if err != nil {
		return err
	}
	for i, line := range strings.Split(string(data), "\n") {
		if i > 10000 {
			break // index the head; whole-file BLOBs are a W2 upgrade
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO memory_index(path, body) VALUES (?, ?)`, key, line); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// deleteRows removes all index rows for one path.
func (m *Memory) deleteRows(key string) error {
	if _, err := m.fts.Exec(`DELETE FROM memory_index WHERE path = ?`, key); err != nil {
		return err
	}
	return nil
}

// Hit is one FTS search result.
type Hit struct {
	Path    string
	Snippet string
	Score   float64
}

// Search queries across all memory files and returns up to limit hits
// ordered by relevance. limit <= 0 means 10.
func (m *Memory) Search(query string, limit int) ([]Hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := m.fts.Query(`
		SELECT path, body, bm25(memory_index) AS score
		FROM memory_index
		WHERE memory_index MATCH ?
		ORDER BY score
		LIMIT ?`, ftsQuery(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Path, &h.Snippet, &h.Score); err != nil {
			return nil, err
		}
		h.Score = -h.Score // bm25() returns negative-better
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// ftsQuery converts a plain string into a benign FTS5 MATCH expression:
// quoted phrases for each token, ANDed. Bare user input would let syntax
// errors (e.g. "a(") abort the query.
func ftsQuery(query string) string {
	toks := strings.Fields(query)
	if len(toks) == 0 {
		return `""`
	}
	parts := make([]string, 0, len(toks))
	for _, t := range toks {
		t = strings.ReplaceAll(t, `"`, "") // raw quotes would break the FTS5 string literal
		if t != "" {
			parts = append(parts, `"`+t+`"`)
		}
	}
	return strings.Join(parts, " AND ")
}

// SpaceReport is the FTS side index's on-disk footprint: total bytes, the
// freelist pages a delete left behind, and the -wal sidecar. It mirrors
// store.SpaceReport so one caller can report both layers in one shape.
type SpaceReport struct {
	Path      string
	SizeBytes int64
	FreeBytes int64
	LiveBytes int64
	PageSize  int64
	WALBytes  int64
}

// Compact reclaims the FTS index's free pages and truncates its WAL sidecar.
//
// Every memory write replaces a path's rows (deleteRows + reindex) — the same
// freelist growth the graph store has, and unbounded here because nothing in
// this package ever compacted. On a long-lived memory server that is a slow
// leak in <root>/index.db and its -wal sidecar, which no amount of Markdown
// editing gives back.
//
// The auto_vacuum upgrade is here for the same reason as in store: SQLite
// adopts a new header mode only while a VACUUM rewrites the file, so the
// request and the VACUUM are both required, and both run at most once, ever.
//
// Safe on a live memory: it takes this handle's own write connection (one per
// Memory, MaxOpenConns(1)) and no other.
func (m *Memory) Compact() error {
	var mode int64
	if err := m.fts.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		// An index too old to answer the pragma is not a reason to skip the
		// reclaim; fall through to the vacuum.
		mode = 0
	}
	if mode == 0 {
		if _, err := m.fts.Exec(`PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
			return fmt.Errorf("memory: request incremental vacuum: %w", err)
		}
		if _, err := m.fts.Exec(`VACUUM`); err != nil {
			return fmt.Errorf("memory: enable incremental vacuum: %w", err)
		}
	}
	if _, err := m.fts.Exec(`PRAGMA incremental_vacuum`); err != nil {
		return fmt.Errorf("memory: incremental vacuum: %w", err)
	}
	if _, err := m.fts.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("memory: wal_checkpoint(TRUNCATE): %w", err)
	}
	return nil
}

// Space reports the FTS side index's footprint. Like store.Space it is a
// no-write probe, and it reports page_count rather than the stat size: in WAL
// mode a mass delete frees pages into the -wal sidecar, and the main file only
// shrinks once that sidecar is checkpointed back — which is what Compact does.
func (m *Memory) Space() (SpaceReport, error) {
	rep := SpaceReport{Path: m.ftsPath}
	if fi, err := os.Stat(m.ftsPath); err == nil {
		rep.SizeBytes = fi.Size()
	}
	if fi, err := os.Stat(m.ftsPath + "-wal"); err == nil {
		rep.WALBytes = fi.Size()
	}
	var pageSize, pageCount, freelist int64
	if err := m.fts.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return rep, fmt.Errorf("memory: page_size: %w", err)
	}
	if err := m.fts.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		return rep, fmt.Errorf("memory: page_count: %w", err)
	}
	if err := m.fts.QueryRow(`PRAGMA freelist_count`).Scan(&freelist); err != nil {
		return rep, fmt.Errorf("memory: freelist_count: %w", err)
	}
	rep.PageSize = pageSize
	rep.FreeBytes = freelist * pageSize
	rep.SizeBytes = pageCount * pageSize
	if rep.SizeBytes < rep.FreeBytes {
		rep.SizeBytes = rep.FreeBytes
	}
	rep.LiveBytes = rep.SizeBytes - rep.FreeBytes
	return rep, nil
}
