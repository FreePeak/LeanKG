package store

import (
	"context"
	"fmt"
)

// Postgres delegates the SQLite maintenance primitives to the server it is
// running on, so one maintenance loop serves both engines (the
// Store <-> PGStore contract in backend.go is method-for-method).
//
// Space is a real probe: pg_database_size reports the whole database, which
// is the closest Postgres analogue of "the file on disk", and FreeBytes is
// reported as 0 because Postgres tracks dead tuples per table and lets
// autovacuum reclaim them without an explicit call — there is no single
// "freelist" number to read.
func (s *PGStore) Space(ctx context.Context) (SpaceReport, error) {
	rep := SpaceReport{Engine: EnginePostgres, Path: s.Path()}
	var size int64
	if err := s.pool.QueryRow(ctx,
		`SELECT pg_database_size(current_database())`).Scan(&size); err != nil {
		return rep, fmt.Errorf("store: pg_database_size: %w", err)
	}
	rep.SizeBytes = size
	rep.LiveBytes = size // autovacuum owns dead-tuple reclamation
	return rep, nil
}

// Maintain runs one maintenance op on PostgreSQL. Only Analyze runs: the
// server owns WAL recycling, dead-tuple reclamation and the keyword index (a
// GIN-indexed tsvector column, not a segment list), so those kinds are
// reported as skipped rather than silently succeeding. A read-only handle
// refuses every kind with ErrReadOnly.
//
// Analyze is ANALYZE (planner statistics refresh), not VACUUM: VACUUM would
// contend with the server's autovacuum for the same locks, whereas ANALYZE is
// cheap, non-blocking, and the thing a bulk embed or delete leaves stale.
func (s *PGStore) Maintain(ctx context.Context, op MaintainOp) (MaintainResult, error) {
	if err := requireWritable(s.mode); err != nil {
		return MaintainResult{}, err
	}
	// A cancelled op never reports success, even for the kinds PostgreSQL skips.
	if err := ctx.Err(); err != nil {
		return MaintainResult{}, err
	}
	switch op.Kind {
	case MaintainUpgrade:
		// auto_vacuum is a server-wide setting this client cannot turn on.
		return MaintainResult{Skipped: skipAutovacuum}, nil
	case MaintainCheckpoint:
		// WAL segment recycling is a server configuration (wal_keep_size,
		// max_wal_size), not a client-callable operation.
		return MaintainResult{Skipped: skipServerWAL}, nil
	case MaintainReclaim, MaintainReclaimFull:
		// Autovacuum reclaims dead tuples; VACUUM runs on a schedule the
		// operator owns.
		return MaintainResult{Skipped: skipAutovacuum}, nil
	case MaintainCompactKeywordIndex:
		// Nothing to merge: PostgreSQL's own autovacuum reclaims what the
		// index churn leaves behind.
		return MaintainResult{Skipped: skipNoKeywordSegments}, nil
	case MaintainAnalyze:
		if _, err := s.pool.Exec(ctx, `ANALYZE`); err != nil {
			return MaintainResult{}, fmt.Errorf("store: analyze: %w", err)
		}
		return MaintainResult{}, nil
	}
	return MaintainResult{}, errUnknownMaintainKind(op.Kind)
}
