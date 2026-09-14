// PostgreSQL file_summaries methods: the mirror of store_summaries.go over
// pgx. Same table shape, same resume semantics; only the placeholders ($n) and
// the timestamp expression differ.
package store

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SummaryUpsert writes one file summary row, replacing any previous row for
// the same path.
func (s *PGStore) SummaryUpsert(sum FileSummary) error {
	if sum.Path == "" {
		return fmt.Errorf("store: summary upsert: path is required")
	}
	if _, err := s.pool.Exec(pgCtx, `INSERT INTO file_summaries (`+summaryValueCols+`, updated_at)
		VALUES ($1,$2,$3,$4,`+pgNow+`)
		ON CONFLICT (path) DO UPDATE SET content_hash=excluded.content_hash,
			model=excluded.model, summary=excluded.summary,
			updated_at=excluded.updated_at`,
		sum.Path, sum.ContentHash, sum.Model, sum.Summary); err != nil {
		return fmt.Errorf("store: summary upsert %s: %w", sum.Path, err)
	}
	return nil
}

// SummaryGet returns the stored summary for one path.
func (s *PGStore) SummaryGet(path string) (FileSummary, bool, error) {
	row := s.pool.QueryRow(pgCtx, `SELECT `+summaryReadCols+` FROM file_summaries WHERE path = $1`, path)
	su, err := scanFileSummary(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return FileSummary{}, false, nil
	}
	if err != nil {
		return FileSummary{}, false, fmt.Errorf("store: summary get %s: %w", path, err)
	}
	return su, true, nil
}

// SummariesAll lists every stored summary ordered by path.
func (s *PGStore) SummariesAll() ([]FileSummary, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT `+summaryReadCols+` FROM file_summaries ORDER BY path`)
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
