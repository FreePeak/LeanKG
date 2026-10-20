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
	// KeepModelIDs are the vector collections that survive the stale-stamp
	// sweep. Empty (the default) means "keep every collection": a caller
	// that does not know which collection is live — a server with no
	// embedder wired, a read-only store — must not be able to delete it.
	// The server passes the embedder's model id here, which is exactly the
	// collection the L3 rung queries.
	KeepModelIDs []string
	// Logf receives one line per pass; nil discards.
	Logf func(format string, args ...any)
}

// SweepResult reports what one reclaim sweep removed.
type SweepResult struct {
	// OrphanEdges is the number of relationship edges whose endpoint no
	// longer exists.
	OrphanEdges int `json:"orphan_edges"`
	// OrphanVectors is the number of vector rows whose element is gone.
	OrphanVectors int `json:"orphan_vectors"`
	// StaleModels is the number of vector collections dropped.
	StaleModels int `json:"stale_models"`
	// StaleVectors is the number of vector rows dropped with them.
	StaleVectors int `json:"stale_vectors"`
	// StaleState is the number of embedding-state rows dropped with them.
	StaleState int `json:"stale_state"`
}

// Empty reports whether the sweep removed nothing.
func (s SweepResult) Empty() bool {
	return s.OrphanEdges == 0 && s.OrphanVectors == 0 &&
		s.StaleModels == 0 && s.StaleVectors == 0 && s.StaleState == 0
}

// Sweep purges the rows a store accumulates without ever reading them:
//
//   - edges into a deleted file (DeleteByFile owns the edges a file
//     SOURCES, but the edges pointing INTO it from other files survive
//     until their sources are re-indexed — incremental index never
//     revisits them);
//   - vectors for elements that no longer exist (the residue
//     `leankg-embed` reports as Orphans);
//   - vector collections under a model id nobody queries any more.
//
// Every step is best-effort: a step that fails leaves its rows for the
// next pass, so the error is logged and the sweep continues. The stale-stamp
// step needs Options.KeepModelIDs — with no keep list it is skipped
// entirely, because dropping the live collection is not recoverable.
func Sweep(st store.Backend, opts Options) SweepResult {
	var out SweepResult
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if n, err := st.DeleteOrphanRelationships(); err != nil {
		logf("maintain: orphan relationships: %v", err)
	} else {
		out.OrphanEdges = n
	}
	if n, err := st.DeleteOrphanVectors(); err != nil {
		logf("maintain: orphan vectors: %v", err)
	} else {
		out.OrphanVectors = n
	}
	if len(opts.KeepModelIDs) == 0 {
		return out
	}
	sw, err := st.DeleteStaleStamps(opts.KeepModelIDs)
	if err != nil {
		logf("maintain: stale stamps: %v", err)
		return out
	}
	out.StaleModels = sw.Models
	out.StaleVectors = sw.Vectors
	out.StaleState = sw.State
	return out
}

// Result is what one pass did and how much it recovered.
type Result struct {
	Engine     string `json:"engine"`
	Path       string `json:"path"`
	Before     int64  `json:"before_bytes"`
	After      int64  `json:"after_bytes"`
	Reclaimed  int64  `json:"reclaimed_bytes"`
	FreeBefore int64  `json:"free_before_bytes"`
	FreeAfter  int64  `json:"free_after_bytes"`
	WALBytes   int64  `json:"wal_bytes,omitempty"`
	Vacuumed   bool   `json:"vacuumed"`
	// Checkpointed reports whether the WAL sidecar was checkpointed.
	Checkpointed bool `json:"checkpointed"`
	// Grew is the bytes the store GREW during the pass. A pass is not always a
	// shrink: the one-time auto_vacuum header upgrade rewrites the store, and
	// on a small store that rewrite lands a few kilobytes LARGER (the header
	// change plus the incremental-vacuum bookkeeping). Reclaimed and Grew are
	// therefore two halves of one signed delta, and exactly one of them is
	// non-zero — a pass can never both reclaim and grow.
	Grew int64 `json:"grew_bytes"`
	// Sweep reports what the reclaim sweep removed before the vacuum
	// handed the space back to the filesystem.
	Sweep SweepResult `json:"sweep"`
}

// ReclaimSummary says what the pass did to the store's size, in the words a
// reader can act on. A pass that grew the store says so, rather than
// reporting the growth as a negative amount reclaimed: "reclaimed -4096
// bytes" reads as a bug in the tool and hides the fact that the pass did
// exactly what it was asked to do.
//
// The delta is derived from Before/After, so a Result built by hand (a test,
// a caller that did not run Pass) still reports the truth.
func (r Result) ReclaimSummary() string {
	if r.After > r.Before {
		return fmt.Sprintf("grew %d bytes", r.After-r.Before)
	}
	return fmt.Sprintf("reclaimed %d bytes", r.Before-r.After)
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

	// The sweep runs BEFORE the vacuum, and that order is the whole point:
	// the sweep returns rows to the freelist, the vacuum hands the
	// freelist back to the filesystem. The other order reclaims the space
	// one pass later — a day late at the default interval.
	res.Sweep = Sweep(st, opts)
	if !res.Sweep.Empty() {
		logf("maintain: swept %d orphan edge(s), %d orphan vector(s), %d stale collection(s) (%d vectors) from %s",
			res.Sweep.OrphanEdges, res.Sweep.OrphanVectors,
			res.Sweep.StaleModels, res.Sweep.StaleVectors, before.Path)
	}

	// The sweep grows the freelist by whatever it removed, so the vacuum
	// decision reads a FRESH probe: a store whose freelist was empty
	// before the sweep may hold megabytes now, and the pre-sweep number
	// would skip the reclaim and leave the space on the freelist for a
	// whole interval.
	freelist := before.FreeBytes
	if after, err := st.Space(); err == nil {
		freelist = after.FreeBytes
	} else {
		logf("maintain: re-probe space before vacuum: %v", err)
	}
	if freelist >= opts.MinFreeBytes {
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
	// The signed delta is split into its two halves, so a pass that grew the
	// store never reports a negative amount as "reclaimed".
	if res.After > res.Before {
		res.Grew = res.After - res.Before
	} else {
		res.Reclaimed = res.Before - res.After
	}
	if res.Reclaimed > 0 {
		logf("maintain: reclaimed %d bytes (%d -> %d) from %s",
			res.Reclaimed, res.Before, res.After, res.Path)
	} else if res.Grew > 0 {
		logf("maintain: store grew %d bytes (%d -> %d) from %s",
			res.Grew, res.Before, res.After, res.Path)
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
// ctx is done. firstIdle delays the FIRST pass — a server that just started is
// mid-write, and this is a maintenance task, not a startup cost; pass 0 to run
// it immediately.
//
// A non-positive interval disables the loop entirely, which is how
// LEANKG_VACUUM_INTERVAL_HOURS=0 keeps the Rust engine's documented
// "disabled" semantics.
func Run(ctx context.Context, st store.Backend, mem *memory.Memory, opts Options, interval, firstIdle time.Duration) {
	RunFleet(ctx, func() []Target {
		return []Target{{Store: st, Mem: mem}}
	}, opts, interval, firstIdle)
}

// Target is one store a fleet pass sweeps.
type Target struct {
	// Store is the store handle; a nil Store is skipped.
	Store store.Backend
	// Mem is the memory layer's store for the same project, if any.
	Mem *memory.Memory
	// Label names the target in the log lines.
	Label string
	// Close releases the handle after the pass. A fleet target is opened
	// for the pass and closed again (the serving process does not want 285
	// project stores resident); the default project's handle is owned by
	// the server, so its Close is nil.
	Close func()
}

// RunFleet drives the pass over a SET of stores on one ticker: a serving
// process fronts many project stores, and the default one is not the only
// one that fills up. The default project is maintained by Run; the projects
// a multi-project server ROUTES to are the ones that used to accumulate
// junk with nothing ever reclaiming it.
//
// targets is called at the start of every pass, so the caller decides which
// stores exist at that moment (a routed project appears the moment it is
// indexed). A target whose Store is nil is skipped; its Close, when set,
// runs after the pass.
func RunFleet(ctx context.Context, targets func() []Target, opts Options, interval, firstIdle time.Duration) {
	if interval <= 0 {
		return
	}
	pass := func() {
		for _, t := range targets() {
			if t.Store == nil {
				continue
			}
			if _, err := Pass(t.Store, opts); err != nil && opts.Logf != nil {
				if t.Label == "" {
					opts.Logf("maintain: %v", err)
				} else {
					opts.Logf("maintain[%s]: %v", t.Label, err)
				}
			}
			CompactMemory(t.Mem, opts)
			if t.Close != nil {
				t.Close()
			}
		}
	}
	go func() {
		if firstIdle > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(firstIdle):
			}
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
