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
	"github.com/FreePeak/LeanKG/internal/metrics"
	"github.com/FreePeak/LeanKG/internal/store"
)

// Defaults for the periodic pass. The interval is the Rust engine's
// LEANKG_VACUUM_INTERVAL_HOURS default (1h), restored; the page cap is new
// and exists because a full sweep of a multi-hundred-MB freelist would stall
// a serving MCP server for seconds.
//
// There is deliberately NO built-in freelist floor: the 1 MB floor this
// package used to carry meant the scheduled pass reclaimed nothing at all
// (every store's freelist was below it — the live leankg store held 745 KB
// against a 1 MB floor), so the reclaim machinery never ran.
const (
	DefaultInterval = time.Hour
	DefaultMaxPages = 4096
	// DefaultCompactFTSEvery is how many passes fall between two FTS
	// compactions. Compaction rewrites the whole keyword index, so it must
	// not run on every tick; at the default 1h interval this is a daily
	// compaction.
	DefaultCompactFTSEvery = 24
	// DefaultSummaryEvery is how many passes fall between two summary
	// lines. A pass that reclaims nothing, prunes nothing and compacts
	// nothing used to be invisible; the summary is what makes it visible
	// without spamming the log every tick.
	DefaultSummaryEvery = 24
	FirstIdle           = 30 * time.Second
)

// Options configures one maintenance pass.
type Options struct {
	// MaxPages caps how many free pages a single IncrementalVacuum call
	// returns (0 = DefaultMaxPages).
	MaxPages int
	// MinFreeBytes is the freelist floor below which the pass only
	// checkpoints. 0 (the default) reclaims as soon as the freelist holds
	// anything at all; a negative value never reclaims. The floor is the
	// caller's, not the package's: a built-in floor is what stopped the
	// scheduled pass from reclaiming anything.
	MinFreeBytes int64
	// KeepModelIDs are the vector collections that survive the stale-stamp
	// sweep. Empty (the default) means "keep every collection": a caller
	// that does not know which collection is live — a server with no
	// embedder wired, a read-only store — must not be able to delete it.
	// The server passes the embedder's model id here, which is exactly the
	// collection the L3 rung queries.
	KeepModelIDs []string
	// KeepModelIDsFunc derives the keep set for one store at pass time.
	// It wins over KeepModelIDs when set, and it is what a writer needs:
	// the live collection changes between passes (a re-embed under a new
	// model), so a keep set frozen at start-up would delete the CURRENT
	// collection. The func is asked the same question every pass: which
	// collection is live for this store?
	KeepModelIDsFunc func(st store.Backend) []string
	// PruneLedgers runs the retention sweeps over the append-only ledgers
	// (context_metrics, embed_runs). The scheduled pass sets it; the
	// one-shot `leankg vacuum` does not, so that verb stays a
	// space-reclaim tool.
	PruneLedgers bool
	// CompactFTS compacts the keyword index on this pass (SQLite: merge
	// the FTS5 segments and rebuild the index from the content table). It
	// rewrites the whole index, so the scheduler turns it on every
	// CompactFTSEvery passes, not on every tick.
	CompactFTS bool
	// CompactFTSEvery is how many passes fall between two FTS
	// compactions (0 = DefaultCompactFTSEvery; negative = never).
	CompactFTSEvery int
	// SummaryEvery is how many passes fall between two summary lines
	// (0 = DefaultSummaryEvery; negative = never).
	SummaryEvery int
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
	// MetricsPruned is the number of context_metrics rows the retention
	// sweep deleted.
	MetricsPruned int64 `json:"metrics_pruned"`
	// EmbedRuns reports what the embed-run ledger prune removed.
	EmbedRuns store.EmbedRunPrune `json:"embed_runs"`
	// FTSCompacted reports whether the keyword index was compacted.
	FTSCompacted bool `json:"fts_compacted"`
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
// step needs a keep list — Options.KeepModelIDs, or the per-pass
// Options.KeepModelIDsFunc — and with no keep list it is skipped entirely,
// because dropping the live collection is not recoverable.
//
// The retention sweeps over the append-only ledgers (context_metrics,
// embed_runs) run only when Options.PruneLedgers is set, which the
// scheduler does and the one-shot verb does not.
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
	if opts.PruneLedgers {
		// The embed-run ledger is trimmed FIRST: the keep set below is
		// derived from it (the newest run names the live collection), and
		// the prune keeps that newest row.
		pr, err := st.PruneEmbedRuns(time.Now().AddDate(0, 0, -metrics.DefaultRetentionDays))
		if err != nil {
			logf("maintain: prune embed runs: %v", err)
		} else {
			out.EmbedRuns = pr
		}
		if n, err := st.CleanupMetrics(metrics.DefaultRetentionDays); err != nil {
			logf("maintain: cleanup metrics: %v", err)
		} else {
			out.MetricsPruned = n
		}
	}
	if opts.CompactFTS {
		if err := st.CompactFTS(); err != nil {
			logf("maintain: fts compaction: %v", err)
		} else {
			out.FTSCompacted = true
		}
	}
	keep := opts.KeepModelIDs
	if opts.KeepModelIDsFunc != nil {
		keep = opts.KeepModelIDsFunc(st)
	}
	if len(keep) == 0 {
		return out
	}
	sw, err := st.DeleteStaleStamps(keep)
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
	// The floor is the CALLER's now: 0 means "reclaim as soon as there is
	// anything to reclaim", and a negative floor never reclaims. The old
	// built-in 1 MB floor is what stopped this from ever running — every
	// store's freelist sat below it, so the hourly pass freed 0 bytes
	// fleet-wide while the machinery looked healthy.
	if opts.MinFreeBytes >= 0 && freelist > 0 && freelist >= opts.MinFreeBytes {
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
	compactEvery := opts.CompactFTSEvery
	if compactEvery == 0 {
		compactEvery = DefaultCompactFTSEvery
	}
	summaryEvery := opts.SummaryEvery
	if summaryEvery == 0 {
		summaryEvery = DefaultSummaryEvery
	}
	passNo := 0
	pass := func() {
		passNo++
		passOpts := opts
		// The scheduled pass is the retention pass: the append-only
		// ledgers are pruned here, not by the one-shot verb.
		passOpts.PruneLedgers = true
		// Compaction rewrites the whole keyword index, so it runs on a
		// slow cadence rather than on every tick.
		passOpts.CompactFTS = opts.CompactFTS || (compactEvery > 0 && passNo%compactEvery == 0)
		var reclaimed, grew int64
		var metricsPruned, runsPruned, runsClosed int64
		var compacted, stores int
		for _, t := range targets() {
			if t.Store == nil {
				continue
			}
			res, err := Pass(t.Store, passOpts)
			if err != nil && opts.Logf != nil {
				if t.Label == "" {
					opts.Logf("maintain: %v", err)
				} else {
					opts.Logf("maintain[%s]: %v", t.Label, err)
				}
			}
			if err == nil {
				stores++
				reclaimed += res.Reclaimed
				grew += res.Grew
				metricsPruned += res.Sweep.MetricsPruned
				runsPruned += res.Sweep.EmbedRuns.Pruned
				runsClosed += res.Sweep.EmbedRuns.Closed
				if res.Sweep.FTSCompacted {
					compacted++
				}
			}
			CompactMemory(t.Mem, opts)
			if t.Close != nil {
				t.Close()
			}
		}
		// A pass that reclaims nothing, prunes nothing and compacts
		// nothing used to leave no trace at all, which is why a sweep
		// that did nothing ran for weeks without anyone noticing. The
		// summary is low-frequency on purpose (daily at the default
		// interval): it says what actually happened, not that a tick
		// happened.
		// The first pass always reports: a fleet that has just started must
		// say what it did, not wait a day for the first summary. After that
		// the cadence keeps the log quiet.
		if opts.Logf != nil && summaryEvery > 0 && (passNo == 1 || passNo%summaryEvery == 0) {
			opts.Logf("maintain: %d pass(es) over %d store(s): reclaimed %d bytes, grew %d bytes, "+
				"pruned %d metric row(s) and %d embed-run row(s), closed %d stuck run(s), "+
				"compacted the FTS index of %d store(s)",
				passNo, stores, reclaimed, grew, metricsPruned, runsPruned, runsClosed, compacted)
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
