// Portfolio registry storage (issue #376): the typed table access over the
// `projects` rows created by migration 013. The PostgreSQL mirror is
// pg_projects.go; both share the record shape declared here.
//
// POLICY lives in internal/portfolioreg (canonicalization, the hot-set cap,
// the fan-out merge, the manifest). This file is one table and four
// statements — the same split as store_accounts.go versus internal/auth.
//
// Two registries exist in the Go engine on purpose, and they answer to
// different owners:
//
//   - internal/registry — ~/.leankg/registry.json, the CLI's local list of
//     NAMED repositories (leankg register <name>), per-user and per-host.
//   - this table — the server-side registry a portfolio query fans out over.
//     It is keyed on the canonical project DIRECTORY (the same identity the
//     PG backend hashes into its schema-per-project name), which is what makes
//     a fleet query addressable at org scale: several users sharing one
//     Postgres see one registry, and a register name never has to match
//     between them.
package store

import (
	"database/sql"
	"fmt"
)

// ProjectRecord is one registered project. Dir is the canonical absolute
// project directory and the primary key; RegisteredAt is the FIRST
// registration's timestamp (a re-register keeps it), and LastIndexed is nil
// until an index run stamps the row — the same JSON-null shape
// registry.RepoEntry carries. ElementCount/FileCount are the last stamped
// totals, which is what lets `leankg projects` answer a manifest WITHOUT
// opening any project store.
type ProjectRecord struct {
	Dir          string  `json:"dir"`
	Name         string  `json:"name"`
	RegisteredAt string  `json:"registered_at"`
	LastIndexed  *string `json:"last_indexed"`
	ElementCount int     `json:"element_count"`
	FileCount    int     `json:"file_count"`
}

const projectCols = `dir, name, registered_at, last_indexed, element_count, file_count`

// ProjectUpsert registers a project, or refreshes the row keyed on Dir:
// name and the two counts are replaced, last_indexed is COALESCEd (re-registering
// with no new stamp never erases a known one), and registered_at keeps the
// FIRST registration.
//
// Names are NOT unique (two checkouts may legitimately be called "api"), so
// name resolution is the reader's job and reports ambiguity rather than picking
// one — internal/projects.Router.byName is the precedent.
func (s *Store) ProjectUpsert(p ProjectRecord) error {
	if p.Dir == "" || p.Name == "" || p.RegisteredAt == "" {
		return fmt.Errorf("store: project upsert: dir, name and registered_at are required")
	}
	if s.mode == RO {
		return fmt.Errorf("store: project upsert requires RW mode")
	}
	_, err := s.db.Exec(`INSERT INTO projects (`+projectCols+`)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(dir) DO UPDATE SET
			name = excluded.name,
			last_indexed = COALESCE(excluded.last_indexed, projects.last_indexed),
			element_count = excluded.element_count,
			file_count = excluded.file_count`,
		p.Dir, p.Name, p.RegisteredAt, nullString(p.LastIndexed), p.ElementCount, p.FileCount)
	if err != nil {
		return fmt.Errorf("store: project upsert %s: %w", p.Dir, err)
	}
	return nil
}

// ProjectGet reads the row for one canonical project directory.
func (s *Store) ProjectGet(dir string) (ProjectRecord, bool, error) {
	row := s.db.QueryRow(`SELECT `+projectCols+` FROM projects WHERE dir = ?`, dir)
	p, err := scanProject(row.Scan)
	if err != nil {
		if isNoRows(err) {
			return ProjectRecord{}, false, nil
		}
		return ProjectRecord{}, false, fmt.Errorf("store: project get: %w", err)
	}
	return p, true, nil
}

// ProjectList enumerates the registry, name-ordered case-insensitively with
// Dir as the tie-break, so identically named projects still list
// deterministically.
func (s *Store) ProjectList() ([]ProjectRecord, error) {
	rows, err := s.db.Query(`SELECT ` + projectCols + ` FROM projects
		ORDER BY name COLLATE NOCASE, dir`)
	if err != nil {
		return nil, fmt.Errorf("store: project list: %w", err)
	}
	defer rows.Close()
	var out []ProjectRecord
	for rows.Next() {
		p, err := scanProject(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: project list: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProjectForget removes the row for one canonical directory and reports
// whether anything went (an unknown dir is a no-op, not an error).
func (s *Store) ProjectForget(dir string) (bool, error) {
	if s.mode == RO {
		return false, fmt.Errorf("store: project forget requires RW mode")
	}
	res, err := s.db.Exec(`DELETE FROM projects WHERE dir = ?`, dir)
	if err != nil {
		return false, fmt.Errorf("store: project forget %s: %w", dir, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// scanProject decodes one projects row through either backend's Scan. NULL
// text columns read back as "" and a NULL last_indexed stays a nil *string.
func scanProject(scan func(dest ...any) error) (ProjectRecord, error) {
	var (
		dir, name, registeredAt, lastIndexed sql.NullString
		elementCount, fileCount              int
	)
	if err := scan(&dir, &name, &registeredAt, &lastIndexed, &elementCount, &fileCount); err != nil {
		return ProjectRecord{}, err
	}
	return ProjectRecord{
		Dir: dir.String, Name: name.String, RegisteredAt: registeredAt.String,
		LastIndexed: nullStringPtr(lastIndexed), ElementCount: elementCount, FileCount: fileCount,
	}, nil
}
