// SQLite file_summaries methods. Contract in summaries.go; the PostgreSQL
// mirror is pg_summaries.go.
package store

import (
	"database/sql"
	"fmt"
)

// SummaryUpsert writes one file summary row, replacing any previous row for
// the same path (the row IS the checkpoint; rewrites are the resume path).
func (s *Store) SummaryUpsert(sum FileSummary) error {
	if sum.Path == "" {
		return fmt.Errorf("store: summary upsert: path is required")
	}
	if _, err := s.db.Exec(`INSERT INTO file_summaries (`+summaryValueCols+`, updated_at)
		VALUES (?,?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(path) DO UPDATE SET content_hash=excluded.content_hash,
			model=excluded.model, summary=excluded.summary,
			updated_at=excluded.updated_at`,
		sum.Path, sum.ContentHash, sum.Model, sum.Summary); err != nil {
		return fmt.Errorf("store: summary upsert %s: %w", sum.Path, err)
	}
	return nil
}

// SummaryGet returns the stored summary for one path.
func (s *Store) SummaryGet(path string) (FileSummary, bool, error) {
	row := s.db.QueryRow(`SELECT `+summaryReadCols+` FROM file_summaries WHERE path = ?`, path)
	su, err := scanFileSummary(row.Scan)
	if err == sql.ErrNoRows {
		return FileSummary{}, false, nil
	}
	if err != nil {
		return FileSummary{}, false, fmt.Errorf("store: summary get %s: %w", path, err)
	}
	return su, true, nil
}

// SummariesAll lists every stored summary ordered by path.
func (s *Store) SummariesAll() ([]FileSummary, error) {
	rows, err := s.db.Query(`SELECT ` + summaryReadCols + ` FROM file_summaries ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("store: summaries list: %w", err)
	}
	defer rows.Close()
	var out []FileSummary
	for rows.Next() {
		su, err := scanFileSummary(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: summaries list: %w", err)
		}
		out = append(out, su)
	}
	return out, rows.Err()
}
