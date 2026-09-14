// PostgreSQL mirror of the portfolio registry accessors (migration 013;
// projects.go owns the record shape, the upsert semantics and the two-registry
// reconciliation note). The dialect is the only difference: $n placeholders,
// pgx rows, and lower(name) where sqlite spells COLLATE NOCASE.
package store

import "fmt"

// ProjectUpsert registers a project, or refreshes the row keyed on Dir — see
// projects.go for the contract (registered_at survives, last_indexed COALESCEs,
// names are not unique).
func (s *PGStore) ProjectUpsert(p ProjectRecord) error {
	if p.Dir == "" || p.Name == "" || p.RegisteredAt == "" {
		return fmt.Errorf("store: project upsert: dir, name and registered_at are required")
	}
	if s.mode == RO {
		return fmt.Errorf("store: project upsert requires RW mode")
	}
	if _, err := s.pool.Exec(pgCtx, `INSERT INTO projects (`+projectCols+`)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(dir) DO UPDATE SET
			name = excluded.name,
			last_indexed = COALESCE(excluded.last_indexed, projects.last_indexed),
			element_count = excluded.element_count,
			file_count = excluded.file_count`,
		p.Dir, p.Name, p.RegisteredAt, nullString(p.LastIndexed), p.ElementCount, p.FileCount); err != nil {
		return fmt.Errorf("store: project upsert %s: %w", p.Dir, err)
	}
	return nil
}

// ProjectGet reads the row for one canonical project directory.
func (s *PGStore) ProjectGet(dir string) (ProjectRecord, bool, error) {
	row := s.pool.QueryRow(pgCtx, `SELECT `+projectCols+` FROM projects WHERE dir = $1`, dir)
	p, err := scanProject(row.Scan)
	if err != nil {
		if isNoRows(err) {
			return ProjectRecord{}, false, nil
		}
		return ProjectRecord{}, false, fmt.Errorf("store: project get: %w", err)
	}
	return p, true, nil
}

// ProjectList enumerates the registry (see projects.go for the ordering).
func (s *PGStore) ProjectList() ([]ProjectRecord, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT `+projectCols+` FROM projects
		ORDER BY lower(name), dir`)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: project list: %w", err)
	}
	return out, nil
}

// ProjectForget removes the row for one canonical directory and reports
// whether anything went (an unknown dir is a no-op, not an error).
func (s *PGStore) ProjectForget(dir string) (bool, error) {
	if s.mode == RO {
		return false, fmt.Errorf("store: project forget requires RW mode")
	}
	ct, err := s.pool.Exec(pgCtx, `DELETE FROM projects WHERE dir = $1`, dir)
	if err != nil {
		return false, fmt.Errorf("store: project forget %s: %w", dir, err)
	}
	return ct.RowsAffected() > 0, nil
}
