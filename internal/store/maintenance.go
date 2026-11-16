package store

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// SpaceReport is the on-disk footprint of one store, plus the bytes SQLite
// holds on its freelist — pages freed by deletes that no vacuum has returned
// to the filesystem. It is the read side of the maintenance pass
// (internal/maintain): the agent-visible `status` payload and
// `leankg doctor --deep` both report it, so bloat is discoverable without
// shelling out to the sqlite3 CLI.
type SpaceReport struct {
	Engine    string `json:"engine"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	// FreeBytes is the freelist: space the database owns but no row uses.
	FreeBytes int64 `json:"free_bytes"`
	// LiveBytes is SizeBytes-FreeBytes: what the file weighs once the
	// freelist is returned, i.e. the most a maintenance pass can reclaim.
	LiveBytes int64 `json:"live_bytes"`
	// PageSize is the store's page size (SQLite only; 0 elsewhere).
	PageSize int64 `json:"page_size,omitempty"`
	// WALBytes is the size of the -wal sidecar (SQLite only; 0 elsewhere).
	WALBytes int64 `json:"wal_bytes,omitempty"`
}

// BloatFraction reports how much of the store file is freelist. Zero is a
// clean store; 0.5 means half the file is dead space.
func (r SpaceReport) BloatFraction() float64 {
	if r.SizeBytes <= 0 {
		return 0
	}
	return float64(r.FreeBytes) / float64(r.SizeBytes)
}

// ErrReadOnly is the one refusal every write on a read-only handle returns:
// the maintenance ops (Maintain) and the Maintainer role's sweeps.
var ErrReadOnly = errors.New("store: read-only handle refuses writes")

// requireWritable is the single read-only check. Every Maintainer write calls
// it before touching the engine.
func requireWritable(mode Mode) error {
	if mode == RO {
		return ErrReadOnly
	}
	return nil
}

// MaintainKind names one engine-neutral maintenance operation. Each engine runs
// the kinds it supports and reports the rest through MaintainResult.Skipped.
type MaintainKind string

const (
	// MaintainCheckpoint flushes the WAL into the main database file and
	// truncates the -wal sidecar to zero.
	MaintainCheckpoint MaintainKind = "checkpoint"
	// MaintainUpgrade is the one-time auto_vacuum header upgrade that lets the
	// store return space at all. On SQLite it rewrites the whole file once; a
	// failure is fatal to the caller. It is a no-op on PostgreSQL.
	MaintainUpgrade MaintainKind = "upgrade"
	// MaintainReclaim hands free pages back to the filesystem, at most
	// MaintainOp.MaxPages per call, so a live server holds the write lock only
	// for a bounded pass.
	MaintainReclaim MaintainKind = "reclaim"
	// MaintainReclaimFull rewrites the whole database file, returning every
	// free page in one pass.
	MaintainReclaimFull MaintainKind = "reclaim-full"
	// MaintainCompactKeywordIndex merges the keyword index segments.
	MaintainCompactKeywordIndex MaintainKind = "compact-keyword-index"
	// MaintainAnalyze refreshes planner statistics.
	MaintainAnalyze MaintainKind = "analyze"
)

// Skip reasons. A skipped op did no work and does not count as done.
const (
	skipServerWAL         = "wal-server-managed"
	skipAutovacuum        = "autovacuum-managed"
	skipNoKeywordSegments = "no-keyword-segments"
	skipSQLiteAnalyze     = "not-run-by-sqlite"
)

// MaintainOp is one maintenance request.
type MaintainOp struct {
	Kind MaintainKind
	// MaxPages caps one MaintainReclaim call; <= 0 means every free page.
	// The other kinds ignore it.
	MaxPages int
}

// MaintainResult reports what an engine did with an op.
type MaintainResult struct {
	// Skipped names why the engine did not run the op; empty when it ran.
	Skipped string
}

func errUnknownMaintainKind(kind MaintainKind) error {
	return fmt.Errorf("store: unknown maintenance kind %q", kind)
}

// SQLite Maintain.
//
// Checkpoint uses TRUNCATE on purpose: it is the only mode that resets the
// -wal sidecar to zero. Without a checkpoint a long-lived server's WAL grows
// until one happens to run, and an unclean shutdown leaves it large on disk.
//
// Upgrade enables auto_vacuum=INCREMENTAL once per store (ensureIncrementalVacuum).
//
// Reclaim is bounded so a live serving database caps how long the write lock
// is held; the next pass continues where this one stopped. It needs
// auto_vacuum=INCREMENTAL, which the Upgrade op enables.
//
// ReclaimFull is the blunt instrument (VACUUM): the write lock is held for the
// whole rewrite and SQLite needs disk space for a second copy. It is for
// one-shot repair, `leankg vacuum --full`.
//
// CompactKeywordIndex: FTS5 keeps the index as a list of b-tree segments and
// every write adds one. 'optimize' merges the segments and rebuilds the index
// from the content table.
//
// Analyze is not a SQLite maintenance kind; it is reported as skipped.
func (s *Store) Maintain(ctx context.Context, op MaintainOp) (MaintainResult, error) {
	if err := requireWritable(s.mode); err != nil {
		return MaintainResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return MaintainResult{}, err
	}
	switch op.Kind {
	case MaintainUpgrade:
		if err := s.ensureIncrementalVacuum(ctx); err != nil {
			return MaintainResult{}, err
		}
	case MaintainCheckpoint:
		if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			return MaintainResult{}, fmt.Errorf("store: wal_checkpoint(TRUNCATE): %w", err)
		}
	case MaintainReclaim:
		// The pragma takes no bind parameter, so the cap is formatted in.
		// MaxPages is an int from the maintenance options, never user text.
		stmt := `PRAGMA incremental_vacuum`
		if op.MaxPages > 0 {
			stmt = fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, op.MaxPages)
		}
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return MaintainResult{}, fmt.Errorf("store: incremental_vacuum: %w", err)
		}
	case MaintainReclaimFull:
		if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
			return MaintainResult{}, fmt.Errorf("store: vacuum: %w", err)
		}
	case MaintainCompactKeywordIndex:
		if _, err := s.db.ExecContext(ctx, `INSERT INTO elements_fts(elements_fts) VALUES('optimize')`); err != nil {
			return MaintainResult{}, fmt.Errorf("store: fts optimize: %w", err)
		}
	case MaintainAnalyze:
		return MaintainResult{Skipped: skipSQLiteAnalyze}, nil
	default:
		return MaintainResult{}, errUnknownMaintainKind(op.Kind)
	}
	return MaintainResult{}, nil
}

// Space reports the store's on-disk footprint. It is a no-write probe — it
// stats the file and reads PRAGMA page_count / freelist_count — so it is safe
// on a read-only handle and safe while a live writer holds the store.
//
// SizeBytes comes from page_count, NOT from os.Stat. In WAL mode those differ
// until a checkpoint runs: a mass delete writes the freed pages into the
// -wal sidecar and shrinks the main file only when that sidecar is folded
// back, so stat()-ing the file under-reports what the database occupies by
// up to a whole WAL. page_count is the authoritative figure; the
// MaintainCheckpoint op is what makes the file on disk match it.
func (s *Store) Space(ctx context.Context) (SpaceReport, error) {
	rep := SpaceReport{Engine: EngineSQLite, Path: s.path}
	if fi, err := os.Stat(s.path); err == nil {
		rep.SizeBytes = fi.Size()
	}
	if fi, err := os.Stat(s.path + "-wal"); err == nil {
		rep.WALBytes = fi.Size()
	}
	var pageSize, pageCount, freelist int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return rep, fmt.Errorf("store: page_size: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		return rep, fmt.Errorf("store: page_count: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freelist); err != nil {
		return rep, fmt.Errorf("store: freelist_count: %w", err)
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

// ensureIncrementalVacuum switches the store to PRAGMA
// auto_vacuum=INCREMENTAL when it was created without it. Every store the Go
// engine makes carries auto_vacuum=NONE, because the DSN in Open never set it —
// and under NONE, deleted pages are reused but never returned to the OS, so
// the file only ever grows.
//
// SQLite stores the mode in the file header and adopts a new one only while a
// VACUUM rewrites the file, so the upgrade is a two-step dance: request the
// mode, then VACUUM. Either half alone is a silent no-op, so the result is
// verified. Idempotent — the mode read is one row, and the VACUUM happens at
// most once per store, ever. Called from the Upgrade op only.
func (s *Store) ensureIncrementalVacuum(ctx context.Context) error {
	var mode int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return fmt.Errorf("store: auto_vacuum pragma: %w", err)
	}
	if mode != 0 { // 1 = FULL, 2 = INCREMENTAL: already reclaiming
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		return fmt.Errorf("store: request incremental vacuum: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("store: enable incremental vacuum: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return fmt.Errorf("store: verify auto_vacuum: %w", err)
	}
	if mode != 2 {
		return fmt.Errorf("store: incremental vacuum not enabled (auto_vacuum=%d after VACUUM)", mode)
	}
	return nil
}
