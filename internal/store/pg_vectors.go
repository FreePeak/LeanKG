package store

// PGStore per-file vector writes (issue #279 remainder). The stamp read/write
// and batch upsert methods live in store_pg.go; this file carries the
// per-file atomic replace that the embed pipeline's live path calls.

import (
	"context"
	"fmt"

	"github.com/pgvector/pgvector-go"
)

// ReplaceFileVectors atomically replaces one file's embedding rows for a
// model: the vectors of the qualified names in `rows` are deleted and
// re-inserted together with their embedding_state rows, in ONE transaction.
// Same per-file atomic unit as the sqlite implementation (see
// Store.ReplaceFileVectors); here the transaction is a pooled pgx tx.
func (s *PGStore) ReplaceFileVectors(ctx context.Context, modelID string, rows []VectorRow, states map[string]string) error {
	if len(rows) == 0 {
		return fmt.Errorf("store: ReplaceFileVectors %s: no vectors", modelID)
	}
	vec := s.vecTable(modelID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, r := range rows {
		if _, err := tx.Exec(ctx, `DELETE FROM `+vec+` WHERE qualified_name = $1`, r.QualifiedName); err != nil {
			return fmt.Errorf("store: replace vector %s: %w", r.QualifiedName, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+vec+` (qualified_name, vec) VALUES ($1, $2::vector)`,
			r.QualifiedName, pgvector.NewVector(r.Vec)); err != nil {
			return fmt.Errorf("store: replace vector %s: %w", r.QualifiedName, err)
		}
	}
	for qn, h := range states {
		if _, err := tx.Exec(ctx, `INSERT INTO `+s.stateTable(modelID)+` (qualified_name, content_hash, state, embedded_at)
			VALUES ($1,$2,'embedded', now())
			ON CONFLICT (qualified_name) DO UPDATE SET content_hash=excluded.content_hash,
				state='embedded', embedded_at=excluded.embedded_at`, qn, h); err != nil {
			return fmt.Errorf("store: replace embedding state %s: %w", qn, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.BumpWatermark(context.WithoutCancel(ctx))
}
