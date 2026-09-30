package store

import (
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

// Space reports the store's on-disk footprint. It is a no-write probe — it
// stats the file and reads PRAGMA page_count / freelist_count — so it is safe
// on a read-only handle and safe while a live writer holds the store.
//
// SizeBytes comes from page_count, NOT from os.Stat. In WAL mode those differ
// until a checkpoint runs: a mass delete writes the freed pages into the
// -wal sidecar and shrinks the main file only when that sidecar is folded
// back, so stat()-ing the file under-reports what the database occupies by
// up to a whole WAL. page_count is the authoritative figure; the checkpoint in
// Checkpoint() is what makes the file on disk match it.
func (s *Store) Space() (SpaceReport, error) {
	rep := SpaceReport{Engine: EngineSQLite, Path: s.path}
	if fi, err := os.Stat(s.path); err == nil {
		rep.SizeBytes = fi.Size()
	}
	if fi, err := os.Stat(s.path + "-wal"); err == nil {
		rep.WALBytes = fi.Size()
	}
	var pageSize, pageCount, freelist int64
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return rep, fmt.Errorf("store: page_size: %w", err)
	}
	if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		return rep, fmt.Errorf("store: page_count: %w", err)
	}
	if err := s.db.QueryRow(`PRAGMA freelist_count`).Scan(&freelist); err != nil {
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

// Checkpoint flushes the WAL into the main database and truncates the -wal
// sidecar to zero. Without it a long-lived server's WAL grows until a
// checkpoint happens to run, and an unclean shutdown leaves it large on disk
// indefinitely.
//
// TRUNCATE is the mode on purpose: it is the only one that resets the sidecar's
// size to zero. It takes the write lock for the duration, which the store's
// single serialized connection already holds between statements.
func (s *Store) Checkpoint() error {
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("store: wal_checkpoint(TRUNCATE): %w", err)
	}
	return nil
}

// IncrementalVacuum hands free pages back to the filesystem in a bounded
// pass: PRAGMA incremental_vacuum with no argument reclaims every free page,
// which on a store whose freelist is measured in gigabytes holds the write
// lock for the whole sweep. maxPages caps it so the caller — which runs this
// from a maintenance loop against a live, serving database — bounds the stall;
// the next pass continues where this one stopped. maxPages <= 0 means "all".
//
// Requires auto_vacuum=INCREMENTAL (see EnsureIncrementalVacuum); under the
// default NONE the pragma is a no-op, because SQLite has no free-page map to
// walk.
func (s *Store) IncrementalVacuum(maxPages int) error {
	if maxPages <= 0 {
		if _, err := s.db.Exec(`PRAGMA incremental_vacuum`); err != nil {
			return fmt.Errorf("store: incremental_vacuum: %w", err)
		}
		return nil
	}
	// The pragma takes no bind parameter — the cap is a literal in the
	// statement, not a value slot — so it is formatted in. maxPages is an int
	// from the maintenance options, never user text.
	if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, maxPages)); err != nil {
		return fmt.Errorf("store: incremental_vacuum(%d): %w", maxPages, err)
	}
	return nil
}

// Vacuum rewrites the whole database file, returning every free page to the
// filesystem in one pass. It is the blunt instrument: the write lock is held
// for the entire rewrite and SQLite needs disk space for a second copy while
// it runs. Prefer IncrementalVacuum from a live server; Vacuum is for one-shot
// repair — `leankg vacuum --full` — where reclaiming everything at once is
// worth the stall.
func (s *Store) Vacuum() error {
	if s.mode == RO {
		return fmt.Errorf("store: vacuum requires RW mode")
	}
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("store: vacuum: %w", err)
	}
	return nil
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
// most once per store, ever.
func (s *Store) ensureIncrementalVacuum() error {
	var mode int64
	if err := s.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return fmt.Errorf("store: auto_vacuum pragma: %w", err)
	}
	if mode != 0 { // 1 = FULL, 2 = INCREMENTAL: already reclaiming
		return nil
	}
	if _, err := s.db.Exec(`PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		return fmt.Errorf("store: request incremental vacuum: %w", err)
	}
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("store: enable incremental vacuum: %w", err)
	}
	if err := s.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return fmt.Errorf("store: verify auto_vacuum: %w", err)
	}
	if mode != 2 {
		return fmt.Errorf("store: incremental vacuum not enabled (auto_vacuum=%d after VACUUM)", mode)
	}
	return nil
}

// EnsureIncrementalVacuum exposes the one-shot header upgrade so a maintenance
// loop can guarantee the store is in a mode that returns space at all, before
// it starts handing pages back incrementally.
func (s *Store) EnsureIncrementalVacuum() error {
	if s.mode == RO {
		return nil // a reader cannot rewrite the header; nothing to do
	}
	return s.ensureIncrementalVacuum()
}
