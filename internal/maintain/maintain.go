// Package maintain is the storage-maintenance pass: the reclaim half of
// DeleteByFile and gc.
//
// Those two return rows to SQLite's freelist — a re-index, a mass deletion or
// a hygiene sweep leaves pages behind that the database owns but no row uses.
// Until a vacuum runs, that space never reaches the filesystem: the file keeps
// the high-water mark of every index it ever held, and PRAGMA
// auto_vacuum=NONE (what every Go-engine store is created with, because Open's
// DSN never sets it) means SQLite will only ever reuse those pages internally.
//
// One pass, three steps, engine-agnostic through store.Backend:
//
//  1. EnsureIncrementalVacuum — a one-time header upgrade so the store can
//     return space at all (and, on the upgrade itself, the whole accumulated
//     freelist).
//  2. IncrementalVacuum(maxPages) — a bounded reclaim, safe on a live serving
//     database because it caps how long the write lock is held.
//  3. Checkpoint — flush and truncate the -wal sidecar, which otherwise
//     lingers at whatever size the last unclean shutdown left it.
//
// The memory layer's FTS side index gets the same treatment through
// CompactMemory, because it is the same engine (SQLite, WAL,
// replace-on-write) at a smaller scale.
//
// The schedule is the caller's business: `leankg serve` and `leankg writer`
// run Run on a ticker, `leankg vacuum` runs Pass once from the command line.
package maintain

import (
	"context"
	"fmt"
	"time"

	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
)

// Defaults for the periodic pass. The interval is the Rust engine's
// LEANKG_VACUUM_INTERVAL_HOURS default (1h), restored; the page cap and the
// freelist floor are new and exist because a full sweep of a
// multi-hundred-MB freelist would stall a serving MCP server for seconds,
// while anything under a megabyte is noise.
const (
	DefaultInterval = time.Hour
	DefaultMaxPages = 4096
	minFreeBytes    = 1 << 20
	FirstIdle       = 30 * time.Second
)

// Options configures one maintenance pass.
type Options struct {
	// MaxPages caps how many free pages a single IncrementalVacuum call
	// returns (0 = DefaultMaxPages).
	MaxPages int
	// MinFreeBytes is the freelist floor below which the pass only
	// checkpoints (0 = the built-in 1 MB floor).
	MinFreeBytes int64
	// Logf receives one line per pass; nil discards.
	Logf func(format string, args ...any)
}

// Result is what one pass did and how much it recovered.
type Result struct {
	Engine       string `json:"engine"`
	Path         string `json:"path"`
	Before       int64  `json:"before_bytes"`
	After        int64  `json:"after_bytes"`
	Reclaimed    int64  `json:"reclaimed_bytes"`
	FreeBefore   int64  `json:"free_before_bytes"`
	FreeAfter    int64  `json:"free_after_bytes"`
	WALBytes     int64  `json:"wal_bytes,omitempty"`
	Vacuumed     bool   `json:"vacuumed"`
	Checkpointed bool   `json:"checkpointed"`
}

// Pass runs the maintenance sequence over one store handle.
//
// A partial failure is not an error: a store that cannot be checkpointed right
// now (a live reader holds it) still gets its space back, and the next pass
// retries. Only a store that cannot even be measured is a real failure.
func Pass(st store.Backend, opts Options) (Result, error) {
	if opts.MaxPages == 0 {
		opts.MaxPages = DefaultMaxPages
	}
	if opts.MinFreeBytes == 0 {
		opts.MinFreeBytes = minFreeBytes
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	before, err := st.Space()
	if err != nil {
		return Result{}, fmt.Errorf("maintain: space probe: %w", err)
	}
	res := Result{
		Engine:     before.Engine,
		Path:       before.Path,
		Before:     before.SizeBytes,
		After:      before.SizeBytes,
		FreeBefore: before.FreeBytes,
		FreeAfter:  before.FreeBytes,
		WALBytes:   before.WALBytes,
	}

	// First and unconditional: one PRAGMA read on any store that already
	// auto-vacuums, exactly one full VACUUM on any store that does not.
	if err := st.EnsureIncrementalVacuum(); err != nil {
		return res, fmt.Errorf("maintain: enable incremental vacuum: %w", err)
	}

	if before.FreeBytes >= opts.MinFreeBytes {
		if err := st.IncrementalVacuum(opts.MaxPages); err != nil {
			// Not fatal: the checkpoint below still applies, and the next
			// pass retries the reclaim.
			logf("maintain: incremental vacuum: %v", err)
		} else {
			res.Vacuumed = true
		}
	}
	if err := st.Checkpoint(); err != nil {
		logf("maintain: checkpoint: %v", err)
	} else {
		res.Checkpointed = true
	}

	if after, err := st.Space(); err == nil {
		res.After = after.SizeBytes
		res.FreeAfter = after.FreeBytes
	} else {
		logf("maintain: re-probe space: %v", err)
	}
	if res.Reclaimed = res.Before - res.After; res.Reclaimed > 0 {
		logf("maintain: reclaimed %d bytes (%d -> %d) from %s",
			res.Reclaimed, res.Before, res.After, res.Path)
	}
	return res, nil
}

// CompactMemory runs the memory layer's half of a pass over the FTS side
// index. mem may be nil (memory is optional under `serve --memory`), in which
// case it is a no-op — that is why it is a separate call rather than a field
// on Options: the store pass must never depend on memory being wired.
//
// A compaction failure is logged, not returned: the graph store's reclaim is
// the one that matters, and losing the memory compaction must not stop it.
func CompactMemory(mem *memory.Memory, opts Options) {
	if mem == nil {
		return
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	before, berr := mem.Space()
	if berr != nil {
		logf("maintain: memory space probe: %v", berr)
		return
	}
	if err := mem.Compact(); err != nil {
		logf("maintain: memory compact: %v", err)
		return
	}
	after, aerr := mem.Space()
	if aerr != nil {
		logf("maintain: memory re-probe: %v", aerr)
		return
	}
	if gained := before.SizeBytes - after.SizeBytes; gained > 0 {
		logf("maintain: memory index reclaimed %d bytes (%d -> %d)",
			gained, before.SizeBytes, after.SizeBytes)
	}
}

// Run drives Pass (and CompactMemory, when mem is non-nil) on a ticker until
// ctx is done, with one first pass after FirstIdle — a server that just started
// is mid-write, and this is a maintenance task, not a startup cost.
//
// A non-positive interval disables the loop entirely, which is how
// LEANKG_VACUUM_INTERVAL_HOURS=0 keeps the Rust engine's documented
// "disabled" semantics.
func Run(ctx context.Context, st store.Backend, mem *memory.Memory, opts Options, interval time.Duration) {
	if interval <= 0 {
		return
	}
	pass := func() {
		if _, err := Pass(st, opts); err != nil && opts.Logf != nil {
			opts.Logf("maintain: %v", err)
		}
		CompactMemory(mem, opts)
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(FirstIdle):
		}
		pass()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pass()
			}
		}
	}()
}

// IntervalFromEnv reads the loop's period from LEANKG_VACUUM_INTERVAL_HOURS,
// the variable name the Rust engine documented and users may still carry in
// their environment. Hours, floatable; 0 / off / false disables. Unset (or
// unparseable) means DefaultInterval — a typo must not silently disable disk
// reclamation.
func IntervalFromEnv(getenv func(string) string) time.Duration {
	raw := getenv("LEANKG_VACUUM_INTERVAL_HOURS")
	if raw == "" {
		return DefaultInterval
	}
	switch raw {
	case "0", "off", "false", "no":
		return 0
	}
	var hours float64
	if _, err := fmt.Sscanf(raw, "%g", &hours); err != nil || hours <= 0 {
		return DefaultInterval
	}
	return time.Duration(hours * float64(time.Hour))
}
