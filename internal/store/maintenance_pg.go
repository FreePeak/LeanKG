package store

import "fmt"

// Postgres delegates the SQLite maintenance primitives to the server it is
// running on, so one maintenance loop serves both engines (the
// Store <-> PGStore contract in backend.go is method-for-method).
//
// Space is a real probe: pg_database_size reports the whole database, which
// is the closest Postgres analogue of "the file on disk", and FreeBytes is
// reported as 0 because Postgres tracks dead tuples per table and lets
// autovacuum reclaim them without an explicit call — there is no single
// "freelist" number to read.
//
// Checkpoint, IncrementalVacuum, Vacuum and EnsureIncrementalVacuum are
// deliberate no-ops: WAL-log truncation is the server's own setting, dead
// tuples are autovacuum's job, and VACUUM runs on a schedule the operator
// owns. Running them from a client would fight the server's own maintenance
// rather than complement it. The one thing worth doing on demand is ANALYZE,
// which is why Vacuum issues it.
func (s *PGStore) Space() (SpaceReport, error) {
	rep := SpaceReport{Engine: EnginePostgres, Path: s.Path()}
	var size int64
	if err := s.pool.QueryRow(pgCtx,
		`SELECT pg_database_size(current_database())`).Scan(&size); err != nil {
		return rep, fmt.Errorf("store: pg_database_size: %w", err)
	}
	rep.SizeBytes = size
	rep.LiveBytes = size // autovacuum owns dead-tuple reclamation
	return rep, nil
}

// Checkpoint is a no-op: WAL segment recycling is a server configuration
// (wal_keep_size / max_wal_size), not a client-callable operation.
func (s *PGStore) Checkpoint() error { return nil }

// IncrementalVacuum is a no-op: autovacuum reclaims dead tuples without one.
func (s *PGStore) IncrementalVacuum(int) error { return nil }

// Vacuum runs ANALYZE (planner statistics refresh), not VACUUM. It is the
// maintenance-loop hook's only Postgres action: VACUUM would contend with the
// server's autovacuum for the same locks, whereas ANALYZE is cheap,
// non-blocking, and the thing a bulk embed or delete actually leaves stale.
func (s *PGStore) Vacuum() error {
	if s.mode == RO {
		return nil
	}
	if _, err := s.pool.Exec(pgCtx, `ANALYZE`); err != nil {
		return fmt.Errorf("store: analyze: %w", err)
	}
	return nil
}

// EnsureIncrementalVacuum is a no-op: auto_vacuum is a Postgres-wide
// setting, not a per-database one this client can turn on safely.
func (s *PGStore) EnsureIncrementalVacuum() error { return nil }
